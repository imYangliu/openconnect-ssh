package atrust

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func l3StateFixture(t *testing.T) *l3TunnelConn {
	t.Helper()
	raw, peer := net.Pipe()
	c := &l3TunnelConn{tlsConn: tls.Client(raw, &tls.Config{}), closeCh: make(chan struct{}), conntrackMgr: newConntrackMgr(), heartbeatInterval: 5 * time.Millisecond, heartbeatMissLimit: 3}
	t.Cleanup(func() { c.Close(); peer.Close() })
	return c
}

func TestOCHHeartbeatClosesSilentPeer(t *testing.T) {
	c := l3StateFixture(t)
	var sent atomic.Int32
	c.writeFrameHook = func([]byte) error { sent.Add(1); return nil }
	done := make(chan struct{})
	go func() { defer close(done); c.heartbeatLoop() }()
	t.Cleanup(func() { c.Close(); <-done })
	select {
	case <-c.closeCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("silent peer stayed open: writes=%d misses=%d", sent.Load(), atomic.LoadInt32(&c.heartbeatMisses))
	}
	if sent.Load() != c.heartbeatMissLimit {
		t.Fatalf("sent %d probes before closing, want %d", sent.Load(), c.heartbeatMissLimit)
	}
}

func TestOCHHeartbeatKeepsRespondingPeerAlive(t *testing.T) {
	c := l3StateFixture(t)
	responses, server := io.Pipe()
	c.reader = bufio.NewReader(responses)
	enough := make(chan struct{})
	var sent int
	c.writeFrameHook = func(data []byte) error {
		if len(data) >= 2 && data[1] == cmdHeartbeatReq {
			if _, err := server.Write([]byte{l3Version, cmdHeartbeatResp, 0, 0}); err != nil {
				return err
			}
			sent++
			if sent == 6 {
				close(enough)
			}
		}
		return nil
	}
	readDone, heartbeatDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(readDone); c.readLoop() }()
	go func() { defer close(heartbeatDone); c.heartbeatLoop() }()
	t.Cleanup(func() { c.Close(); server.Close(); responses.Close(); <-readDone; <-heartbeatDone })
	select {
	case <-enough:
	case <-c.closeCh:
		t.Fatal("responding peer was closed")
	case <-time.After(2 * time.Second):
		t.Fatal("responding peer did not receive enough probes")
	}
	select {
	case <-c.closeCh:
		t.Fatal("responding peer was closed")
	default:
	}
}

func TestOCHHeartbeatPreservesConcurrentOrdinaryActivity(t *testing.T) {
	c := l3StateFixture(t)
	c.writeFrameHook = func(data []byte) error {
		if len(data) >= 2 && data[1] == cmdHeartbeatReq {
			return c.writeFrame([]byte{l3Version, cmdDataReq, 0, 0})
		}
		return nil
	}
	if !c.sendHeartbeat() {
		t.Fatal("heartbeat failed")
	}
	if atomic.LoadUint32(&c.heartbeatHasWrite) != 1 {
		t.Fatal("heartbeat erased concurrent ordinary activity")
	}
}

func authTupleFixture(t *testing.T, m *conntrackMgr, protocol int) *conntrack {
	t.Helper()
	meta := packetMeta{atype: 4, proto: protocol, srcIP: net.IPv4(10, 0, 0, 1), dstIP: net.IPv4(10, 0, 0, 2), srcPort: 4444, dstPort: 443}
	meta.key = connTrackKey(meta)
	ct := m.getOrCreate(meta.key, "app", "group")
	if _, err := m.cachePacket(ct, meta, []byte{1}); err != nil {
		t.Fatal(err)
	}
	return ct
}

func TestOCHAuthResponseFallbackMatchesProtocolAndRejectsAmbiguity(t *testing.T) {
	response := authIP{Atype: 0x800, SrcAddr: "10.0.0.1", DestAddr: "10.0.0.2", SrcPort: 4444, DestPort: 443}
	m := newConntrackMgr()
	udp := authTupleFixture(t, m, 17)
	response.Protocol = 6
	if m.getByAuthResponseIP(response) != nil {
		t.Fatal("TCP response matched UDP connection")
	}
	response.Protocol = 0
	if m.getByAuthResponseIP(response) != udp {
		t.Fatal("unique protocol-omitted fallback rejected")
	}
	tcp := authTupleFixture(t, m, 6)
	if m.getByAuthResponseIP(response) != nil {
		t.Fatal("ambiguous protocol-omitted response matched connection")
	}
	response.Protocol = 6
	if m.getByAuthResponseIP(response) != tcp {
		t.Fatal("TCP response did not match TCP connection")
	}
	response.Protocol = 17
	if m.getByAuthResponseIP(response) != udp {
		t.Fatal("UDP response did not match UDP connection")
	}
}

func TestOCHAuthMetadataRemainsStableDuringConcurrentFlush(t *testing.T) {
	m := newConntrackMgr()
	meta := packetMeta{atype: 4, proto: 6, srcIP: net.IPv4(10, 0, 0, 1), dstIP: net.IPv4(10, 0, 0, 2), srcPort: 4444, dstPort: 443}
	meta.key = connTrackKey(meta)
	packet := make([]byte, 40)
	packet[0], packet[2], packet[3], packet[9] = 0x45, 0, 40, 6
	copy(packet[12:16], []byte{10, 0, 0, 1})
	copy(packet[16:20], []byte{10, 0, 0, 2})
	binary.BigEndian.PutUint16(packet[20:22], 4444)
	binary.BigEndian.PutUint16(packet[22:24], 443)
	packet[32], packet[33] = 0x50, 0x10
	ct := m.getOrCreate(meta.key, "app", "group")
	for i := 0; i < defaultPendingPacketLimit; i++ {
		if _, err := m.cachePacket(ct, meta, packet); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{})
	var once sync.Once
	var writes atomic.Int32
	c := &l3TunnelConn{conntrackMgr: m, closeCh: make(chan struct{})}
	c.writeFrameHook = func([]byte) error { writes.Add(1); once.Do(func() { close(started) }); runtime.Gosched(); return nil }
	done := make(chan struct{})
	go func() { defer close(done); c.completeAuthentication(ct.authID, "token", nil) }()
	<-started
	var writers sync.WaitGroup
	for i := 0; i < 20; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if err := c.WritePacket(meta, "app", "group", packet); err != nil {
				t.Error(err)
			}
		}()
	}
	writers.Wait()
	<-done
	if ct.authMeta.proto != 6 || writes.Load() != defaultPendingPacketLimit+20 {
		t.Fatalf("metadata or packet delivery changed: protocol=%d writes=%d", ct.authMeta.proto, writes.Load())
	}
}
