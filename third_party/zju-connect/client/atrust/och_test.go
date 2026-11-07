package atrust

import (
	"errors"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
	"net"
	"testing"
)

func TestOCHInvalidSessionReturnsErrors(t *testing.T) {
	data := []byte(`{"code":10000004,"message":"fixture invalid session"}`)
	if err := parseIPAuthResponse(data); err == nil {
		t.Fatal("IP auth accepted invalid session")
	}
	if err := parseTCPTunnelAuthResponse(string(data)); err == nil {
		t.Fatal("TCP auth accepted invalid session")
	}
}
func TestOCHTunnelTLSVerifiesByDefault(t *testing.T) {
	if tunnelTLSConfig(nil).InsecureSkipVerify {
		t.Fatal("default tunnel skips TLS verification")
	}
	c := NewClient(ClientOptions{})
	defer c.Close()
	if c.tunnelTLSConfig().InsecureSkipVerify {
		t.Fatal("client skips TLS verification")
	}
}

func TestOCHSessionHealthReportsTerminalFailure(t *testing.T) {
	c := NewClient(ClientOptions{})
	defer c.Close()
	c.setSessionSID("fixture", nil)
	if err := c.SessionError(); err != nil {
		t.Fatal(err)
	}
	want := errors.New("fixture expired")
	c.setSessionSID("", want)
	if !errors.Is(c.SessionError(), want) {
		t.Fatal("missing terminal session error")
	}
	c.Close()
	if !errors.Is(c.SessionError(), net.ErrClosed) {
		t.Fatal("closed session reported healthy")
	}
}

func TestOCHIPExpiryIsDistinctFromResourceRefusal(t *testing.T) {
	for _, data := range []string{`{"code":10000004}`, `{"code":75500002}`} {
		if err := parseIPAuthResponse([]byte(data)); !errors.Is(err, auth.ErrSessionInvalid) {
			t.Fatal("invalid SID not classified", err)
		}
	}
	if err := parseIPAuthResponse([]byte(`{"code":12345,"message":"resource denied"}`)); err == nil || errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatal("resource refusal classified as session expiry", err)
	}
}
