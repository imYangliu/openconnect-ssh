package forward

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const operationTimeout = 30 * time.Second
const retryDelay = time.Second

type sshSession struct {
	ctx     context.Context
	profile config.SSHProfile
	dial    DialFunc
	mu      sync.Mutex
	current *sshConnection
	retryAt time.Time
}

type sshConnection struct {
	client    *ssh.Client
	transport net.Conn
	done      chan struct{}
	keepalive chan struct{}
}

type logicalAddress string

func (a logicalAddress) Network() string { return "tcp" }
func (a logicalAddress) String() string  { return string(a) }

func (s *sshSession) get(ctx context.Context) (*sshConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.current != nil {
		select {
		case <-s.current.done:
			s.current.close()
			s.current = nil
		default:
			return s.current, nil
		}
	}
	if time.Now().Before(s.retryAt) {
		return nil, errors.New("SSH reconnect is cooling down; retry shortly")
	}
	c, err := s.connect(ctx)
	if err != nil {
		s.retryAt = time.Now().Add(retryDelay)
		return nil, err
	}
	s.current = c
	return c, nil
}

func (s *sshSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		s.current.close()
	}
}

func (s *sshSession) connect(ctx context.Context) (*sshConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	verify, err := knownhosts.New(s.profile.KnownHostsFile)
	if err != nil {
		return nil, errors.New("could not load SSH known_hosts_file")
	}
	clientConfig := &ssh.ClientConfig{User: s.profile.Username, HostKeyCallback: func(host string, _ net.Addr, key ssh.PublicKey) error {
		// IPC's peer is a Unix socket; host identity is the configured SSH endpoint.
		if err := verify(host, logicalAddress(s.profile.Address), key); err != nil {
			return fmt.Errorf("SSH host key verification failed: %w", err)
		}
		return nil
	}}
	var signers []ssh.Signer
	if path := s.profile.IdentityFile; path != "" {
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("could not read SSH identity_file")
		}
		var signer ssh.Signer
		if name := s.profile.KeyPassphraseEnv; name != "" {
			passphrase := os.Getenv(name)
			if passphrase == "" {
				return nil, errors.New("SSH key passphrase environment variable is empty")
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, errors.New("could not parse SSH identity_file; encrypted keys require key_passphrase_env")
		}
		signers = append(signers, signer)
	}
	if name := s.profile.PasswordEnv; name != "" {
		password := os.Getenv(name)
		if password == "" {
			return nil, errors.New("SSH password environment variable is empty")
		}
		clientConfig.Auth = append(clientConfig.Auth, ssh.Password(password))
	}
	var agentConn net.Conn
	if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
		agentConn, err = (&net.Dialer{}).DialContext(ctx, "unix", socket)
		if err != nil && len(signers) == 0 && len(clientConfig.Auth) == 0 {
			return nil, errors.New("could not connect to SSH agent")
		}
	}
	if agentConn != nil {
		defer agentConn.Close()
		if deadline, ok := ctx.Deadline(); ok {
			agentConn.SetDeadline(deadline)
		}
		stopAgent := context.AfterFunc(ctx, func() { agentConn.Close() })
		defer stopAgent()
	}
	if len(signers) != 0 || agentConn != nil {
		publicKeys := ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			keys := append([]ssh.Signer(nil), signers...)
			if agentConn != nil {
				fallback := len(keys) != 0 || s.profile.PasswordEnv != ""
				if fallback {
					deadline := time.Now().Add(time.Second)
					if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
						deadline = end
					}
					agentConn.SetDeadline(deadline)
				}
				agentKeys, err := agent.NewClient(agentConn).Signers()
				if err != nil {
					agentConn.Close()
					if !fallback {
						return nil, errors.New("could not read SSH agent keys")
					}
				} else {
					if end, ok := ctx.Deadline(); ok {
						agentConn.SetDeadline(end)
					}
					keys = append(keys, agentKeys...)
				}
			}
			return keys, nil
		})
		clientConfig.Auth = append([]ssh.AuthMethod{publicKeys}, clientConfig.Auth...)
	}
	if len(clientConfig.Auth) == 0 {
		return nil, errors.New("SSH requires identity_file, password_env or an available SSH agent")
	}
	transport, err := s.dial(ctx, s.profile.Via, s.profile.Address)
	if err != nil {
		return nil, err
	}
	stopHandshake := context.AfterFunc(ctx, func() { transport.Close() })
	if deadline, ok := ctx.Deadline(); ok {
		transport.SetDeadline(deadline)
	}
	conn, channels, requests, err := ssh.NewClientConn(transport, s.profile.Address, clientConfig)
	stopped := stopHandshake()
	if err != nil || !stopped || ctx.Err() != nil {
		transport.Close()
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = context.Canceled
		}
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}
	transport.SetDeadline(time.Time{})
	c := &sshConnection{client: ssh.NewClient(conn, channels, requests), transport: transport, done: make(chan struct{}), keepalive: make(chan struct{})}
	stop := context.AfterFunc(s.ctx, func() { transport.Close() })
	go func() {
		c.client.Wait()
		transport.Close()
		stop()
		close(c.done)
	}()
	go c.runKeepalive()
	return c, nil
}

func (c *sshConnection) close() {
	c.transport.Close()
	<-c.done
	<-c.keepalive
}

func (c *sshConnection) runKeepalive() {
	defer close(c.keepalive)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			_, err := sshOperation(context.Background(), c, func() (bool, error) {
				_, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil)
				return true, err
			})
			if err != nil {
				c.transport.Close()
				return
			}
		}
	}
}

// SSH channel opens and remote binds have no cancel request; closing their
// transport on timeout is necessary to release the blocked protocol operation.
func sshOperation[T any](ctx context.Context, c *sshConnection, operation func() (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	type result struct {
		value T
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		value, err := operation()
		resultCh <- result{value, err}
	}()
	select {
	case r := <-resultCh:
		return r.value, r.err
	case <-ctx.Done():
		c.transport.Close()
		r := <-resultCh
		if closer, ok := any(r.value).(net.Conn); ok && closer != nil {
			closer.Close()
		}
		var zero T
		return zero, ctx.Err()
	}
}
