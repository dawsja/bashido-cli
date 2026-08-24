package main

import (
	"fmt"
	"strings"
)

const shortHelp = `Usage: bashido [--profile NAME] <command>

Library
  script      List, search, create, edit, and remove scripts
  note        Show, set, edit, and clear script notes

Account
  auth        Log in, log out, and inspect authentication
  profile     Manage named server profiles

System
  completion  Generate shell completion
  upgrade     Install the latest bashido release
  uninstall   Revoke credentials and remove bashido
  version     Print the version

Run 'bashido <command> --help' for command details.`

var commandHelp = map[string]string{
	"auth": `Usage: bashido auth <command>

Commands:
  login   Start device linking
  status  Show the current login
  logout  Revoke and remove the current credential

Run 'bashido auth <command> --help' for details.`,
	"auth login": `Usage: bashido auth login [--no-browser] [--replace]

Start device linking for the current profile.

Flags:
  --no-browser  Do not open a browser
  --replace     Replace an existing credential
  --help        Show this help`,
	"auth status": `Usage: bashido auth status

Show the current profile, server, and account identity.`,
	"auth logout": `Usage: bashido auth logout [--local-only]

Revoke and remove the current profile credential.

Flags:
  --local-only  Do not revoke the credential on the server
  --help        Show this help`,
	"profile": `Usage: bashido profile <command>

Commands:
  list    List server profiles
  add     Add a named server profile
  use     Select the current profile
  remove  Remove a profile and its credential

Run 'bashido profile <command> --help' for details.`,
	"profile list": `Usage: bashido profile list

List named server profiles.`,
	"profile add": `Usage: bashido profile add NAME URL [--ca-file FILE] [--use]

Add a named server profile.

Flags:
  --ca-file FILE  Custom CA PEM file
  --use           Make this the current profile
  --help          Show this help`,
	"profile use": `Usage: bashido profile use NAME

Select the current profile.`,
	"profile remove": `Usage: bashido profile remove NAME [--local-only] --yes

Remove a profile and its local credential.

Flags:
  --local-only  Do not revoke the credential on the server
  --yes         Confirm removal
  --help        Show this help`,
	"script": `Usage: bashido script <command>

Commands:
  list     List scripts
  search   Search scripts
  show     Print script content
  create   Create a script
  update   Update a script
  edit     Edit a script in an editor
  delete   Move a script to trash
  restore  Restore a script from trash
  purge    Permanently delete a script

Run 'bashido script <command> --help' for details.`,
	"script list": `Usage: bashido script list [--trash|--all] [--json]

List scripts on the current profile.

Flags:
  --trash  Show trashed scripts
  --all    Show active and trashed scripts
  --json   JSON output
  --help   Show this help`,
	"script search": `Usage: bashido script search QUERY [--trash|--all] [--json]

Search scripts on the current profile.

Flags:
  --trash  Search trashed scripts
  --all    Search active and trashed scripts
  --json   JSON output
  --help   Show this help`,
	"script show": `Usage: bashido script show REF [--json]

Print stored script content. On a terminal, metadata is written to stderr.

Flags:
  --json  JSON output
  --help  Show this help`,
	"script create": `Usage: bashido script create FILE|- [--title TITLE] [--notes-file FILE]

Create a script. --title defaults to the file name; stdin requires --title.

Flags:
  --title TITLE       Script title
  --notes-file FILE   Notes file
  --help              Show this help`,
	"script update": `Usage: bashido script update REF [FILE|-] [--title TITLE] [--force]

Update a script's content and/or title.

Flags:
  --title TITLE  New title
  --force        Omit the revision check
  --help         Show this help`,
	"script edit": `Usage: bashido script edit REF [--force]

Edit a script in $BASHIDO_EDITOR, $VISUAL, $EDITOR, or vi.

Flags:
  --force  Omit the revision check
  --help   Show this help`,
	"script delete": `Usage: bashido script delete REF

Move a script to trash.`,
	"script restore": `Usage: bashido script restore REF

Restore a script from trash.`,
	"script purge": `Usage: bashido script purge REF --yes

Permanently delete a script.

Flags:
  --yes   Confirm permanent deletion
  --help  Show this help`,
	"note": `Usage: bashido note <command>

Commands:
  show   Print notes for a script
  set    Replace notes from a file
  edit   Edit notes in an editor
  clear  Clear notes

REF is the parent script title or ID.

Run 'bashido note <command> --help' for details.`,
	"note show": `Usage: bashido note show REF [--json]

Print stored notes for a script.

Flags:
  --json  JSON output
  --help  Show this help`,
	"note set": `Usage: bashido note set REF FILE|-

Replace notes for a script from a file or stdin.`,
	"note edit": `Usage: bashido note edit REF [--force]

Edit notes in $BASHIDO_EDITOR, $VISUAL, $EDITOR, or vi.

Flags:
  --force  Omit the revision check
  --help   Show this help`,
	"note clear": `Usage: bashido note clear REF --yes

Clear notes for a script.

Flags:
  --yes   Confirm clearing notes
  --help  Show this help`,
	"completion": `Usage: bashido completion bash|install

Print the Bash completion script or add it to ~/.bashrc.`,
	"uninstall": `Usage: bashido uninstall [--local-only] --yes

Revoke saved credentials and remove bashido.

Flags:
  --local-only  Do not revoke credentials on servers
  --yes         Confirm uninstallation
  --help        Show this help`,
	"upgrade": `Usage: bashido upgrade

Download and verify the latest bashido release, then replace this executable.`,
	"version": `Usage: bashido version

Print the bashido version.`,
}

func isHelp(arg string) bool {
	return arg == "--help" || arg == "-h"
}

func hasHelp(args []string) bool {
	for _, arg := range args {
		if isHelp(arg) {
			return true
		}
	}
	return false
}

func helpFor(name string) string {
	if text, ok := commandHelp[name]; ok {
		return text
	}
	return "Usage: bashido " + name
}

func (a *app) printHelp(name string) error {
	_, err := fmt.Fprintln(a.out, a.colorHelp(a.out, helpFor(name)))
	return err
}

func (a *app) helpCommand(args []string) error {
	if len(args) == 0 {
		_, err := fmt.Fprintln(a.out, a.help(a.out))
		return err
	}
	key := args[0]
	if len(args) > 1 && !isHelp(args[1]) {
		key = args[0] + " " + args[1]
	}
	if _, ok := commandHelp[key]; !ok {
		if len(args) > 1 && !isHelp(args[1]) {
			return fail(2, "unknown command %q", key)
		}
		return fail(2, "unknown command %q", args[0])
	}
	return a.printHelp(key)
}

func extractProfile(args []string) (string, []string, error) {
	var rest []string
	name := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--profile":
			if i+1 >= len(args) {
				return "", nil, fail(2, "usage: bashido --profile NAME <command>")
			}
			i++
			name = args[i]
			if name == "" {
				return "", nil, fail(2, "usage: bashido --profile NAME <command>")
			}
		case strings.HasPrefix(arg, "--profile="):
			name = strings.TrimPrefix(arg, "--profile=")
			if name == "" {
				return "", nil, fail(2, "usage: bashido --profile NAME <command>")
			}
		default:
			rest = append(rest, arg)
		}
	}
	return name, rest, nil
}
