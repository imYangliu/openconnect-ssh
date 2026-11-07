package browser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfilePersistenceAndEphemeralCleanup(t *testing.T) {
	temporary, release, err := prepareProfile("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temporary, "fixture-cookie"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatal("temporary profile retained")
	}

	persistent := filepath.Join(t.TempDir(), "och-only")
	path, release, err := prepareProfile(persistent)
	if err != nil {
		t.Fatal(err)
	}
	cookie := filepath.Join(path, "fixture-cookie")
	if err := os.WriteFile(cookie, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	release()
	_, release, err = prepareProfile(persistent)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if data, err := os.ReadFile(cookie); err != nil || string(data) != "fixture" {
		t.Fatal("remembered state lost")
	}
}

func TestPersistentProfileExclusiveUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "och-only")
	_, release, err := prepareProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, secondRelease, err := prepareProfile(path); err == nil {
		secondRelease()
		t.Fatal("concurrent profile use accepted")
	}
	release()
	_, release, err = prepareProfile(path)
	if err != nil {
		t.Fatal("lock not released", err)
	}
	release()
}

func TestPersistentProfileRejectsUnsafeLocations(t *testing.T) {
	root := t.TempDir()
	public := filepath.Join(root, "public")
	os.Mkdir(public, 0755)
	os.Chmod(public, 0755)
	private := filepath.Join(root, "private")
	os.Mkdir(private, 0700)
	link := filepath.Join(root, "linked")
	os.Symlink(private, link)
	unmanaged := filepath.Join(root, "personal-browser")
	os.Mkdir(unmanaged, 0700)
	os.WriteFile(filepath.Join(unmanaged, "Preferences"), []byte("personal-fixture"), 0600)
	for _, path := range []string{"relative/profile", public, link, unmanaged} {
		if _, release, err := prepareProfile(path); err == nil {
			release()
			t.Fatal("unsafe profile accepted")
		}
	}
	entries, _ := os.ReadDir(unmanaged)
	if len(entries) != 1 || entries[0].Name() != "Preferences" {
		t.Fatal("personal browser directory modified")
	}
}

func TestPersistentProfileRejectsSymlinkLockAndMarker(t *testing.T) {
	for _, name := range []string{".och-profile.lock", ".och-profile"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			profile := filepath.Join(root, "och-only")
			_, release, err := prepareProfile(profile)
			if err != nil {
				t.Fatal(err)
			}
			release()
			outside := filepath.Join(root, "outside")
			os.WriteFile(outside, []byte(profileMarker), 0600)
			os.Remove(filepath.Join(profile, name))
			os.Symlink(outside, filepath.Join(profile, name))
			if _, release, err := prepareProfile(profile); err == nil {
				release()
				t.Fatal("symlink profile metadata accepted")
			}
			data, _ := os.ReadFile(outside)
			if string(data) != profileMarker {
				t.Fatal("symlink target modified")
			}
		})
	}
}

func TestRememberedProfileSurvivesBrowserCancellation(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "och-only")
	executable := filepath.Join(root, "fixture-browser")
	os.WriteFile(executable, []byte("#!/bin/sh\nexec sleep 30\n"), 0700)
	_, release, err := prepareProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	release()
	os.WriteFile(filepath.Join(profile, "fixture-cookie"), []byte("fixture"), 0600)
	os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte("1\n/devtools/browser/stale"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = OpenCAS(ctx, "https://vpn.example/passport/v1/public/casLogin?sfDomain=campus", Options{Executable: executable, ProfileDirectory: profile})
	if err == nil {
		t.Fatal("fixture login succeeded")
	}
	if data, err := os.ReadFile(filepath.Join(profile, "fixture-cookie")); err != nil || string(data) != "fixture" {
		t.Fatal("remembered cookie deleted")
	}
	if _, err := os.Stat(filepath.Join(profile, "DevToolsActivePort")); !os.IsNotExist(err) {
		t.Fatal("stale debugger port retained")
	}
	if strings.Contains(err.Error(), "fixture-cookie") {
		t.Fatal("private state leaked")
	}
	_, release, err = prepareProfile(profile)
	if err != nil {
		t.Fatal("cancel left profile locked", err)
	}
	release()
}
