package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/routing"
)

const maxSOCKSUDPDatagram = 65507

type socksUDPOptions struct {
	maxFlows     int
	queueSize    int
	idleTimeout  time.Duration
	dialTimeout  time.Duration
	writeTimeout time.Duration
}

func defaultSOCKSUDPOptions() socksUDPOptions {
	return socksUDPOptions{maxFlows: 64, queueSize: 8, idleTimeout: time.Minute, dialTimeout: 10 * time.Second, writeTimeout: 5 * time.Second}
}

type socksUDPFlow struct {
	queue        chan []byte
	done         chan struct{}
	cancel       context.CancelFunc
	lastActivity atomic.Int64
}

type socksUDPRelay struct {
	conn     *net.UDPConn
	sessions Dialer
	via      string
	options  socksUDPOptions
	flows    map[string]*socksUDPFlow
	workers  sync.WaitGroup
}

func readSOCKSAddress(reader io.Reader, atyp byte) (string, string, error) {
	var host string
	switch atyp {
	case 1, 4:
		length := net.IPv4len
		if atyp == 4 {
			length = net.IPv6len
		}
		address := make([]byte, length)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", "", err
		}
		host = net.IP(address).String()
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return "", "", err
		}
		if length[0] == 0 {
			return "", "", errors.New("empty SOCKS domain")
		}
		address := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", "", err
		}
		host = string(address)
	default:
		return "", "", errors.New("unsupported SOCKS address type")
	}
	var port [2]byte
	if _, err := io.ReadFull(reader, port[:]); err != nil {
		return "", "", err
	}
	return host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))), nil
}

func decodeSOCKSUDP(frame []byte) (string, []byte, error) {
	if len(frame) < 4 || len(frame) > maxSOCKSUDPDatagram || frame[0] != 0 || frame[1] != 0 || frame[2] != 0 {
		return "", nil, errors.New("invalid or fragmented SOCKS UDP frame")
	}
	reader := bytes.NewReader(frame[4:])
	host, port, err := readSOCKSAddress(reader, frame[3])
	if err != nil {
		return "", nil, err
	}
	if err := routing.ValidateTarget(host, port); err != nil {
		return "", nil, err
	}
	return net.JoinHostPort(host, port), frame[len(frame)-reader.Len():], nil
}

func numericSOCKSAddress(addr net.Addr) ([]byte, error) {
	host, portString, err := net.SplitHostPort(addr.String())
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	port, err := strconv.Atoi(portString)
	if ip == nil || err != nil || port < 1 || port > 65535 {
		return nil, errors.New("SOCKS UDP endpoint must be numeric")
	}
	var address []byte
	if v4 := ip.To4(); v4 != nil {
		address = append([]byte{1}, v4...)
	} else {
		address = append([]byte{4}, ip.To16()...)
	}
	return binary.BigEndian.AppendUint16(address, uint16(port)), nil
}

func handleSOCKSUDP(ctx context.Context, control net.Conn, via string, sessions Dialer, atyp byte, host, port string, options socksUDPOptions) {
	peer, ok := control.RemoteAddr().(*net.TCPAddr)
	local, localOK := control.LocalAddr().(*net.TCPAddr)
	sourceIP := net.ParseIP(host)
	if atyp == 3 || !ok || !localOK || sourceIP == nil || !local.IP.IsLoopback() || (!sourceIP.IsUnspecified() && !sourceIP.Equal(peer.IP)) {
		socksReply(control, 8)
		return
	}
	control.SetDeadline(time.Now().Add(65 * time.Second))
	associationCtx, cancel := context.WithCancel(ctx)
	stopControl := context.AfterFunc(associationCtx, func() { control.Close() })
	defer stopControl()
	var watchers sync.WaitGroup
	watchers.Add(1)
	go func() {
		defer watchers.Done()
		_, _ = io.Copy(io.Discard, control)
		cancel()
	}()
	defer func() {
		cancel()
		control.Close()
		watchers.Wait()
	}()
	initCtx, initCancel := context.WithTimeout(associationCtx, 60*time.Second)
	sessionDone, err := sessions.SessionDone(initCtx, via)
	initCancel()
	if err != nil {
		socksReply(control, 5)
		return
	}
	select {
	case <-sessionDone:
		socksReply(control, 5)
		return
	default:
	}
	watchers.Add(1)
	go func() {
		defer watchers.Done()
		select {
		case <-associationCtx.Done():
		case <-sessionDone:
			cancel()
		}
	}()
	network := "udp4"
	if local.IP.To4() == nil {
		network = "udp6"
	}
	udp, err := net.ListenUDP(network, &net.UDPAddr{IP: local.IP, Zone: local.Zone})
	if err != nil {
		socksReply(control, 1)
		return
	}
	defer udp.Close()
	stopUDP := context.AfterFunc(associationCtx, func() { udp.Close() })
	defer stopUDP()
	address, err := numericSOCKSAddress(udp.LocalAddr())
	if err != nil {
		socksReply(control, 1)
		return
	}
	if _, err := control.Write(append([]byte{5, 0, 0}, address...)); err != nil {
		return
	}
	control.SetDeadline(time.Time{})
	sourcePort, _ := strconv.Atoi(port)
	relay := socksUDPRelay{conn: udp, sessions: sessions, via: via, options: options, flows: make(map[string]*socksUDPFlow)}
	relay.run(associationCtx, &net.UDPAddr{IP: peer.IP, Zone: peer.Zone, Port: sourcePort})
}

