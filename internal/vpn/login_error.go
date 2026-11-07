package vpn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// LoginError contains only a fixed diagnostic category and a numeric server code.
type LoginError struct {
	category string
	status   int
}

var loginDiagnostics = map[string]string{
	"login_required":   "VPN authentication expired or missing; run och login",
	"unknown":          "VPN authentication or tunnel setup failed",
	"canceled":         "VPN login was canceled",
	"timeout":          "VPN login timed out",
	"certificate":      "aTrust gateway certificate validation failed",
	"cert_hostname":    "aTrust tunnel or gateway certificate does not match the TLS server name",
	"cert_authority":   "aTrust tunnel or gateway certificate is not trusted",
	"cert_validity":    "aTrust tunnel or gateway certificate is outside its validity period",
	"network":          "aTrust gateway network request failed",
	"network_timeout":  "aTrust gateway network request timed out",
	"redirect_scheme":  "CAS gateway returned a non-HTTPS portal redirect",
	"redirect_origin":  "CAS gateway returned a portal redirect for another origin",
	"redirect_path":    "CAS gateway returned an unsupported portal redirect path",
	"redirect_data":    "CAS gateway portal redirect is missing its data field",
	"redirect_url":     "CAS gateway returned a malformed portal redirect",
	"portal_data":      "CAS gateway portal data is invalid or missing its ticket",
	"secondary":        "aTrust requires an unsupported secondary authentication challenge",
	"browser":          "CAS browser login failed or was canceled",
	"callback":         "CAS browser callback did not match the configured gateway and domain",
	"node":             "aTrust has no reachable tunnel node",
	"tunnel":           "aTrust could not initialize its IP tunnel",
	"manifest":         "aTrust gateway manifest is unavailable or invalid",
	"auth_config":      "aTrust authentication configuration response is invalid",
	"cas_http":         "CAS callback returned HTTP status",
	"auth_config_http": "aTrust authentication configuration returned HTTP status",
	"auth_config_code": "aTrust authentication configuration failed with code",
	"report_env_code":  "aTrust environment report failed with code",
	"auth_check_code":  "aTrust authentication check failed with code",
	"ip_auth_code":     "aTrust IP tunnel authentication failed with code",
	"manifest_code":    "aTrust gateway manifest failed with code",
}

func LoginFailure(category string, status int) *LoginError {
	if _, ok := loginDiagnostics[category]; !ok {
		category = "unknown"
		status = 0
	}
	return &LoginError{category: category, status: status}
}
func (e *LoginError) Error() string {
	message := loginDiagnostics[e.category]
	if message == "" {
		return loginDiagnostics["unknown"]
	}
	if strings.HasSuffix(e.category, "_code") || strings.HasSuffix(e.category, "_http") {
		return fmt.Sprintf("%s %d", message, e.status)
	}
	return message
}
func (e *LoginError) Unwrap() error {
	if e.category == "login_required" {
		return ErrLoginRequired
	}
	if e.category == "canceled" {
		return context.Canceled
	}
	if e.category == "timeout" {
		return context.DeadlineExceeded
	}
	return nil
}
func LoginFailureDetails(err error) (string, int) {
	if errors.Is(err, ErrLoginRequired) {
		return "login_required", 0
	}
	var safe *LoginError
	if errors.As(err, &safe) {
		return safe.category, safe.status
	}
	if errors.Is(err, context.Canceled) {
		return "canceled", 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", 0
	}
	return "unknown", 0
}

var numericLoginFailures = []struct {
	pattern  *regexp.Regexp
	category string
}{
	{regexp.MustCompile(`^invalid status code: ([0-9]{3})$`), "cas_http"},
	{regexp.MustCompile(`(?:^|: )authConfig HTTP ([0-9]{3})$`), "auth_config_http"},
	{regexp.MustCompile(`(?:^|: )authConfig (?:failed with code |code )(-?[0-9]+)(?:$|:)`), "auth_config_code"},
	{regexp.MustCompile(`^reportEnv failed with code (-?[0-9]+)(?:$|:)`), "report_env_code"},
	{regexp.MustCompile(`^authCheck failed with code (-?[0-9]+)(?:$|:)`), "auth_check_code"},
	{regexp.MustCompile(`^IP tunnel authentication failed \(code (-?[0-9]+)\)`), "ip_auth_code"},
	{regexp.MustCompile(`^IP tunnel authentication status ([0-9]+)$`), "ip_auth_code"},
	{regexp.MustCompile(`^aTrust server manifest failed with code (-?[0-9]+)$`), "manifest_code"},
}

func classifyCASFailure(err error) *LoginError {
	category, status := LoginFailureDetails(err)
	if category != "unknown" {
		return LoginFailure(category, status)
	}
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &hostname) {
		return LoginFailure("cert_hostname", 0)
	}
	if errors.As(err, &unknown) {
		return LoginFailure("cert_authority", 0)
	}
	if errors.As(err, &invalid) && invalid.Reason == x509.Expired {
		return LoginFailure("cert_validity", 0)
	}
	if errors.As(err, &cert) || errors.As(err, &invalid) {
		return LoginFailure("certificate", 0)
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return LoginFailure("network_timeout", 0)
		}
		return LoginFailure("network", 0)
	}
	message := err.Error()
	for _, rule := range numericLoginFailures {
		if match := rule.pattern.FindStringSubmatch(message); len(match) == 2 {
			if number, e := strconv.Atoi(match[1]); e == nil {
				return LoginFailure(rule.category, number)
			}
		}
	}
	for _, rule := range []struct{ prefix, kind string }{
		{"invalid redirect url: scheme", "redirect_scheme"},
		{"invalid redirect url: host", "redirect_origin"},
		{"invalid redirect url: path", "redirect_path"},
		{"invalid redirect url: data", "redirect_data"},
		{"invalid redirect url:", "redirect_url"},
		{"invalid portal data:", "portal_data"},
		{"no reachable node", "node"},
		{"no available node", "node"},
		{"IP not available", "tunnel"},
		{"L3 tunnel not initialized", "tunnel"},
		{"failed to create L3 tunnel", "tunnel"},
		{"failed to get client IP", "tunnel"},
		{"failed to acquire aTrust server manifest", "manifest"},
		{"failed to parse aTrust server manifest", "manifest"},
		{"aTrust server manifest returned", "manifest"},
		{"authConfig response missing", "auth_config"},
		{"unsupported next authentication service", "secondary"},
		{"complete secondary", "secondary"},
		{"complete aTrust captcha", "secondary"},
		{"unsupported external login challenge", "secondary"},
		{"CAS browser login canceled", "browser"},
		{"CAS callback does not match", "callback"},
		{"CAS login URL does not match", "callback"},
	} {
		if strings.HasPrefix(message, rule.prefix) {
			return LoginFailure(rule.kind, 0)
		}
	}
	return LoginFailure("unknown", 0)
}
