# Quota remaining overview and account refresh

The quota-monitor plugin exposes its cached quota data to the dashboard without
requiring a management key in the browser. Snapshot reads remain read-only.

## Snapshot

`GET /v0/resource/plugins/quota-monitor/data/accounts`

In addition to the existing account windows, the response includes a server-side
`overview` grouped by provider and model family. It covers all non-disabled
accounts, not just the most recently used account. Antigravity Gemini and Claude
allowances are reported separately. Remaining percentages describe the most
constrained known allowance; percentages across different accounts are never
added. Unknown allowances stay unknown, and stale data is explicitly marked.
Overage billing and code-review allowances are excluded from subscription usage.
This is quota utilization, not an estimate of remaining tokens or money.

## Explicit refresh

- All accounts: `GET /v0/resource/plugins/quota-monitor/data/refresh?confirm=1`
- One account: `GET /v0/resource/plugins/quota-monitor/data/refresh?confirm=1&account_key=<URL-encoded snapshot key>`
- Authenticated management equivalent: `POST /v0/management/plugins/quota-monitor/refresh?account_key=<key>`

The resource host currently supports GET dispatch only, so the confirmed resource
refresh is an explicit action and must never be automatically polled. The key is
an existing opaque snapshot identity, not an auth index, token or upstream URL.

An individual refresh probes only that account and retains every other account's
data. It does not scan the auth directory, refresh account inventory or renew the
whole snapshot's freshness timestamp. A failed probe preserves last-good quota
and marks only the target as stale/error. Use full refresh to discover account
additions or deletions.

Individual refreshes have a 30-second per-account cooldown, including failures.
They share the collection flight with background/full refresh work to prevent
older batches overwriting the account patch. A busy collection returns `409`;
unknown keys return `404`, unsupported/disabled accounts `422`, cooldown `429`,
and an unconfigured plugin `503`. Malformed or unconfirmed resource requests
return `400`. A completed attempt returns `200` with `refreshed: true|false`;
clients must inspect this field, not just HTTP success. Upstream error details
are not returned.

The dashboard offers both full refresh and per-account buttons, showing progress,
cooldown and result feedback. Overview data stays global when account filters are
changed.
