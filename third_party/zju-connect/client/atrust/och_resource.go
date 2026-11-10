package atrust

import (
	"fmt"
	"net"

	"github.com/mythologyli/zju-connect/client"
)

// CheckL3Target uses the same authorization matcher as emitted L3 packets.
func (c *Client) CheckL3Target(ip net.IP, protocol string, port int) error {
	if protocol != "udp" && protocol != "icmp" {
		return fmt.Errorf("unsupported L3 probe protocol: %w", client.ErrResourceNotFound)
	}
	if _, ok := matchL3IPResource(c.resourceIndex, ip, protocol, port); !ok {
		return client.ErrResourceNotFound
	}
	return nil
}
