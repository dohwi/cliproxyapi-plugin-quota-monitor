# Security policy

## Deployment boundary

This is a native plugin loaded inside CLIProxyAPI, not an independently authenticated web service. The host process and its configuration are trusted.

**Route boundary.** CLIProxyAPI serves `/v0/resource/plugins/<id>/...` without authentication, so this plugin puts only static UI there: the dashboard page shell and its self-hosted fonts. Everything dynamic is behind the host's management authentication under `/v0/management/plugins/quota-monitor/...`:

| Route | Purpose |
|---|---|
| `GET  /data/accounts` | Sanitized quota snapshot for the dashboard (never triggers a probe) |
| `POST /data/refresh?confirm=1` | Explicit refresh (30-second global cooldown) |
| `GET  /accounts`, `GET /status`, `POST /refresh` | Management API |

The unauthenticated `/v0/resource/.../data/*` routes do not exist (HTTP 404). The static page therefore asks the operator for the management key; it is sent only as an `Authorization: Bearer` header (never in a URL), kept in the tab's `sessionStorage` after the host accepted it, and dropped on a 401/403. It is never written to `localStorage`, cookies or the server. Without a key the page sends no data request, because CLIProxyAPI counts every rejected management request toward a temporary IP ban.

`confirm=1` on the refresh action is a safeguard against stray calls, not authentication or CSRF protection: authentication is the host's management key.

Management HTTP access is restricted to loopback URLs; redirects and environment proxies are disabled. Auth-directory scanning rejects final symlinks and nonregular files and applies size/count bounds. Keep the configured directory and its ancestors on a trusted filesystem.

## Response hardening

The dashboard page is served with a strict `Content-Security-Policy` (`default-src 'none'`; scripts and styles inline only, fonts/connect `'self'`, framing only by the same origin (the management console embeds the page in an iframe), `base-uri 'none'`, `form-action 'none'`), plus `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and `X-Frame-Options: SAMEORIGIN`. Fonts and JSON responses are `nosniff`. The page loads no external origin.

## Data handling

Dashboard DTOs omit raw tokens, internal auth indexes, paths and proxy URLs. Account display identifiers are not anonymous: treat snapshots and screenshots as potentially sensitive. The dashboard persists its language preference (`localStorage`) and, for the current tab only, the accepted management key (`sessionStorage`). Never commit operational auth files, usage logs, keys or real account identifiers; use reserved example domains for fixtures.

## Verification

Use the patched Go runtime pinned in `.mise.toml`. Run `make verify`, `make native-smoke`, `make verify-i18n`, and `govulncheck ./...` before release; the Release workflow builds and smoke-tests every store platform natively. Dependency scanning and regression tests reduce risk but do not prove the absence of vulnerabilities.

## Reporting

Do not publish credentials or sensitive deployment details in issues. Use GitHub private vulnerability reporting when available, or contact the repository owner privately before sharing a security report.
