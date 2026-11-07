package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestCASCallbackValidation(t *testing.T) {
	gateway, domain, err := parseLoginURL("https://vpn.example/passport/v1/public/casLogin?sfDomain=campus")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"https://vpn.example/passport/v1/auth/cas?sfDomain=campus&ticket=ST-fixture",
		"https://VPN.EXAMPLE:443/passport/v1/auth/cas?ticket=ST-fixture&sfDomain=campus",
	} {
		match, err := callbackFor(raw, gateway, domain)
		if !match || err != nil {
			t.Fatal("valid callback rejected")
		}
	}
	for _, raw := range []string{
		"https://vpn.example.evil/passport/v1/auth/cas?sfDomain=campus&ticket=ST-fixture",
		"http://vpn.example/passport/v1/auth/cas?sfDomain=campus&ticket=ST-fixture",
		"https://vpn.example:444/passport/v1/auth/cas?sfDomain=campus&ticket=ST-fixture",
		"https://vpn.example/other?sfDomain=campus&ticket=ST-fixture",
	} {
		match, _ := callbackFor(raw, gateway, domain)
		if match {
			t.Fatal("foreign callback accepted")
		}
	}
	for _, raw := range []string{
		"https://vpn.example/passport/v1/auth/cas?sfDomain=campus&ticket=private-fixture&ticket=duplicate",
		"https://vpn.example/passport/v1/auth/cas?sfDomain=other&ticket=private-fixture",
		"https://vpn.example/passport/v1/auth/cas?sfDomain=campus&sfDomain=campus&ticket=private-fixture",
		"https://vpn.example/passport/v1/auth/cas?sfDomain=campus&ticket=",
		"https://vpn.example/passport/v1/auth/cas?sfDomain=campus&ticket=private-fixture#fragment",
		"https://user@vpn.example/passport/v1/auth/cas?sfDomain=campus&ticket=private-fixture",
		"https://vpn.example/passport/v1/auth/%63as?sfDomain=campus&ticket=private-fixture",
	} {
		match, err := callbackFor(raw, gateway, domain)
		if !match || err == nil {
			t.Fatal("invalid callback not rejected")
		}
		if strings.Contains(err.Error(), "private-fixture") {
			t.Fatal("ticket leaked")
		}
	}
}

func TestCASInvalidLoginAndSecretEnvironment(t *testing.T) {
	for _, raw := range []string{"http://vpn.example/?sfDomain=x", "https://user:secret@vpn.example/?sfDomain=x", "https://vpn.example/", "https://vpn.example/?sfDomain=x&sfDomain=y"} {
		if _, _, err := parseLoginURL(raw); err == nil {
			t.Fatal("invalid login accepted")
		}
	}
	t.Setenv("VPN_PASSWORD", "private-fixture")
	t.Setenv("OCH_TOKEN", "private-fixture")
	for _, value := range browserEnvironment() {
		if strings.Contains(value, "private-fixture") {
			t.Fatal("credential inherited by browser")
		}
	}
}

type testCommand struct {
	ID     int                        `json:"id"`
	Method string                     `json:"method"`
	Params map[string]json.RawMessage `json:"params"`
}

func TestCaptureContinuesUnrelatedRequestsAndAbortsTicket(t *testing.T) {
	login := "https://vpn.example/passport/v1/public/casLogin?sfDomain=campus"
	callback := "https://vpn.example:443/passport/v1/auth/cas?sfDomain=campus&ticket=ST-fixture"
	gateway, domain, _ := parseLoginURL(login)
	observed := make(chan []string, 1)
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		var methods []string
		read := func() testCommand {
			var c testCommand
			if websocket.JSON.Receive(ws, &c) != nil {
				return c
			}
			methods = append(methods, c.Method)
			return c
		}
		first := read()
		websocket.JSON.Send(ws, map[string]any{"id": first.ID, "result": map[string]any{}})
		navigate := read()
		websocket.JSON.Send(ws, map[string]any{"id": navigate.ID, "result": map[string]any{}})
		websocket.JSON.Send(ws, map[string]any{"method": "Fetch.requestPaused", "params": map[string]any{"requestId": "ordinary", "request": map[string]string{"url": "https://sso.example/login"}}})
		read()
		websocket.JSON.Send(ws, map[string]any{"method": "Fetch.requestPaused", "params": map[string]any{"requestId": "callback", "request": map[string]string{"url": callback}}})
		last := read()
		var reason string
		json.Unmarshal(last.Params["errorReason"], &reason)
		if reason != "Aborted" {
			methods = append(methods, "wrong-abort-reason")
		}
		websocket.JSON.Send(ws, map[string]any{"id": last.ID, "result": map[string]any{}})
		observed <- methods
	}))
	defer server.Close()
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(server.URL, "http"), "", debuggerOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	defer stop()
	got, err := captureCAS(ctx, ws, login, gateway, domain)
	if err != nil || got != callback {
		t.Fatal("capture failed", err)
	}
	select {
	case methods := <-observed:
		if strings.Join(methods, ",") != "Fetch.enable,Page.navigate,Fetch.continueRequest,Fetch.failRequest" {
			t.Fatal(methods)
		}
	case <-ctx.Done():
		t.Fatal("no interception evidence")
	}
}

