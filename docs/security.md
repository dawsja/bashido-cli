# Security notes

- Tokens, device codes, bearer headers, and request bodies are never logged or printed.
- Credentials are kept separately from profile configuration, bound to an exact origin, and rejected when permissions or symlink state are unsafe.
- Origins contain only scheme, host, and optional port. HTTPS is required except for `localhost` and loopback IP addresses.
- Redirects are limited and cross-origin redirects are rejected before credentials can be forwarded.
- Connections have dial, response-header, total-request, and cancellation bounds. Response and input bodies are size-limited and JSON responses reject unknown fields.
- Custom CA files extend the system trust pool for one profile.
- Script content is displayed or transferred only. The CLI never executes it.
- Human-readable metadata replaces terminal control characters. JSON and raw content remain unmodified.
- Editors are launched directly, never through a shell. Temporary files use mode `0600`; conflicts preserve recovery content.

Protect the account running `bashido`, its configuration directory, editor configuration, custom CA files, and server. Use `auth logout` to revoke a credential; use `--local-only` only when the server is unreachable and revoke separately later.
