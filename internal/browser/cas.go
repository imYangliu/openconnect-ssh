// Package browser captures CAS callbacks before a browser can consume the ticket.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/websocket"
)

const debuggerOrigin = "http://127.0.0.1"

type Options struct {
	Executable       string
	ProfileDirectory string
}

// OpenCAS opens an OCH-only Chromium window and returns an unconsumed CAS callback.
// An empty ProfileDirectory uses temporary state; an explicit private directory retains cookies.
func OpenCAS(ctx context.Context, loginURL string, options Options) (string, error) {
	gateway, domain, err := parseLoginURL(loginURL)
	if err != nil {
		return "", err
	}
	executable, err := findExecutable(options.Executable)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	profile, releaseProfile, err := prepareProfile(options.ProfileDirectory)
	if err != nil {
		return "", err
	}
	defer releaseProfile()
	// A previous browser's ephemeral port must never be mistaken for this instance.
	if err := os.Remove(filepath.Join(profile, "DevToolsActivePort")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("cannot reset the OCH browser debugger state")
	}
	cmd := exec.Command(executable,
		"--user-data-dir="+profile,
		"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0",
		"--remote-allow-origins="+debuggerOrigin,
		"--no-first-run", "--no-default-browser-check", "--disable-background-mode",
		"--new-window", "about:blank")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = browserEnvironment()
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = cmd.Start(); err != nil {
		return "", errors.New("cannot start CAS browser")
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer stopBrowser(cmd, done)
	startup, stopStartup := context.WithTimeout(ctx, 15*time.Second)
	port, err := waitDebugger(startup, profile, done)
	stopStartup()
	if err != nil {
		return "", err
	}
	endpoint, err := createTarget(ctx, port)
	if err != nil {
		return "", err
	}
	cfg, err := websocket.NewConfig(endpoint, debuggerOrigin)
	if err != nil {
		return "", errors.New("invalid local browser debugger endpoint")
	}
	ws, err := cfg.DialContext(ctx)
	if err != nil {
		return "", errors.New("cannot connect to local browser debugger")
	}
	defer ws.Close()
	defer func() {
		_ = ws.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
		_ = websocket.JSON.Send(ws, map[string]any{"id": 2147483647, "method": "Browser.close"})
	}()
	ws.MaxPayloadBytes = 1 << 20
	stopCancel := context.AfterFunc(ctx, func() { ws.Close() })
	defer stopCancel()
	return captureCAS(ctx, ws, loginURL, gateway, domain)
}

func parseLoginURL(raw string) (*url.URL, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.EscapedPath() != "/passport/v1/public/casLogin" {
		return nil, "", errors.New("CAS login must use an HTTPS gateway URL")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["sfDomain"]) != 1 || q.Get("sfDomain") == "" {
		return nil, "", errors.New("CAS login must specify one login domain")
	}
	return u, q.Get("sfDomain"), nil
}

func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() == "" {
			return "443"
		}
		return u.Port()
	}
	return a.Scheme == "https" && b.Scheme == "https" &&
		strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func callbackFor(raw string, gateway *url.URL, domain string) (bool, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return false, nil
	}
	if !sameOrigin(u, gateway) || u.Path != "/passport/v1/auth/cas" {
		return false, nil
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || u.User != nil || u.Fragment != "" || u.EscapedPath() != "/passport/v1/auth/cas" ||
		len(q["ticket"]) != 1 || strings.TrimSpace(q.Get("ticket")) == "" ||
		len(q["sfDomain"]) != 1 || q.Get("sfDomain") != domain {
		return true, errors.New("CAS callback does not match the requested login")
	}
	return true, nil
}

func findExecutable(explicit string) (string, error) {
	if explicit != "" {
		p, err := exec.LookPath(explicit)
		if err != nil {
			return "", errors.New("configured browser executable was not found")
		}
		return p, nil
	}
	var candidates []string
	if runtime.GOOS == "darwin" {
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	}
	candidates = append(candidates, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge")
	for _, candidate := range candidates {
		if p, err := exec.LookPath(candidate); err == nil {
			return p, nil
		}
	}
	return "", errors.New("Chrome, Chromium or Edge is required for CAS login; select it with --browser PATH")
}

func browserEnvironment() []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if key == "HOME" || key == "PATH" || key == "TMPDIR" || key == "LANG" || strings.HasPrefix(key, "LC_") ||
			key == "DISPLAY" || key == "WAYLAND_DISPLAY" || key == "XDG_RUNTIME_DIR" || key == "DBUS_SESSION_BUS_ADDRESS" {
			env = append(env, item)
		}
	}
	return env
}

