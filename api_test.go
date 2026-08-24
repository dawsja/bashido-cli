package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serverClient(t *testing.T, h http.Handler) (*client, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(h)
	cl, err := newClient(profile{Origin: ts.URL}, "top-secret")
	if err != nil {
		ts.Close()
		t.Fatal(err)
	}
	return cl, ts
}

func TestAPIHeadersStrictJSONAndError(t *testing.T) {
	cl, ts := serverClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer top-secret" {
			t.Error("missing bearer header")
		}
		switch r.URL.Path {
		case "/ok":
			io.WriteString(w, `{"id":"12345678","title":"x","revision":1}`)
		case "/unknown":
			io.WriteString(w, `{"id":"x","extra":true}`)
		default:
			w.WriteHeader(422)
			io.WriteString(w, `{"error":{"code":"invalid","message":"bad request"}}`)
		}
	}))
	defer ts.Close()
	var s script
	if _, err := cl.do(t.Context(), "GET", "/ok", nil, &s, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.do(t.Context(), "GET", "/unknown", nil, &s, nil); err == nil {
		t.Fatal("unknown JSON field accepted")
	}
	_, err := cl.do(t.Context(), "GET", "/error", nil, nil, nil)
	var ae *apiError
	if !errors.As(err, &ae) || ae.Code != "invalid" || ae.Message != "bad request" {
		t.Fatalf("error = %#v", err)
	}
}

func TestAPIErrorRedactsTokenAndControls(t *testing.T) {
	cl, ts := serverClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"code":"bad","message":"top-secret\u001b leaked"}}`)
	}))
	defer ts.Close()
	_, err := cl.do(t.Context(), "GET", "/", nil, nil, nil)
	if err == nil || strings.Contains(err.Error(), "top-secret") || strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("unsafe error = %q", err)
	}
}

func TestCrossOriginRedirectRejected(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect destination reached") }))
	defer destination.Close()
	cl, source := serverClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer source.Close()
	if _, err := cl.do(t.Context(), "GET", "/", nil, nil, nil); err == nil || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestDevicePollingPendingSlowDownSuccess(t *testing.T) {
	var calls atomic.Int32
	cl, ts := serverClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["grant_type"] != "urn:ietf:params:oauth:grant-type:device_code" || body["client_id"] != "bashido-cli" || body["device_code"] != "private-code" {
			t.Errorf("body = %#v", body)
		}
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"authorization_pending","error_description":"pending"}`)
		case 2:
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"slow_down","error_description":"slow"}`)
		default:
			io.WriteString(w, `{"access_token":"token","token_type":"Bearer"}`)
		}
	}))
	defer ts.Close()
	cl.token = ""
	original := pollDelay
	defer func() { pollDelay = original }()
	var waits []time.Duration
	pollDelay = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	token, err := pollToken(t.Context(), cl, deviceCode{DeviceCode: "private-code", ExpiresIn: 60, Interval: 1})
	if err != nil || token != "token" {
		t.Fatalf("poll = %q, %v", token, err)
	}
	if len(waits) != 3 || waits[2] != 6*time.Second {
		t.Fatalf("waits = %v", waits)
	}
}

func TestResolveReferences(t *testing.T) {
	rows := []script{{ID: "aaaaaaaa11111111", Title: "unique", Revision: 1}, {ID: "bbbbbbbb22222222", Title: "duplicate", Revision: 1}, {ID: "cccccccc33333333", Title: "duplicate", Revision: 1}}
	cl, ts := serverClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/scripts/")
		if r.URL.Path == "/api/v1/scripts" {
			json.NewEncoder(w).Encode(scriptsEnvelope{Scripts: rows})
			return
		}
		for _, s := range rows {
			if id == s.ID {
				json.NewEncoder(w).Encode(scriptEnvelope{Script: s})
				return
			}
		}
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"code":"not_found","message":"missing"}}`)
	}))
	defer ts.Close()
	if s, err := resolveScript(t.Context(), cl, "aaaaaaaa"); err != nil || s.ID != rows[0].ID {
		t.Fatalf("prefix = %#v, %v", s, err)
	}
	if s, err := resolveScript(t.Context(), cl, "unique"); err != nil || s.ID != rows[0].ID {
		t.Fatalf("title = %#v, %v", s, err)
	}
	if _, err := resolveScript(t.Context(), cl, "duplicate"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate = %v", err)
	}
	if _, err := resolveScript(t.Context(), cl, "aaaa"); err == nil {
		t.Fatal("short prefix accepted")
	}
}

func TestLoginFlowSavesOnlyAccessToken(t *testing.T) {
	var origin string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/device/code":
			io.WriteString(w, `{"device_code":"private-device-code","user_code":"123456","verification_uri":"`+origin+`/link","verification_uri_complete":"`+origin+`/link?code=123456","expires_in":60,"interval":1}`)
		case "/api/auth/device/api-key-token":
			io.WriteString(w, `{"access_token":"private-access-token","token_type":"Bearer"}`)
		case "/api/v1/me":
			if r.Header.Get("Authorization") != "Bearer private-access-token" {
				t.Error("validation missing token")
			}
			io.WriteString(w, `{}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer ts.Close()
	origin = ts.URL
	a, out, errOut := testApp(t)
	a.in = strings.NewReader("n\n")
	a.isInteractive = func() bool { return true }
	cfg, creds, dir, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["p"] = profile{Origin: origin}
	cfg.Current = "p"
	if err = saveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	original := pollDelay
	defer func() { pollDelay = original }()
	pollDelay = func(context.Context, time.Duration) error { return nil }
	if err = a.authLogin(t.Context(), []string{"--no-browser"}); err != nil {
		t.Fatal(err)
	}
	_, saved, _, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Profiles["p"].Token != "private-access-token" {
		t.Fatal("access token not saved")
	}
	log := errOut.String()
	if !strings.Contains(log, origin+"/link") || !strings.Contains(log, "123 456") || !strings.Contains(log, "Waiting for authorization...") {
		t.Fatalf("login output = %q", log)
	}
	if strings.Contains(log, "private-device-code") || strings.Contains(log, "private-access-token") {
		t.Fatalf("secret leaked: %q", log)
	}
	if !strings.Contains(log, "Set up Bash tab completion? [y/N]") {
		t.Fatalf("completion setup was not offered: %q", log)
	}
	if got := out.String(); got != "Logged in to "+origin+" as profile \"p\".\n" {
		t.Fatalf("completion output = %q", got)
	}
	if strings.Contains(log, "Logged in") {
		t.Fatalf("completion written to stderr: %q", log)
	}
	_ = creds
}
