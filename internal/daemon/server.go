package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/diagnostic"
	"github.com/imyangliu/openconnect-ssh/internal/proxy"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

type Reply struct {
	TCPingVersion int                `json:"tcping_version,omitempty"`
	TCPing        *TCPingResult      `json:"tcping,omitempty"`
	PingVersion   int                `json:"ping_version,omitempty"`
	Ping          *vpn.PingResult    `json:"ping,omitempty"`
	Error         string             `json:"error,omitempty"`
	Diagnostic    *diagnostic.Report `json:"diagnostic,omitempty"`
	Profiles      map[string]string  `json:"profiles,omitempty"`
	Listeners     map[string]string  `json:"listeners,omitempty"`
}
type daemon struct {
	router    *routing.Router
	sessions  *vpn.Manager
	cancel    context.CancelFunc
	mu        sync.Mutex
	listeners []listenerBinding
	active    map[net.Conn]struct{}
	wg        sync.WaitGroup
}

type listenerBinding struct {
	net.Listener
	via string
}

func Serve(ctx context.Context, c config.Config, factory vpn.Factory, ready func(map[string]string)) error {
	dir := filepath.Dir(c.Socket)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("socket directory must be a private directory (0700)")
	}
	lock, err := os.OpenFile(c.Socket+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("OCH is already serving this socket")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if info, err := os.Lstat(c.Socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to remove a non-socket at socket path")
		}
		probe, err := net.DialTimeout("unix", c.Socket, time.Second)
		if err == nil {
			probe.Close()
			return errors.New("socket is already in use")
		}
		if err = os.Remove(c.Socket); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sessions := vpn.NewManager(ctx, c.Profiles, factory)
	defer sessions.Close()
	d := &daemon{sessions: sessions, cancel: cancel, active: make(map[net.Conn]struct{})}
	d.router = routing.New(c, sessions)
	control, err := net.Listen("unix", c.Socket)
	if err != nil {
		return err
	}
	d.listeners = append(d.listeners, listenerBinding{Listener: control})
	defer func() {
		for _, l := range d.listeners {
			l.Close()
		}
		os.Remove(c.Socket)
	}()
	if err = os.Chmod(c.Socket, 0600); err != nil {
		return err
	}
	addresses := make(map[string]string)
	for name, l := range c.Listeners {
		listener, err := net.Listen("tcp", l.Listen)
		if err != nil {
			return fmt.Errorf("listener %s: %w", name, err)
		}
		d.listeners = append(d.listeners, listenerBinding{Listener: listener, via: l.Via})
		addresses[name] = listener.Addr().String()
	}
	d.wg.Add(1)
	go d.accept(ctx, control, func(c net.Conn) { d.handleControl(ctx, c, addresses) })
	mixed := proxy.NewHandler(sessions)
	for _, listener := range d.listeners[1:] {
		d.wg.Add(1)
		go d.accept(ctx, listener.Listener, func(conn net.Conn) { mixed.HandleMixed(ctx, conn, listener.via) })
	}
	if ready != nil {
		ready(addresses)
	}
	<-ctx.Done()
	for _, l := range d.listeners {
		l.Close()
	}
	d.mu.Lock()
	for conn := range d.active {
		conn.Close()
	}
	d.mu.Unlock()
	sessions.Close()
	d.wg.Wait()
	return nil
}
func (d *daemon) accept(ctx context.Context, listener net.Listener, handler func(net.Conn)) {
	defer d.wg.Done()
	for {
		conn, err := proxy.Accept(ctx, listener)
		if err != nil {
			return
		}
		d.mu.Lock()
		if ctx.Err() != nil {
			d.mu.Unlock()
			conn.Close()
			return
		}
		d.active[conn] = struct{}{}
		d.wg.Add(1)
		d.mu.Unlock()
		go func() {
			defer d.wg.Done()
			defer conn.Close()
			defer func() { d.mu.Lock(); delete(d.active, conn); d.mu.Unlock() }()
			handler(conn)
		}()
	}
}
func (d *daemon) handleControl(ctx context.Context, conn net.Conn, addresses map[string]string) {
	conn.SetDeadline(time.Now().Add(65 * time.Second))
	reader := bufio.NewReaderSize(conn, 16*1024)
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return
	}
	var request Request
	if err = json.Unmarshal(line, &request); err != nil {
		json.NewEncoder(conn).Encode(Reply{Error: "invalid request"})
		return
	}
	reply := Reply{}
	switch request.Action {
	case "tcping":
		d.handleTCPing(ctx, conn, reader, request)
	case "ping":
		d.handlePing(ctx, conn, reader, request)
	case "diagnose":
		result := d.diagnose(ctx, request)
		json.NewEncoder(conn).Encode(Reply{Diagnostic: &result})
	case "login":
		d.handleLogin(ctx, conn, reader, request.Via)
	case "ensure":
		ensureCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		if err := d.sessions.Ensure(ensureCtx, request.Via); err != nil {
			reply.Error = err.Error()
		}
		json.NewEncoder(conn).Encode(reply)
	case "logout":
		if err := d.sessions.Logout(ctx, request.Via); err != nil {
			reply.Error = err.Error()
		}
		json.NewEncoder(conn).Encode(reply)
	case "status":
		reply.Profiles = d.sessions.Status()
		reply.Listeners = addresses
		json.NewEncoder(conn).Encode(reply)
	case "stop":
		json.NewEncoder(conn).Encode(reply)
		d.cancel()
	case "connect":
		dialCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		remote, err := d.router.Dial(dialCtx, request.Request)
		if err != nil {
			json.NewEncoder(conn).Encode(Reply{Error: err.Error()})
			return
		}
		defer remote.Close()
		if err = json.NewEncoder(conn).Encode(reply); err != nil {
			return
		}
		conn.SetDeadline(time.Time{})
		proxy.Relay(ctx, proxy.Buffered(conn, reader), remote)
	default:
		json.NewEncoder(conn).Encode(Reply{Error: "unknown action"})
	}
}

type Request struct {
	Action       string        `json:"action"`
	PingSequence int           `json:"ping_sequence,omitempty"`
	Timeout      time.Duration `json:"timeout,omitempty"`
	routing.Request
}
