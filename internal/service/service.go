// Package service starts the user's OCH daemon without privileged network changes.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/daemon"
)

func IsRunning(ctx context.Context, c config.Config) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, _, err := daemon.RequestControl(ctx, c, daemon.Request{Action: "status"})
	if conn != nil {
		conn.Close()
	}
	return err == nil
}
func Start(ctx context.Context, executable, configPath string, c config.Config) error {
	if IsRunning(ctx, c) {
		return nil
	}
	dir := filepath.Dir(c.Socket)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("daemon runtime directory must be private (0700)")
	}
	lock, err := os.OpenFile(c.Socket+".start.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("cannot open daemon startup lock")
	}
	defer lock.Close()
	if err := privateFile(lock); err != nil {
		return err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return WaitReady(ctx, c, 10*time.Second)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if IsRunning(ctx, c) {
		return nil
	}
	log, err := os.OpenFile(filepath.Join(dir, "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	if err := privateFile(log); err != nil {
		return err
	}
	cmd := exec.Command(executable, "serve", "--config", configPath)
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return errors.New("could not start OCH service")
	}
	// Reap while setup is running; a successful daemon outlives this command.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			terminate(cmd, done)
			return ctx.Err()
		case <-deadline.C:
			terminate(cmd, done)
			return errors.New("OCH service did not become ready; inspect runtime service.log")
		case <-done:
			return errors.New("OCH service exited before becoming ready; inspect runtime service.log")
		case <-tick.C:
			if IsRunning(ctx, c) {
				return nil
			}
		}
	}
}
func WaitReady(ctx context.Context, c config.Config, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if IsRunning(ctx, c) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("another service start did not become ready")
		case <-tick.C:
		}
	}
}
func Label(configPath string) string {
	sum := sha256.Sum256([]byte(configPath))
	return fmt.Sprintf("dev.och.%x", sum[:6])
}
func Definition(goos, home, executable, configPath string) (string, []byte, error) {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(configPath) || strings.ContainsAny(executable+configPath, "\x00\r\n") {
		return "", nil, errors.New("service requires absolute single-line paths")
	}
	label := Label(configPath)
	switch goos {
	case "darwin":
		esc := func(s string) string { var b strings.Builder; xml.EscapeText(&b, []byte(s)); return b.String() }
		logs := filepath.Join(home, ".config", "och", "logs")
		text := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>serve</string><string>--config</string><string>%s</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>30</integer><key>Umask</key><integer>63</integer>
<key>EnvironmentVariables</key><dict><key>HOME</key><string>%s</string></dict>
<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, esc(label), esc(executable), esc(configPath), esc(home), esc(filepath.Join(logs, label+".log")), esc(filepath.Join(logs, label+".log")))
		return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), []byte(text), nil
	case "linux":
		quote := func(s string) string {
			return strconv.Quote(strings.ReplaceAll(strings.ReplaceAll(s, "%", "%%"), "$", "$$"))
		}
		text := fmt.Sprintf("[Unit]\nDescription=OCH native VPN service\nAfter=network-online.target\n\n[Service]\nExecStart=%s serve --config %s\nRestart=on-failure\nRestartSec=5\nUMask=0077\n\n[Install]\nWantedBy=default.target\n", quote(executable), quote(configPath))
		return filepath.Join(home, ".config", "systemd", "user", label+".service"), []byte(text), nil
	default:
		return "", nil, errors.New("automatic user service installation supports macOS and Linux")
	}
}
func Install(ctx context.Context, executable, configPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path, data, err := Definition(runtime.GOOS, home, executable, configPath)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(path); err == nil {
		return errors.New("user service definition already exists; update it explicitly instead of overwriting")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		logs := filepath.Join(home, ".config", "och", "logs")
		if err = os.MkdirAll(logs, 0700); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	var commands [][]string
	if runtime.GOOS == "darwin" {
		commands = [][]string{{"launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), path}}
	} else {
		commands = [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", Label(configPath) + ".service"}}
	}
	for _, args := range commands {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if _, err = cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("user service registration failed; definition remains at %s", path)
		}
	}
	return nil
}

// ReadStatus is a read-only readiness check, not proof of VPN authentication.
func ReadStatus(ctx context.Context, c config.Config) (daemon.Reply, error) {
	conn, reply, err := daemon.RequestControl(ctx, c, daemon.Request{Action: "status"})
	if conn != nil {
		conn.Close()
	}
	return reply, err
}

func privateFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || int(stat.Uid) != os.Getuid() {
		return errors.New("service runtime files must be owned private regular files (0600)")
	}
	return nil
}
func terminate(cmd *exec.Cmd, done <-chan error) {
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-time.After(2 * time.Second):
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
