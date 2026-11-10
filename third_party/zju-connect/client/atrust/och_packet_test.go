package atrust

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"

	"github.com/mythologyli/zju-connect/internal/zctcpip"
)

func metadataPacket(protocol byte, payloadLength int) []byte {
	packet := make([]byte, zctcpip.IPv4HeaderSize+payloadLength)
	packet[0] = 0x45
	packet[9] = protocol
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	copy(packet[12:16], []byte{10, 0, 0, 1})
	copy(packet[16:20], []byte{10, 0, 0, 2})
	if payloadLength >= 4 {
		binary.BigEndian.PutUint16(packet[20:22], 1234)
		binary.BigEndian.PutUint16(packet[22:24], 22)
	}
	return packet
}

func TestOCHIncomingShortTransportPacketDoesNotPanic(t *testing.T) {
	for _, protocol := range []byte{zctcpip.TCP, zctcpip.UDP} {
		packet := metadataPacket(protocol, 0)
		packets, _, err := splitIncomingIPPackets(packet)
		if err != nil || len(packets) != 1 {
			t.Fatalf("frame rejected unexpectedly: %v", err)
		}
		c := &l3TunnelConn{conntrackMgr: newConntrackMgr()}
		c.refreshIncomingConntrack(packets[0])
	}
}

func TestOCHMetadataRejectsMalformedPacketBoundaries(t *testing.T) {
	malformed := [][]byte{nil, make([]byte, 10), metadataPacket(zctcpip.TCP, 0), metadataPacket(zctcpip.TCP, 19), metadataPacket(zctcpip.UDP, 0), metadataPacket(zctcpip.UDP, 7)}
	for _, change := range []func([]byte){
		func(p []byte) { p[0] = 0x65 },
		func(p []byte) { p[0] = 0x44 },
		func(p []byte) { p[0] = 0x4f },
		func(p []byte) { binary.BigEndian.PutUint16(p[2:4], 19) },
		func(p []byte) { binary.BigEndian.PutUint16(p[2:4], uint16(len(p)+1)) },
	} {
		p := metadataPacket(zctcpip.TCP, 20)
		change(p)
		malformed = append(malformed, p)
	}
	for i, p := range malformed {
		if _, err := buildPacketMeta(p); err == nil {
			t.Errorf("accepted malformed packet %d", i)
		}
	}
}

func TestOCHMetadataAcceptsCompleteTransportHeaders(t *testing.T) {
	for _, protocol := range []byte{zctcpip.TCP, zctcpip.UDP, zctcpip.ICMP} {
		payload := 20
		if protocol != zctcpip.TCP {
			payload = 8
		}
		meta, err := buildPacketMeta(metadataPacket(protocol, payload))
		if err != nil {
			t.Fatal(err)
		}
		if !meta.srcIP.Equal(net.ParseIP("10.0.0.1")) || !meta.dstIP.Equal(net.ParseIP("10.0.0.2")) {
			t.Fatalf("addresses changed: %+v", meta)
		}
		if protocol != zctcpip.ICMP && (meta.srcPort != 1234 || meta.dstPort != 22) {
			t.Fatalf("ports changed: %+v", meta)
		}
	}
}

func TestOCHIPv6DeliveryDoesNotDependOnIPv4Metadata(t *testing.T) {
	packet := make([]byte, 40)
	packet[0] = 0x60
	packets, _, err := splitIncomingIPPackets(packet)
	if err != nil || len(packets) != 1 {
		t.Fatal(err)
	}
	c := &l3TunnelConn{conntrackMgr: newConntrackMgr(), incoming: make(chan []byte, 1), closeCh: make(chan struct{})}
	c.refreshIncomingConntrack(packets[0])
	if !c.deliverIncoming(packets[0]) {
		t.Fatal("IPv6 delivery rejected")
	}
	got, err := c.ReadPacket()
	if err != nil || !bytes.Equal(got, packet) {
		t.Fatalf("IPv6 packet changed: %v", err)
	}
}
