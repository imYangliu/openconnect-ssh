package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/routing"
)

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *bufferedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
func Relay(ctx context.Context, a, b net.Conn) {
	stop := context.AfterFunc(ctx, func() { a.Close(); b.Close() })
	defer stop()
	done := make(chan struct{}, 2)
	copyTo := func(dst, src net.Conn) {
		_, err := io.Copy(dst, src)
		if err != nil {
			a.Close()
			b.Close()
		} else if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyTo(a, b)
	go copyTo(b, a)
	<-done
	<-done
}

type Handler struct {
	sessions   Dialer
	udpSlots   chan struct{}
	udpOptions socksUDPOptions
}

func NewHandler(sessions Dialer) *Handler {
	return &Handler{sessions: sessions, udpSlots: make(chan struct{}, 128), udpOptions: defaultSOCKSUDPOptions()}
}

func (h *Handler) HandleMixed(ctx context.Context, conn net.Conn, via string) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	reader := bufio.NewReader(conn)
	first, err := reader.Peek(1)
	if err != nil {
		return
	}
	buffered := &bufferedConn{Conn: conn, reader: reader}
	if first[0] == 5 {
		h.handleSOCKS(ctx, buffered, via)
	} else {
		handleHTTP(ctx, buffered, via, h.sessions)
	}
}
func (h *Handler) handleSOCKS(ctx context.Context, conn net.Conn, via string) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil || header[0] != 5 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	found := false
	for _, m := range methods {
		if m == 0 {
			found = true
		}
	}
	if !found {
		conn.Write([]byte{5, 255})
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(conn, request); err != nil {
		return
	}
	if request[0] != 5 || (request[1] != 1 && request[1] != 3) || request[2] != 0 {
		socksReply(conn, 7)
		return
	}
	if request[3] != 1 && request[3] != 3 && request[3] != 4 {
		socksReply(conn, 8)
		return
	}
	host, port, err := readSOCKSAddress(conn, request[3])
	if err != nil {
		return
	}
	if request[1] == 3 {
		select {
		case h.udpSlots <- struct{}{}:
			defer func() { <-h.udpSlots }()
		default:
			socksReply(conn, 1)
			return
		}
		handleSOCKSUDP(ctx, conn, via, h.sessions, request[3], host, port, h.udpOptions)
		return
	}
	if err := routing.ValidateTarget(host, port); err != nil {
		socksReply(conn, 1)
		return
	}
	conn.SetDeadline(time.Now().Add(65 * time.Second))
	dialCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	remote, err := h.sessions.Dial(dialCtx, via, net.JoinHostPort(host, port))
	if err != nil {
		socksReply(conn, 5)
		return
	}
	defer remote.Close()
	if err = socksReply(conn, 0); err != nil {
		return
	}
	conn.SetDeadline(time.Time{})
	Relay(ctx, conn, remote)
}
func socksReply(conn net.Conn, code byte) error {
	_, err := conn.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}
func handleHTTP(ctx context.Context, conn net.Conn, via string, sessions Dialer) {
	reader := bufio.NewReader(conn)
	var header bytes.Buffer
	for {
		line, err := reader.ReadSlice('\n')
		if header.Len()+len(line) > 64*1024 {
			writeHTTPError(conn, 431, "request headers too large")
			return
		}
		header.Write(line)
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
		if err == nil && (bytes.Equal(line, []byte("\r\n")) || bytes.Equal(line, []byte("\n"))) {
			break
		}
	}
	reader = bufio.NewReader(io.MultiReader(bytes.NewReader(header.Bytes()), reader))
	request, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	defer func() {
		// Body.Close can drain unread uploads; close this one-shot connection first.
		conn.Close()
		request.Body.Close()
	}()
	if request.Method == http.MethodConnect {
		host, port, err := net.SplitHostPort(request.Host)
		if err != nil || routing.ValidateTarget(host, port) != nil {
			writeHTTPError(conn, 400, "invalid CONNECT target")
			return
		}
		conn.SetDeadline(time.Now().Add(65 * time.Second))
		dialCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		remote, err := sessions.Dial(dialCtx, via, net.JoinHostPort(host, port))
		if err != nil {
			writeHTTPError(conn, 502, "VPN connection failed")
			return
		}
		defer remote.Close()
		if _, err = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		conn.SetDeadline(time.Time{})
		Relay(ctx, &bufferedConn{Conn: conn, reader: reader}, remote)
		return
	}
	if request.URL.Scheme != "http" || request.URL.Host == "" || request.URL.User != nil {
		writeHTTPError(conn, 400, "absolute http URL required")
		return
	}
	// A request-scoped transport prevents proxy clients from sharing authenticated streams.
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 60 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return sessions.Dial(ctx, via, address)
		}}
	defer transport.CloseIdleConnections()
	request.RequestURI = ""
	request.Host = request.URL.Host
	request.Close = true
	stripHopHeaders(request.Header)
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	request = request.WithContext(httptrace.WithClientTrace(requestCtx, &httptrace.ClientTrace{
		Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
			forwarded := http.Header(header).Clone()
			stripHopHeaders(forwarded)
			response := &http.Response{StatusCode: code, ProtoMajor: 1, ProtoMinor: 1, Header: forwarded}
			return response.Write(conn)
		},
	}))
	conn.SetDeadline(time.Now().Add(5 * time.Minute))
	response, err := transport.RoundTrip(request)
	if err != nil {
		writeHTTPError(conn, 502, "VPN connection failed")
		return
	}
	defer response.Body.Close()
	stripHopHeaders(response.Header)
	response.Close = true
	response.Write(conn)
}
func stripHopHeaders(headers http.Header) {
	for _, token := range strings.Split(headers.Get("Connection"), ",") {
		headers.Del(strings.TrimSpace(token))
	}
	for _, key := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		headers.Del(key)
	}
}
func writeHTTPError(conn net.Conn, code int, message string) {
	body := message + "\n"
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nConnection: close\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", code, http.StatusText(code), len(body), body)
}

// Dialer opens a connection through the listener's fixed VPN profile.
type Dialer interface {
	Dial(context.Context, string, string) (net.Conn, error)
	DialNetwork(context.Context, string, string, string) (net.Conn, error)
	SessionDone(context.Context, string) (<-chan struct{}, error)
}

func Buffered(conn net.Conn, reader *bufio.Reader) net.Conn {
	return &bufferedConn{Conn: conn, reader: reader}
}
