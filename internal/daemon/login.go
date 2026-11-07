package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

type loginMessage struct {
	LoginURL      string `json:"login_url,omitempty"`
	Kind          string `json:"kind,omitempty"`
	CallbackURL   string `json:"callback_url,omitempty"`
	Password      string `json:"password,omitempty"`
	Authenticated bool   `json:"authenticated,omitempty"`
	Error         string `json:"error,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	ErrorStatus   int    `json:"error_status,omitempty"`
}

func readLoginMessage(reader *bufio.Reader) (loginMessage, error) {
	var message loginMessage
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return message, errors.New("login exchange ended or exceeded its message limit")
	}
	if err = json.Unmarshal(line, &message); err != nil {
		return message, errors.New("invalid login message")
	}
	return message, nil
}

func (d *daemon) handleLogin(ctx context.Context, conn net.Conn, reader *bufio.Reader, via string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	conn.SetDeadline(time.Now().Add(5 * time.Minute))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	responses := make(chan loginMessage, 1)
	go func() {
		for {
			message, err := readLoginMessage(reader)
			if err != nil {
				cancel()
				return
			}
			select {
			case responses <- message:
			case <-ctx.Done():
				return
			}
		}
	}()
	callback := func(ctx context.Context, loginURL string) (string, error) {
		if err := json.NewEncoder(conn).Encode(loginMessage{Kind: "cas", LoginURL: loginURL}); err != nil {
			return "", errors.New("login client disconnected")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case response := <-responses:
			if response.CallbackURL == "" {
				return "", errors.New("browser did not return a CAS callback")
			}
			return response.CallbackURL, nil
		}
	}
	password := func(ctx context.Context) (string, error) {
		if err := json.NewEncoder(conn).Encode(loginMessage{Kind: "password"}); err != nil {
			return "", errors.New("login client disconnected")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case response := <-responses:
			if response.Password == "" {
				return "", errors.New("password input was empty")
			}
			return response.Password, nil
		}
	}
	err := d.sessions.Login(ctx, via, vpn.BootstrapOptions{Browser: callback, PasswordPrompt: password})
	if err != nil {
		// Backend errors can contain a single-use ticket inside an HTTP request URL.
		category, status := vpn.LoginFailureDetails(err)
		safe := vpn.LoginFailure(category, status)
		json.NewEncoder(conn).Encode(loginMessage{Error: safe.Error(), ErrorCode: category, ErrorStatus: status})
		return
	}
	json.NewEncoder(conn).Encode(loginMessage{Authenticated: true})
}

// Login performs an explicit interactive exchange, separate from the SSH data stream.
func Login(ctx context.Context, c config.Config, via string, options vpn.BootstrapOptions) error {
	if via == "" {
		return errors.New("login requires a VPN profile")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return errors.New("OCH service unavailable; start och serve before login")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(5 * time.Minute))
	if err = json.NewEncoder(conn).Encode(Request{Action: "login", Request: routing.Request{Via: via}}); err != nil {
		return errors.New("could not start login exchange")
	}
	reader := bufio.NewReaderSize(conn, 16*1024)
	for {
		message, err := readLoginMessage(reader)
		if err != nil {
			return err
		}
		if message.Error != "" {
			return vpn.LoginFailure(message.ErrorCode, message.ErrorStatus)
		}
		if message.Authenticated {
			return nil
		}
		if message.Kind == "password" {
			if options.PasswordPrompt == nil {
				return errors.New("password input is unavailable")
			}
			password, err := options.PasswordPrompt(ctx)
			if err != nil || password == "" {
				return errors.New("password input failed or was canceled")
			}
			if len(password) > 12*1024 {
				return errors.New("password is too large")
			}
			if err = json.NewEncoder(conn).Encode(loginMessage{Password: password}); err != nil {
				return errors.New("could not return login credential")
			}
			continue
		}
		if message.Kind != "cas" || message.LoginURL == "" || options.Browser == nil {
			return errors.New("invalid login challenge")
		}
		callback, err := options.Browser(ctx, message.LoginURL)
		if err != nil {
			return errors.New("CAS browser login failed or was canceled")
		}
		if len(callback) > 12*1024 {
			return errors.New("CAS callback is too large")
		}
		if err = json.NewEncoder(conn).Encode(loginMessage{CallbackURL: callback}); err != nil {
			return errors.New("could not return login callback")
		}
	}
}
