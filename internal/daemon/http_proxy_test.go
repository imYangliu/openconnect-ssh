package daemon

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
)

func TestHTTPProxyForwardsContinueAndFinalResponse(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			fmt.Fprint(w, string(body))
		}
	}))
	defer origin.Close()
	_, addresses := startDaemon(t, config.Config{Listeners: map[string]config.Listener{"mixed": {Listen: "127.0.0.1:0", Via: "campus"}}}, directFactory)
	conn, err := net.Dial("tcp", addresses["mixed"])
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST %s/upload HTTP/1.1\r\nHost: %s\r\nContent-Length: 5\r\nExpect: 100-continue\r\n\r\n", origin.URL, strings.TrimPrefix(origin.URL, "http://"))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatalf("proxy did not relay 100 Continue before body: %v", err)
	}
	if response.StatusCode != http.StatusContinue {
		t.Fatalf("got status %d, want100", response.StatusCode)
	}
	response.Body.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	conn.Write([]byte("hello"))
	response, err = http.ReadResponse(reader, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "hello" {
		t.Fatalf("unexpected body %q", body)
	}
	if rest, err := io.ReadAll(reader); err != nil || len(rest) != 0 {
		t.Fatalf("data after final response: %q %v", rest, err)
	}
}

func TestHTTPProxyForwardsEarlyHintsWithoutHopHeaders(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", "</style.css>; rel=preload; as=style")
		w.Header().Set("Connection", "X-Internal")
		w.Header().Set("X-Internal", "private-hop-value")
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Del("Connection")
		w.Header().Del("X-Internal")
		fmt.Fprint(w, "final-body")
	}))
	defer origin.Close()
	_, addresses := startDaemon(t, config.Config{Listeners: map[string]config.Listener{"mixed": {Listen: "127.0.0.1:0", Via: "campus"}}}, directFactory)
	conn, err := net.Dial("tcp", addresses["mixed"])
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprintf(conn, "GET %s/ HTTP/1.1\r\nHost: ignored.invalid\r\n\r\n", origin.URL)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusEarlyHints || response.Header.Get("Link") == "" || response.Header.Get("X-Internal") != "" || response.Header.Get("Connection") != "" {
		t.Fatalf("incorrect early hints response: %+v", response)
	}
	response.Body.Close()
	response, err = http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "final-body" {
		t.Fatalf("incorrect final response: status=%d body=%q err=%v", response.StatusCode, body, err)
	}
	if rest, err := io.ReadAll(reader); err != nil || len(rest) != 0 {
		t.Fatalf("data after final response: %q %v", rest, err)
	}
}

func TestHTTPProxyRejectsExpectContinueWithoutWaitingForBody(t *testing.T) {
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		c, err := origin.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(2 * time.Second))
		request, err := http.ReadRequest(bufio.NewReader(c))
		if err != nil || request.Header.Get("Expect") != "100-continue" {
			return
		}
		io.WriteString(c, "HTTP/1.1 413 Payload Too Large\r\nContent-Length: 8\r\nConnection: close\r\n\r\nrejected")
	}()
	_, addresses := startDaemon(t, config.Config{Listeners: map[string]config.Listener{"mixed": {Listen: "127.0.0.1:0", Via: "campus"}}}, directFactory)
	conn, err := net.Dial("tcp", addresses["mixed"])
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprintf(conn, "POST http://%s/upload HTTP/1.1\r\nHost: %s\r\nContent-Length: 5\r\nExpect: 100-continue\r\n\r\n", origin.Addr(), origin.Addr())
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatalf("early rejection did not reach client before its body: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusRequestEntityTooLarge || string(body) != "rejected" {
		t.Fatalf("incorrect early rejection: status=%d body=%q err=%v", response.StatusCode, body, err)
	}
	if rest, err := io.ReadAll(reader); err != nil || len(rest) != 0 {
		t.Fatalf("early rejection did not close its completed response: %q %v", rest, err)
	}
}
