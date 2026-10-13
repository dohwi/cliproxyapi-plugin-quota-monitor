# quota-monitor management contract

This file is the quota-monitor operational supplement. The authoritative
cross-repository ABI, probe, resource, and release contract is
[contract.md](contract.md).

`quota-monitor` is a local, independent, unpublished plugin. Its metadata links to the actual upstream host project at <https://github.com/router-for-me/CLIProxyAPI>; that URL does not claim this plugin is published there.

## Configuration

The server process must set both:

- `QUOTA_MONITOR_MGMT_URL`: CLIProxyAPI management base URL. Only `http`/`https` loopback hosts (`localhost`, `127.0.0.0/8`, `::1`, or other literal loopback IPs) are accepted. Userinfo, query strings, and fragments are rejected. `localhost` remains accepted for compatibility with existing local host configurations; use a literal loopback IP when hardening against local DNS rebinding/misresolution.
- `QUOTA_MONITOR_MGMT_KEY`: management key, retained in memory and sent as `Authorization: Bearer ...` only to the configured loopback endpoint. Redirects are rejected by the reused quota client.

When either value is absent or invalid, the plugin remains registered but reports `configured:false`, an empty `accounts` array, `stale:false`, and a safe configuration `reason`. `configured:true` means only that the environment was syntactically valid and a loopback quota client was created; it does not mean the upstream quota source is reachable or fresh.

## Host integration

Capability: `management_api` only. The host owns and enforces management authentication. The plugin does not inspect client authorization headers and does not expose auth-file JSON.

Collection may perform bounded, scheduled active probes for explicitly
supported provider endpoints. Authenticated management `GET /accounts` and
`GET /status` own first cache fill; authenticated `POST /refresh` is the
explicit force path. Automatic dashboard resource polling is snapshot-only and
never requests a refresh/probe cycle. The toolbar's **전체 갱신** action explicitly
calls keyless `GET /v0/resource/plugins/quota-monitor/data/refresh?confirm=1`.
The deployed host supports only resource GET dispatch. This separate explicit
action requires `confirm=1` (HTTP 400 otherwise); snapshot GETs remain read-only. See the root contract for the C ABI and
resource boundary guarantees.

Registered routes (host-required `/v0/management/` prefix):

- `GET /v0/management/plugins/quota-monitor/status`
- `GET /v0/management/plugins/quota-monitor/accounts`
- `POST /v0/management/plugins/quota-monitor/refresh`

A management read that races with `plugin.reconfigure` retries against the new
configuration generation within a bounded request deadline, so the transient
old-generation cancellation is not exposed as a zero/stale startup result.

The plugin registers RPC `schema_version: 6`, matching the current host SDK schema confirmed in `/tmp/cliproxyapi-src/sdk/pluginabi` and `/tmp/cliproxyapi-src/internal/pluginhost/rpc_schema.go`. Schema 6 is required so management JSON response bodies containing `<`, `>`, or `&` remain raw after the host decodes the RPC `Body`; hosts treating this plugin as schema `<6` may HTML-escape JSON string values for legacy compatibility.

The native management response uses the SDK wire contract: `StatusCode`, `Headers`, and `Body`, where `Body` is `[]byte` (base64 when represented inside JSON RPC).

## Data and failure behavior

The wrapper reuses `quota-monitor.Client` and its strict auth-file parser. Fetches have a 10-second deadline, successful results are cached for 60 seconds, and simultaneous cache misses are coalesced.

Response fields:

- `configured`: environment/configuration validity only. `false` means required env is missing or invalid; `true` means the configured loopback URL/key passed local validation.
- `accounts`: the last safe parsed quota snapshot for `/accounts`, or its count for `/status`. Empty when unconfigured or when no successful lookup has ever completed.
- `stale`: collection-level freshness only. It is `false` after a successful auth-files/global collection commit or for an unexpired successful cache, even when one account carries account-local stale/error state. It becomes `true` only for collection-level auth-files fetch failure, timeout/cancellation, configuration-generation abort, or cache expiry.
- `cachedAt`: present on `/status` only after at least one successful lookup, and records when the retained snapshot was fetched.
- `reason`: present for configuration failures. On lookup failure it is the generic string `quota lookup failed`; upstream error details are intentionally suppressed. A lookup failure can occur even when `configured:true`, because the configured source may be unreachable, stale, unauthorized, or returning invalid data.

