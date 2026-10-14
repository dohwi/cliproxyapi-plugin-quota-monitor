# Quota Monitor Contract

This document is the source-level contract for the `quota-monitor` native
plugin and its management/dashboard boundary. The plugin is an independent,
local module; the repository URL in registration metadata identifies the
upstream host compatibility reference, not publication of this plugin there.

## 1. Data and probe model

`quota-monitor` parses the host's management `auth-files` response into safe
quota DTOs. It never returns auth-file JSON, tokens, cookies, authorization
headers, management keys, or provider response bodies.

The management snapshot is passive with respect to credential contents, but
quota collection currently includes a bounded **active probe** phase for the
providers/endpoints explicitly supported by the adapter (currently Codex,
Antigravity, and official Claude OAuth). Active probes are:

- initiated only during a collection cycle (or an explicitly confirmed/manual
  refresh);
- limited to the configured loopback management endpoint and fixed
  allowlisted provider routes;
- subject to the normal 10-second collection deadline and per-account
  scheduling/backoff;
- optional enrichment: probe failure does not discard a successful passive
  snapshot.

The dashboard data route, `GET /v0/management/plugins/quota-monitor/data/accounts`,
is a read-only view of the retained safe snapshot and requires the management key.
It never triggers collection, provider probes, or auth-directory scans.
Authenticated management `GET /accounts` and `GET /status` own cache fill;
authenticated `POST /refresh` is the explicit force path. Dashboard responses
contain only sanitized account DTOs and enum-valued endpoint metadata.
The dashboard toolbar's **전체 갱신** button explicitly uses the authenticated
`POST /v0/management/plugins/quota-monitor/data/refresh?confirm=1`, independent of
filters, and requires explicit `confirm=1` (HTTP 400 otherwise). The browser sends
the management key typed into the dashboard's own gate as an `Authorization:
Bearer` header (never in a URL), keeps it in the tab's `sessionStorage` once the
host accepted it, and drops it on HTTP 401/403. Automatic snapshot polling never
calls this action. The server globally reserves the 30-second cooldown before
collection, including failed and concurrent requests; additional requests return
HTTP 429 without upstream work. Existing eligibility, deadlines, and probe caps
remain. The unauthenticated `/v0/resource/...` prefix carries no data and cannot
trigger a refresh.

### Atomic Initial Snapshot Contract (v8.2.1)

To prevent empty UI rendering or partial flicker where plan badges, quotas, or
credits update piece-by-piece:
- The dashboard snapshot exposes `snapshot_complete: true` only after an
  authenticated collection successfully commits a complete generation containing
  auth-files scanning, plan resolution, active provider quota, and Codex usage
  enrichment.
- During in-flight collection or before initial collection completes, the snapshot
  returns `snapshot_complete: false` and the dashboard holds its skeleton view
  without committing partial data into `cachedAccounts`.
- If an update fails or times out, the dashboard retains the previous complete
  snapshot as stale instead of overwriting with partial or blank state.
- Responses include `generation`, `collected_at` (or `cached_at`), and
  `last_success` (or `last_success_at`).

### Current Account Selection Contract

- At most one account per provider is marked `current_account: true`, and only
  when a complete usage-audit read proves a completed successful generation
  reported `total_tokens > 0` in the last 15 minutes. A successful collection
  with no qualifying audited use (including a failed/incomplete audit read)
  commits no new current selection; request counts and priority cannot establish
  token use. A failed or timed-out whole collection can retain the previous
  complete snapshot as stale, including its previously selected account.
  Resource snapshots still clear the border when that audited use exceeds 15
  minutes or the selected account becomes unroutable. Progressive updates never
  move the selection to another account before the final successful commit.
- Eligibility requires the account to be healthy and routable: accounts that are
  `disabled`, `error`, `unavailable`, or have exhausted quota windows (short-term 5h
  or long-term weekly) are strictly disqualified. If the newest audited account
  is ineligible, an older eligible audited account may be selected; otherwise
  there is no selection. There is no healthy-priority fallback.
- A selected account exposes `current_kind: recent`, `current_reason: recent_usage`,
  and `selected_by: recent_usage`. The dashboard distinguishes recent token use
  with a blue card border and an accessible card label, without a visible
  badge; other cards use a subtle 1px default border independent of availability.
  The recent-use border remains blue and 2px, with compensated padding to preserve
  identical content insets. The small lock icon sits inline beside the restriction title. Quota-locked cards use the disabled
  gray surface and desaturated content with readable gray countdowns; lock labels,
  availability semantics, text selection, and a recent-use blue border remain intact.
  Identity/freshness, centered quota content, and bottom-aligned optional reset/API
  credits form three card regions, distributing spare height without a blank tail
  below credits. Stale legacy candidate metadata does not produce the blue highlight.

### Codex Extras Merge & Credits Null/Zero Disambiguation (v8.2.1)

- `applyLastGoodWindows` merges and retains plan markers (`plan_type`), typed API
  credits (`api_credits`), and reset rights (`reset_credits`) by immutable
  `account_key`, preventing alias or email collisions.
- Active probe skips or optional detail failures do not erase previously observed
  plan or credit state.
- `api_credits` explicitly distinguishes `null` (not present / uncollected), `0`
  (`balance: 0` or `has_credits: false` rendered as `잔액 0`), and `unlimited`.

Supported passive observation status is provider-dependent: `codex` and
`claude` are recognized by the upstream capture path; unsupported providers
are represented as `unsupported`, not as a provider-side claim that no quota
exists. Account statuses are `known`, `unknown`, `stale`, `unsupported`, or
`error`. Optional numeric fields use pointers so explicit zero is distinct from
missing data, and all quota windows are preserved and normalized safely.

### Official Claude usage tracking

- Official `claude` accounts use the fixed OAuth usage endpoint
  `GET https://api.anthropic.com/api/oauth/usage?cedar_ember=1`, through the existing loopback
  management `api-call` route with `auth_index`, literal `Bearer $TOKEN$`, and
  `Anthropic-Beta: oauth-2025-04-20`. The plugin never reads provider tokens.
