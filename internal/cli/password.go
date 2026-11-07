package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func readPassword(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("password input requires an interactive terminal")
	}
	fmt.Fprint(os.Stderr, "VPN password: ")
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", errors.New("could not secure terminal input")
	}
	defer func() { term.Restore(fd, state); fmt.Fprintln(os.Stderr) }()
	var password []byte
	defer func() {
		for i := range password {
			password[i] = 0
		}
	}()
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		_, err = unix.Poll(poll, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return "", errors.New("could not read terminal input")
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return "", errors.New("terminal input ended")
		}
		if poll[0].Revents&unix.POLLIN == 0 {
			continue
		}
		var b [1]byte
		n, err := unix.Read(fd, b[:])
		if err != nil || n != 1 {
			return "", errors.New("terminal input ended")
		}
		switch b[0] {
		case '\r', '\n':
			if len(password) == 0 {
				return "", errors.New("VPN password is empty")
			}
			return string(password), nil
		case 3:
			return "", context.Canceled
		case 4:
			return "", errors.New("terminal input ended")
		case 8, 127:
			if len(password) > 0 {
				_, size := utf8.DecodeLastRune(password)
				password = password[:len(password)-size]
			}
		default:
			if b[0] >= 32 {
				password = append(password, b[0])
				if len(password) > 12*1024 {
					return "", errors.New("VPN password is too long")
				}
			}
		}
	}
}
