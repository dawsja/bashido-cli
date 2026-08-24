package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"text/tabwriter"
)

func TestSanitizeAndRawInput(t *testing.T) {
	if got := sanitize("safe\x1b[31m\nname"); got != "safe?[31m?name" {
		t.Fatalf("sanitize = %q", got)
	}
	dir := t.TempDir()
	path := dir + "/raw"
	want := "#!/bin/sh\nprintf x"
	if err := os.WriteFile(path, []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readInput(strings.NewReader("ignored"), path)
	if err != nil || got != want {
		t.Fatalf("readInput = %q, %v", got, err)
	}
	var b bytes.Buffer
	if err := writeJSON(&b, map[string]string{"content": "<x>"}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "{\"content\":\"<x>\"}\n" {
		t.Fatalf("JSON = %q", b.String())
	}
}

func TestColorOutput(t *testing.T) {
	a, out, errOut := testApp(t)
	a.useColor = func(io.Writer) bool { return true }

	if _, err := a.successf("Created %q.\n", "Deploy"); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "\x1b[32mCreated \"Deploy\".\x1b[0m\n"; got != want {
		t.Fatalf("colored success = %q, want %q", got, want)
	}
	if _, err := a.warningf("Warning: unavailable.\n"); err != nil {
		t.Fatal(err)
	}
	if got, want := errOut.String(), "\x1b[33mWarning: unavailable.\x1b[0m\n"; got != want {
		t.Fatalf("colored warning = %q, want %q", got, want)
	}
	if help := a.help(out); !strings.Contains(help, "\x1b[1mUsage:\x1b[0m") || !strings.Contains(help, "\x1b[36mscript\x1b[0m") || !strings.Contains(help, "\x1b[1mLibrary\x1b[0m") {
		t.Fatalf("colored help missing styles: %q", help)
	}
}

func TestColorDisabledByDefault(t *testing.T) {
	a, out, _ := testApp(t)
	if _, err := a.successf("Created.\n"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Created.\n" {
		t.Fatalf("redirected output = %q", got)
	}
}

func TestTableColorPreservesAlignment(t *testing.T) {
	a, _, _ := testApp(t)
	a.useColor = func(io.Writer) bool { return true }
	var colored bytes.Buffer
	w := tabwriter.NewWriter(&colored, 0, 4, 2, ' ', tabwriter.StripEscape)
	fmt.Fprintf(w, "%s\tTITLE\n%s\tDeploy\n", a.tablePaint(&colored, ansiBold, "ID"), a.tablePaint(&colored, ansiCyan, "abc"))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	plain := strings.NewReplacer(ansiBold, "", ansiCyan, "", ansiReset, "").Replace(colored.String())
	if want := "ID    TITLE\nabc  Deploy\n"; plain != want {
		t.Fatalf("table without color = %q, want %q", plain, want)
	}
	if strings.ContainsRune(colored.String(), tabwriter.Escape) {
		t.Fatalf("table output contains tabwriter escape: %q", colored.String())
	}
}

func TestSplitEditorCommand(t *testing.T) {
	got, err := splitCommand(`code --wait "a b" 'c d'`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"code", "--wait", "a b", "c d"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("argv = %#v", got)
	}
	if _, err := splitCommand(`sh -c 'unterminated`); err == nil {
		t.Fatal("unterminated quote accepted")
	}
}

func TestInstallerArchitectureAndSafetyMapping(t *testing.T) {
	b, err := os.ReadFile("scripts/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, required := range []string{"x86_64|amd64) arch=amd64", "aarch64|arm64) arch=arm64", "sha256sum", "shasum -a 256", "openssl dgst -sha256", "mv -f \"$tmp_bin\" \"$bin_dir/bashido\"", "BASHIDO_SERVER"} {
		if !strings.Contains(s, required) {
			t.Errorf("installer missing %q", required)
		}
	}
}
