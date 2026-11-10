package ping

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestOCHProbeUsesValidHostPort(t *testing.T) {
	for _, host := range []string{"::1", "127.0.0.1", "vpn.internal"} {
		p := NewTCPing()
		p.SetTarget(&Target{Host: host, Port: 441, Timeout: time.Second})
		got := ""
		p.SetDialContext(func(_ context.Context, _, address string) (net.Conn, error) {
			got = address
			return nil, errors.New("fixture")
		})
		p.ping(context.Background())
		want := host + ":441"
		if host == "::1" {
			want = "[::1]:441"
		}
		if got != want {
			t.Errorf("probe endpoint %q, want %q", got, want)
		}
	}
}
