# Security policy

## Deployment boundary

This is a native plugin loaded inside CLIProxyAPI, not an independently authenticated web service. The host process and its configuration are trusted. Keep the plugin resource routes behind a trusted network or an authenticated reverse proxy; do not expose them directly to the public Internet.

The resource dashboard and `/data/accounts` do not require the management key. They intentionally expose quota/account display information. `/data/refresh?confirm=1` can trigger bounded upstream work with a cooldown; `confirm=1` is confirmation, not authentication or CSRF protection. Management routes depend on the host enforcing management authentication before dispatch.

Management HTTP access is restricted to loopback URLs; redirects and environment proxies are disabled. Auth-directory scanning rejects final symlinks and nonregular files and applies size/count bounds. Keep the configured directory and its ancestors on a trusted filesystem.

## Data handling

Resource DTOs omit raw tokens, internal auth indexes, paths and proxy URLs. Account display identifiers are not anonymous: treat snapshots and screenshots as potentially sensitive. The dashboard persists only its language preference. Never commit operational auth files, usage logs, keys or real account identifiers; use reserved example domains for fixtures.

## Verification

Use the patched Go runtime pinned in `.mise.toml`. Run `make verify`, `make native-smoke`, `make verify-i18n`, and `govulncheck ./...` before release. Dependency scanning and regression tests reduce risk but do not prove the absence of vulnerabilities.

## Reporting

Do not publish credentials or sensitive deployment details in issues. Use GitHub private vulnerability reporting when available, or contact the repository owner privately before sharing a security report.
