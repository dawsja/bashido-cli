package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

type script struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content,omitempty"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	DeletedAt *int64 `json:"deletedAt,omitempty"`
	Revision  int64  `json:"revision"`
}

type scriptsEnvelope struct {
	Scripts []script `json:"scripts"`
}

type scriptEnvelope struct {
	Script script `json:"script"`
}

type note struct {
	Notes    string `json:"notes"`
	Revision int64  `json:"revision"`
}

func (a *app) api() (*client, error) {
	cfg, creds, _, err := a.load()
	if err != nil {
		return nil, err
	}
	_, p, t, err := bearer(cfg, creds)
	if err != nil {
		return nil, err
	}
	return newClient(p, t)
}

func stateFlags(f *flag.FlagSet) (*bool, *bool) {
	return f.Bool("trash", false, "trashed scripts"), f.Bool("all", false, "all scripts")
}
func chooseState(trash, all bool) (string, error) {
	if trash && all {
		return "", fail(2, "--trash and --all are mutually exclusive")
	}
	if trash {
		return "trash", nil
	}
	if all {
		return "all", nil
	}
	return "active", nil
}

func (a *app) scriptCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fail(2, "usage: bashido script list|search|show|create|update|edit|delete|restore|purge")
	}
	switch args[0] {
	case "list", "search":
		return a.listScripts(ctx, args[0], args[1:])
	case "show":
		return a.showScript(ctx, args[1:])
	case "create":
		return a.createScript(ctx, args[1:])
	case "update":
		return a.updateScript(ctx, args[1:])
	case "edit":
		return a.editScript(ctx, args[1:])
	case "delete", "restore", "purge":
		return a.mutateScript(ctx, args[0], args[1:])
	default:
		return fail(2, "unknown script command %q", args[0])
	}
}

func (a *app) listScripts(ctx context.Context, cmd string, args []string) error {
	f := a.flags("script " + cmd)
	trash, all := stateFlags(f)
	asJSON := f.Bool("json", false, "JSON output")
	if err := f.Parse(optionsFirst(args, nil)); err != nil {
		return fail(2, "%v", err)
	}
	query := ""
	if cmd == "search" {
		if f.NArg() != 1 {
			return fail(2, "usage: bashido script search QUERY [--trash|--all] [--json]")
		}
		query = f.Arg(0)
	} else if f.NArg() != 0 {
		return fail(2, "script list takes no arguments")
	}
	state, err := chooseState(*trash, *all)
	if err != nil {
		return err
	}
	cl, err := a.api()
	if err != nil {
		return err
	}
	var response scriptsEnvelope
	path := "/api/v1/scripts?state=" + state + "&q=" + url.QueryEscape(query)
	if _, err = cl.do(ctx, "GET", path, nil, &response, nil); err != nil {
		return err
	}
	rows := response.Scripts
	if *asJSON {
		return writeJSON(a.out, rows)
	}
	w := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', tabwriter.StripEscape)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.tablePaint(a.out, ansiBold, "ID"), a.tablePaint(a.out, ansiBold, "TITLE"), a.tablePaint(a.out, ansiBold, "UPDATED"), a.tablePaint(a.out, ansiBold, "STATE"))
	for _, s := range rows {
		st := "active"
		if s.DeletedAt != nil {
			st = "trash"
		}
		stateColor := ansiGreen
		if st == "trash" {
			stateColor = ansiRed
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.tablePaint(a.out, ansiCyan, sanitize(s.ID)), sanitize(s.Title), a.tablePaint(a.out, ansiDim, formatMillis(s.UpdatedAt)), a.tablePaint(a.out, stateColor, st))
	}
	return w.Flush()
}

func (a *app) showScript(ctx context.Context, args []string) error {
	f := a.flags("script show")
	asJSON := f.Bool("json", false, "JSON output")
	if err := f.Parse(optionsFirst(args, nil)); err != nil {
		return fail(2, "%v", err)
	}
	if f.NArg() != 1 {
		return fail(2, "usage: bashido script show REF [--json]")
	}
	cl, err := a.api()
	if err != nil {
		return err
	}
	s, err := resolveScript(ctx, cl, f.Arg(0))
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.out, s)
	}
	_, err = io.WriteString(a.out, s.Content)
	return err
}

func readInput(in io.Reader, path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(io.LimitReader(in, maxResponse+1))
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	if len(b) > maxResponse {
		return "", errors.New("input exceeds size limit")
	}
	return string(b), nil
}

func (a *app) createScript(ctx context.Context, args []string) error {
	f := a.flags("script create")
	title := f.String("title", "", "script title")
	notesFile := f.String("notes-file", "", "notes file")
	if err := f.Parse(optionsFirst(args, map[string]bool{"--title": true, "--notes-file": true})); err != nil {
		return fail(2, "%v", err)
	}
	if f.NArg() != 1 || *title == "" {
		return fail(2, "usage: bashido script create FILE|- --title TITLE [--notes-file FILE]")
	}
	content, err := readInput(a.in, f.Arg(0))
	if err != nil {
		return err
	}
	body := map[string]any{"title": *title, "content": content}
	if *notesFile != "" {
		n, e := readInput(a.in, *notesFile)
		if e != nil {
			return e
		}
		body["notes"] = n
	}
	cl, err := a.api()
	if err != nil {
		return err
	}
	var response scriptEnvelope
	if _, err = cl.do(ctx, "POST", "/api/v1/scripts", body, &response, nil); err != nil {
		return err
	}
	if response.Script.ID == "" {
		return errors.New("create response missing script ID")
	}
	if response.Script.Title == "" {
		return errors.New("create response missing script title")
	}
	_, err = a.successf("Created script %q (%s).\n", sanitize(response.Script.Title), sanitize(response.Script.ID))
	return err
}

