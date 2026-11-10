package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOCHSessionRejectsHTTPSDowngradeRedirect(t *testing.T) {
	var received atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		data, _ := io.ReadAll(r.Body)
		if string(data) != "fixture-auth-body" {
			t.Errorf("body=%q", data)
		}
		w.Write([]byte("{}"))
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer secure.Close()
	u, _ := url.Parse(secure.URL)
	s := NewSessionContext(context.Background(), u.Host, nil)
	s.SetTLSConfig(secure.Client().Transport.(*http.Transport).TLSClientConfig)
	req, _ := http.NewRequest(http.MethodPost, secure.URL+"/passport/v1/auth/psw", strings.NewReader("fixture-auth-body"))
	resp, err := s.request(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || received.Load() != 0 {
		t.Fatalf("HTTPS auth redirect downgraded and replayed body: err=%v cleartext requests=%d", err, received.Load())
	}
}

func TestOCHSessionAllowsHTTPSRedirect(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		data, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || string(data) != "fixture-auth-body" {
			t.Errorf("redirected request changed: %s %q", r.Method, data)
		}
		w.Write([]byte("{}"))
	}))
	defer target.Close()
	gateway := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	gateway.TLS = target.TLS.Clone()
	gateway.StartTLS()
	defer gateway.Close()
	u, _ := url.Parse(gateway.URL)
	s := NewSessionContext(context.Background(), u.Host, nil)
	s.SetTLSConfig(target.Client().Transport.(*http.Transport).TLSClientConfig)
	req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/auth", strings.NewReader("fixture-auth-body"))
	resp, err := s.request(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err != nil || received.Load() != 1 {
		t.Fatalf("valid HTTPS redirect failed: %v count=%d", err, received.Load())
	}
}

func TestOCHRedirectPolicyDoesNotExposeRedirectURL(t *testing.T) {
	for _, location := range []string{
		"http://not-used.invalid/path?ticket=private-fixture-secret",
		"https://user:private-fixture-secret@not-used.invalid/path?ticket=private-fixture-secret",
		"/loop?ticket=private-fixture-secret",
	} {
		gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, location, http.StatusTemporaryRedirect)
		}))
		u, _ := url.Parse(gateway.URL)
		s := NewSessionContext(context.Background(), u.Host, nil)
		s.SetTLSConfig(gateway.Client().Transport.(*http.Transport).TLSClientConfig)
		req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/start", nil)
		resp, err := s.request(req)
		if resp != nil {
			resp.Body.Close()
		}
		gateway.Close()
		if err == nil || strings.Contains(err.Error(), "private-fixture-secret") {
			t.Fatalf("unsafe redirect result: %v", err)
		}
	}
}
