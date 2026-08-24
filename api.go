package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxResponse = 8 << 20

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("server error (%s): %s", sanitize(e.Code), sanitize(e.Message))
	}
	return fmt.Sprintf("server returned HTTP %d", e.Status)
}

type client struct {
	origin string
	token  string
	http   *http.Client
}

func newClient(p profile, token string) (*client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if p.CAFile != "" {
		pem, err := os.ReadFile(p.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsConfig, DialContext: (&netDialer).DialContext, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: 30 * time.Second}
	c := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme+"://"+req.URL.Host != p.Origin {
			return errors.New("refusing cross-origin redirect")
		}
		return nil
	}
	return &client{origin: p.Origin, token: token, http: c}, nil
}

var netDialer = struct {
	DialContext func(context.Context, string, string) (net.Conn, error)
}{DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext}

func (c *client) do(ctx context.Context, method, path string, body, out any, headers map[string]string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxResponse+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return resp, err
	}
	if len(b) > maxResponse {
		return resp, errors.New("server response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error            json.RawMessage `json:"error"`
			ErrorDescription string          `json:"error_description"`
		}
		_ = json.Unmarshal(b, &envelope)
		var code, message string
		if len(envelope.Error) > 0 && envelope.Error[0] == '"' {
			_ = json.Unmarshal(envelope.Error, &code)
			message = envelope.ErrorDescription
		} else {
			var nested struct{ Code, Message string }
			_ = json.Unmarshal(envelope.Error, &nested)
			code, message = nested.Code, nested.Message
		}
		if c.token != "" {
			message = strings.ReplaceAll(message, c.token, "[REDACTED]")
		}
		return resp, &apiError{Status: resp.StatusCode, Code: code, Message: message}
	}
	if out != nil && len(bytes.TrimSpace(b)) != 0 {
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if err := d.Decode(out); err != nil {
			return resp, fmt.Errorf("decode server response: %w", err)
		}
		if d.Decode(&struct{}{}) != io.EOF {
			return resp, errors.New("decode server response: trailing data")
		}
	}
	return resp, nil
}

func bearer(cfg *config, creds *credentials) (string, profile, string, error) {
	name, p, err := active(cfg)
	if err != nil {
		return "", profile{}, "", err
	}
	return credentialFor(name, p, creds)
}

func (a *app) active(cfg *config) (string, profile, error) {
	if a.profileName == "" {
		return active(cfg)
	}
	p, ok := cfg.Profiles[a.profileName]
	if !ok {
		return "", profile{}, fail(2, "profile %q does not exist", a.profileName)
	}
	return a.profileName, p, nil
}

func (a *app) bearer(cfg *config, creds *credentials) (string, profile, string, error) {
	name, p, err := a.active(cfg)
	if err != nil {
		return "", profile{}, "", err
	}
	return credentialFor(name, p, creds)
}

func credentialFor(name string, p profile, creds *credentials) (string, profile, string, error) {
	c, ok := creds.Profiles[name]
	if !ok || c.Token == "" {
		return name, p, "", fail(3, "not logged in for profile %q; run 'bashido auth login'", name)
	}
	if c.Origin != p.Origin {
		return name, p, "", fail(3, "credential origin does not match profile; log in again")
	}
	return name, p, strings.TrimSpace(c.Token), nil
}
