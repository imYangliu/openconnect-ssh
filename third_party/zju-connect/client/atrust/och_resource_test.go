package atrust

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/mythologyli/zju-connect/client"
	"github.com/mythologyli/zju-connect/internal/ipresource"
)

func TestOCHCheckL3TargetUsesResourceProtocolPortAndAddress(t *testing.T) {
	resources := []client.IPResource{
		{IPMin: net.ParseIP("10.20.0.1"), IPMax: net.ParseIP("10.20.0.5"), Protocol: "udp", PortMin: 53, PortMax: 54},
		{IPMin: net.ParseIP("10.20.0.10"), IPMax: net.ParseIP("10.20.0.10"), Protocol: "icmp"},
		{IPMin: net.ParseIP("10.20.0.20"), IPMax: net.ParseIP("10.20.0.20"), Protocol: "tcp", PortMin: 1, PortMax: 65535, EnableTCPPrefL3: true},
		{IPMin: net.ParseIP("10.20.0.30"), IPMax: net.ParseIP("10.20.0.30"), Protocol: "all", PortMin: 100, PortMax: 200},
	}
	c := &Client{resourceIndex: ipresource.New(resources)}
	for _, tc := range []struct {
		address, protocol string
		port              int
		allowed           bool
	}{
		{"10.20.0.1", "udp", 53, true},
		{"10.20.0.5", "udp", 54, true},
		{"10.20.0.1", "udp", 52, false},
		{"10.20.0.5", "udp", 55, false},
		{"10.20.0.6", "udp", 53, false},
		{"10.20.0.1", "icmp", -1, false},
		{"10.20.0.10", "icmp", -1, true},
		{"10.20.0.10", "udp", 53, false},
		{"10.20.0.20", "icmp", -1, false},
		{"10.20.0.20", "udp", 53, false},
		{"10.20.0.20", "tcp", 53, false},
		{"10.20.0.30", "udp", 100, true},
		{"10.20.0.30", "udp", 201, false},
		{"10.20.0.30", "icmp", -1, true},
		{"fd99::1", "udp", 53, false},
		{"fd99::1", "icmp", -1, false},
		{"not-an-IP", "icmp", -1, false},
	} {
		t.Run(fmt.Sprintf("%s/%s/%d", tc.address, tc.protocol, tc.port), func(t *testing.T) {
			err := c.CheckL3Target(net.ParseIP(tc.address), tc.protocol, tc.port)
			if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, client.ErrResourceNotFound) {
				t.Fatalf("CheckL3Target(%s, %s, %d) = %v, allowed=%v", tc.address, tc.protocol, tc.port, err, tc.allowed)
			}
		})
	}
}

func TestOCHCheckL3TargetWithNoResourcesDenies(t *testing.T) {
	for _, c := range []*Client{{}, {resourceIndex: ipresource.New(nil)}} {
		for _, protocol := range []string{"udp", "icmp"} {
			if err := c.CheckL3Target(net.ParseIP("10.20.0.1"), protocol, 53); !errors.Is(err, client.ErrResourceNotFound) {
				t.Fatalf("empty resource index accepted %s: %v", protocol, err)
			}
		}
	}
}
