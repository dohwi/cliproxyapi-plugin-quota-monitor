# Antigravity quota: Gemini weekly limit missing (client version gate)

## Symptom

Antigravity cards showed `Weekly limit — / Not provided` for the Gemini family,
while the official `agy` CLI (`Models & Quota` screen) showed a real Gemini
weekly limit (for example 97.02%) for the same account.

## Root cause

`retrieveUserQuotaSummary` decides which quota groups to return **based on the
client version in the `User-Agent` header**. The plugin sent
`antigravity/cli/1.0.13 (...)`, and Google answered with only the
`Claude and GPT models` group. The `Gemini Models` group was omitted entirely,
without any error. The official client sends `1.4.12`.

Verified against 6 live accounts through the CLIProxyAPI management
`/api-call` route (same account, same project, only the header changed):

| User-Agent | Groups returned |
| --- | --- |
| `antigravity/cli/1.0.13 (aidev_client; ...)` | `Claude and GPT models` |
| `antigravity/cli/1.4.12 (aidev_client; ...)` | `Gemini Models`, `Claude and GPT models` |
| `antigravity/cli/1.4.12 (...)` + `X-Goog-Api-Client` | both groups |
| `antigravity/1.4.12 linux/amd64` (wrong format) | `Claude and GPT models` |

The `1.4.12` response matched `agy`: Gemini weekly 97.02%, Gemini 5-hour 100%,
Claude/GPT weekly 0%, Claude/GPT 5-hour 100%.

## What was misleading

- The parser and dashboard were correct. The data simply was not in the response.
- The per-model endpoint (`retrieveUserQuota`) and `fetchAvailableModels` only
  carry a 5-hour `remainingFraction` per model, so they looked like "the only
  Gemini data" and made a missing weekly limit seem like an upstream gap.
- A 200 response with a missing group is indistinguishable from "no such quota".
  Two releases (v0.3.3, v0.3.4) treated it as absent data (placeholder text, then
  hiding the row) before the header was identified. Do not hide or special-case
  a row for missing upstream data without first comparing against the official
  client.

## Fix

`antigravityUserAgent` in `internal/quota/quota.go` is pinned to the official
client version (`1.4.12`). A unit test asserts the version string. Released as
v0.3.5.

## If it happens again

If a group (or window) that `agy` shows disappears from the dashboard:

1. Run `agy` and open `Models & Quota`; note the version in the banner.
2. Call `retrieveUserQuotaSummary` for the same account with that version in the
   `User-Agent` and compare the returned `groups[].displayName`.
3. Bump `antigravityUserAgent` to the version that returns the full set, update
   the unit test, and bump the plugin version.

Useful facts from the official binary: its usage view (`UsageModel.renderQuotaGroup`)
renders the groups from `retrieveUserQuotaSummary` and shows per group a
`Weekly Limit Remaining` and a `Five Hour Limit Remaining`.

## Staying current automatically

The official updater publishes the stable client version in plain text:
`GET https://antigravity-cli-auto-updater-974169037036.us-central1.run.app`
returns `... Stable Version: 1.4.12. Rolled out to 100%`.

- `scripts/check-antigravity-client-version.sh` compares it with the pinned
  `antigravityUserAgent` (exit 0 in sync, 1 drift, 2 lookup failed).
- `.github/workflows/antigravity-client-version.yml` runs it weekly and opens a
  single `Antigravity client version drift` issue when they differ.

The plugin deliberately does not fetch the version at runtime or bump it itself:
it would add an outbound network dependency to a hardened plugin, and a new
version could change which groups Google returns. A bump stays a reviewed change
following "If it happens again" above (check the groups first, then release).