func TestCaptureCancellationUnblocksRead(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		var c testCommand
		websocket.JSON.Receive(ws, &c)
		var next testCommand
		websocket.JSON.Receive(ws, &next)
	}))
	defer server.Close()
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(server.URL, "http"), "", debuggerOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ctx, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	defer stop()
	gateway, domain, _ := parseLoginURL("https://vpn.example/passport/v1/public/casLogin?sfDomain=campus")
	done := make(chan error, 1)
	go func() { _, err := captureCAS(ctx, ws, gateway.String(), gateway, domain); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled login succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not unblock debugger")
	}
}

func TestTargetRejectsForeignDebuggerEndpoint(t *testing.T) {
	for _, endpoint := range []string{"ws://evil.example/devtools/page/one", "ws://127.0.0.1:1/devtools/page/one", "file:///tmp/one"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": endpoint})
		}))
		u, _ := url.Parse(server.URL)
		_, p, _ := net.SplitHostPort(u.Host)
		port, _ := strconv.Atoi(p)
		if _, err := createTarget(context.Background(), port); err == nil {
			t.Fatal("foreign debugger accepted")
		}
		server.Close()
	}
}

func TestBrowserCancellationRemovesPrivateProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	executable := filepath.Join(root, "fixture-browser")
	// A dedicated fixture process never starts a real browser or touches a profile.
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := OpenCAS(ctx, "https://vpn.example/passport/v1/public/casLogin?sfDomain=campus", Options{Executable: executable})
	if err == nil {
		t.Fatal("fixture login unexpectedly completed")
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "och-cas-browser-") {
			t.Fatal("browser profile survived cleanup")
		}
	}
}

func TestDebuggerWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitDebugger(ctx, t.TempDir(), make(chan struct{}))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrong cancellation result")
	}
}

func TestCaptureRequiresAbortAcknowledgement(t *testing.T) {
	for _, mode := range []string{"rejected", "missing"} {
		t.Run(mode, func(t *testing.T) {
			login := "https://vpn.example/passport/v1/public/casLogin?sfDomain=campus"
			gateway, domain, _ := parseLoginURL(login)
			server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
				defer ws.Close()
				var command testCommand
				websocket.JSON.Receive(ws, &command)
				websocket.JSON.Send(ws, map[string]any{"id": command.ID, "result": map[string]any{}})
				websocket.JSON.Receive(ws, &command)
				websocket.JSON.Send(ws, map[string]any{"method": "Fetch.requestPaused", "params": map[string]any{"requestId": "callback", "request": map[string]string{"url": "https://vpn.example/passport/v1/auth/cas?sfDomain=campus&ticket=private-fixture"}}})
				websocket.JSON.Receive(ws, &command)
				if mode == "rejected" {
					websocket.JSON.Send(ws, map[string]any{"id": command.ID, "error": map[string]any{"message": "private-fixture"}})
				} else {
					websocket.JSON.Receive(ws, &command)
				}
			}))
			defer server.Close()
			ws, err := websocket.Dial("ws"+strings.TrimPrefix(server.URL, "http"), "", debuggerOrigin)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			stop := context.AfterFunc(ctx, func() { ws.Close() })
			defer stop()
			callback, err := captureCAS(ctx, ws, login, gateway, domain)
			if err == nil || callback != "" {
				t.Fatal("ticket handed off without successful abort acknowledgement")
			}
			if strings.Contains(err.Error(), "private-fixture") {
				t.Fatal("ticket leaked through debugger error")
			}
		})
	}
}
