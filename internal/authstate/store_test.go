package authstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, Binding) {
	t.Helper()
	return filepath.Join(t.TempDir(), "auth", "state.json"), Binding{Gateway: "https://vpn.example", Protocol: "atrust", AuthType: "auth/cas", LoginDomain: "campus"}
}
func TestStateSurvivesCloseAndNewOwner(t *testing.T) {
	path, b := fixture(t)
	s, e := Open(path, b)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Save([]byte(`{"cookies":[{"value":"fixture-bearer"}]}`)); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(path, b); e == nil {
		t.Fatal("parallel writer accepted")
	}
	s.Close()
	s, e = Open(path, b)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	data, e := s.Load()
	if e != nil || !strings.Contains(string(data), "fixture-bearer") {
		t.Fatal("state lost", e)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("state permissions")
	}
	if e = s.Clear(); e != nil {
		t.Fatal(e)
	}
	data, e = s.Load()
	if e != nil || data != nil {
		t.Fatal("clear failed")
	}
}
func TestBindingCannotBeLoadedOverwrittenOrCleared(t *testing.T) {
	path, b := fixture(t)
	s, _ := Open(path, b)
	s.Save([]byte(`{"token":"private-fixture"}`))
	s.Close()
	b.Gateway = "https://other.example"
	s, e := Open(path, b)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, e = range []error{func() error { _, e := s.Load(); return e }(), s.Save([]byte(`{}`)), s.Clear()} {
		if e == nil || strings.Contains(e.Error(), "private-fixture") {
			t.Fatal("binding or privacy failure", e)
		}
	}
}
func TestStateRejectsUnsafeFilesAndCorruption(t *testing.T) {
	for _, kind := range []string{"mode", "symlink", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			path, b := fixture(t)
			s, e := Open(path, b)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			switch kind {
			case "mode":
				os.WriteFile(path, []byte(`{}`), 0644)
			case "symlink":
				target := filepath.Join(filepath.Dir(path), "target")
				os.WriteFile(target, []byte(`{}`), 0600)
				os.Symlink(target, path)
			case "corrupt":
				os.WriteFile(path, []byte(`private-fixture-not-json`), 0600)
			}
			_, e = s.Load()
			if e == nil || strings.Contains(e.Error(), "private-fixture") {
				t.Fatal("unsafe state accepted", e)
			}
			if e = s.Save([]byte(`{}`)); e == nil {
				t.Fatal("unsafe state overwritten")
			}
			if kind == "corrupt" {
				if e = s.Clear(); e != nil {
					t.Fatal("explicit corrupt-state clear failed", e)
				}
			}
		})
	}
}
func TestStateRejectsSymlinkDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	os.Mkdir(target, 0700)
	link := filepath.Join(dir, "link")
	os.Symlink(target, link)
	if s, e := Open(filepath.Join(link, "state"), Binding{}); e == nil {
		s.Close()
		t.Fatal("symlink directory accepted")
	}
}
