package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

func (a *app) colorEnabled(w io.Writer) bool {
	if a.useColor != nil {
		return a.useColor(w)
	}
	return false
}

func (a *app) paint(w io.Writer, code, text string) string {
	if !a.colorEnabled(w) {
		return text
	}
	if strings.HasSuffix(text, "\n") {
		return code + strings.TrimSuffix(text, "\n") + ansiReset + "\n"
	}
	return code + text + ansiReset
}

// tablePaint hides ANSI sequences from tabwriter's width calculation.
func (a *app) tablePaint(w io.Writer, code, text string) string {
	if !a.colorEnabled(w) {
		return text
	}
	escape := string([]byte{tabwriter.Escape})
	return escape + code + escape + text + escape + ansiReset + escape
}

func (a *app) successf(format string, args ...any) (int, error) {
	return fmt.Fprint(a.out, a.paint(a.out, ansiGreen, fmt.Sprintf(format, args...)))
}

func (a *app) warningf(format string, args ...any) (int, error) {
	return fmt.Fprint(a.errOut, a.paint(a.errOut, ansiYellow, fmt.Sprintf(format, args...)))
}

func (a *app) help(w io.Writer) string {
	if !a.colorEnabled(w) {
		return shortHelp
	}
	text := strings.Replace(shortHelp, "Usage:", ansiBold+"Usage:"+ansiReset, 1)
	text = strings.Replace(text, "Commands:", ansiBold+"Commands:"+ansiReset, 1)
	for _, command := range []string{"auth", "profile", "script", "note", "completion", "uninstall", "upgrade", "version"} {
		text = strings.Replace(text, "  "+command, "  "+ansiCyan+command+ansiReset, 1)
	}
	return text
}

func terminalColor(w io.Writer, getenv func(string) string) bool {
	return isTerminal(w) && getenv("NO_COLOR") == "" && getenv("TERM") != "dumb"
}
