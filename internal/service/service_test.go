package service

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/daemon"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "serve" && os.Args[2] == "--config" {
		c, err := config.Load(os.Args[3])
		if err != nil {
			os.Exit(2)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		defer cancel()
		err = daemon.Serve(ctx, c, func(context.Context, config.Profile) (vpn.Session, error) {
			return nil, errors.New("fixture must not authenticate")
		}, nil)
		if err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestServiceDefinitionsContainOnlyPathsAndEscapeThem(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		path, b, err := Definition(platform, "/tmp/user", "/tmp/with spaces%$value/och", "/tmp/config%$value.yaml")
		if err != nil || path == "" {
			t.Fatal(path, err)
		}
		if strings.Contains(string(b), "PASSWORD") || strings.Contains(string(b), "cookie") {
			t.Fatal("secret in unit")
		}
		if platform == "darwin" {
			decoder := xml.NewDecoder(strings.NewReader(string(b)))
			for {
				_, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
		} else {
			if !strings.Contains(string(b), "%%$$value") {
				t.Fatal("systemd specifier/environment escaping missing", string(b))
			}
		}
	}
	if _, _, err := Definition("linux", "/tmp/home", "/tmp/och\nmalicious", "/tmp/config"); err == nil {
		t.Fatal("accepted multiline executable")
	}
}
func TestStartUsesIsolatedDaemonAndStopsCleanly(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "och-start-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.yaml")
	sock := filepath.Join(dir, "run", "och.sock")
	os.WriteFile(path, []byte("socket: "+sock+"\nprofiles: {}\n"), 0600)
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := os.Executable()
	if err = Start(context.Background(), executable, path, c); err != nil {
		t.Fatal(err)
	}
	defer func() {
		conn, _, _ := daemon.RequestControl(context.Background(), c, daemon.Request{Action: "stop"})
		if conn != nil {
			conn.Close()
		}
	}()
	if !IsRunning(context.Background(), c) {
		t.Fatal("not ready")
	}
	if err = Start(context.Background(), executable, path, c); err != nil {
		t.Fatal("idempotent start failed", err)
	}
	conn, _, err := daemon.RequestControl(context.Background(), c, daemon.Request{Action: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, e := net.DialTimeout("unix", sock, 20*time.Millisecond)
		if e != nil {
			return
		}
		conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon did not stop")
}
func TestStartRejectsPublicRuntimeFile(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "och-lock-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	c := config.Config{Socket: filepath.Join(dir, "och.sock")}
	os.WriteFile(c.Socket+".start.lock", nil, 0644)
	err = Start(context.Background(), "/does/not/exist", "/does/not/exist", c)
	if err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatal(err)
	}
}