func (r *socksUDPRelay) run(ctx context.Context, client *net.UDPAddr) {
	defer func() {
		for _, flow := range r.flows {
			flow.cancel()
		}
		r.workers.Wait()
	}()
	buffer := make([]byte, 65536)
	pollInterval := min(time.Second, r.options.idleTimeout)
	for ctx.Err() == nil {
		r.prune(time.Now())
		r.conn.SetReadDeadline(time.Now().Add(pollInterval))
		n, source, err := r.conn.ReadFromUDP(buffer)
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				continue
			}
			return
		}
		if !source.IP.Equal(client.IP) || (client.Port != 0 && source.Port != client.Port) {
			continue
		}
		target, payload, err := decodeSOCKSUDP(buffer[:n])
		if err != nil {
			continue
		}
		if client.Port == 0 {
			client.Port = source.Port
		}
		flow, ok := r.flows[target]
		if !ok {
			if len(r.flows) >= r.options.maxFlows {
				continue
			}
			flowCtx, cancel := context.WithCancel(ctx)
			flow = &socksUDPFlow{queue: make(chan []byte, r.options.queueSize), done: make(chan struct{}), cancel: cancel}
			flow.lastActivity.Store(time.Now().UnixNano())
			r.flows[target] = flow
			r.workers.Add(1)
			go func() {
				defer r.workers.Done()
				r.runFlow(flowCtx, flow, target, client)
			}()
		}
		select {
		case flow.queue <- bytes.Clone(payload):
			flow.lastActivity.Store(time.Now().UnixNano())
		default:
		}
	}
}

func (r *socksUDPRelay) prune(now time.Time) {
	for target, flow := range r.flows {
		if now.Sub(time.Unix(0, flow.lastActivity.Load())) >= r.options.idleTimeout {
			flow.cancel()
		}
		select {
		case <-flow.done:
			delete(r.flows, target)
		default:
		}
	}
}

func (r *socksUDPRelay) runFlow(ctx context.Context, flow *socksUDPFlow, target string, client *net.UDPAddr) {
	defer close(flow.done)
	defer flow.cancel()
	dialCtx, cancel := context.WithTimeout(ctx, r.options.dialTimeout)
	remote, err := r.sessions.DialNetwork(dialCtx, r.via, "udp", target)
	cancel()
	if err != nil {
		return
	}
	defer remote.Close()
	stop := context.AfterFunc(ctx, func() { remote.Close() })
	defer stop()
	source, err := numericSOCKSAddress(remote.RemoteAddr())
	if err != nil {
		return
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer flow.cancel()
		buffer := make([]byte, 65536)
		for {
			n, err := remote.Read(buffer)
			if err != nil {
				return
			}
			if n+3+len(source) > maxSOCKSUDPDatagram {
				continue
			}
			frame := append(append(make([]byte, 3, 3+len(source)+n), source...), buffer[:n]...)
			r.conn.SetWriteDeadline(time.Now().Add(r.options.writeTimeout))
			if _, err := r.conn.WriteToUDP(frame, client); err != nil {
				return
			}
			flow.lastActivity.Store(time.Now().UnixNano())
		}
	}()
	defer func() {
		remote.Close()
		<-readerDone
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case payload := <-flow.queue:
			remote.SetWriteDeadline(time.Now().Add(r.options.writeTimeout))
			if n, err := remote.Write(payload); err != nil || n != len(payload) {
				return
			}
		}
	}
}
