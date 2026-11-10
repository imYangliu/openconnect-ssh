package forward

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/proxy"
)

type DialFunc func(context.Context, string, string) (net.Conn, error)

type Event struct {
	Name    string
	State   string
	Address string
	Err     error
}

type runner struct {
	ctx       context.Context
	dial      DialFunc
	sessions  map[string]*sshSession
	listeners []net.Listener
	wg        sync.WaitGroup
	report    func(Event)
	reportMu  sync.Mutex
}

type binding struct {
	name       string
	rule       config.Forward
	listener   net.Listener
	connection *sshConnection
}

func Run(ctx context.Context, settings config.Config, rules map[string]config.Forward, dial DialFunc, report func(Event)) error {
	if len(rules) == 0 {
		return errors.New("select at least one forwarding rule")
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &runner{ctx: ctx, dial: dial, sessions: make(map[string]*sshSession), report: report}
	defer func() {
		cancel()
		// Transport shutdown also closes remote listeners. Only local listeners
		// belong in this list; remote Close would send another SSH request.
		for _, s := range r.sessions {
			s.close()
		}
		for _, l := range r.listeners {
			l.Close()
		}
		r.wg.Wait()
	}()
	names := make([]string, 0, len(rules))
	for name, rule := range rules {
		if err := settings.ValidateForward(rule); err != nil {
			return fmt.Errorf("forward %s: %w", name, err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var bindings []binding
	for _, name := range names {
		rule := rules[name].WithDefaults()
		var conn *sshConnection
		if rule.SSH != "" {
			s := r.sessions[rule.SSH]
			if s == nil {
				s = &sshSession{ctx: ctx, profile: settings.SSH[rule.SSH], dial: dial}
				r.sessions[rule.SSH] = s
			}
			var err error
			conn, err = s.get(ctx)
			if err != nil {
				return fmt.Errorf("forward %s: %w", name, err)
			}
		}
		var listener net.Listener
		var err error
		if rule.Direction == "remote" {
			listener, err = sshOperation(ctx, conn, func() (net.Listener, error) { return conn.client.Listen("tcp", rule.Listen) })
		} else {
			listener, err = net.Listen("tcp", rule.Listen)
		}
		if err != nil {
			return fmt.Errorf("forward %s: could not listen: %w", name, err)
		}
		if rule.Direction != "remote" {
			r.listeners = append(r.listeners, listener)
		}
		bindings = append(bindings, binding{name: name, rule: rule, listener: listener, connection: conn})
	}
	for _, b := range bindings {
		r.emit(Event{Name: b.name, State: "ready", Address: b.listener.Addr().String()})
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			if b.rule.Direction == "remote" {
				r.remote(b)
			} else {
				r.accept(b, false)
			}
		}()
	}
	<-ctx.Done()
	return nil
}

func (r *runner) emit(e Event) {
	if r.report != nil && r.ctx.Err() == nil {
		r.reportMu.Lock()
		r.report(e)
		r.reportMu.Unlock()
	}
}

func (r *runner) accept(b binding, remote bool) {
	for {
		conn, err := proxy.Accept(r.ctx, b.listener)
		if err != nil {
			return
		}
		if r.ctx.Err() != nil {
			conn.Close()
			return
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer conn.Close()
			stop := context.AfterFunc(r.ctx, func() { conn.Close() })
			defer stop()
			ctx, cancel := context.WithTimeout(r.ctx, operationTimeout)
			var target net.Conn
			var err error
			switch {
			case remote:
				target, err = r.dial(ctx, "direct", b.rule.Target)
			case b.rule.SSH != "":
				var c *sshConnection
				c, err = r.sessions[b.rule.SSH].get(ctx)
				if err == nil {
					target, err = sshOperation(ctx, c, func() (net.Conn, error) { return c.client.Dial("tcp", b.rule.Target) })
				}
			default:
				target, err = r.dial(ctx, b.rule.Via, b.rule.Target)
			}
			cancel()
			if err != nil {
				r.emit(Event{Name: b.name, State: "connection_failed", Err: err})
				return
			}
			defer target.Close()
			proxy.Relay(r.ctx, conn, target)
		}()
	}
}

func (r *runner) remote(b binding) {
	for {
		r.accept(b, true)
		if r.ctx.Err() != nil {
			return
		}
		b.connection.transport.Close()
		r.emit(Event{Name: b.name, State: "reconnecting"})
		for {
			timer := time.NewTimer(retryDelay)
			select {
			case <-r.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			c, err := r.sessions[b.rule.SSH].get(r.ctx)
			if err != nil {
				r.emit(Event{Name: b.name, State: "reconnect_failed", Err: err})
				continue
			}
			l, err := sshOperation(r.ctx, c, func() (net.Listener, error) { return c.client.Listen("tcp", b.rule.Listen) })
			if err != nil {
				r.emit(Event{Name: b.name, State: "reconnect_failed", Err: err})
				continue
			}
			b.listener, b.connection = l, c
			r.emit(Event{Name: b.name, State: "ready", Address: l.Addr().String()})
			break
		}
	}
}
