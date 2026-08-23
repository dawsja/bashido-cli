package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func splitCommand(s string) ([]string, error) {
	var out []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	started := false
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
		} else if r == ' ' || r == '\t' || r == '\n' {
			if started {
				out = append(out, b.String())
				b.Reset()
				started = false
			}
		} else {
			b.WriteRune(r)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, errors.New("invalid editor command quoting")
	}
	if started {
		out = append(out, b.String())
	}
	if len(out) == 0 {
		return nil, errors.New("empty editor command")
	}
	return out, nil
}

func (a *app) editor() ([]string, error) {
	for _, k := range []string{"BASHIDO_EDITOR", "VISUAL", "EDITOR"} {
		if v := a.getenv(k); v != "" {
			return splitCommand(v)
		}
	}
	return []string{"vi"}, nil
}

func (a *app) editValue(ctx context.Context, initial, suffix string) (string, string, error) {
	dir := a.getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "bashido-*"+suffix)
	if err != nil {
		return "", "", err
	}
	path := f.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if err = f.Chmod(0600); err == nil {
		_, err = io.WriteString(f, initial)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", "", err
	}
	argv, err := a.editor()
	if err != nil {
		return "", "", err
	}
	cmd := exec.CommandContext(ctx, argv[0], append(argv[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = a.errOut
	cmd.Stderr = a.errOut
	if err = cmd.Run(); err != nil {
		return "", "", fmt.Errorf("editor: %w", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	keep = true
	return string(b), path, nil
}

func (a *app) editScript(ctx context.Context, args []string) error {
	f := a.flags("script edit")
	force := f.Bool("force", false, "omit revision check")
	if err := f.Parse(optionsFirst(args, nil)); err != nil {
		return fail(2, "%v", err)
	}
	if f.NArg() != 1 {
		return fail(2, "usage: bashido script edit REF [--force]")
	}
	cl, err := a.api()
	if err != nil {
		return err
	}
	s, err := resolveScript(ctx, cl, f.Arg(0))
	if err != nil {
		return err
	}
	content, recovery, err := a.editValue(ctx, s.Content, ".sh")
	if err != nil {
		return err
	}
	if content == s.Content {
		_ = os.Remove(recovery)
		_, err = fmt.Fprintf(a.out, "No changes to script %q (%s).\n", sanitize(s.Title), sanitize(s.ID))
		return err
	}
	var response scriptEnvelope
	_, err = cl.do(ctx, "PATCH", "/api/v1/scripts/"+url.PathEscape(s.ID), map[string]string{"content": content}, &response, revisionHeader(s.Revision, *force))
	if err != nil {
		if isAPIStatus(err, 409) || isAPIStatus(err, 412) {
			return fmt.Errorf("revision conflict; edited content preserved at %s", filepath.Clean(recovery))
		}
		_ = os.Remove(recovery)
		return err
	}
	_ = os.Remove(recovery)
	_, err = a.successf("Updated script %q (%s).\n", sanitize(s.Title), sanitize(s.ID))
	return err
}

func (a *app) noteCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fail(2, "usage: bashido note show|set|edit|clear")
	}
	cl, err := a.api()
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		f := a.flags("note show")
		asJSON := f.Bool("json", false, "JSON output")
		if err = f.Parse(optionsFirst(args[1:], nil)); err != nil {
			return fail(2, "%v", err)
		}
		if f.NArg() != 1 {
			return fail(2, "usage: bashido note show REF [--json]")
		}
		s, e := resolveScript(ctx, cl, f.Arg(0))
		if e != nil {
			return e
		}
		var n note
		if _, e = cl.do(ctx, "GET", "/api/v1/scripts/"+url.PathEscape(s.ID)+"/notes", nil, &n, nil); e != nil {
			return e
		}
		if *asJSON {
			return writeJSON(a.out, n)
		}
		_, e = io.WriteString(a.out, n.Notes)
		return e
	case "set":
		if len(args) != 3 {
			return fail(2, "usage: bashido note set REF FILE|-")
		}
		s, e := resolveScript(ctx, cl, args[1])
		if e != nil {
			return e
		}
		v, e := readInput(a.in, args[2])
		if e != nil {
			return e
		}
		if _, e = cl.do(ctx, "PUT", "/api/v1/scripts/"+url.PathEscape(s.ID)+"/notes", map[string]string{"notes": v}, nil, revisionHeader(s.Revision, false)); e != nil {
			return e
		}
		_, e = a.successf("Updated notes for script %q (%s).\n", sanitize(s.Title), sanitize(s.ID))
		return e
	case "edit":
		f := a.flags("note edit")
		force := f.Bool("force", false, "omit revision check")
		if err = f.Parse(optionsFirst(args[1:], nil)); err != nil {
			return fail(2, "%v", err)
		}
		if f.NArg() != 1 {
			return fail(2, "usage: bashido note edit REF [--force]")
		}
		s, e := resolveScript(ctx, cl, f.Arg(0))
		if e != nil {
			return e
		}
		var n note
		if _, e = cl.do(ctx, "GET", "/api/v1/scripts/"+url.PathEscape(s.ID)+"/notes", nil, &n, nil); e != nil {
			return e
		}
		v, recovery, e := a.editValue(ctx, n.Notes, ".txt")
		if e != nil {
			return e
		}
		if v == n.Notes {
			_ = os.Remove(recovery)
			_, e = fmt.Fprintf(a.out, "Notes unchanged for script %q (%s).\n", sanitize(s.Title), sanitize(s.ID))
			return e
		}
		_, e = cl.do(ctx, "PUT", "/api/v1/scripts/"+url.PathEscape(s.ID)+"/notes", map[string]string{"notes": v}, nil, revisionHeader(n.Revision, *force))
		if e != nil {
			if isAPIStatus(e, 409) || isAPIStatus(e, 412) {
				return fmt.Errorf("revision conflict; edited notes preserved at %s", recovery)
			}
			_ = os.Remove(recovery)
			return e
		}
		_ = os.Remove(recovery)
		_, e = a.successf("Updated notes for script %q (%s).\n", sanitize(s.Title), sanitize(s.ID))
		return e
	case "clear":
		f := a.flags("note clear")
		yes := f.Bool("yes", false, "confirm clearing notes")
		if err = f.Parse(optionsFirst(args[1:], nil)); err != nil {
			return fail(2, "%v", err)
		}
		if f.NArg() != 1 || !*yes {
			return fail(2, "usage: bashido note clear REF --yes")
		}
		s, e := resolveScript(ctx, cl, f.Arg(0))
		if e != nil {
			return e
		}
		if _, e = cl.do(ctx, "DELETE", "/api/v1/scripts/"+url.PathEscape(s.ID)+"/notes", nil, nil, revisionHeader(s.Revision, false)); e != nil {
			return e
		}
		_, e = a.successf("Cleared notes for script %q (%s).\n", sanitize(s.Title), sanitize(s.ID))
		return e
	default:
		return fail(2, "unknown note command %q", args[0])
	}
}
