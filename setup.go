package main

import (
	"bufio"

	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/term"
)

func setup(c config) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("och setup 需要交互式终端")
	}
	reader := bufio.NewReader(os.Stdin)
	prompt := func(label string, value *string) error {
		fmt.Print(label)
		if *value != "" {
			fmt.Printf(" [%s]", *value)
		}
		fmt.Print(": ")
		s, e := reader.ReadString('\n')
		if e != nil {
			return e
		}
		if s = strings.TrimSpace(s); s != "" {
			*value = s
		}
		return nil
	}
	for _, f := range []struct {
		label string
		v     *string
	}{{"VPN 网关", &c.VPN.Host}, {"VPN 用户", &c.VPN.User}, {"认证组（可留空）", &c.VPN.Group}} {
		if e := prompt(f.label, f.v); e != nil {
			return e
		}
	}
	if e := c.checkVPN(); e != nil {
		return e
	}
	password, e := readSecret()
	if e != nil {
		return e
	}
	fmt.Print("VPN 密码（有已存密码时回车保留）: ")
	b, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if e != nil {
		return e
	}
	if len(b) > 0 {
		password = string(b)
	}
	if password == "" {
		return errors.New("VPN 密码为空")
	}
	target := c.SSH.Target
	if target == "" {
		target = "-"
	}
	if e := prompt("SSH 地址或已有别名（- 跳过）", &target); e != nil {
		return e
	}
	if target != "-" {
		if !sshToken(target) {
			return errors.New("无效 SSH 地址")
		}
		if target != c.SSH.Target {
			out, e := exec.Command("ssh", "-G", "--", target).Output()
			if e != nil {
				return e
			}
			c.SSH.Target = target
			c.SSH.Host = "och-target"
			for _, line := range strings.Split(string(out), "\n") {
				f := strings.Fields(line)
				if len(f) != 2 {
					continue
				}
				switch f[0] {
				case "hostname":
					c.SSH.Target = f[1]
					if target != f[1] {
						c.SSH.Host = "och-" + target
					}
				case "user":
					c.SSH.User = f[1]
				case "port":
					c.SSH.Port = f[1]
				}
			}
		}
		if c.SSH.Host == "" {
			c.SSH.Host = "och-target"
		}
		for _, f := range []struct {
			label string
			v     *string
		}{{"托管 SSH 别名", &c.SSH.Host}, {"SSH 用户", &c.SSH.User}, {"SSH 端口", &c.SSH.Port}} {
			if e := prompt(f.label, f.v); e != nil {
				return e
			}
		}
		if e := writeSSH(c); e != nil {
			return e
		}
	}
	cfg, e := encode(c)
	if e != nil {
		return e
	}
	secret, e := encode(map[string]string{"VPN_PASSWORD": password})
	if e != nil {
		return e
	}
	if e = writePrivate(secretPath(), secret); e != nil {
		return e
	}
	if e = writePrivate(configPath(), cfg); e != nil {
		return e
	}
	if target == "-" {
		fmt.Println("下一步运行 och vpn connect")
	} else {
		fmt.Println("下一步运行 ssh", c.SSH.Host)
	}
	return nil
}

var validToken = regexp.MustCompile(`^[a-zA-Z0-9_.:@%+-]+$`)

func sshToken(s string) bool { return validToken.MatchString(s) && !strings.HasPrefix(s, "-") }
func writeSSH(c config) error {
	if !sshToken(c.SSH.Host) || !sshToken(c.SSH.Target) || !sshToken(c.SSH.User) {
		return errors.New("无效 SSH 别名、地址或用户")
	}
	if _, e := endpoint(c.SSH.Target, c.SSH.Port); e != nil {
		return e
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	if strings.ContainsAny(exe, "\n\r") {
		return errors.New("无效程序路径")
	}
	exe = "'" + strings.ReplaceAll(strings.ReplaceAll(exe, "%", "%%"), "'", "'\\''") + "'"
	text := fmt.Sprintf("Host %s\n  HostName %s\n  User %s\n  Port %s\n  ProxyCommand %s proxy-command %%h %%p\n  ServerAliveInterval 30\n  ServerAliveCountMax 3\n", c.SSH.Host, c.SSH.Target, c.SSH.User, c.SSH.Port, exe)
	dir := filepath.Join(os.Getenv("HOME"), ".ssh")
	main := filepath.Join(dir, "config")
	old, e := os.ReadFile(main)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".och-check-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.WriteString(text)
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if out, e := exec.Command("ssh", "-F", f.Name(), "-G", "--", c.SSH.Host).CombinedOutput(); e != nil {
		return fmt.Errorf("SSH 配置无效: %s", out)
	}
	if e = writePrivate(filepath.Join(dir, "och.config"), []byte(text)); e != nil {
		return e
	}
	const include = "Include ~/.ssh/och.config"
	for _, line := range strings.Split(string(old), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.EqualFold(fields[0], "Host") || strings.EqualFold(fields[0], "Match") {
			break
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "Include") && fields[1] == "~/.ssh/och.config" {
			return nil
		}
	}
	if resolved, err := filepath.EvalSymlinks(main); err == nil {
		main = resolved
	}
	return writePrivate(main, append([]byte(include+"\n"), old...))
}
