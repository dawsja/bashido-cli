# bashido CLI

`bashido` is the Linux command-line client for self-hosted Bashido servers. It stores scripts but never executes them. Builds are static (`CGO_ENABLED=0`) for Linux amd64 and arm64.

## Install

```sh
curl -fsSL https://bashido.example.com/install.sh | sh
```

The server-provided installer configures that Bashido instance automatically. It downloads the matching release asset and verifies it against `checksums.txt`. It installs to `~/.local/bin`, or `/usr/local/bin` when run as root. It does not use `sudo` or edit shell startup files.

## Start

```sh
bashido profile add work https://bashido.example.com --use
bashido auth login
bashido script list
bashido script create deploy.sh --title "Deploy"
bashido script show Deploy
```

The first credentialless interactive run starts device linking. Profiles allow independent servers and credentials. See [docs/commands.md](docs/commands.md) for every command.

Upgrade to the latest checksum-verified release without changing local configuration:

```sh
bashido upgrade
```

To revoke every saved server credential and remove the CLI and its local configuration:

```sh
bashido uninstall --yes
```

## Configuration

Configuration is in `${XDG_CONFIG_HOME:-~/.config}/bashido/config.json`; credentials are in a separate `credentials.json`. Directories and files must be private (`0700` and `0600`). A custom CA can be assigned with `profile add --ca-file FILE`.

## Security

HTTPS is mandatory except for explicit loopback HTTP. Credentials are bound to the exact profile origin and are never sent through cross-origin redirects. Human metadata is stripped of terminal control characters. Editor commands are parsed without a shell and temporary files are private. See [docs/security.md](docs/security.md).

## Development

Requires Go 1.24 or newer.

```sh
gofmt -w .
go vet ./...
go test -race ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build .
```

Licensed under the MIT License.