- The safe window DTO retains only utilization percentages and reset deadlines
  from `five_hour`, `seven_day`, and supported optional model/app weekly windows.
  Utilization is a percentage, not a fraction; null/missing utilization is not
  interpreted as unused quota. Billing/extra-usage data and unknown fields are
  discarded. Passive `overage` billing windows are ignored consistently for
  subscription availability and audited recent-use selection, so disabled extra
  billing cannot suppress the blue border or prevent recovery after a healthy
  subscription probe. Actual five-hour and weekly exhaustion still disqualify an
  account. Failures retain account-local last-good windows with stale/error
  metadata under the existing scheduling, deadline, and cooldown rules.
- The official provider is displayed as lowercase **claude** in a separate provider card.
  The existing Claude/GPT quota family inside an Antigravity/Gemini card retains
  its Claude icon, tooltips, and accessible labels. The card context distinguishes
  the providers; no family rename or usage-audit regrouping is needed.

### Reset-right display support

Reset-right summaries are provider-independent: any validated `reset_credits`
DTO displays its positive owned count and optional nearest expiry. Codex supplies
these values through WHAM; optional expiry lookups run for each eligible account
within the shared bounded detail budget. Claude supplies a `cedar_ember`
inventory in its OAuth usage response when the fixed `cedar_ember=1` query is
requested. Only eligible, nonpaused, started, unexpired grants contribute
`resets_left` to the owned count; `usable_now` does not change ownership.
Only the count and nearest expiry survive parsing, never grant IDs or redemption
handles. Valid empty inventories clear previous counts and expiry. A successful
Claude usage probe replaces the complete reset inventory: unknown, absent,
ineligible, malformed, or oversized inventory hides previously known credits,
rather than presenting old rights as freshly confirmed. This never fails the
ordinary usage lookup; failed or skipped probes still retain last-good data.
Unsupported query responses (HTTP 400/403) fall back once to ordinary usage;
401/429/5xx never retry. Other providers require a confirmed read-only reset-right
API before populating the DTO. Ordinary quota reset times,
Claude extra-usage balances, and Antigravity AI credits must not be relabeled as
reset rights. Unsupported/missing values remain omitted, not displayed as zero.

