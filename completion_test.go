package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBashCompletionScript(t *testing.T) {
	a, out, _ := testApp(t)
	if err := a.run(t.Context(), []string{"completion", "bash"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != bashCompletion {
		t.Fatal("completion command did not emit the Bash completion script")
	}
	for _, required := range []string{"complete -F _bashido bashido", "__complete scripts", "__complete profiles", "script:restore|script:purge", "note:show|note:set|note:edit|note:clear", "--ca-file=*|--notes-file=*", "compopt -o filenames 2>/dev/null || true"} {
		if !strings.Contains(out.String(), required) {
			t.Errorf("completion script missing %q", required)
		}
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(out.String())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v: %s", err, output)
	}

	fake := filepath.Join(t.TempDir(), "bashido")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' 'Deploy App' Other\n"), 0700); err != nil {
		t.Fatal(err)
	}
	probe := bashCompletion + `
COMP_WORDS=("$1" script show --json De)
COMP_CWORD=4
_bashido
printf '%s\n' "${COMPREPLY[@]}"
COMP_WORDS=(bashido uninstall --yes --l)
COMP_CWORD=3
_bashido
printf '%s\n' "${COMPREPLY[@]}"
`
	cmd = exec.Command("bash", "-c", probe, "completion-test", fake)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execute completion: %v: %s", err, output)
	} else if string(output) != "Deploy App\n--local-only\n" {
		t.Fatalf("dynamic completion = %q", output)
	}
}

func TestOfferBashCompletion(t *testing.T) {
	home := t.TempDir()
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("export TEST=1"), 0600); err != nil {
		t.Fatal(err)
	}
	a, out, errOut := testApp(t)
	getenv := a.getenv
	a.getenv = func(key string) string {
		if key == "HOME" {
			return home
		}
		return getenv(key)
	}
	a.isInteractive = func() bool { return true }
	a.in = strings.NewReader("maybe\ny\n")
	_, _, dir, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.offerBashCompletion(dir); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "export TEST=1\n\n# Bashido tab completion\n"+bashCompletionSource+"\n") {
		t.Fatalf(".bashrc = %q", content)
	}
	if strings.Count(string(content), bashCompletionSource) != 1 {
		t.Fatalf("completion entry count = %d", strings.Count(string(content), bashCompletionSource))
	}
	saved, _, _, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.CompletionOffered {
		t.Fatal("completion offer was not persisted")
	}
	if got := errOut.String(); got != "Set up Bash tab completion? [y/N] Please answer y or n.\nSet up Bash tab completion? [y/N] " {
		t.Fatalf("prompt output = %q", got)
	}
	if got := out.String(); got != "Enabled Bash completion in "+bashrc+"; open a new shell to use it.\n" {
		t.Fatalf("setup output = %q", got)
	}

	out.Reset()
	errOut.Reset()
	if err = a.installBashCompletion(); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), bashCompletionSource) != 1 {
		t.Fatal("completion setup was duplicated")
	}
	if got := out.String(); got != "Bash completion is already configured in "+bashrc+".\n" {
		t.Fatalf("idempotent output = %q", got)
	}
}

func TestCompletionOfferSkipsNonInteractiveInput(t *testing.T) {
	a, out, errOut := testApp(t)
	a.in = strings.NewReader("y\n")
	a.isInteractive = func() bool { return false }
	_, _, dir, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.offerBashCompletion(dir); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("non-interactive output = %q, %q", out.String(), errOut.String())
	}
	saved, _, _, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.CompletionOffered {
		t.Fatal("non-interactive offer was marked complete")
	}
}

func TestCompletionInstallRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "target")
	if err := os.WriteFile(target, []byte("unchanged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".bashrc")); err != nil {
		t.Fatal(err)
	}
	a, _, _ := testApp(t)
	getenv := a.getenv
	a.getenv = func(key string) string {
		if key == "HOME" {
			return home
		}
		return getenv(key)
	}
	if err := a.installBashCompletion(); err == nil || !strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("symlink error = %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "unchanged\n" {
		t.Fatalf("symlink target changed: %q", content)
	}
}

