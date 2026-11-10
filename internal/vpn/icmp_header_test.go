package vpn

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNativeEchoRejectsCorruptIPv4Header(t *testing.T) {
	s := packetTestImmediateEcho(t, false, func(request []byte) []byte {
		reply := packetTestEchoReply(request, nil)
		reply[8] ^= 1
		return reply
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := packetTestManager(t, s).Ping(ctx, "fixture", "10.99.0.2", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("corrupt IPv4 header accepted: %v", err)
	}
}
