package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func testApp(t *testing.T) (*app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, "bin", "bashido")
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{in: bytes.NewReader(nil), out: out, errOut: errOut, getenv: func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	}, executable: func() (string, error) { return executable, nil }}
	return a, out, errOut
}

func TestCanonicalOrigin(t *testing.T) {
	valid := map[string]string{
		"https://example.com":      "https://example.com",
		"HTTPS://EXAMPLE.COM":      "https://example.com",
		"https://example.com/":     "https://example.com",
		"https://example.com:8443": "https://example.com:8443",
		"http://localhost:3000":    "http://localhost:3000",
		"http://127.0.0.1":         "http://127.0.0.1",
		"http://[::1]:8080":        "http://[::1]:8080",
	}
	for input, want := range valid {
		got, err := canonicalOrigin(input)
		if err != nil || got != want {
			t.Errorf("canonicalOrigin(%q) = %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"example.com", "http://example.com", "ftp://example.com", "https://u@example.com", "https://example.com/a", "https://example.com?q=x", "https://example.com/#x"} {
		if _, err := canonicalOrigin(input); err == nil {
			t.Errorf("canonicalOrigin(%q) unexpectedly succeeded", input)
		}
	}
}

func TestProfileAddFlagsAfterArguments(t *testing.T) {
	a, out, _ := testApp(t)
	if err := a.run(t.Context(), []string{"profile", "add", "work", "https://example.com", "--use"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Added and selected profile \"work\" (https://example.com).\n" {
		t.Fatalf("profile add output = %q", got)
	}
	out.Reset()
	if err := a.run(t.Context(), []string{"profile", "add", "work", "https://example.com", "--use"}); err != nil {
		t.Fatalf("idempotent profile add: %v", err)
	}
	if got := out.String(); got != "Profile \"work\" already exists with these settings and is already selected.\n" {
		t.Fatalf("idempotent profile add output = %q", got)
	}
	out.Reset()
	if err := a.run(t.Context(), []string{"profile", "use", "work"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Already using profile \"work\".\n" {
		t.Fatalf("profile use output = %q", got)
	}
	out.Reset()
	if err := a.run(t.Context(), []string{"profile", "list"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got == "" || !bytes.Contains([]byte(got), []byte("work")) {
		t.Fatalf("profile list = %q", got)
	}
	dir, _ := a.configDir()
	for _, name := range []string{configFile} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Fatalf("mode = %o", fi.Mode().Perm())
		}
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0700 {
		t.Fatalf("dir mode = %o", fi.Mode().Perm())
	}
}

func TestProfileAddAndUseAcknowledgements(t *testing.T) {
	a, out, _ := testApp(t)
	if err := a.run(t.Context(), []string{"profile", "add", "work", "https://work.example"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := a.run(t.Context(), []string{"profile", "add", "personal", "https://personal.example"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Added profile \"personal\" (https://personal.example).\n" {
		t.Fatalf("profile add output = %q", got)
	}
	out.Reset()
	if err := a.run(t.Context(), []string{"profile", "use", "personal"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Now using profile \"personal\".\n" {
		t.Fatalf("profile use output = %q", got)
	}
	out.Reset()
	if err := a.run(t.Context(), []string{"profile", "add", "work", "https://work.example", "--use"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Profile \"work\" already exists; now using it.\n" {
		t.Fatalf("existing profile selection output = %q", got)
	}
}

func TestUnsafePermissionsAndCredentialSymlink(t *testing.T) {
	a, _, _ := testApp(t)
	dir, _ := a.configDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.load(); err == nil {
		t.Fatal("unsafe directory accepted")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "creds")
	if err := os.WriteFile(target, []byte(`{"profiles":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, credFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.load(); err == nil {
		t.Fatal("credential symlink accepted")
	}
}

func TestCredentialOriginBinding(t *testing.T) {
	cfg := &config{Current: "p", Profiles: map[string]profile{"p": {Origin: "https://one.example"}}}
	creds := &credentials{Profiles: map[string]credential{"p": {Origin: "https://two.example", Token: "secret"}}}
	if _, _, _, err := bearer(cfg, creds); err == nil {
		t.Fatal("mismatched origin accepted")
	}
}
