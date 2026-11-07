package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOCHRefreshOutlivesSetupContext(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"isLogin":1}}`))
	}))
	defer server.Close()
	setup, cancelSetup := context.WithCancel(context.Background())
	life, cancelLife := context.WithCancel(context.Background())
	defer cancelLife()
	s := NewSessionContext(setup, strings.TrimPrefix(server.URL, "https://"), nil)
	s.SetTLSConfig(server.Client().Transport.(*http.Transport).TLSClientConfig)
	s.Restore("fixture-device", "fixture-session", nil)
	s.SetContext(life)
	cancelSetup()
	got, err := s.Refresh(life)
	if err != nil {
		t.Fatalf("refresh inherited canceled setup context: %v", err)
	}
	if count.Load() != 1 || got.SID != "fixture-session" {
		t.Fatalf("refresh requests=%d, SID=%q", count.Load(), got.SID)
	}
}

func TestOCHPortalRedirectNormalizesHTTPSOrigin(t *testing.T) {
	query := url.Values{"data": {`{"ticket":"private-fixture"}`}}.Encode()
	for _, host := range []string{"vpn.example.edu", "vpn.example.edu:443", "VPN.EXAMPLE.EDU:443"} {
		for _, base := range []string{"vpn.example.edu", "vpn.example.edu:443"} {
			ticket, err := parsePortalTicketFromRedirect("https://"+host+"/portal/shortcut.html?"+query, base)
			if err != nil || ticket != "private-fixture" {
				t.Fatal(host, base, err)
			}
		}
	}
	for _, bad := range []string{
		"http://vpn.example.edu/portal/shortcut.html?" + query,
		"https://vpn.example.edu:444/portal/shortcut.html?" + query,
		"https://evil.example/portal/shortcut.html?" + query,
		"https://user@vpn.example.edu/portal/shortcut.html?" + query,
		"https://vpn.example.edu/portal/shortcut.html?" + query + "#fragment",
		"https://vpn.example.edu/portal/shortcut.html?" + query + "&" + query,
		"https://vpn.example.edu/portal/shortcut.html?data=%zz",
		"https://vpn.example.edu/portal/shortcut.html?data=private-fixture",
		"https://vpn.example.edu/%70ortal/shortcut.html?" + query,
	} {
		_, err := parsePortalTicketFromRedirect(bad, "vpn.example.edu")
		if err == nil || strings.Contains(err.Error(), "private-fixture") {
			t.Fatalf("unsafe redirect result: %v", err)
		}
	}
}
