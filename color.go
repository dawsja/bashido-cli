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
	return a.colorHelp(w, shortHelp)
}

func (a *app) colorHelp(w io.Writer, text string) string {
	if !a.colorEnabled(w) {
		return text
	}
	var b strings.Builder
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch {
		case strings.HasPrefix(line, "Usage:"):
			b.WriteString(ansiBold + "Usage:" + ansiReset + strings.TrimPrefix(line, "Usage:"))
		case line == "Commands:" || line == "Flags:" || line == "Library" || line == "Account" || line == "System":
			b.WriteString(ansiBold + line + ansiReset)
		case strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   "):
			rest := line[2:]
			n := 0
			for n < len(rest) && rest[n] != ' ' {
				n++
			}
			name := rest[:n]
			if name != "" && !strings.HasPrefix(name, "-") {
				b.WriteString("  " + ansiCyan + name + ansiReset + rest[n:])
			} else {
				b.WriteString(line)
			}
		default:
			b.WriteString(line)
		}
	}
	return b.String()
}

func terminalColor(w io.Writer, getenv func(string) string) bool {
	return isTerminal(w) && getenv("NO_COLOR") == "" && getenv("TERM") != "dumb"
}

func (a *app) writerIsTTY(w io.Writer) bool {
	if a.isTTY != nil {
		return a.isTTY(w)
	}
	return isTerminal(w)
}
