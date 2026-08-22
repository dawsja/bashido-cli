package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installTestExecutable(t *testing.T, a *app) string {
	t.Helper()
	path, err := a.executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func configureProfiles(t *testing.T, a *app, origin string, tokens map[string]string) string {
	t.Helper()
	cfg, creds, dir, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range tokens {
		cfg.Profiles[name] = profile{Origin: origin}
		creds.Profiles[name] = credential{Origin: origin, Token: token}
	}
	if err = saveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if err = saveCredentials(dir, creds); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUninstallRevokesAllCredentialsAndRemovesFiles(t *testing.T) {
	requests := map[string]int{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/me/credential" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		requests[r.Header.Get("Authorization")]++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	a, out, _ := testApp(t)
	executable := installTestExecutable(t, a)
	dir := configureProfiles(t, a, ts.URL, map[string]string{"one": "token-one", "two": "token-two"})
	if err := a.run(t.Context(), []string{"uninstall", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if requests["Bearer token-one"] != 1 || requests["Bearer token-two"] != 1 {
		t.Fatalf("requests = %#v", requests)
	}
	if _, err := os.Lstat(executable); !os.IsNotExist(err) {
		t.Fatalf("executable still exists: %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("configuration directory still exists: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Revoked 2 credential(s).") || !strings.Contains(got, "Removed "+executable) {
		t.Fatalf("output = %q", got)
	}
}

func TestUninstallFailureRetainsInstallationAndFailedCredential(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer failed" {
			http.Error(w, `{"error":{"code":"unavailable","message":"try later"}}`, http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	a, _, _ := testApp(t)
	executable := installTestExecutable(t, a)
	dir := configureProfiles(t, a, ts.URL, map[string]string{"failed": "failed", "revoked": "revoked"})
	err := a.run(t.Context(), []string{"uninstall", "--yes"})
	if err == nil || !strings.Contains(err.Error(), "profiles failed") {
		t.Fatalf("error = %v", err)
	}
	if _, err = os.Stat(executable); err != nil {
		t.Fatalf("executable removed: %v", err)
	}
	var saved credentials
	if err = readJSON(filepath.Join(dir, credFile), &saved, true); err != nil {
		t.Fatal(err)
	}
	if len(saved.Profiles) != 1 || saved.Profiles["failed"].Token != "failed" {
		encoded, _ := json.Marshal(saved)
		t.Fatalf("saved credentials = %s", encoded)
	}
}

func TestUninstallLocalOnly(t *testing.T) {
	a, out, errOut := testApp(t)
	executable := installTestExecutable(t, a)
	configureProfiles(t, a, "https://unreachable.example", map[string]string{"one": "secret"})
	if err := a.run(t.Context(), []string{"uninstall", "--local-only", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(executable); !os.IsNotExist(err) {
		t.Fatalf("executable still exists: %v", err)
	}
	if strings.Contains(out.String(), "Revoked") || !strings.Contains(errOut.String(), "not revoked") {
		t.Fatalf("stdout = %q, stderr = %q", out.String(), errOut.String())
	}
}

func TestUninstallRequiresConfirmation(t *testing.T) {
	a, _, _ := testApp(t)
	if err := a.run(t.Context(), []string{"uninstall"}); err == nil || !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("error = %v", err)
	}
}