A failed refresh returns the last successful safe account snapshot as `stale`, preserving its original `observed_at`; if no successful snapshot exists, `accounts` is empty. Error details, management URL/key, raw auth files, tokens, and unknown signals are never returned or logged by the wrapper.

## Official Claude usage quotas

Official `claude` accounts participate in the bounded active collection cycle.
One GET to the fixed `https://api.anthropic.com/api/oauth/usage?cedar_ember=1` endpoint is
forwarded via management `api-call` using the account's `auth_index`, the
literal `Bearer $TOKEN$` placeholder, and `Anthropic-Beta: oauth-2025-04-20`.
The plugin does not read credentials. Five-hour, overall weekly, and supported
optional model/app weekly utilization percentages and reset times are retained;
null/missing values are not treated as zero usage. Extra-usage billing fields
are not exposed. Passive `overage` billing windows are excluded from subscription
availability and recent-use selection alike; disabled extra billing must not
suppress the audited recent-use border or prevent recovery after a healthy
subscription probe. Five-hour and weekly subscription
exhaustion still disqualify an account. Provider failures preserve last-good windows with account-local
stale/error state; normal backoff, disabled-account exclusion, and deadlines apply.

The official provider is displayed as lowercase **claude** in its own card, matching the existing provider-badge naming style. The existing
Claude/GPT quota section within Antigravity/Gemini retains its Claude icon and
labels; the card context distinguishes providers without a family rename.
Underlying provider/window identifiers and audit grouping do not change.

## V5.3 Codex safe enrichments

During the bounded active-probe cycle, Codex WHAM usage responses may populate two independent safe DTOs:

- `reset_credits`: nullable `available_count`, nullable `applicable_available_count`, and nullable `nearest_expiry` only. Claude also populates the same DTO from eligible `cedar_ember.grants`: sum `resets_left` only for nonpaused grants that have started and not expired; ignore `usable_now` for ownership. Retain only count and nearest expiry (no grant IDs). Cap inventory at 200 records and 50 rights. Valid empty inventory clears old count/expiry. A successful Claude usage probe replaces the complete inventory: unknown, absent, ineligible, malformed, or oversized inventory hides previously known credits without breaking usage. Failed or skipped probes still retain last-good data. The fixed inventory query falls back once on 400/403 within the same deadline, but not on 401/429/5xx. When `available_count > 0`, each eligible Codex account receives an optional detail request to the fixed WHAM reset-credit endpoint within the shared collection budget (up to 16 accounts). Only `status:"available"` future `expires_at` values contribute the nearest expiry. Credit IDs, grants, and raw responses are discarded.
- `api_credits`: nullable `has_credits`, `unlimited`, and a finite, bounded `balance` from the separate WHAM `credits` object. This is never treated as a reset right.

The optional detail request shares the normal loopback `/management/api-call`, auth-index, and `$TOKEN$` injection route, has the normal bounded deadline, and cannot fail the usage snapshot.

## V8.2.1 Atomic Initial Snapshot & Current Selection Hardening

