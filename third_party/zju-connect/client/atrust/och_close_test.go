package atrust

import (
	"bufio"
	"net"
	"sync"
	"testing"
	"time"
)

type writeSignalConn struct {
	net.Conn
	entered chan struct{}
	once    sync.Once
}

func (c *writeSignalConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.entered) })
	return c.Conn.Write(p)
}

func TestOCHCloseUnblocksBlockedTCPTunnelWrite(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()
	signal := &writeSignalConn{Conn: raw, entered: make(chan struct{})}
	c := &tcpTunnelConn{tlsConn: signal, reader: bufio.NewReader(signal), reuse: true}
	writeDone := make(chan struct{})
	go func() { defer close(writeDone); c.Write([]byte("payload")) }()
	<-signal.entered
	closeDone := make(chan struct{})
	go func() { defer close(closeDone); c.Close() }()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		raw.Close()
		<-writeDone
		<-closeDone
		t.Fatal("tcpTunnelConn.Close blocks behind blocked Write instead of closing transport")
	}
	<-writeDone
}
