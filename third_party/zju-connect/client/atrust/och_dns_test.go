package atrust

import (
	"net"
	"testing"
)

func TestOCHPublishedDNSNamesMatchNativeCanonicalLookup(t *testing.T) {
	c := NewClient(ClientOptions{})
	defer c.Close()
	body := []byte(`{"data":{"appList":{"data":{"appInfo":[{"apps":[{"id":"app","nodeGroupID":"group","accessModel":"L3VPN","addrPretend":false,"addressList":[{"protocol":"tcp","port":"22","host":"HOST.Internal.","ip":["10.2.3.4"]},{"protocol":"tcp","port":"22","host":"host.internal","ip":["10.2.3.5"]}]}]}]}}}}`)
	if err := c.parseResource(body); err != nil {
		t.Fatal(err)
	}
	records, err := c.DNSResource()
	if err != nil {
		t.Fatal(err)
	}
	ips := records["host.internal"]
	if len(records) != 1 || len(ips) != 2 || !ips[0].Equal(net.ParseIP("10.2.3.4")) || !ips[1].Equal(net.ParseIP("10.2.3.5")) {
		t.Fatalf("native lookup host.internal misses gateway published DNS mapping: %v", records)
	}
}
