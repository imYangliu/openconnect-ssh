package daemon

import (
	"context"
	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
)

func Ensure(ctx context.Context, c config.Config, via string) error {
	conn, _, err := RequestControl(ctx, c, Request{Action: "ensure", Request: routing.Request{Via: via}})
	if conn != nil {
		conn.Close()
	}
	return err
}
func Logout(ctx context.Context, c config.Config, via string) error {
	conn, _, err := RequestControl(ctx, c, Request{Action: "logout", Request: routing.Request{Via: via}})
	if conn != nil {
		conn.Close()
	}
	return err
}