func stopBrowser(cmd *exec.Cmd, done <-chan struct{}) {
	// Let Browser.close flush persistent cookies before terminating the owned process group.
	select {
	case <-done:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return
	case <-time.After(time.Second):
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func waitDebugger(ctx context.Context, profile string, done <-chan struct{}) (int, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		b, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if err == nil {
			line, _, _ := strings.Cut(string(b), "\n")
			port, err := strconv.Atoi(line)
			if err == nil && port > 0 && port <= 65535 {
				return port, nil
			}
		}
		select {
		case <-ctx.Done():
			return 0, errors.New("CAS browser did not become ready before cancellation or timeout")
		case <-done:
			return 0, errors.New("CAS browser exited before becoming ready")
		case <-ticker.C:
		}
	}
}

func createTarget(ctx context.Context, port int) (string, error) {
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, origin+"/json/new?about:blank", nil)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return "", errors.New("cannot create isolated CAS browser tab")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("browser rejected the CAS tab")
	}
	var target struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&target); err != nil {
		return "", errors.New("invalid browser target response")
	}
	u, err := url.Parse(target.WebSocketDebuggerURL)
	if err != nil || u.Scheme != "ws" || u.User != nil || u.Fragment != "" || u.RawQuery != "" ||
		u.Host != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) || !strings.HasPrefix(u.Path, "/devtools/page/") {
		return "", errors.New("browser debugger endpoint is not the expected loopback target")
	}
	return u.String(), nil
}

type cdpMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Error  json.RawMessage `json:"error"`
	Params struct {
		RequestID string `json:"requestId"`
		Request   struct {
			URL string `json:"url"`
		} `json:"request"`
	} `json:"params"`
}

func captureCAS(ctx context.Context, ws *websocket.Conn, loginURL string, gateway *url.URL, domain string) (string, error) {
	id := 0
	send := func(method string, params any) error {
		id++
		return websocket.JSON.Send(ws, map[string]any{"id": id, "method": method, "params": params})
	}
	if err := send("Fetch.enable", map[string]any{"patterns": []map[string]string{{"urlPattern": "*", "requestStage": "Request"}}}); err != nil {
		return "", errors.New("cannot enable CAS callback interception")
	}
	pendingID := 0
	pendingCallback := ""
	var pendingError error
	for {
		var message cdpMessage
		if err := websocket.JSON.Receive(ws, &message); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New("CAS browser closed before login completed")
		}
		if len(message.Error) > 0 && string(message.Error) != "null" {
			return "", errors.New("browser could not complete the CAS login operation")
		}
		if pendingID != 0 && message.ID == pendingID {
			if pendingError != nil {
				return "", pendingError
			}
			return pendingCallback, nil
		}
		if message.ID == 1 {
			if err := send("Page.navigate", map[string]string{"url": loginURL}); err != nil {
				return "", errors.New("cannot navigate to CAS login")
			}
			continue
		}
		if message.Method != "Fetch.requestPaused" {
			continue
		}
		match, invalid := callbackFor(message.Params.Request.URL, gateway, domain)
		if match || pendingID != 0 {
			if err := send("Fetch.failRequest", map[string]string{"requestId": message.Params.RequestID, "errorReason": "Aborted"}); err != nil {
				return "", errors.New("cannot stop browser from consuming the CAS callback")
			}
			if pendingID == 0 {
				pendingID, pendingCallback, pendingError = id, message.Params.Request.URL, invalid
			}
			continue
		}
		if err := send("Fetch.continueRequest", map[string]string{"requestId": message.Params.RequestID}); err != nil {
			return "", errors.New("cannot continue CAS browser navigation")
		}
	}
}
