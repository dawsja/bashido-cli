package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type deviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	APIKeyID    string `json:"api_key_id"`
}

func (a *app) authCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fail(2, "usage: bashido auth login|status|logout")
	}
	if isHelp(args[0]) {
		return a.printHelp("auth")
	}
	switch args[0] {
	case "login":
		return a.authLogin(ctx, args[1:])
	case "status":
		if hasHelp(args[1:]) {
			return a.printHelp("auth status")
		}
		if len(args) != 1 {
			return fail(2, "auth status takes no arguments")
		}
		cfg, creds, _, err := a.load()
		if err != nil {
			return err
		}
		name, p, token, err := a.bearer(cfg, creds)
		if err != nil {
			return err
		}
		cl, err := newClient(p, token)
		if err != nil {
			return err
		}
		var me map[string]any
		if _, err = cl.do(ctx, "GET", "/api/v1/me", nil, &me, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s %s (%s)\n", a.paint(a.out, ansiGreen+ansiBold, "Logged in:"), sanitize(name), sanitize(p.Origin))
		if identity := meIdentity(me); identity != "" {
			fmt.Fprintf(a.out, "%s   %s\n", a.paint(a.out, ansiBold, "Account:"), sanitize(identity))
		}
		return nil
	case "logout":
		if hasHelp(args[1:]) {
			return a.printHelp("auth logout")
		}
		f := a.flags("auth logout")
		local := f.Bool("local-only", false, "do not revoke remotely")
		if err := f.Parse(optionsFirst(args[1:], nil)); err != nil {
			return fail(2, "%v", err)
		}
		if f.NArg() != 0 {
			return fail(2, "auth logout takes no arguments")
		}
		cfg, creds, dir, err := a.load()
		if err != nil {
			return err
		}
		name, p, err := a.active(cfg)
		if err != nil {
			return err
		}
		c, ok := creds.Profiles[name]
		if !ok {
			_, err = fmt.Fprintf(a.out, "Already logged out of profile %q.\n", sanitize(name))
			return err
		}
		revoked := false
		if !*local && c.Origin == p.Origin && c.Token != "" {
			cl, e := newClient(p, c.Token)
			if e != nil {
				return e
			}
			if _, e = cl.do(ctx, "DELETE", "/api/v1/me/credential", nil, nil, nil); e != nil {
				return fmt.Errorf("revoke credential (local credential retained): %w", e)
			}
			revoked = true
		}
		delete(creds.Profiles, name)
		if err = saveCredentials(dir, creds); err != nil {
			return err
		}
		if revoked {
			_, err = a.successf("Logged out of profile %q and revoked its credential.\n", sanitize(name))
			return err
		}
		if c.Token != "" {
			if _, err = a.warningf("Warning: the server credential was not revoked.\n"); err != nil {
				return err
			}
		}
		_, err = a.successf("Removed the local credential for profile %q.\n", sanitize(name))
		return err
	default:
		return fail(2, "unknown auth command %q", args[0])
	}
}

