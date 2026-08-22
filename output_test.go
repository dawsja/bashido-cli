package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
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
