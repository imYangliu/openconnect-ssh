package diagnostic

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"net"
)

type Check struct {
	Stage   string `json:"stage"`
	State   string `json:"state"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Advice  string `json:"advice,omitempty"`
}

type Report struct {
	Version   int               `json:"version"`
	Source    string            `json:"source"`
	Checks    []Check           `json:"checks"`
	Route     *routing.Decision `json:"route,omitempty"`
	Profiles  map[string]string `json:"profiles,omitempty"`
	Listeners map[string]string `json:"listeners,omitempty"`
}

func (r Report) OK() bool {
	for _, check := range r.Checks {
		if check.State == "failed" {
			return false
		}
	}
	return true
}

func (r Report) MarshalJSON() ([]byte, error) {
	type reportData Report
	return json.Marshal(struct {
		reportData
		OK bool `json:"ok"`
	}{reportData(r), r.OK()})
}

func NetworkCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dns *net.DNSError
	var routeDNS *routing.DNSError
	if errors.As(err, &dns) || errors.As(err, &routeDNS) {
		return "dns"
	}
	var n net.Error
	if errors.As(err, &n) && n.Timeout() {
		return "timeout"
	}
	return "connection_failed"
}
