package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

func upgradeServer(t *testing.T, binary []byte, checksum []byte) *httptest.Server {
	t.Helper()
	asset := "bashido-linux-" + runtime.GOARCH
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checksums.txt":
			fmt.Fprintf(w, "%x  %s\n", checksum, asset)
		case "/" + asset:
			w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestUpgradeReplacesExecutable(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("unsupported test architecture")
	}
	newBinary := []byte("new binary")
	sum := sha256.Sum256(newBinary)
	ts := upgradeServer(t, newBinary, sum[:])
	defer ts.Close()

	a, out, _ := testApp(t)
	a.releaseBase = ts.URL
	executable := installTestExecutable(t, a)
	if err := a.run(t.Context(), []string{"upgrade"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBinary) {
		t.Fatalf("executable = %q", got)
	}
	fi, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	if !strings.Contains(out.String(), "Upgraded bashido") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestUpgradeAlreadyCurrent(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("unsupported test architecture")
	}
	current := []byte("binary")
	sum := sha256.Sum256(current)
	ts := upgradeServer(t, current, sum[:])
	defer ts.Close()

	a, out, _ := testApp(t)
	a.releaseBase = ts.URL
	installTestExecutable(t, a)
	if err := a.run(t.Context(), []string{"upgrade"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestUpgradeChecksumFailurePreservesExecutable(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("unsupported test architecture")
	}
	wrong := sha256.Sum256([]byte("different binary"))
	ts := upgradeServer(t, []byte("new binary"), wrong[:])
	defer ts.Close()

	a, _, _ := testApp(t)
	a.releaseBase = ts.URL
	executable := installTestExecutable(t, a)
	if err := a.run(t.Context(), []string{"upgrade"}); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v", err)
	}
	got, err := os.ReadFile(executable)
	if err != nil || string(got) != "binary" {
		t.Fatalf("executable = %q, error = %v", got, err)
	}
}

func TestReleaseChecksumValidation(t *testing.T) {
	asset := "bashido-linux-amd64"
	sum := sha256.Sum256([]byte("binary"))
	if got, err := releaseChecksum([]byte(fmt.Sprintf("%x  %s\n", sum, asset)), asset); err != nil || string(got) != string(sum[:]) {
		t.Fatalf("checksum = %x, error = %v", got, err)
	}
	for _, data := range []string{
		"invalid  " + asset + "\n",
		fmt.Sprintf("%x  other\n", sum),
		fmt.Sprintf("%x  %s\n%x  %s\n", sum, asset, sum, asset),
	} {
		if _, err := releaseChecksum([]byte(data), asset); err == nil {
			t.Fatalf("accepted invalid checksums %q", data)
		}
	}
}

func TestSafeReleaseURL(t *testing.T) {
	for _, raw := range []string{"https://github.com/release", "http://localhost/release", "http://127.0.0.1/release"} {
		if !safeReleaseURL(raw) {
			t.Errorf("rejected %q", raw)
		}
	}
	for _, raw := range []string{"http://example.com/release", "ftp://example.com/release", "https://user@example.com/release"} {
		if safeReleaseURL(raw) {
			t.Errorf("accepted %q", raw)
		}
	}
}