func (a *app) updateScript(ctx context.Context, args []string) error {
	f := a.flags("script update")
	title := f.String("title", "", "new title")
	force := f.Bool("force", false, "omit revision check")
	if err := f.Parse(optionsFirst(args, map[string]bool{"--title": true})); err != nil {
		return fail(2, "%v", err)
	}
	if f.NArg() < 1 || f.NArg() > 2 || (*title == "" && f.NArg() != 2) {
		return fail(2, "usage: bashido script update REF [FILE|-] [--title TITLE] [--force]")
	}
	cl, err := a.api()
	if err != nil {
		return err
	}
	s, err := resolveScript(ctx, cl, f.Arg(0))
	if err != nil {
		return err
	}
	body := map[string]any{}
	if *title != "" {
		body["title"] = *title
	}
	if f.NArg() == 2 {
		content, e := readInput(a.in, f.Arg(1))
		if e != nil {
			return e
		}
		body["content"] = content
	}
	headers := revisionHeader(s.Revision, *force)
	var response scriptEnvelope
	if _, err = cl.do(ctx, "PATCH", "/api/v1/scripts/"+url.PathEscape(s.ID), body, &response, headers); err != nil {
		return err
	}
	if response.Script.ID == "" {
		return errors.New("update response missing script ID")
	}
	if response.Script.Title == "" {
		return errors.New("update response missing script title")
	}
	_, err = a.successf("Updated script %q (%s).\n", sanitize(response.Script.Title), sanitize(response.Script.ID))
	return err
}

func (a *app) mutateScript(ctx context.Context, cmd string, args []string) error {
	f := a.flags("script " + cmd)
	yes := f.Bool("yes", false, "confirm permanent deletion")
	if err := f.Parse(optionsFirst(args, nil)); err != nil {
		return fail(2, "%v", err)
	}
	if f.NArg() != 1 {
		return fail(2, "usage: bashido script %s REF", cmd)
	}
	if cmd == "purge" && !*yes {
		return fail(2, "script purge requires --yes")
	}
	cl, err := a.api()
	if err != nil {
		return err
	}
	s, err := resolveScript(ctx, cl, f.Arg(0))
	if err != nil {
		return err
	}
	path := "/api/v1/scripts/" + url.PathEscape(s.ID)
	method := "DELETE"
	action := "Deleted"
	if cmd == "restore" {
		method = "POST"
		path += "/restore"
		action = "Restored"
	}
	if cmd == "purge" {
		path += "/permanent"
		action = "Permanently deleted"
	}
	if _, err = cl.do(ctx, method, path, nil, nil, nil); err != nil {
		return err
	}
	_, err = a.successf("%s script %q (%s).\n", action, sanitize(s.Title), sanitize(s.ID))
	return err
}

func resolveScript(ctx context.Context, cl *client, ref string) (script, error) {
	if ref == "" {
		return script{}, fail(2, "empty script reference")
	}
	var response scriptsEnvelope
	if _, err := cl.do(ctx, "GET", "/api/v1/scripts?state=all&q=", nil, &response, nil); err != nil {
		return script{}, err
	}
	rows := response.Scripts
	for i := range rows {
		if rows[i].ID == ref {
			return fetchScript(ctx, cl, rows[i].ID)
		}
	}
	if len(ref) >= 8 {
		var match *script
		for i := range rows {
			if strings.HasPrefix(rows[i].ID, ref) {
				if match != nil {
					return script{}, fail(4, "reference %q matches multiple IDs", ref)
				}
				match = &rows[i]
			}
		}
		if match != nil {
			return fetchScript(ctx, cl, match.ID)
		}
	}
	var title *script
	for i := range rows {
		if rows[i].Title == ref {
			if title != nil {
				return script{}, fail(4, "title %q is ambiguous", ref)
			}
			title = &rows[i]
		}
	}
	if title != nil {
		return fetchScript(ctx, cl, title.ID)
	}
	return script{}, fail(4, "script %q not found", ref)
}

func fetchScript(ctx context.Context, cl *client, id string) (script, error) {
	var response scriptEnvelope
	_, err := cl.do(ctx, "GET", "/api/v1/scripts/"+url.PathEscape(id), nil, &response, nil)
	return response.Script, err
}

func formatMillis(value int64) string {
	if value == 0 {
		return "-"
	}
	return time.UnixMilli(value).Local().Format("2006-01-02 15:04")
}
func revisionHeader(rev int64, force bool) map[string]string {
	if force {
		return nil
	}
	return map[string]string{"If-Match": strconv.FormatInt(rev, 10)}
}
func writeJSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetEscapeHTML(false)
	return e.Encode(v)
}
