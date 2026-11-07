package setup

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var sshName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var sshUser = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@-]*$`)

func (r *Runner) ssh(configPath, via string) error {
	host, e := r.ask("SSH target host", "")
	if e != nil {
		return e
	}
	user, e := r.ask("SSH username", os.Getenv("USER"))
	if e != nil {
		return e
	}
	port, e := r.ask("SSH port", "22")
	if e != nil {
		return e
	}
	alias, e := r.ask("SSH alias", via)
	if e != nil {
		return e
	}
	if e = WriteSSH(r.Home, r.Executable, configPath, via, alias, host, user, port); e != nil {
		return e
	}
	fmt.Fprintf(r.Output, "SSH configured. Run: ssh %s\n", alias)
	fmt.Fprintf(r.Output, "If other Include files define this alias, verify the effective configuration with ssh -G %s.\n", alias)
	return nil
}
func WriteSSH(home, executable, configPath, via, alias, host, user, port string) error {
	if !sshName.MatchString(alias) || !sshUser.MatchString(user) || !validName.MatchString(via) {
		return errors.New("invalid SSH alias, user or VPN profile name")
	}
	if net.ParseIP(strings.Trim(host, "[]")) == nil && !sshName.MatchString(host) {
		return errors.New("invalid SSH target host")
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return errors.New("invalid SSH port")
	}
	if !filepath.IsAbs(executable) || !filepath.IsAbs(configPath) || strings.ContainsAny(executable+configPath, "\r\n\x00") {
		return errors.New("SSH ProxyCommand requires absolute single-line paths")
	}
	dir := filepath.Join(home, ".ssh")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	main := filepath.Join(dir, "config")
	if i, e := os.Lstat(main); e == nil && !i.Mode().IsRegular() {
		return errors.New("SSH config must be a regular file, not a symlink")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	old, e := os.ReadFile(main)
	existed := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	files, _ := filepath.Glob(filepath.Join(dir, "och.d", "*.config"))
	contents := [][]byte{old}
	for _, p := range files {
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		contents = append(contents, b)
	}
	for _, b := range contents {
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 1 && strings.EqualFold(fields[0], "Host") {
				for _, h := range fields[1:] {
					if h == alias {
						return errors.New("SSH alias already exists; choose another alias or edit it explicitly")
					}
				}
			}
		}
	}
	managed := filepath.Join(dir, "och.d")
	if i, e := os.Lstat(managed); e == nil && (!i.IsDir() || i.Mode()&os.ModeSymlink != 0) {
		return errors.New("SSH managed directory must not be a symlink")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = os.MkdirAll(managed, 0700); e != nil {
		return e
	}
	quote := func(s string) string {
		s = strings.ReplaceAll(s, "%", "%%")
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	text := fmt.Sprintf("Host %s\n  HostName %s\n  User %s\n  Port %d\n  ProxyCommand %s proxy-command --config %s --via %s %%h %%p\n", alias, host, user, n, quote(executable), quote(configPath), via)
	path := filepath.Join(managed, alias+".config")
	if _, e = os.Lstat(path); !os.IsNotExist(e) {
		return errors.New("SSH managed file already exists")
	}
	if e = writeNewAtomic(path, []byte(text)); e != nil {
		return e
	}
	const include = "Include ~/.ssh/och.d/*.config"
	// The include must precede existing Host/Match sections for all new aliases.
	present := false
	for _, line := range strings.Split(string(old), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if strings.EqualFold(fields[0], "Host") || strings.EqualFold(fields[0], "Match") {
			break
		}
		if strings.TrimSpace(line) == include {
			present = true
		}
	}
	if !present {
		current, readErr := os.ReadFile(main)
		if existed && (readErr != nil || !bytes.Equal(current, old)) || !existed && !os.IsNotExist(readErr) {
			os.Remove(path)
			return errors.New("SSH configuration changed during setup; existing edits were preserved")
		}
		if e = writeAtomic(main, append([]byte(include+"\n"), old...)); e != nil {
			os.Remove(path)
			return e
		}
	}
	return nil
}

func writeNewAtomic(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".och-ssh-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Link(f.Name(), path)
}
