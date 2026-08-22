# Command reference

All command data is written to stdout. Prompts, progress, and errors use stderr. `FILE|-` means a file path or stdin.

```text
bashido
bashido version
bashido completion bash
bashido completion install
bashido upgrade
bashido uninstall [--local-only] --yes
bashido auth login [--no-browser] [--replace]
bashido auth status
bashido auth logout [--local-only]
bashido profile list
bashido profile add NAME URL [--ca-file FILE] [--use]
bashido profile use NAME
bashido profile remove NAME [--local-only] [--yes]
bashido script list [--trash|--all] [--json]
bashido script search QUERY [--trash|--all] [--json]
bashido script show REF [--json]
bashido script create FILE|- --title TITLE [--notes-file FILE]
bashido script update REF [FILE|-] [--title TITLE] [--force]
bashido script edit REF [--force]
bashido script delete REF
bashido script restore REF
bashido script purge REF --yes
bashido note show REF [--json]
bashido note set REF FILE|-
bashido note edit REF [--force]
bashido note clear REF --yes
```

## Bash completion

Enable completion for the current shell with:

```sh
source <(bashido completion bash)
```

Add that command to your Bash startup file to enable completion in new shells.

After the first successful login, an interactive run asks once whether to set this up automatically. If the first login is non-interactive, the offer is deferred until the next interactive no-argument run. Answering `y` adds the source command to a regular, non-symlink `~/.bashrc`; answering `n` or pressing Enter leaves the file unchanged. Use `bashido completion install` to install or retry setup directly.

Completion covers commands, subcommands, flags, profile names, and script references. Note commands complete the script title or ID associated with the note. Script titles are fetched from the active profile when completion is requested; duplicate titles are omitted because they are ambiguous, but their full IDs remain available.

`REF` is resolved as a full ID, a unique ID prefix of at least eight characters, then an exact title. Duplicate titles are rejected as ambiguous. There is no fuzzy matching.

`script show` and `note show` emit stored content byte-for-byte unless `--json` is used. Successful state-changing commands acknowledge the action on stdout. Editor commands also report when no changes were made. Updates and editor operations use the current revision; `--force` deliberately omits that check. On an editor conflict, the private recovery file path is reported.

Script and note acknowledgements include the script title and ID. Profile and authentication acknowledgements include the profile name. Interactive login instructions remain on stderr, while its final success message is written to stdout.

`upgrade` downloads the latest release for the current architecture from GitHub, verifies it against the release's SHA-256 checksums, and atomically replaces the executable. Configuration and credentials are not changed.

Logout, profile removal, and uninstall revoke server credentials before deleting local state. Uninstall revokes credentials for every profile, removes Bashido's configuration files, and removes the running executable. `--local-only` skips remote revocation when the servers are unavailable and prints a warning that the server credential may remain active. Permanent script deletion, note clearing, profile removal, and uninstall require `--yes` where shown.
