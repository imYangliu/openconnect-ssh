// Package setup guides first use without putting credentials in YAML.
package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/daemon"
	"github.com/imyangliu/openconnect-ssh/internal/discovery"
	"github.com/imyangliu/openconnect-ssh/internal/service"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/unix"
)

type Options struct {
	ConfigPath, Server, Protocol, Name, AuthType, LoginDomain, AuthGroup, Username, Listen, PasswordEnv string
	NonInteractive, SkipLogin, NoStart, InstallService                                                  bool
}
type Runner struct {
	Input            io.Reader
	Output           io.Writer
	Home, Executable string
	Discover         func(context.Context, string, string) (discovery.Result, error)
	Bootstrap        func(context.Context, config.Profile, vpn.BootstrapOptions) (config.Profile, error)
	Browser          vpn.CASCallback
	BrowserFor       func(string, config.Profile) vpn.CASCallback
	ReadSecret       func(context.Context) (string, error)
	Start            func(context.Context, string, string, config.Config) error
	Install          func(context.Context, string, string) error
	Ensure           func(context.Context, config.Config, string) error
	reader           io.Reader
	context          context.Context
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func (r *Runner) Run(ctx context.Context, o Options) error {
	if r.Input == nil {
		r.Input = os.Stdin
	}
	if r.Output == nil {
		r.Output = os.Stdout
	}
	r.reader = r.Input
	r.context = ctx
	if r.Home == "" {
		r.Home, _ = os.UserHomeDir()
	}
	if r.Executable == "" {
		r.Executable, _ = os.Executable()
	}
	if r.Discover == nil {
		r.Discover = discovery.Client{}.Discover
	}
	if r.Bootstrap == nil {
		r.Bootstrap = vpn.Bootstrap
	}
	if r.Start == nil {
		r.Start = service.Start
	}
	if r.Ensure == nil {
		r.Ensure = daemon.Ensure
	}
	if r.Install == nil {
		r.Install = service.Install
	}
	if o.ConfigPath == "" {
		o.ConfigPath = config.DefaultPath()
	}
	path, err := filepath.Abs(o.ConfigPath)
	if err != nil {
		return err
	}
	if o.SkipLogin && !o.NoStart {
		return errors.New("--skip-login requires --no-start; an unauthenticated draft must not be reported ready")
	}
	if o.NoStart && o.InstallService {
		return errors.New("--no-start and --install-service cannot be combined")
	}
	if err = ensureConfigParent(path); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".setup.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("cannot open setup lock")
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another setup is editing this configuration")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	original, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var c config.Config
	if exists {
		c, err = config.Load(path)
		if err != nil {
			return err
		}
	} else {
		c = config.Config{Socket: filepath.Join(filepath.Dir(path), "run", "och.sock")}
	}
	if service.IsRunning(ctx, c) {
		return errors.New("this configuration's service is running; stop it before setup so active connections are not interrupted")
	}
	if o.Server == "" {
		if o.NonInteractive {
			return errors.New("--server is required in non-interactive setup")
		}
		o.Server, err = r.ask("VPN gateway", "")
		if err != nil {
			return err
		}
	}
	result, err := r.Discover(ctx, o.Server, o.Protocol)
	if errors.Is(err, discovery.ErrUnknownProtocol) && !o.NonInteractive {
		fmt.Fprintln(r.Output, "Protocol could not be identified without credentials.")
		protocol, e := r.ask("Protocol (anyconnect or atrust)", "")
		if e != nil {
			return e
		}
		result, err = r.Discover(ctx, o.Server, protocol)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(r.Output, "Protocol: %s\n", result.Protocol)
	if o.Name == "" {
		if o.NonInteractive {
			o.Name = "campus"
		} else {
			o.Name, err = r.ask("VPN profile name", "campus")
			if err != nil {
				return err
			}
		}
	}
	if !validName.MatchString(o.Name) || o.Name == "direct" {
		return errors.New("profile name must use letters, digits, hyphens or underscores and cannot be direct")
	}
	previous, overwrite := c.Profiles[o.Name]
	if overwrite {
		if o.NonInteractive {
			return errors.New("profile already exists; interactive confirmation is required to replace it")
		}
		confirmed, e := r.confirm("Replace this existing profile?", false)
		if e != nil {
			return e
		}
		if !confirmed {
			return errors.New("setup canceled; existing configuration unchanged")
		}
	}
	p := config.Profile{Protocol: result.Protocol, Server: result.Server, Username: o.Username}
	if overwrite && previous.Server == p.Server && previous.Protocol == p.Protocol {
		p = previous
		p.Username = o.Username
		if p.Username == "" {
			p.Username = previous.Username
		}
	}
	if p.Protocol == "atrust" {
		method, e := r.chooseMethod(result.Methods, o)
		if e != nil {
			return e
		}
		p.AuthType, p.LoginDomain = method.Type, method.Domain
	} else if len(result.Groups) > 0 && !o.NonInteractive {
		for i, g := range result.Groups {
			fmt.Fprintf(r.Output, "  %d. %s\n", i+1, g)
		}
		choice, e := r.ask("Authentication group number (blank for default)", "")
		if e != nil {
			return e
		}
		if choice != "" {
			n, e := strconv.Atoi(choice)
			if e != nil || n < 1 || n > len(result.Groups) {
				return errors.New("invalid authentication group")
			}
			p.AuthGroup = result.Groups[n-1]
		}
	}
	if p.Protocol == "anyconnect" || p.AuthType == "auth/psw" {
		if p.Username == "" {
			if o.NonInteractive {
				return errors.New("--username is required for password authentication")
			}
			p.Username, err = r.ask("VPN username", "")
			if err != nil {
				return err
			}
		}
		if p.Username == "" {
			return errors.New("VPN username is required")
		}
	}
	if o.AuthGroup != "" {
		p.AuthGroup = o.AuthGroup
	}
	p.PasswordEnv = ""
	p.CookieEnv = ""
	managedStatePath := config.ManagedAuthStatePath(r.Home, o.Name, p)
	p.AuthStateFile = managedStatePath
	if o.Listen == "" {
		if old, ok := c.Listeners[o.Name]; ok {
			if old.Via != o.Name {
				return errors.New("listener name already belongs to another profile")
			}
			o.Listen = old.Listen
		} else {
			var reserved []string
			for _, listener := range c.Listeners {
				reserved = append(reserved, listener.Listen)
			}
			o.Listen = availableListener(reserved...)
		}
		if !o.NonInteractive {
			o.Listen, err = r.ask("Mixed SOCKS5/HTTP listener", o.Listen)
			if err != nil {
				return err
			}
		}
	}
	if err = validateListener(o.Listen); err != nil {
		return err
	}
	if c.Profiles == nil {
		c.Profiles = map[string]config.Profile{}
	}
	if c.Listeners == nil {
		c.Listeners = map[string]config.Listener{}
	}
	c.Profiles[o.Name] = p
	c.Listeners[o.Name] = config.Listener{Listen: o.Listen, Via: o.Name}
	data, err := mergeConfig(original, c, o.Name, managedStatePath)
	if err != nil {
		return err
	}
	if err = validateDraft(path, data); err != nil {
		return err
	}
	if !o.SkipLogin {
		browserCallback := r.Browser
		if r.BrowserFor != nil {
			browserCallback = r.BrowserFor(o.Name, p)
		}
		bootstrap := vpn.BootstrapOptions{Browser: browserCallback, TrustCertificates: func(ctx context.Context, certificates []vpn.CertificateInfo, suggestedName string) (string, error) {
			if o.NonInteractive {
				return "", errors.New("untrusted tunnel certificates require interactive confirmation")
			}
			return r.trustCertificates(ctx, certificates, suggestedName)
		}}
		if o.PasswordEnv != "" {
			bootstrap.Password = os.Getenv(o.PasswordEnv)
			if bootstrap.Password == "" {
				return errors.New("configured password environment variable is empty")
			}
		}
		bootstrap.PasswordPrompt = func(ctx context.Context) (string, error) {
			if o.NonInteractive {
				return "", errors.New("password authentication requires --password-env in non-interactive setup")
			}
			if r.ReadSecret == nil {
				return "", errors.New("secure password input is unavailable")
			}
			return r.ReadSecret(ctx)
		}
		p, err = r.Bootstrap(ctx, p, bootstrap)
		if err != nil {
			if info, statErr := os.Lstat(p.AuthStateFile); statErr == nil && info.Mode().IsRegular() {
				return fmt.Errorf("configuration was not saved; private authentication state remains at %q for retry: %w", p.AuthStateFile, err)
			}
			return fmt.Errorf("configuration was not saved: %w", err)
		}
		c.Profiles[o.Name] = p
		data, err = mergeConfig(original, c, o.Name, managedStatePath)
		if err != nil {
			return err
		}
	}
	if err = saveConfig(path, data, original, exists); err != nil {
		return err
	}
	fmt.Fprintf(r.Output, "Configuration saved: %s\n", path)
	if o.SkipLogin {
		fmt.Fprintln(r.Output, "Draft only: authentication was skipped; no service was started.")
		return nil
	}
	install := o.InstallService
	if !o.NoStart && !o.NonInteractive && !install {
		install, err = r.confirm("Start automatically when you log in?", true)
		if err != nil {
			return err
		}
	}
	if !o.NoStart {
		if install {
			err = r.Install(ctx, r.Executable, path)
			if err == nil {
				err = service.WaitReady(ctx, c, 10*time.Second)
			}
		} else {
			err = r.Start(ctx, r.Executable, path, c)
		}
		if err != nil {
			return fmt.Errorf("configuration and authentication saved, but service startup failed: %w", err)
		}
		if r.Ensure != nil {
			if err = r.Ensure(ctx, c, o.Name); err != nil {
				return fmt.Errorf("service started, but saved authentication could not be restored: %w", err)
			}
		}
		fmt.Fprintln(r.Output, "VPN ready. Saved authentication was restored by the background service.")
	} else {
		fmt.Fprintln(r.Output, "Authentication saved. Start the service with och serve --config <file>.")
	}
	fmt.Fprintf(r.Output, "Mixed proxy: %s (fixed VPN %s)\n", o.Listen, o.Name)
	if !o.NonInteractive {
		write, e := r.confirm("Configure an SSH alias now?", false)
		if e != nil {
			return e
		}
		if write {
			if e = r.ssh(path, o.Name); e != nil {
				return fmt.Errorf("VPN configured, but SSH setup failed: %w", e)
			}
		}
	}
	return nil
}
func (r *Runner) ask(label, fallback string) (string, error) {
	if fallback != "" {
		fmt.Fprintf(r.Output, "%s [%s]: ", label, fallback)
	} else {
		fmt.Fprintf(r.Output, "%s: ", label)
	}
	line, err := readLine(r.context, r.reader)
	if err != nil {
		return "", errors.New("setup input ended")
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = fallback
	}
	return line, nil
}
func (r *Runner) confirm(label string, yes bool) (bool, error) {
	fallback := "n"
	if yes {
		fallback = "y"
	}
	s, e := r.ask(label+" (y/n)", fallback)
	return strings.EqualFold(s, "y") || strings.EqualFold(s, "yes"), e
}
func (r *Runner) chooseMethod(methods []discovery.Method, o Options) (discovery.Method, error) {
	var supported []discovery.Method
	for i, m := range methods {
		note := ""
		if !m.Supported {
			note = " (not supported)"
		} else {
			supported = append(supported, m)
		}
		fmt.Fprintf(r.Output, "  %d. %q [%q, %q]%s\n", i+1, m.Name, m.Type, m.Domain, note)
	}
	if o.AuthType != "" {
		for _, m := range methods {
			if m.Type == o.AuthType && m.Domain == o.LoginDomain && m.Supported {
				return m, nil
			}
		}
		return discovery.Method{}, errors.New("requested authentication type/domain is not advertised and supported")
	}
	if len(supported) == 1 {
		return supported[0], nil
	}
	if len(supported) == 0 {
		return discovery.Method{}, errors.New("gateway has no authentication method supported by this version")
	}
	if o.NonInteractive {
		return discovery.Method{}, errors.New("multiple authentication methods; provide --auth-type and --login-domain")
	}
	s, e := r.ask("Authentication method number", "")
	if e != nil {
		return discovery.Method{}, e
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 || n > len(methods) || !methods[n-1].Supported {
		return discovery.Method{}, errors.New("invalid or unsupported authentication method")
	}
	return methods[n-1], nil
}
func (r *Runner) trustCertificates(ctx context.Context, certs []vpn.CertificateInfo, suggestedName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fmt.Fprintln(r.Output, "These private tunnel certificates are not trusted by the system. Confirm their fingerprints before trusting them only in OCH:")
	for _, c := range certs {
		fmt.Fprintf(r.Output, "  SHA-256 %s\n  Subject: %q\n  DNS: %q\n  Expires: %s\n", c.SHA256, c.Subject, strings.Join(c.DNSNames, ", "), c.NotAfter.Format("2006-01-02"))
	}
	ok, e := r.confirm("Trust exactly these certificates for this VPN?", false)
	if e != nil {
		return "", e
	}
	if !ok {
		return "", errors.New("tunnel certificate trust was not confirmed")
	}
	if suggestedName != "" {
		return suggestedName, nil
	}
	name, e := r.ask("Tunnel TLS server name", "")
	if e != nil {
		return "", e
	}
	if name == "" {
		return "", errors.New("TLS server name is required")
	}
	return name, nil
}
func availableListener(reserved ...string) string {
	ports := make(map[int]bool, len(reserved))
	for _, address := range reserved {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(port)
		if err == nil {
			ports[n] = true
		}
	}
	for port := 1080; port < 1100; port++ {
		if ports[port] {
			continue
		}
		a := fmt.Sprintf("127.0.0.1:%d", port)
		l, e := net.Listen("tcp", a)
		if e == nil {
			l.Close()
			return a
		}
	}
	var rejected []net.Listener
	defer func() {
		for _, listener := range rejected {
			listener.Close()
		}
	}()
	for range len(ports) + 1 {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return ""
		}
		if ports[l.Addr().(*net.TCPAddr).Port] {
			rejected = append(rejected, l)
			continue
		}
		address := l.Addr().String()
		l.Close()
		return address
	}
	return ""
}
func ensureConfigParent(path string) error {
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("configuration must be a regular file, not a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(filepath.Dir(path), 0700)
}
func mergeConfig(original []byte, c config.Config, name, managedStatePath string) ([]byte, error) {
	var doc yaml.Node
	if len(original) > 0 {
		if err := yaml.Unmarshal(original, &doc); err != nil {
			return nil, errors.New("invalid existing YAML")
		}
	} else {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	copy, err := copyYAMLNode(&doc)
	if err != nil {
		return nil, err
	}
	doc = *copy
	root := doc.Content[0]
	var effective map[string]yaml.Node
	if err := root.Decode(&effective); err != nil {
		return nil, errors.New("invalid existing YAML mapping")
	}
	p := c.Profiles[name]
	if p.AuthStateFile == managedStatePath {
		p.AuthStateFile = config.ManagedPath
	}
	if p.TunnelCertificateFile == managedStatePath+".certificates.pem" {
		p.TunnelCertificateFile = config.ManagedPath
	}
	for key, value := range map[string]any{"profiles": p, "listeners": c.Listeners[name]} {
		mapping, err := mappingNode(root, key, effective[key])
		if err != nil {
			return nil, err
		}
		setNode(mapping, name, value)
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func copyYAMLNode(node *yaml.Node) (*yaml.Node, error) {
	remaining := 100000
	remainingBytes := 8 << 20
	active := make(map[*yaml.Node]bool)
	var clone func(*yaml.Node, int) (*yaml.Node, error)
	clone = func(node *yaml.Node, depth int) (*yaml.Node, error) {
		if node == nil {
			return nil, errors.New("configuration contains an invalid YAML alias")
		}
		if active[node] {
			return nil, errors.New("configuration uses recursive YAML aliases; remove the cycle before running setup")
		}
		size := len(node.Value) + len(node.Tag) + len(node.HeadComment) + len(node.LineComment) + len(node.FootComment)
		if remaining <= len(node.Content) || depth > 256 || size > remainingBytes {
			return nil, errors.New("configuration YAML is too large or deeply nested to expand safely; simplify it before running setup")
		}
		remaining--
		remainingBytes -= size
		active[node] = true
		defer delete(active, node)
		if node.Kind == yaml.AliasNode {
			copy, err := clone(node.Alias, depth+1)
			if err != nil {
				return nil, err
			}
			if node.HeadComment != "" {
				copy.HeadComment = node.HeadComment
			}
			if node.LineComment != "" {
				copy.LineComment = node.LineComment
			}
			if node.FootComment != "" {
				copy.FootComment = node.FootComment
			}
			return copy, nil
		}
		copy := *node
		copy.Anchor, copy.Alias = "", nil
		copy.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			var err error
			copy.Content[i], err = clone(child, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return &copy, nil
	}
	return clone(node, 0)
}
func mappingNode(parent *yaml.Node, key string, inherited yaml.Node) (*yaml.Node, error) {
	for i := 0; i < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			if parent.Content[i+1].Kind != yaml.MappingNode {
				parent.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
			return parent.Content[i+1], nil
		}
	}
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if inherited.Kind == yaml.MappingNode {
		var err error
		n, err = copyYAMLNode(&inherited)
		if err != nil {
			return nil, err
		}
	}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"}, n)
	return n, nil
}
func setNode(parent *yaml.Node, key string, value any) {
	var node yaml.Node
	node.Encode(value)
	for i := 0; i < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = &node
			return
		}
	}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"}, &node)
}
func validateDraft(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".och-validate-*.yaml")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	f.Close()
	_, e = config.Load(f.Name())
	return e
}
func saveConfig(path string, data, original []byte, existed bool) error {
	if err := validateDraft(path, data); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if existed && (err != nil || !bytes.Equal(current, original)) || !existed && !os.IsNotExist(err) {
		return errors.New("configuration changed during setup; authentication is saved, but configuration was not overwritten")
	}
	return writeAtomic(path, data)
}
func writeAtomic(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".och-write-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e == nil {
		e = dir.Sync()
		dir.Close()
	}
	return e
}

func readLine(ctx context.Context, input io.Reader) (string, error) {
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if f, ok := input.(*os.File); ok {
			descriptors := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}}
			_, err := unix.Poll(descriptors, 100)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil {
				return "", err
			}
			if descriptors[0].Revents&unix.POLLIN == 0 {
				if descriptors[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
					return "", io.EOF
				}
				continue
			}
		}
		var b [1]byte
		if _, err := io.ReadFull(input, b[:]); err != nil {
			return "", err
		}
		if b[0] == '\n' {
			return string(line), nil
		}
		line = append(line, b[0])
		if len(line) > 8192 {
			return "", errors.New("setup input is too long")
		}
	}
}

func validateListener(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("mixed listener must be a loopback IP and numeric port")
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || n < 1 || n > 65535 {
		return errors.New("mixed listener requires loopback IP and port 1..65535")
	}
	return nil
}
