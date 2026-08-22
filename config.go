package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	configFile = "config.json"
	credFile   = "credentials.json"
)

var profileNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type profile struct {
	Origin string `json:"origin"`
	CAFile string `json:"caFile,omitempty"`
}

type config struct {
	Current  string             `json:"current,omitempty"`
	Profiles map[string]profile `json:"profiles"`
}

type credential struct {
	Origin string `json:"origin"`
	Token  string `json:"token"`
}

type credentials struct {
	Profiles map[string]credential `json:"profiles"`
}

func canonicalOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("origin must be an absolute URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("origin must not contain userinfo, path, query, or fragment")
	}
	host := u.Hostname()
	if host == "" {
		return "", errors.New("origin has no hostname")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(host)
		loopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
		if u.Scheme != "http" || !loopback {
			return "", errors.New("HTTPS is required except for loopback HTTP")
		}
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("origin scheme must be https")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host = strings.ToLower(host)
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	u.Host = host
	u.Path = ""
	u.RawPath = ""
	return strings.TrimSuffix(u.String(), "/"), nil
}

func (a *app) configDir() (string, error) {
	if x := a.getenv("XDG_CONFIG_HOME"); x != "" {
		if !filepath.IsAbs(x) {
			return "", errors.New("XDG_CONFIG_HOME must be absolute")
		}
		return filepath.Join(x, "bashido"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "bashido"), nil
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("configuration directory is not a real directory")
	}
	if fi.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("configuration directory %s has unsafe permissions %04o", dir, fi.Mode().Perm())
	}
	return nil
}

func readJSON(path string, dst any, sensitive bool) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || (sensitive && (fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm()&0077 != 0)) {
		return fmt.Errorf("%s is not a safe private regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("read %s: trailing data", path)
	}
	return nil
}

func atomicJSON(path string, value any) error {
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (a *app) load() (*config, *credentials, string, error) {
	dir, err := a.configDir()
	if err != nil {
		return nil, nil, "", err
	}
	cfg := &config{Profiles: map[string]profile{}}
	creds := &credentials{Profiles: map[string]credential{}}
	if _, statErr := os.Lstat(dir); statErr == nil {
		if err := ensurePrivateDir(dir); err != nil {
			return nil, nil, "", err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, nil, "", statErr
	}
	if err := readJSON(filepath.Join(dir, configFile), cfg, true); err != nil {
		return nil, nil, "", err
	}
	if err := readJSON(filepath.Join(dir, credFile), creds, true); err != nil {
		return nil, nil, "", err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]profile{}
	}
	if creds.Profiles == nil {
		creds.Profiles = map[string]credential{}
	}
	for name, p := range cfg.Profiles {
		if !profileNameRE.MatchString(name) {
			return nil, nil, "", fmt.Errorf("invalid profile name %q in configuration", name)
		}
		origin, originErr := canonicalOrigin(p.Origin)
		if originErr != nil || origin != p.Origin {
			return nil, nil, "", fmt.Errorf("profile %q has a non-canonical origin", name)
		}
		if p.CAFile != "" && !filepath.IsAbs(p.CAFile) {
			return nil, nil, "", fmt.Errorf("profile %q has a relative CA file", name)
		}
	}
	return cfg, creds, dir, nil
}

func saveConfig(dir string, cfg *config) error {
	return atomicJSON(filepath.Join(dir, configFile), cfg)
}
func saveCredentials(dir string, c *credentials) error {
	return atomicJSON(filepath.Join(dir, credFile), c)
}

func active(cfg *config) (string, profile, error) {
	if cfg.Current == "" {
		return "", profile{}, fail(2, "no current profile; use 'bashido profile add NAME URL --use'")
	}
	p, ok := cfg.Profiles[cfg.Current]
	if !ok {
		return "", profile{}, fail(2, "current profile %q does not exist", cfg.Current)
	}
	return cfg.Current, p, nil
}
