package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func locked(fn func() error) error {
	if e := os.MkdirAll(stateDir(), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(stateDir(), "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	return fn()
}
func pidPath() string { return env("PID_FILE", filepath.Join(stateDir(), "openconnect.pid")) }
func activePID() string {
	b, e := os.ReadFile(pidPath())
	if e != nil {
		return ""
	}
	pid := strings.TrimSpace(string(b))
	n, e := strconv.Atoi(pid)
	if e != nil || n <= 1 {
		return ""
	}
	name, e := exec.Command("ps", "-p", pid, "-o", "comm=").Output()
	if e != nil || filepath.Base(strings.TrimSpace(string(name))) != "openconnect" {
		return ""
	}
	args, e := exec.Command("ps", "-ww", "-p", pid, "-o", "args=").Output()
	if e != nil || !strings.Contains(" "+strings.TrimSpace(string(args))+" ", " --pid-file="+pidPath()+" ") {
		return ""
	}
	return pid
}
func sudoArgs() []string {
	if os.Getenv("SUDO_ASKPASS") != "" {
		return []string{"-A"}
	}
	return []string{"-n"}
}

// Helpers own the VPN state and credentials. Keep their messages off the SSH stream.
func helperAction(path, action string) error {
	cmd := exec.Command("sudo", "-n", "--", path, action)
	cmd.Stdout = os.Stderr
	if action == "status" {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("VPN helper %s: %w", action, err)
	}
	return nil
}

func connect(c config) error {
	if c.VPN.Helper != "" {
		return helperAction(c.VPN.Helper, "connect")
	}
	if e := c.checkVPN(); e != nil {
		return e
	}
	if activePID() != "" {
		return nil
	}
	bin, e := exec.LookPath("openconnect")
	if e != nil {
		return e
	}
	password := os.Getenv("VPN_PASSWORD")
	if password == "" {
		password, e = readSecret()
		if e != nil {
			return e
		}
	}
	if password == "" && runtime.GOOS == "darwin" {
		b, e := exec.Command("/usr/bin/security", "find-generic-password", "-s", env("OCH_KEYCHAIN_SERVICE", "och"), "-a", c.VPN.User, "-w").Output()
		if e == nil {
			password = strings.TrimRight(string(b), "\r\n")
		}
	}
	if password == "" {
		return errors.New("缺少 VPN 密码，请运行 och setup 或设置 VPN_PASSWORD")
	}
	args := append(sudoArgs(), "env")
	extra := ""
	if c.Routes.Mode == "extra" {
		extra = strings.Join(c.Routes.Extra, " ")
	}
	args = append(args, "OCH_ROUTES_EXTRA="+extra, "OCH_DNS_MODE="+c.DNS.Mode, bin, "--user="+c.VPN.User, "--os=win", "--useragent=AnyConnect", "--passwd-on-stdin", "--background", "--pid-file="+pidPath())
	if c.VPN.Group != "" {
		args = append(args, "--authgroup="+c.VPN.Group)
	}
	if runtime.GOOS == "darwin" && (extra != "" || c.DNS.Mode == "ignore") {
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		wrapper := filepath.Join(filepath.Dir(exe), "../libexec/och/macos-vpnc-route-wrapper.sh")
		if _, e = os.Stat(wrapper); e != nil {
			return fmt.Errorf("缺少路由脚本: %w", e)
		}
		args = append(args, "--script", wrapper)
	}
	args = append(args, "--", c.VPN.Host)
	log, e := os.OpenFile(logPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd := exec.Command("sudo", args...)
	cmd.Stdin = strings.NewReader(password + "\n")
	cmd.Stdout = log
	cmd.Stderr = log
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("启动 VPN 失败，请检查 sudo -v 授权和日志 %s: %w", logPath(), e)
	}
	return nil
}
func disconnect() error {
	pid := activePID()
	if pid == "" {
		fmt.Println("VPN 未连接")
		return nil
	}
	return interactive("sudo", append(sudoArgs(), "kill", "-TERM", pid)...)
}
