package vpn

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/imyangliu/openconnect-ssh/internal/config"
)

func TestCASDiagnosticsOnlyExposeWhitelistedCategoryAndNumber(t *testing.T) {
	cases := []struct {
		err      error
		category string
		status   int
	}{
		{errors.New("invalid redirect url: host not match"), "redirect_origin", 0},
		{errors.New("invalid redirect url: path not match"), "redirect_path", 0},
		{errors.New("invalid status code: 403"), "cas_http", 403},
		{errors.New("authCheck failed with code 123: private-fixture https://x/?ticket=secret"), "auth_check_code", 123},
		{errors.New("reportEnv failed with code -5"), "report_env_code", -5},
		{errors.New("no reachable node for ip request"), "node", 0},
		{&url.Error{Op: "Get", URL: "https://x/?ticket=private-fixture", Err: x509.UnknownAuthorityError{}}, "cert_authority", 0},
		{errors.New("GET https://x/?ticket=private-fixture"), "unknown", 0},
	}
	for _, tc := range cases {
		safe := classifyCASFailure(tc.err)
		category, status := LoginFailureDetails(safe)
		if category != tc.category || status != tc.status {
			t.Fatal(category, status, tc.category, tc.status)
		}
		if strings.Contains(safe.Error(), "private-fixture") || strings.Contains(safe.Error(), "https://") {
			t.Fatal("unsafe diagnostic", safe)
		}
	}
	if strings.Contains(LoginFailure("private-fixture", 0).Error(), "private-fixture") {
		t.Fatal("unknown wire category echoed")
	}
}
func TestManagerPreservesOnlyTypedSafeLoginError(t *testing.T) {
	for _, typed := range []bool{false, true} {
		m := NewManager(context.Background(), casProfiles(), func(context.Context, config.Profile) (Session, error) {
			if typed {
				return nil, LoginFailure("cas_http", 403)
			}
			return nil, errors.New("invalid status code: 403 private-fixture")
		})
		err := m.Login(context.Background(), "campus", BootstrapOptions{Browser: func(context.Context, string) (string, error) { return "", nil }})
		category, _ := LoginFailureDetails(err)
		if typed && category != "cas_http" || !typed && category != "unknown" {
			t.Fatal(typed, category)
		}
		_, err = m.Dial(context.Background(), "campus", "host:22")
		if strings.Contains(err.Error(), "private-fixture") {
			t.Fatal("cached error leaked")
		}
		m.Close()
	}
}

func TestLoginCacheDiscardsWrappersIncludingCancellation(t *testing.T) {
	for _, cause := range []error{LoginFailure("cas_http", 403), context.Canceled, context.DeadlineExceeded} {
		m := NewManager(context.Background(), casProfiles(), func(context.Context, config.Profile) (Session, error) {
			return nil, fmt.Errorf("GET https://vpn.example/?ticket=private-fixture: %w", cause)
		})
		err := m.Login(context.Background(), "campus", BootstrapOptions{Browser: func(context.Context, string) (string, error) { return "", nil }})
		if err == nil || strings.Contains(err.Error(), "private-fixture") {
			t.Fatal("wrapped login error leaked", err)
		}
		if (cause == context.Canceled || cause == context.DeadlineExceeded) && !errors.Is(err, cause) {
			t.Fatal("cancellation identity lost", err)
		}
		_, err = m.Dial(context.Background(), "campus", "host:22")
		if strings.Contains(err.Error(), "private-fixture") {
			t.Fatal("cached wrapper leaked", err)
		}
		m.Close()
	}
}
func TestCASCertificateFailureCategories(t *testing.T) {
	cases := []struct {
		err      error
		category string
	}{
		{x509.HostnameError{Host: "private-fixture"}, "cert_hostname"},
		{x509.UnknownAuthorityError{}, "cert_authority"},
		{x509.CertificateInvalidError{Reason: x509.Expired}, "cert_validity"},
	}
	for _, tc := range cases {
		err := classifyCASFailure(&url.Error{Op: "Get", URL: "https://x/?ticket=private-fixture", Err: tc.err})
		code, _ := LoginFailureDetails(err)
		if code != tc.category || strings.Contains(err.Error(), "private-fixture") {
			t.Fatal(code, err)
		}
	}
}