## 2. Configuration and host routes

The plugin accepts these environment variables at registration:

- `QUOTA_MONITOR_MGMT_URL`: only `http`/`https` loopback URLs are accepted;
  userinfo, query, and fragment are rejected.
- `QUOTA_MONITOR_MGMT_KEY`: sent only as a bearer credential to that configured
  loopback endpoint and never included in output or errors.

When configuration is absent or invalid, registration still succeeds and the
plugin reports `configured:false` with a generic reason and empty accounts.
Configured means local validation succeeded, not that the upstream endpoint is
reachable.

The host registers these management routes:

- `GET /v0/management/plugins/quota-monitor/status`
- `GET /v0/management/plugins/quota-monitor/accounts`
- `POST /v0/management/plugins/quota-monitor/refresh`
- `GET /v0/management/plugins/quota-monitor/dashboard`
- `GET /v0/management/plugins/quota-monitor/data/accounts`
- `POST /v0/management/plugins/quota-monitor/data/refresh`

The browser-navigable resource registration includes `/dashboard` and the font
assets only (static, unauthenticated). The data routes `GET /data/accounts` and
`POST /data/refresh` are registered as management routes and so sit behind host
management authentication, which remains the trust boundary. The plugin does not
implement a second client-authentication layer.

## 3. RPC envelopes and response safety

All plugin calls use the host envelope:

```json
{"ok":true,"result":{}}
{"ok":false,"error":{"code":"...","message":"..."}}
```

Management response `Body` is a byte slice in the host SDK wire contract, and
therefore is base64-encoded inside the RPC JSON envelope. The decoded HTTP
body is raw JSON or dashboard bytes. Registration uses `schema_version: 6` so
raw management body text is not legacy HTML-escaped.

## 4. Native C ABI

The shared object exports:

```c
int  cliproxy_plugin_init(const cliproxy_host_api *host,
                          cliproxy_plugin_api *plugin);
int  cliproxyPluginCall(char *method, uint8_t *request, size_t request_len,
                        cliproxy_buffer *response);
void cliproxyPluginFree(void *ptr, size_t len);
void cliproxyPluginShutdown(void);
```

The plugin ABI version is `1`. A supplied host API with another version is
rejected; a null host is accepted for standalone loading/smoke verification.
The init and exported lifecycle/call functions recover panics and never allow a
Go panic to unwind into the host. Failures return a bounded JSON error envelope
where possible.

The C boundary enforces:

- `response != NULL` for calls;
- `request == NULL` only when `request_len == 0`;
- an explicit maximum request length of 8 MiB;
- safe `size_t` to `int` conversion before `C.GoBytes` (including 32-bit int
  overflow protection).

`cliproxyPluginFree` accepts only buffers returned by the plugin and ignores
its length for allocator compatibility. Hosts must free every non-null response
exactly once before shutdown/unload.

## 5. Verification and artifacts

From the repository root:

```bash
make verify
make native-smoke                 # usage-audit and quota-monitor
make build-quota
make package-dry-run
```

The quota smoke test dynamically loads the `.so`, resolves init, checks host
ABI rejection and plugin ABI registration, exercises register/management
register/dashboard/accounts/status/shutdown/free, and verifies hostile request
lengths return errors without aborting the host process.

`quota-monitor/artifacts/` is a local build output. `make build-quota` creates
a stripped, path-independent `c-shared` build and its generated header. The
release script builds in temporary directories and may stage a versioned
release package only after verification, sanitizer, both native smokes, and
reproducibility checks pass. It never changes `release/dist-current` or any
operational runtime path.
