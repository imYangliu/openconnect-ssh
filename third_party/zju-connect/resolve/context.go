package resolve

import (
	"context"
	"github.com/mythologyli/zju-connect/client"
)

type contextKey string

var (
	ContextKeyFakeIP          = contextKey("FAKE_IP")
	ContextKeyResolveHost     = contextKey("RESOLVE_HOST")
	ContextKeyDomainResource  = contextKey("DOMAIN_RESOURCE")
	ContextKeyIPResource      = contextKey("IP_RESOURCE")
	contextKeyIgnoreTCPPrefL3 = contextKey("IGNORE_TCP_PREF_L3")
)

func WithIgnoreTCPPrefL3(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKeyIgnoreTCPPrefL3, true)
}

func IgnoreTCPPrefL3(ctx context.Context) bool {
	ignore, _ := ctx.Value(contextKeyIgnoreTCPPrefL3).(bool)
	return ignore
}

func TCPPrefersL3(ctx context.Context) bool {
	if resource, ok := ctx.Value(ContextKeyDomainResource).(client.DomainResource); ok {
		return resource.EnableTCPPrefL3
	}
	if resource, ok := ctx.Value(ContextKeyIPResource).(client.IPResource); ok {
		return resource.EnableTCPPrefL3
	}
	return false
}
