package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

func TestLoginIPCTransfersChallengeAndCallback(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "och-login-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	c := config.Config{Socket: filepath.Join(dir, "och.sock")}
	listener, err := net.Listen("unix", c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReaderSize(conn, 16*1024)
		first, err := reader.ReadSlice('\n')
		if err != nil {
			done <- err
			return
		}
		var request Request
		if err = json.Unmarshal(first, &request); err != nil || request.Action != "login" || request.Via != "campus" {
			done <- errors.New("invalid login request")
			return
		}
		json.NewEncoder(conn).Encode(loginMessage{Kind: "cas", LoginURL: "https://vpn.example.edu/passport/v1/public/casLogin?sfDomain=example.edu"})
		response, err := readLoginMessage(reader)
		if err != nil || response.CallbackURL != "https://vpn.example.edu/passport/v1/auth/cas?sfDomain=example.edu&ticket=fixture" {
			done <- errors.New("callback changed")
			return
		}
		err = json.NewEncoder(conn).Encode(loginMessage{Authenticated: true})
		done <- err
	}()
	err = Login(context.Background(), c, "campus", vpn.BootstrapOptions{Browser: func(ctx context.Context, u string) (string, error) {
		if !strings.Contains(u, "/public/casLogin") {
			t.Error("login URL changed")
		}
		return "https://vpn.example.edu/passport/v1/auth/cas?sfDomain=example.edu&ticket=fixture", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestLoginIPCInstallsSharedSessionAndSanitizesFactoryErrors(t *testing.T) {
	var count atomic.Int32
	c, _ := startDaemon(t, config.Config{Profiles: map[string]config.Profile{"campus": {Protocol: "atrust", AuthType: "auth/cas"}}}, func(context.Context, config.Profile) (vpn.Session, error) {
		count.Add(1)
		return &testSession{dial: (&net.Dialer{}).DialContext}, nil
	})
	callback := func(context.Context, string) (string, error) { t.Error("unexpected browser challenge"); return "", nil }
	if err := Login(context.Background(), c, "campus", vpn.BootstrapOptions{Browser: callback}); err != nil {
		t.Fatal(err)
	}
	if err := Login(context.Background(), c, "campus", vpn.BootstrapOptions{Browser: callback}); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatal("recreated healthy session")
	}
	failed, _ := startDaemon(t, config.Config{Profiles: map[string]config.Profile{"campus": {Protocol: "atrust", AuthType: "auth/cas"}}}, func(context.Context, config.Profile) (vpn.Session, error) {
		return nil, errors.New("request https://vpn.example/cas?ticket=private-fixture failed")
	})
	err := Login(context.Background(), failed, "campus", vpn.BootstrapOptions{Browser: callback})
	if err == nil || strings.Contains(err.Error(), "private-fixture") || strings.Contains(err.Error(), "https://") {
		t.Fatal("unsafe error", err)
	}
}
func TestLoginClientCancellationClosesExchange(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "och-login-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	c := config.Config{Socket: filepath.Join(dir, "och.sock")}
	listener, err := net.Listen("unix", c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Login(ctx, c, "campus", vpn.BootstrapOptions{Browser: func(context.Context, string) (string, error) { return "", nil }})
	}()
	remote := <-accepted
	defer remote.Close()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled login succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("login ignored cancellation")
	}
}

func TestLoginIPCPreservesSafeFailureCategory(t *testing.T) {
	c, _ := startDaemon(t, config.Config{Profiles: map[string]config.Profile{"campus": {Protocol: "atrust", AuthType: "auth/cas"}}}, func(context.Context, config.Profile) (vpn.Session, error) {
		return nil, vpn.LoginFailure("auth_check_code", 123)
	})
	err := Login(context.Background(), c, "campus", vpn.BootstrapOptions{Browser: func(context.Context, string) (string, error) { return "", errors.New("unexpected browser") }})
	if err == nil || err.Error() != "aTrust authentication check failed with code 123" {
		t.Fatal(err)
	}
}

func TestLoginPasswordUsesPrivateIPCWithoutEcho(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "och-pass-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	c := config.Config{Socket: filepath.Join(dir, "och.sock")}
	listener, err := net.Listen("unix", c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if _, err = reader.ReadString('\n'); err != nil {
			done <- err
			return
		}
		json.NewEncoder(conn).Encode(loginMessage{Kind: "password"})
		response, err := readLoginMessage(reader)
		if err != nil {
			done <- err
			return
		}
		if response.Password != "private-fixture" || response.CallbackURL != "" {
			done <- errors.New("credential exchange changed")
			return
		}
		done <- json.NewEncoder(conn).Encode(loginMessage{Authenticated: true})
	}()
	err = Login(context.Background(), c, "campus", vpn.BootstrapOptions{PasswordPrompt: func(context.Context) (string, error) { return "private-fixture", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestEnsureWarmsSessionAndLogoutClosesIt(t *testing.T) {
	var creates atomic.Int32
	session := &testSession{dial: (&net.Dialer{}).DialContext}
	c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) { creates.Add(1); return session, nil })
	if err := Ensure(context.Background(), c, "campus"); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(context.Background(), c, "campus"); err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 1 {
		t.Fatal("ensure recreated session")
	}
	if err := Logout(context.Background(), c, "campus"); err != nil {
		t.Fatal(err)
	}
	if session.closed.Load() != 1 {
		t.Fatal("logout did not close session")
	}
}
