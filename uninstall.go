package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func (a *app) uninstall(ctx context.Context, args []string) error {
	f := a.flags("uninstall")
	local := f.Bool("local-only", false, "do not revoke credentials remotely")
	yes := f.Bool("yes", false, "confirm uninstallation")
	if err := f.Parse(optionsFirst(args, nil)); err != nil {
		return fail(2, "%v", err)
	}
	if f.NArg() != 0 {
		return fail(2, "uninstall takes no arguments")
	}
	if !*yes {
		return fail(2, "uninstall requires --yes")
	}

	executable, err := a.uninstallExecutable()
	if err != nil {
		return err
	}
	cfg, creds, dir, err := a.load()
	if err != nil {
		return err
	}

	revoked := 0
	if !*local {
		names := make([]string, 0, len(creds.Profiles))
		for name := range creds.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		var failed []string
		for _, name := range names {
			c := creds.Profiles[name]
			p, ok := cfg.Profiles[name]
			if !ok || c.Origin != p.Origin || c.Token == "" {
				failed = append(failed, name)
				continue
			}
			cl, clientErr := newClient(p, c.Token)
			if clientErr == nil {
				_, clientErr = cl.do(ctx, "DELETE", "/api/v1/me/credential", nil, nil, nil)
			}
			// An unauthorized token is already unusable and does not need preserving locally.
			if clientErr == nil || isAPIStatus(clientErr, 401) {
				delete(creds.Profiles, name)
				revoked++
				continue
			}
			failed = append(failed, name)
		}
		if len(failed) > 0 {
			if err = saveCredentials(dir, creds); err != nil {
				return fmt.Errorf("save credentials after partial revocation: %w", err)
			}
			return fmt.Errorf("could not revoke credentials for profiles %s; installation retained (retry or use --local-only)", strings.Join(failed, ", "))
		}
	}

	for _, name := range []string{credFile, configFile} {
		if err = os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove local configuration (executable retained): %w", err)
		}
	}
	if err = os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) {
		return fmt.Errorf("remove configuration directory (executable retained): %w", err)
	}
	if err = os.Remove(executable); err != nil {
		return fmt.Errorf("remove executable %s: %w", executable, err)
	}

	if *local {
		fmt.Fprintln(a.errOut, "Warning: server credentials were not revoked.")
	} else {
		fmt.Fprintf(a.out, "Revoked %d credential(s).\n", revoked)
	}
	fmt.Fprintf(a.out, "Removed %s.\n", executable)
	return nil
}

func (a *app) uninstallExecutable() (string, error) {
	if a.executable == nil {
		return "", errors.New("cannot locate the bashido executable")
	}
	path, err := a.executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	if filepath.Base(path) != "bashido" {
		return "", fmt.Errorf("refusing to remove executable named %q; remove it manually", filepath.Base(path))
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect executable: %w", err)
	}
	if !fi.Mode().IsRegular() && fi.Mode()&os.ModeSymlink == 0 {
		return "", errors.New("refusing to remove an executable that is not a regular file or symlink")
	}
	probe, err := os.CreateTemp(filepath.Dir(path), ".bashido-uninstall-")
	if err != nil {
		return "", fmt.Errorf("executable directory is not writable: %w", err)
	}
	probeName := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		os.Remove(probeName)
		return "", closeErr
	}
	if err = os.Remove(probeName); err != nil {
		return "", fmt.Errorf("remove uninstall probe: %w", err)
	}
	return path, nil
}