func TestCompletionInstallIgnoresCommentedEntry(t *testing.T) {
	home := t.TempDir()
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("# "+bashCompletionSource+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, _, _ := testApp(t)
	getenv := a.getenv
	a.getenv = func(key string) string {
		if key == "HOME" {
			return home
		}
		return getenv(key)
	}
	if err := a.installBashCompletion(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == bashCompletionSource {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active completion entries = %d: %q", active, content)
	}
}

func TestConcurrentCompletionInstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	bashrc := filepath.Join(home, ".bashrc")
	apps := make([]*app, 2)
	for i := range apps {
		a, _, _ := testApp(t)
		getenv := a.getenv
		a.getenv = func(key string) string {
			if key == "HOME" {
				return home
			}
			return getenv(key)
		}
		apps[i] = a
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(apps))
	for _, a := range apps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- a.installBashCompletion()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), bashCompletionSource) != 1 {
		t.Fatalf("concurrent completion entries = %d: %q", strings.Count(string(content), bashCompletionSource), content)
	}
}

func TestCompletionSetupFailureIsWarning(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".bashrc")); err != nil {
		t.Fatal(err)
	}
	a, _, errOut := testApp(t)
	getenv := a.getenv
	a.getenv = func(key string) string {
		if key == "HOME" {
			return home
		}
		return getenv(key)
	}
	a.in = strings.NewReader("y\n")
	a.isInteractive = func() bool { return true }
	_, _, dir, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.offerBashCompletion(dir); err != nil {
		t.Fatalf("optional setup failed login: %v", err)
	}
	saved, _, _, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.CompletionOffered {
		t.Fatal("failed setup offer was not persisted")
	}
	if got := errOut.String(); !strings.Contains(got, "Run 'bashido completion install' to retry.") {
		t.Fatalf("warning = %q", got)
	}
}

func TestTerminalDetectionRejectsDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("/dev/null detected as a terminal")
	}
}

func TestCompletionCandidates(t *testing.T) {
	const (
		firstID  = "aaaaaaaa11111111"
		secondID = "bbbbbbbb22222222"
		thirdID  = "cccccccc33333333"
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scripts" || r.URL.Query().Get("state") != "all" {
			t.Errorf("unexpected %s", r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(scriptsEnvelope{Scripts: []script{
			{ID: firstID, Title: "Deploy App"},
			{ID: secondID, Title: "Duplicate"},
			{ID: thirdID, Title: "Duplicate"},
		}})
	}))
	defer ts.Close()

	a, out, _ := testApp(t)
	configureServer(t, a, ts.URL)
	if err := a.run(t.Context(), []string{"__complete", "scripts", "all", "Dep"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Deploy App\n" {
		t.Fatalf("prefixed script candidates = %q", got)
	}
	out.Reset()
	if err := a.run(t.Context(), []string{"__complete", "scripts", "all"}); err != nil {
		t.Fatal(err)
	}
	want := "Deploy App\n" + firstID + "\n" + secondID + "\n" + thirdID + "\n"
	if got := out.String(); got != want {
		t.Fatalf("script candidates = %q, want %q", got, want)
	}

	out.Reset()
	cfg, _, dir, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["alpha"] = profile{Origin: "https://alpha.example"}
	cfg.Profiles["zulu"] = profile{Origin: "https://zulu.example"}
	if err = saveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if err = a.run(t.Context(), []string{"__complete", "profiles"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "alpha\np\nzulu\n" {
		t.Fatalf("profile candidates = %q", got)
	}
}

func TestCompletionCandidatesAreBounded(t *testing.T) {
	rows := make([]script, maxCompletionCandidates+25)
	for i := range rows {
		rows[i].ID = "id-" + strconv.Itoa(i)
	}
	rows[len(rows)-1].Title = "zulu"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(scriptsEnvelope{Scripts: rows})
	}))
	defer ts.Close()

	a, out, _ := testApp(t)
	configureServer(t, a, ts.URL)
	if err := a.run(t.Context(), []string{"__complete", "scripts", "all"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out.String(), "\n"); got != maxCompletionCandidates {
		t.Fatalf("candidate count = %d, want %d", got, maxCompletionCandidates)
	}
	out.Reset()
	if err := a.run(t.Context(), []string{"__complete", "scripts", "all", "z"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "zulu\n" {
		t.Fatalf("prefixed candidates = %q", got)
	}
}

func TestCompletionRequestTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer ts.Close()

	a, _, _ := testApp(t)
	configureServer(t, a, ts.URL)
	original := completionRequestTimeout
	completionRequestTimeout = 10 * time.Millisecond
	defer func() { completionRequestTimeout = original }()
	if err := a.run(t.Context(), []string{"__complete", "scripts", "all"}); err == nil {
		t.Fatal("completion request did not time out")
	}
}