func (a *app) authLogin(ctx context.Context, args []string) error {
	if hasHelp(args) {
		return a.printHelp("auth login")
	}
	f := a.flags("auth login")
	noBrowser := f.Bool("no-browser", false, "do not open browser")
	replace := f.Bool("replace", false, "replace existing credential")
	if err := f.Parse(optionsFirst(args, nil)); err != nil {
		return fail(2, "%v", err)
	}
	if f.NArg() != 0 {
		return fail(2, "auth login takes no arguments")
	}
	cfg, creds, dir, err := a.load()
	if err != nil {
		return err
	}
	name, p, err := a.active(cfg)
	if err != nil {
		return err
	}
	c, hasCredential := creds.Profiles[name]
	if hasCredential && c.Token != "" && !*replace {
		return fail(2, "profile %q already has a credential; use --replace", name)
	}
	cl, err := newClient(p, "")
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	host = safeDeviceName(host)
	var dc deviceCode
	if _, err = cl.do(ctx, "POST", "/api/auth/device/code", map[string]string{"client_id": "bashido-cli", "scope": "scripts:read scripts:write", "device_name": host}, &dc, nil); err != nil {
		return err
	}
	if dc.DeviceCode == "" || dc.UserCode == "" || dc.ExpiresIn <= 0 {
		return errors.New("invalid device authorization response")
	}
	if matched, _ := regexp.MatchString(`^[0-9]{6}$`, dc.UserCode); !matched {
		return errors.New("device authorization response has an invalid user code")
	}
	link := p.Origin + "/link"
	fmt.Fprintf(a.errOut, "Open  %s\nEnter %s\n\n", a.paint(a.errOut, ansiCyan, link), a.paint(a.errOut, ansiBold, formatUserCode(sanitize(dc.UserCode))))
	fmt.Fprintln(a.errOut, a.paint(a.errOut, ansiDim, "Waiting for authorization..."))
	if !*noBrowser && dc.VerificationURIComplete != "" {
		if u, e := url.Parse(dc.VerificationURIComplete); e == nil && u.Scheme+"://"+u.Host == p.Origin {
			_ = exec.CommandContext(ctx, "xdg-open", dc.VerificationURIComplete).Start()
		}
	}
	token, err := pollToken(ctx, cl, dc)
	if err != nil {
		return err
	}
	cl.token = token
	var me map[string]any
	if _, err = cl.do(ctx, "GET", "/api/v1/me", nil, &me, nil); err != nil {
		return fmt.Errorf("validate credential: %w", err)
	}
	creds.Profiles[name] = credential{Origin: p.Origin, Token: token}
	if err = saveCredentials(dir, creds); err != nil {
		return err
	}
	if _, err = a.successf("Logged in to %s as profile %q.\n", sanitize(p.Origin), sanitize(name)); err != nil {
		return err
	}
	if !cfg.CompletionOffered {
		return a.offerBashCompletion(dir)
	}
	return nil
}

func formatUserCode(code string) string {
	if len(code) == 6 {
		return code[:3] + " " + code[3:]
	}
	return code
}

func meIdentity(me map[string]any) string {
	for _, key := range []string{"email", "username", "login", "name"} {
		v, ok := me[key].(string)
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func safeDeviceName(value string) string {
	value = regexp.MustCompile(`[^A-Za-z0-9 ._()/-]+`).ReplaceAllString(value, "-")
	value = strings.TrimSpace(value)
	if len(value) > 64 {
		value = value[:64]
	}
	if value == "" || !regexp.MustCompile(`^[A-Za-z0-9]`).MatchString(value) {
		return "Linux device"
	}
	return value
}

func pollToken(ctx context.Context, cl *client, dc deviceCode) (string, error) {
	interval := dc.Interval
	if interval < 1 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", errors.New("device authorization expired")
		}
		delay := time.Duration(interval) * time.Second
		if delay > remaining {
			delay = remaining
		}
		if err := pollDelay(ctx, delay); err != nil {
			return "", err
		}
		if time.Now().After(deadline) {
			return "", errors.New("device authorization expired")
		}
		var tr tokenResponse
		_, err := cl.do(ctx, "POST", "/api/auth/device/api-key-token", map[string]string{"grant_type": "urn:ietf:params:oauth:grant-type:device_code", "device_code": dc.DeviceCode, "client_id": "bashido-cli"}, &tr, nil)
		if err == nil {
			if tr.AccessToken == "" {
				return "", errors.New("token endpoint returned an empty token")
			}
			if tr.TokenType != "" && !strings.EqualFold(tr.TokenType, "Bearer") {
				return "", errors.New("token endpoint returned an unsupported token type")
			}
			return tr.AccessToken, nil
		}
		var ae *apiError
		if !errors.As(err, &ae) {
			return "", err
		}
		switch ae.Code {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5
			continue
		case "access_denied":
			return "", errors.New("device authorization denied")
		case "expired_token":
			return "", errors.New("device authorization expired")
		default:
			return "", fmt.Errorf("device token request failed (%s)", sanitize(ae.Code))
		}
	}
}

var pollDelay = func(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
