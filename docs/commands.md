# Command reference

All command data is written to stdout. Prompts, progress, and errors use stderr. `FILE|-` means a file path or stdin.

```text
bashido
bashido version
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

`REF` is resolved as a full ID, a unique ID prefix of at least eight characters, then an exact title. Duplicate titles are rejected as ambiguous. There is no fuzzy matching.

`script show` and `note show` emit stored content byte-for-byte unless `--json` is used. Updates and editor operations use the current revision; `--force` deliberately omits that check. On an editor conflict, the private recovery file path is reported.

`upgrade` downloads the latest release for the current architecture from GitHub, verifies it against the release's SHA-256 checksums, and atomically replaces the executable. Configuration and credentials are not changed.

Logout, profile removal, and uninstall revoke server credentials before deleting local state. Uninstall revokes credentials for every profile, removes Bashido's configuration files, and removes the running executable. `--local-only` skips remote revocation when the servers are unavailable. Permanent script deletion, note clearing, profile removal, and uninstall require `--yes` where shown.
