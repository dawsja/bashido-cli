package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultReleaseBase = "https://github.com/dawsja/bashido-cli/releases/latest/download"

func (a *app) upgrade(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return fail(2, "upgrade takes no arguments")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return fmt.Errorf("unsupported architecture %q", runtime.GOARCH)
	}
	executable, err := a.managedExecutable()
	if err != nil {
		return err
	}
	base := a.releaseBase
	if base == "" {
		base = defaultReleaseBase
	}
	asset := "bashido-linux-" + runtime.GOARCH

	var checksums bytes.Buffer
	if err = downloadRelease(ctx, base+"/checksums.txt", &checksums, 1<<20); err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	expected, err := releaseChecksum(checksums.Bytes(), asset)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(executable), ".bashido-upgrade-")
	if err != nil {
		return fmt.Errorf("create upgrade file: %w", err)
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			tmp.Close()
		}
		os.Remove(tmpName)
	}()
	hash := sha256.New()
	if err = downloadRelease(ctx, base+"/"+asset, io.MultiWriter(tmp, hash), 128<<20); err != nil {
		return fmt.Errorf("download release: %w", err)
	}
	if !bytes.Equal(hash.Sum(nil), expected) {
		return errors.New("release checksum mismatch")
	}
	current, err := fileChecksum(executable)
	if err != nil {
		return fmt.Errorf("checksum current executable: %w", err)
	}
	if bytes.Equal(current, expected) {
		fmt.Fprintf(a.out, "bashido %s is already up to date.\n", version)
		return nil
	}
	if err = tmp.Chmod(0755); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	closed = true
	if err != nil {
		return fmt.Errorf("write upgrade: %w", err)
	}
	if err = os.Rename(tmpName, executable); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	fmt.Fprintln(a.out, "Upgraded bashido to the latest release.")
	return nil
}

func downloadRelease(ctx context.Context, rawURL string, dst io.Writer, limit int64) error {
	if !safeReleaseURL(rawURL) {
		return errors.New("release URL must use HTTPS")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Minute}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if !safeReleaseURL(req.URL.String()) {
			return errors.New("refusing insecure release redirect")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}
	written, err := io.Copy(dst, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if written > limit {
		return errors.New("release download exceeds size limit")
	}
	return nil
}

func safeReleaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (strings.EqualFold(u.Hostname(), "localhost") || ip != nil && ip.IsLoopback())
}

func releaseChecksum(data []byte, asset string) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var checksum []byte
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		if checksum != nil {
			return nil, fmt.Errorf("duplicate checksum for %s", asset)
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("invalid checksum for %s", asset)
		}
		checksum = decoded
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if checksum == nil {
		return nil, fmt.Errorf("checksum for %s is missing", asset)
	}
	return checksum, nil
}

func fileChecksum(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}
