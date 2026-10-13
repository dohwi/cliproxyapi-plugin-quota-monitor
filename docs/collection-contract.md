# Plugin collection/security contract

Plugin-local integration note; dashboard, host entry point, quota parser/client, and root documentation are maintained separately.

## HTTP/RPC compatibility

- `GET /v0/resource/plugins/quota-monitor/data/accounts` is **snapshot-only**. Empty/expired caches and an in-flight collection never trigger management HTTP, provider probes, or an auth-directory scan. It remains HTTP 200 with `Cache-Control: no-store`.
- `/v0/resource/plugins/quota-monitor/data/refresh` is no longer registered and returns 404 for GET/POST, including `?confirm=1`. Do not implement startup recovery through this unauthenticated URL.
- Forced collection remains `POST /v0/management/plugins/quota-monitor/refresh`, using the host's existing management authentication. GET refresh is not registered. The plugin relies on the host separating authenticated management dispatch from resource dispatch; it does not obtain management authorization from query/body fields.
- Authenticated management GET `/accounts` and `/status` trigger/coalesce the first collection when the cache is empty or expired. A resource dashboard alone does not populate an empty cache: it polls the snapshot only and shows the not-collected/expired state. Explicit `POST /refresh` is the authenticated force path. If reconfigure invalidates a joined flight, management reads retry once against the new generation (within a bounded request deadline) instead of exposing the transient old-generation empty/stale result.
- A force request joins an already-running collection rather than queueing another wave of probes. Otherwise it bypasses probe due times, except for disabled accounts and the 30-second manual-refresh cooldown. A cooldown response retains the previous error/stale result (it cannot turn a failed refresh into a success).

## Additive DTO fields

Resource accounts retain masking and omit internal AuthIndex/LastUsedAt. The response includes `configured`, a safe `reason`, `stale`, `generation`, `truncated`, and `health`; `cached_at` (also legacy `cachedAt`) and `last_success_at` are included when available. Account `source` is exposed with retained quota state. `cached_at` is the time the retained snapshot was committed; `last_success_at` is the last collection without a collection-level failure, not proof every optional enrichment/probe succeeded. Per-account `last_success` remains the active quota success timestamp. Optional probe/enrichment failures are visible through account state and health category/counters.

Management account and refresh responses additionally carry the result's generation/stale/truncated metadata. All coalesced callers receive the same immutable flight result; caller-local cancellation may end one wait without canceling shared work. Internal upstream errors/paths/URLs never enter collection health.

## Collection limits and lifecycle

- 25-second overall deadline covers directory scan, passive fetch, usage enrichment, queue wait, active probes, and optional reset details. Network phases also use 10-second child deadlines.
- At most 512 returned/processed accounts, 128 active **account jobs**, three workers, and four optional reset-detail jobs per collection. Antigravity may internally try its client's fixed endpoint fallback list within one account job; the account-job limit is not a raw HTTP-request count.
- Reset detail runs inside those same workers, only after a successful, actually attempted Codex usage probe. Skipped/disabled accounts cause no usage or detail calls. Omitted work is marked `truncated`.
- Scheduling stores `LastAttempt`, `LastSuccess`, and `NextDue`. Only dequeued, non-canceled eligible jobs update attempts/due; passive refreshes and queued-but-canceled jobs do not postpone due times. Removed/disabled entries are pruned and maps are bounded.
- Reconfigure cancels/detaches the old flight, increments generation, and resets cache/scheduling/cooldown/health. Old collectors cannot publish into the new configuration or clear its flight. Their existing waiters receive `configuration_changed` with the old generation and no old accounts.
- Explicit deep copying preserves all exported mutable fields, LastUsedAt, and quota package private immutable identity strings (via struct assignment), without JSON roundtrips.

## Auth-directory scan boundaries

Linux FD-based scan: reject a final directory symlink, open the directory with `O_DIRECTORY|O_NOFOLLOW`, enumerate at most 2,049 entries (2,048 processed), inspect at most 512 JSON candidates, and open children relative to the directory FD with `openat(O_NOFOLLOW|O_NONBLOCK)`. `fstat` must identify a regular file no larger than 256 KiB; `LimitReader(max+1)` also handles concurrent growth. All FDs are closed. Context is checked before/between scan operations and before publishing scan maps.

These checks do not make hostile kernel/filesystem operations interruptible and do not prohibit symlinked intermediate ancestors of the explicitly configured directory. Keep auth-dir on a trusted local filesystem. The quota client's own response-body bound applies before plugin account slicing; the plugin cannot bound the parser's transient allocations before `FetchAuthFiles` returns without changing that client.

## Validation

Focused regression coverage is in `collection_security_test.go`; resource registration and resolver snapshot tests were updated to the new contract. Use:

```sh
go test -race ./internal/plugin
go vet ./...
go test -race ./...
```

The full-module suite may temporarily reflect concurrent quota/dashboard work; report such failures separately rather than changing other agents' files.
