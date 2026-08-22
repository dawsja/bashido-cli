package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const shortHelp = `Usage: bashido <command>

Commands:
  auth       Log in, log out, and inspect authentication
  profile    Manage named server profiles
  script     List, search, create, edit, and remove scripts
  note       Show, set, edit, and clear script notes
  completion Generate shell completion
  uninstall Revoke credentials and remove bashido
  upgrade   Install the latest bashido release
  version    Print the version

Run 'bashido <command> --help' for command details.`

func (a *app) flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(a.errOut)
	f.Usage = func() { fmt.Fprintln(a.errOut, shortHelp) }
	return f
}

// optionsFirst lets flags follow positional arguments, as documented.
func optionsFirst(args []string, values map[string]bool) []string {
	var opts, pos []string
	for i := 0; i < len(args); i++ {
		x := args[i]
		if strings.HasPrefix(x, "-") && x != "-" {
			opts = append(opts, x)
			name := x
			if j := strings.IndexByte(name, '='); j >= 0 {
				name = name[:j]
			}
			if values[name] && !strings.Contains(x, "=") && i+1 < len(args) {
				i++
				opts = append(opts, args[i])
			}
		} else {
			pos = append(pos, x)
		}
	}
	return append(opts, pos...)
}

func (a *app) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		cfg, creds, dir, err := a.load()
		if err != nil {
			return err
		}
		name, p, err := active(cfg)
		if err != nil {
			fmt.Fprintln(a.errOut, shortHelp)
			return nil
		}
		if c, ok := creds.Profiles[name]; !ok || c.Token == "" || c.Origin != p.Origin {
			return a.authLogin(ctx, nil)
		}
		if !cfg.CompletionOffered {
			if err = a.offerBashCompletion(dir); err != nil {
				return err
			}
		}
		fmt.Fprintf(a.out, "Profile: %s\nServer:  %s\n", name, p.Origin)
		fmt.Fprintln(a.errOut, "Run 'bashido --help' for commands.")
		return nil
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(a.out, shortHelp)
		return nil
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return fail(2, "version takes no arguments")
		}
		fmt.Fprintf(a.out, "bashido %s\n", version)
		return nil
	case "profile":
		return a.profileCommand(ctx, args[1:])
	case "auth":
		return a.authCommand(ctx, args[1:])
	case "script":
		return a.scriptCommand(ctx, args[1:])
	case "note":
		return a.noteCommand(ctx, args[1:])
	case "completion":
		return a.completionCommand(args[1:])
	case "__complete":
		return a.completionCandidates(ctx, args[1:])
	case "uninstall":
		return a.uninstall(ctx, args[1:])
	case "upgrade":
		return a.upgrade(ctx, args[1:])
	default:
		return fail(2, "unknown command %q", args[0])
	}
}

