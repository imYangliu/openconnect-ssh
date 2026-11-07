package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestATrustDiscoveryIsPublicAndNormalizesPortal(t *testing.T) {
	var credentials atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			credentials.Store(true)
		}
		switch r.URL.Path {
		case "/public/manifest":
			w.Write([]byte(`{"code":0,"data":{"capacities":{"certInfo":{}}}}`))
		case "/passport/v1/public/authConfig":
			w.Write([]byte(`{"code":0,"data":{"csrfToken":"private-fixture","authServerInfoList":[{"authType":"auth/cas","loginDomain":"campus","authName":"SSO","loginUrl":"secret-url"},{"authType":"auth/qrcode","authName":"QR"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	u, _ := url.Parse(server.URL)
	client.Jar.SetCookies(u, []*http.Cookie{{Name: "secret", Value: "fixture"}})
	got, err := (Client{HTTP: client}).Discover(context.Background(), server.URL+"/portal/", "")
	if err != nil || got.Protocol != "atrust" || got.Server != server.URL || len(got.Methods) != 2 || !got.Methods[0].Supported || got.Methods[1].Supported {
		t.Fatal(got, err)
	}
	out, _ := json.Marshal(got)
	if strings.Contains(string(out), "private-fixture") || strings.Contains(string(out), "secret-url") || credentials.Load() {
		t.Fatal("discovery exposed or sent credentials")
	}
}
func TestHTMLIsNotDetectedAsAnyConnect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>ordinary website</html>")) }))
	defer server.Close()
	c := Client{HTTP: server.Client()}
	if _, err := c.Discover(context.Background(), server.URL, ""); !errors.Is(err, ErrUnknownProtocol) {
		t.Fatal(err)
	}
	r, err := c.Discover(context.Background(), server.URL, "anyconnect")
	if err != nil || r.Detected || r.Protocol != "anyconnect" {
		t.Fatal(r, err)
	}
}
func TestAnyConnectXMLGroups(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`<config-auth><auth><form><select name="group_list"><option value="students"/><option value="staff"/></select></form></auth></config-auth>`))
	}))
	defer server.Close()
	r, err := (Client{HTTP: server.Client()}).Discover(context.Background(), server.URL, "")
	if err != nil || !r.Detected || r.Protocol != "anyconnect" || strings.Join(r.Groups, ",") != "students,staff" {
		t.Fatal(r, err)
	}
}
func TestDiscoveryRejectsUntrustedTLSAndCrossOriginRedirect(t *testing.T) {
	var count atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
	defer destination.Close()
	if _, err := (Client{}).Discover(context.Background(), destination.URL, ""); err == nil {
		t.Fatal("accepted untrusted TLS")
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer redirect.Close()
	_, err := (Client{HTTP: redirect.Client()}).Discover(context.Background(), redirect.URL, "")
	if err == nil || count.Load() != 0 {
		t.Fatal("followed unrelated origin", err, count.Load())
	}
}
func TestNormalizeRejectsCredentialsAndUnsafeSchemes(t *testing.T) {
	for _, s := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/?ticket=secret", "https://example.com/#secret", "https://"} {
		if _, err := NormalizeServer(s); err == nil {
			t.Fatal("accepted", s)
		}
	}
	if v, err := NormalizeServer("VPN.EXAMPLE.COM/"); err != nil || v != "https://vpn.example.com" {
		t.Fatal(v, err)
	}
}
