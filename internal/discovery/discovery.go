// Package discovery identifies public VPN authentication methods without credentials.
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Method struct {
	Type      string `json:"type"`
	Domain    string `json:"domain,omitempty"`
	Name      string `json:"name"`
	Supported bool   `json:"supported"`
}
type Result struct {
	Server   string   `json:"server"`
	Protocol string   `json:"protocol"`
	Detected bool     `json:"detected"`
	Methods  []Method `json:"methods"`
	Groups   []string `json:"groups,omitempty"`
}
type Client struct{ HTTP *http.Client }

var ErrUnknownProtocol = errors.New("gateway protocol was not recognized; select anyconnect or atrust explicitly")

func NormalizeServer(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("gateway must be an HTTPS URL without credentials, query or fragment")
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/"), nil
}
func (c Client) Discover(ctx context.Context, raw, protocol string) (Result, error) {
	server, err := NormalizeServer(raw)
	if err != nil {
		return Result{}, err
	}
	if protocol != "" && protocol != "atrust" && protocol != "anyconnect" {
		return Result{}, errors.New("protocol must be anyconnect or atrust")
	}
	if protocol != "anyconnect" {
		r, recognized, err := c.atrust(ctx, server)
		if err != nil {
			return Result{}, err
		}
		if recognized {
			return r, nil
		}
		if protocol == "atrust" {
			return Result{}, errors.New("gateway did not return aTrust authentication methods")
		}
	}
	r, recognized, err := c.anyconnect(ctx, server)
	if err != nil {
		return Result{}, err
	}
	if recognized || protocol == "anyconnect" {
		r.Detected = recognized
		return r, nil
	}
	return Result{}, ErrUnknownProtocol
}
func (c Client) request(ctx context.Context, method, endpoint string, body []byte) ([]byte, int, error) {
	base := c.HTTP
	if base == nil {
		base = &http.Client{Timeout: 12 * time.Second}
	}
	client := *base
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 4 || req.URL.Scheme != "https" || origin(req.URL) != origin(via[0].URL) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	if client.Timeout == 0 {
		client.Timeout = 12 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, errors.New("invalid discovery request")
	}
	req.Header.Set("User-Agent", "OCH authentication discovery")
	if body != nil {
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("User-Agent", "AnyConnect-compatible OCH")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("could not securely contact gateway for authentication discovery")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, 0, errors.New("gateway discovery response is unreadable or too large")
	}
	return data, response.StatusCode, nil
}
func (c Client) atrust(ctx context.Context, server string) (Result, bool, error) {
	u, _ := url.Parse(server)
	u.Path = "/public/manifest"
	b, status, err := c.request(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, false, err
	}
	var manifest struct {
		Code *int `json:"code"`
		Data struct {
			Capacities map[string]json.RawMessage `json:"capacities"`
		} `json:"data"`
	}
	if status != 200 || json.Unmarshal(b, &manifest) != nil || manifest.Code == nil || *manifest.Code != 0 || len(manifest.Data.Capacities) == 0 {
		return Result{}, false, nil
	}
	u.Path = "/passport/v1/public/authConfig"
	u.RawQuery = "clientType=SDPClient&platform=Linux&lang=en-US"
	b, status, err = c.request(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, false, err
	}
	var auth struct {
		Code *int `json:"code"`
		Data struct {
			Methods []struct {
				Type   string `json:"authType"`
				Domain string `json:"loginDomain"`
				Name   string `json:"authName"`
			} `json:"authServerInfoList"`
		} `json:"data"`
	}
	if status != 200 || json.Unmarshal(b, &auth) != nil || auth.Code == nil || *auth.Code != 0 {
		return Result{}, false, errors.New("aTrust gateway authentication discovery failed")
	}
	u.RawQuery = ""
	u.Path = ""
	r := Result{Server: u.String(), Protocol: "atrust", Detected: true}
	for _, m := range auth.Data.Methods {
		if m.Type != "" {
			r.Methods = append(r.Methods, Method{Type: m.Type, Domain: m.Domain, Name: m.Name, Supported: m.Type == "auth/cas" || m.Type == "auth/psw"})
		}
	}
	if len(r.Methods) == 0 {
		return Result{}, false, errors.New("aTrust gateway did not advertise an authentication method")
	}
	return r, true, nil
}
func (c Client) anyconnect(ctx context.Context, server string) (Result, bool, error) {
	r := Result{Server: server, Protocol: "anyconnect", Methods: []Method{{Type: "password", Name: "AnyConnect credentials", Supported: true}}}
	body := []byte(`<?xml version="1.0"?><config-auth client="vpn" type="init" aggregate-auth-version="2"><version who="vpn">1.0</version><device-id>linux-64</device-id></config-auth>`)
	data, status, err := c.request(ctx, http.MethodPost, server, body)
	if err != nil {
		return r, false, err
	}
	var doc struct {
		XMLName xml.Name
		Auth    struct {
			Form struct {
				Selects []struct {
					Name    string `xml:"name,attr"`
					Options []struct {
						Value string `xml:"value,attr"`
					} `xml:"option"`
				} `xml:"select"`
			} `xml:"form"`
		} `xml:"auth"`
	}
	if status != 200 || xml.Unmarshal(data, &doc) != nil || doc.XMLName.Local != "config-auth" {
		return r, false, nil
	}
	for _, selectField := range doc.Auth.Form.Selects {
		if selectField.Name == "group_list" {
			for _, o := range selectField.Options {
				r.Groups = append(r.Groups, o.Value)
			}
		}
	}
	return r, true, nil
}

func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return strings.ToLower(u.Scheme+"://"+u.Hostname()) + ":" + port
}