func (a *app) profileCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fail(2, "usage: bashido profile list|add|use|remove")
	}
	cfg, creds, dir, err := a.load()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fail(2, "profile list takes no arguments")
		}
		names := make([]string, 0, len(cfg.Profiles))
		for n := range cfg.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			mark := " "
			if n == cfg.Current {
				mark = "*"
			}
			fmt.Fprintf(a.out, "%s %-16s %s\n", mark, sanitize(n), sanitize(cfg.Profiles[n].Origin))
		}
		return nil
	case "add":
		f := a.flags("profile add")
		ca := f.String("ca-file", "", "custom CA PEM file")
		use := f.Bool("use", false, "make current")
		if err := f.Parse(optionsFirst(args[1:], map[string]bool{"--ca-file": true, "-ca-file": true})); err != nil {
			return fail(2, "%v", err)
		}
		if f.NArg() != 2 {
			return fail(2, "usage: bashido profile add NAME URL [--ca-file FILE] [--use]")
		}
		name := f.Arg(0)
		if !profileNameRE.MatchString(name) {
			return fail(2, "invalid profile name")
		}
		origin, err := canonicalOrigin(f.Arg(1))
		if err != nil {
			return fail(2, "%v", err)
		}
		if *ca != "" {
			absolute, absErr := filepath.Abs(*ca)
			if absErr != nil {
				return absErr
			}
			fi, statErr := os.Stat(absolute)
			if statErr != nil {
				return fmt.Errorf("CA file: %w", statErr)
			}
			if !fi.Mode().IsRegular() {
				return fail(2, "CA file is not a regular file")
			}
			*ca = absolute
		}
		if current, exists := cfg.Profiles[name]; exists {
			if current.Origin != origin || current.CAFile != *ca {
				return fail(2, "profile %q already exists with different settings", name)
			}
			if *use {
				if cfg.Current == name {
					_, err = fmt.Fprintf(a.out, "Profile %q already exists with these settings and is already selected.\n", sanitize(name))
					return err
				}
				cfg.Current = name
				if err = saveConfig(dir, cfg); err != nil {
					return err
				}
				_, err = fmt.Fprintf(a.out, "Profile %q already exists; now using it.\n", sanitize(name))
				return err
			}
			_, err = fmt.Fprintf(a.out, "Profile %q already exists with these settings.\n", sanitize(name))
			return err
		}
		cfg.Profiles[name] = profile{Origin: origin, CAFile: *ca}
		selected := *use || cfg.Current == ""
		if selected {
			cfg.Current = name
		}
		if err = saveConfig(dir, cfg); err != nil {
			return err
		}
		if selected {
			_, err = fmt.Fprintf(a.out, "Added and selected profile %q (%s).\n", sanitize(name), sanitize(origin))
		} else {
			_, err = fmt.Fprintf(a.out, "Added profile %q (%s).\n", sanitize(name), sanitize(origin))
		}
		return err
	case "use":
		if len(args) != 2 {
			return fail(2, "usage: bashido profile use NAME")
		}
		if _, ok := cfg.Profiles[args[1]]; !ok {
			return fail(2, "profile %q does not exist", args[1])
		}
		if cfg.Current == args[1] {
			_, err = fmt.Fprintf(a.out, "Already using profile %q.\n", sanitize(args[1]))
			return err
		}
		cfg.Current = args[1]
		if err = saveConfig(dir, cfg); err != nil {
			return err
		}
		_, err = fmt.Fprintf(a.out, "Now using profile %q.\n", sanitize(args[1]))
		return err
	case "remove":
		f := a.flags("profile remove")
		local := f.Bool("local-only", false, "do not revoke remotely")
		yes := f.Bool("yes", false, "confirm removal")
		if err := f.Parse(optionsFirst(args[1:], nil)); err != nil {
			return fail(2, "%v", err)
		}
		if f.NArg() != 1 {
			return fail(2, "usage: bashido profile remove NAME [--local-only] [--yes]")
		}
		if !*yes {
			return fail(2, "profile removal requires --yes")
		}
		name := f.Arg(0)
		p, ok := cfg.Profiles[name]
		if !ok {
			return fail(2, "profile %q does not exist", name)
		}
		c, hadCredential := creds.Profiles[name]
		revoked := false
		wasCurrent := cfg.Current == name
		if !*local {
			if hadCredential && c.Origin == p.Origin && c.Token != "" {
				cl, e := newClient(p, c.Token)
				if e != nil {
					return e
				}
				if _, e = cl.do(ctx, "DELETE", "/api/v1/me/credential", nil, nil, nil); e != nil {
					return fmt.Errorf("revoke credential (profile retained): %w", e)
				}
				revoked = true
			}
		}
		delete(cfg.Profiles, name)
		delete(creds.Profiles, name)
		if cfg.Current == name {
			cfg.Current = ""
			names := make([]string, 0, len(cfg.Profiles))
			for n := range cfg.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)
			if len(names) > 0 {
				cfg.Current = names[0]
			}
		}
		if err := saveCredentials(dir, creds); err != nil {
			return err
		}
		if err := saveConfig(dir, cfg); err != nil {
			return err
		}
		if revoked {
			_, err = fmt.Fprintf(a.out, "Removed profile %q and revoked its credential.\n", sanitize(name))
		} else if hadCredential {
			if c.Token != "" {
				if _, err = fmt.Fprintln(a.errOut, "Warning: the server credential was not revoked."); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(a.out, "Removed profile %q and its local credential.\n", sanitize(name))
		} else {
			_, err = fmt.Fprintf(a.out, "Removed profile %q.\n", sanitize(name))
		}
		if err != nil || !wasCurrent {
			return err
		}
		if cfg.Current == "" {
			_, err = fmt.Fprintln(a.out, "No current profile is selected.")
		} else {
			_, err = fmt.Fprintf(a.out, "Current profile is now %q.\n", sanitize(cfg.Current))
		}
		return err
	default:
		return fail(2, "unknown profile command %q", args[0])
	}
}

func sanitize(s string) string {
	r := []rune(s)
	for i, c := range r {
		if c < 0x20 || c == 0x7f || (c >= 0x80 && c <= 0x9f) {
			r[i] = '?'
		}
	}
	return string(r)
}

func isAPIStatus(err error, status int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == status
}
