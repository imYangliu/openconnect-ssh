package browser

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const profileMarker = "och-cas-browser-profile-v1\n"

func prepareProfile(path string) (string, func(), error) {
	if path == "" {
		path, err := os.MkdirTemp("", "och-cas-browser-")
		if err != nil {
			return "", nil, errors.New("cannot create private browser profile")
		}
		return path, func() { _ = os.RemoveAll(path) }, nil
	}
	if !filepath.IsAbs(path) {
		return "", nil, errors.New("OCH browser profile directory must be absolute")
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", nil, errors.New("cannot create OCH browser profile directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || !privateOwned(info, 0700) {
		return "", nil, errors.New("OCH browser profile must be an owned private directory (0700), not a symlink")
	}
	if err := checkProfileMarker(path); err != nil {
		return "", nil, err
	}
	lock, err := os.OpenFile(filepath.Join(path, ".och-profile.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return "", nil, errors.New("cannot lock OCH browser profile")
	}
	info, err = lock.Stat()
	if err != nil || !info.Mode().IsRegular() || !privateOwned(info, 0600) {
		lock.Close()
		return "", nil, errors.New("OCH browser profile lock must be a private regular file")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return "", nil, errors.New("another OCH login is using this browser profile")
	}
	release := func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); _ = lock.Close() }
	return path, release, nil
}

func privateOwned(info os.FileInfo, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid() && info.Mode().Perm() == mode
}

func checkProfileMarker(path string) error {
	marker := filepath.Join(path, ".och-profile")
	file, err := os.OpenFile(marker, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err == nil {
		defer file.Close()
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || !privateOwned(info, 0600) {
			return errors.New("invalid OCH browser profile marker")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 128))
		if readErr != nil || string(data) != profileMarker {
			return errors.New("invalid OCH browser profile marker")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot inspect OCH browser profile marker")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return errors.New("cannot inspect OCH browser profile directory")
	}
	for _, entry := range entries {
		if entry.Name() != ".och-profile.lock" {
			return errors.New("refusing to use an existing non-OCH browser profile")
		}
	}
	file, err = os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("cannot mark OCH browser profile directory")
	}
	_, err = file.WriteString(profileMarker)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot write OCH browser profile marker")
	}
	return nil
}
