package browser

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPersistentProfileRejectsFIFOMarkerWithoutWriter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".och-profile")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- checkProfileMarker(root) }()
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
		t.Fatal("browser profile check blocked waiting for a FIFO writer before rejecting nonregular marker")
	}
}
