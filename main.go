package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const help = `och — OpenConnect + SSH

  och setup                    配置 VPN 和可选的 SSH 别名
  ssh <别名>                   直接连接，必要时启动 VPN
  och vpn connect|disconnect   手动连接或断开
  och vpn status|verify|logs   查看连接、检查目标或查看日志
  och doctor                   检查配置和依赖
  och proxy-command HOST PORT  OpenSSH ProxyCommand
  och update                   更新已发布的 CLI`

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "och:", e)
		os.Exit(1)
	}
}
func run(a []string) error {
	if len(a) == 0 || len(a) == 1 && (a[0] == "--help" || a[0] == "-h" || a[0] == "help") {
		fmt.Println(help)
		return nil
	}
	if len(a) == 1 && a[0] == "update" {
		return interactive("bash", "-c", `set -euo pipefail; curl -fsSL "$1" | bash -s -- --update`, "och", env("OCH_INSTALL_URL", "https://raw.githubusercontent.com/imyangliu/openconnect-ssh/main/install.sh"))
	}
	c, e := readConfig()
	if e != nil {
		return e
	}
	switch {
	case len(a) == 1 && a[0] == "setup":
		return setup(c)
	case len(a) == 1 && (a[0] == "doctor" || a[0] == "d"):
		return doctor(c)
	case len(a) == 3 && (a[0] == "proxy-command" || a[0] == "--proxy-command"):
		return proxy(c, a[1], a[2])
	case len(a) == 2 && (a[0] == "vpn" || a[0] == "v"):
		switch a[1] {
		case "connect", "c":
			return locked(func() error { return connect(c) })
		case "disconnect", "x":
			if c.VPN.Helper != "" {
				return helperAction(c.VPN.Helper, "disconnect")
			}
			return locked(disconnect)
		case "status", "s":
			if c.VPN.Helper != "" {
				return helperAction(c.VPN.Helper, "status")
			}
			printStatus(c)
			return nil
		case "verify", "v":
			conn, e := dial(c.SSH.Target, c.SSH.Port)
			if e != nil {
				return e
			}
			conn.Close()
			fmt.Println("目标端口可达")
			return nil
		case "logs", "l":
			if c.VPN.Helper != "" {
				return errors.New("VPN 日志由 helper 管理，请运行 och vpn status 查看状态")
			}
			return interactive("tail", "-n", "80", logPath())
		case "ssh":
			if c.SSH.Host == "" {
				return errors.New("请先配置 SSH 别名")
			}
			return interactive("ssh", "--", c.SSH.Host)
		}
	}
	return errors.New("未知命令或参数，请运行 och --help")
}
func doctor(c config) error {
	if c.VPN.Helper != "" {
		if e := helperAction(c.VPN.Helper, "status"); e != nil {
			return e
		}
	} else {
		if e := c.checkVPN(); e != nil {
			return e
		}
		for _, tool := range []string{"ssh", "sudo", "openconnect"} {
			if _, e := exec.LookPath(tool); e != nil {
				return e
			}
		}
		if _, e := readSecret(); e != nil {
			return e
		}
	}
	if c.SSH.Target != "" {
		out, e := exec.Command("ssh", "-G", "--", c.SSH.Host).Output()
		if e != nil || !strings.Contains(string(out), " proxy-command %h %p") {
			return errors.New("SSH 别名未使用 OCH，请运行 och setup")
		}
	}
	fmt.Println("配置、依赖检查通过")
	return nil
}

func interactive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func endpoint(host, port string) (string, error) {
	if host == "" {
		return "", errors.New("未设置 [ssh].target_host")
	}
	p, e := strconv.Atoi(port)
	if e != nil || p < 1 || p > 65535 {
		return "", errors.New("无效 SSH 端口")
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port), nil
}
func dial(h, p string) (net.Conn, error) {
	a, e := endpoint(h, p)
	if e != nil {
		return nil, e
	}
	return net.DialTimeout("tcp", a, 3*time.Second)
}
func proxy(c config, h, p string) error {
	if _, e := endpoint(h, p); e != nil {
		return e
	}
	conn, e := dial(h, p)
	if e != nil {
		fmt.Fprintln(os.Stderr, "[och] 目标暂不可达，检查 VPN")
		e = locked(func() error { return connect(c) })
		if e != nil {
			return e
		}
		until := time.Now().Add(15 * time.Second)
		for {
			conn, e = dial(h, p)
			if e == nil {
				break
			}
			if time.Now().After(until) {
				return fmt.Errorf("VPN 已启动，但目标仍不可达: %w", e)
			}
			time.Sleep(400 * time.Millisecond)
		}
	}
	defer conn.Close()
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		if err != nil {
			conn.Close()
			return
		}
		conn.(*net.TCPConn).CloseWrite()
	}()
	_, e = io.Copy(os.Stdout, conn)
	return e
}
func logPath() string { return env("LOG_FILE", filepath.Join(stateDir(), "openconnect.log")) }

func printStatus(c config) {
	lang := env("OCH_APP_LANGUAGE", c.App.Language)
	if lang == "system" || lang == "" {
		lang = env("LC_ALL", env("LC_MESSAGES", os.Getenv("LANG")))
	}
	pid := activePID()
	if strings.HasPrefix(lang, "zh") {
		if pid == "" {
			fmt.Println("VPN 未连接")
		} else {
			fmt.Println("VPN 已连接，PID", pid)
		}
		if c.SSH.Target == "" {
			fmt.Println("未配置 [ssh].target_host")
		}
	} else {
		if pid == "" {
			fmt.Println("VPN disconnected")
		} else {
			fmt.Println("VPN connected, PID:", pid)
		}
		if c.SSH.Target == "" {
			fmt.Println("[ssh].target_host is not configured")
		}
	}
}