- `snapshot_complete`: Boolean flag returned in resource data indicating that at least one atomic, fully enriched generation has been committed. Before the first complete snapshot the dashboard stays on skeletons.
- After the first complete snapshot, refreshes are progressive by account. Resource data exposes `collection_in_progress`, an opaque non-sensitive `run_id`, and `progress` with `completed`, `total`, `started_at`, and `updated_at`.
- Collection start keeps the previously committed complete snapshot, order, and current-account highlights. Fresh passive auth metadata is merged only for already committed `account_key` values; newly discovered accounts and removals are not published until final commit.
- Each completed worker publishes only its own account after generation and run-ID guards. Missing `plan_type` and window fields retain the previous non-missing value. Non-nil partial `api_credits` and `reset_credits` are merged field by field: incoming nil pointers retain cloned previous values, while explicit zero and false pointers are authoritative. A failed account retains last-good data and receives only account-local stale/error state; sibling accounts and global snapshot state are not marked stale by that worker failure.
- During a batch, committed card order and current-account selection remain fixed. Only successful batch completion finalizes new/deleted accounts, recalculates order/current, and commits global `snapshot_complete`, health, schedules, and `cached_at`. Results from an old generation or old run are discarded.
- Browser behavior: while `collection_in_progress:true`, the dashboard performs snapshot-only polling every 2 seconds. On completion it returns to the server `next_poll_hint_ms`. Full rendering is permitted for progressive updates but preserves scroll position and the active element focus path.
- Recent token-use card border (`current_reason=recent_usage`, with an accessible card label and no visible badge) identifies the most recently requested **completed successful generation with reported total_tokens > 0** per provider in the last 15 minutes, matched by provider + auth_index via usage-audit. All pages in that window must be read; failed/incomplete audit reads produce no new recent-use selection on a successful collection. It is not a live in-flight request indicator, and zero/missing reported token counts cannot prove consumption.
- Card layout: non-recent cards use a subtle 1px default border regardless of availability; only recent-use cards receive a blue 2px border, with compensated padding to preserve identical content insets. The small lock icon sits inline beside the restriction title. Quota-locked cards use a disabled gray surface and desaturated content with readable gray countdowns; availability semantics, lock labels, text selection, and the independent recent-use blue border are preserved. Other semantic backgrounds remain unchanged. Account identity and observation time stay together at the top, quota/lock content is centered in the flexible middle, and optional reset rights/API credits sit at the bottom with only normal card padding below them. Shared content-driven card heights and responsive wrapping are preserved.
- Current account eligibility: accounts in `disabled`, `error`, `unavailable`, or `limited` status (including 5h request limit exhaustion) are strictly excluded from `current_account`. Selection checks all eligible recently token-using accounts; on successful collection, when none exist (or the usage-audit read fails), no new account is marked current. A failed or timed-out whole collection may retain the previous complete snapshot as stale, including its previously selected account. Resource snapshots still clear the border once audited use exceeds 15 minutes or the selected account becomes unroutable; progressive updates do not move it to another account before the final successful commit. Priority and request counters do not prove token use. Only `current_kind=recent`, `current_reason=recent_usage`, and `selected_by=recent_usage` accompany a selected account; legacy candidate metadata cannot produce a dashboard highlight.
- Extras retention: `applyLastGoodWindows` merges `plan_type`, all nullable `api_credits` fields (`has_credits`, `unlimited`, `balance`), and all nullable `reset_credits` fields (`available_count`, `applicable_available_count`, `nearest_expiry`) by `account_key`; explicit `false`/`0` values are preserved.
- Codex plans are canonicalized to the existing safe labels (`Plus`, `Team`, `Free`, `Pro`, plus existing `K12`/`Enterprise`) from explicit passive/active `plan_type` / `chatgpt_plan_type`, subscription, organization, or workspace plan fields. ChatGPT `Business` is normalized to `Team` because it is the renamed Team subscription. Organization/workspace existence, names, IDs, roles, arbitrary account labels, unknown paid strings, and a raw management entry `name` ending in `-team.json` never imply Team. Filename-based Team inference is accepted only when the resolver matches that exact canonical basename with an exact lowercase `.json` suffix to an on-disk credential whose source `type` is exactly `codex`; immutable-ID-only and filename-alias matches are insufficient. Unavailable, rejected, unsafe/case-mismatched, missing, or contradictory non-Codex resolution remains blank. Explicit payload/header/WHAM/subscription/workspace Team remains authoritative.

## Refresh timing (ELI5)

