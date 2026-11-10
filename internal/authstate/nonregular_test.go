package authstate

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestStateRejectsFIFOWithoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth", "cache.json")
	s, err := Open(path, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Load(); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(150 * time.Millisecond):
		unblock, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unblock.Close()
		<-done
		t.Fatal("authentication state Load blocked waiting for a FIFO writer before rejecting nonregular file")
	}
}