- Background collector: starts one complete collection immediately after configuration, then normally tries every **60 seconds** (`internal/plugin/plugin.go`: `defaultBackgroundInterval`). After a failed collection it retries after **15 seconds** (`backgroundFailureBackoff`).
- Snapshot freshness: a successful cached collection becomes `cache_expired` after **120 seconds** (`cacheTTL`). Due account probes may bypass this collection cache so a reset recheck is not delayed by cache freshness. The snapshot GET remains read-only and does not trigger provider calls.
- Exhausted quota resets override the normal **20-minute short-limit / 45-minute exhausted** account probe tiers: the next probe is scheduled for the reset deadline plus **5 seconds**, with at least **60 seconds** between attempts. A past reset that upstream still reports as exhausted is retried every **60 seconds**; disabled accounts and authentication errors retain their normal policy. The background collector checks due probes on its normal **60-second** tick. A reset deadline alone never restores quota or unlocks a card.
- Browser polling: the server hint is derived from account probe tiers; a current Codex account normally yields **120 seconds** (`probeIntervalCurrent`). The dashboard falls back to **30s / 90s / 300s / 900s** for current / healthy / pending-stale / all-limited states and caps hints at **900 seconds** (`internal/dashboard/dashboard.go`). An incomplete initial snapshot or any progressive refresh in progress is polled every **2 seconds**. Browser fetch timeout is **16 seconds**.
- Around exhausted windows' reset deadlines, browser snapshot polling is brought forward to the deadline plus **5 seconds**; already expired exhausted windows are polled at most every **30 seconds**. This only reads the snapshot and does not initiate provider requests.
- Visible countdowns redraw every **30 seconds**. Returning to a visible browser tab triggers an immediate snapshot read; the redundant page-level synchronization timestamp is intentionally not displayed.
- Each account card shows its own **last checked** time as plain secondary text from the newest valid account/window `observed_at` (falling back to `last_success`), including an exact Korean timestamp tooltip. Missing timestamps are explicit, observations at least 15 minutes old are visually aged, and observations at least 45 minutes old are labeled as old information.
- When a card receives a newer provider observation, the card briefly flashes with a neutral blue market-ticker-style cue. Material quota, lock, availability, or credit changes retain their semantic green/amber/red cue and take precedence over the neutral synchronization cue. Reduced-motion preference disables the animation.
- Management behavior: authenticated `GET /status` and `GET /accounts` collect when the cache is empty, at least **120 seconds** old, or an eligible account probe is due; authenticated `POST /refresh` forces collection, with a **30-second** force cooldown. Plugin startup/reconfigure launches the immediate background collection. The dashboard `/data/accounts` endpoint itself never refreshes upstream.
- Manual browser refresh: **전체 갱신**, beside filter **초기화**, ignores the current Provider/Endpoint/status filters and explicitly calls keyless `GET /v0/resource/plugins/quota-monitor/data/refresh?confirm=1` (resource POST is unsupported by the deployed host). No browser credential or password dialog is used. Automatic polling, filter changes, and visibility events never call this action. The request rejects redirects, omits cookies, bypasses cache, and has a **60-second** browser timeout. The server globally reserves a **30-second** cooldown before collection, including failed and concurrent requests; additional requests return **HTTP 429** without upstream work. The UI disables the button during collection and for 30 seconds on success or HTTP 429. Anyone able to reach the resource route can request this bounded collection; the resource should therefore be protected by the deployment's network/access policy where needed. Host management authentication and the server-side loopback management key remain unchanged. Failures preserve the last displayed snapshot and show generic retry feedback. Disabled/unsupported accounts are not actively probed, at most **128** eligible accounts are probed per collection, and per-account failures or truncation do not imply fresh quota for every account.
- Reset-credit UI is provider-independent: any account with a validated `reset_credits` DTO uses the same summary. Only confirmed provider reset-right APIs may populate it; quota reset timestamps, additional-usage money balances, and AI credits are not reset rights. Missing unsupported data is omitted, never invented as zero.
- Reset-credit UI intentionally shows only the owned `available_count` (`리셋권 N장 보유`) and optional nearest expiry. `applicable_available_count` remains available in the data contract and snapshot diffing but is not displayed, because upstream does not provide enough reason context for a clear user-facing explanation.
- API credits disambiguation: `has_credits: false` / `balance: 0` is safely displayed as `잔액 0`, distinct from uncollected/null state.

The dashboard self-hosts SUIT Regular and SemiBold through plugin resource assets (no CDN). Provenance: SUIT project, SIL Open Font License 1.1. SHA-256: Regular `7a1971b4e54d6d797f105f2d8ad2fc5f5d6fd532195f582d74f4403177e83185`; SemiBold `a0ea79e549e2ef42930d16c9c5b40600132a7f3661fe0e26a84a20c2c8d182a0`.
