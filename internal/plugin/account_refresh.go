package plugin

import (
	"context"
	"errors"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

// refreshSingleAccount probes only an identity already present in the private
// snapshot. Public callers cannot supply an auth index, provider URL or token.
// It shares the collector flight so full/background collections cannot overwrite
// the patch. A partial refresh must not renew the age of the entire snapshot.
func refreshSingleAccount(key string) (int, map[string]any) {
	st := state
	st.mu.Lock()
	fail := func(code int, message string) (int, map[string]any) {
		st.mu.Unlock()
		return code, map[string]any{"error": message}
	}
	if !st.configured || st.client == nil {
		return fail(503, "not configured")
	}
	var account quotamonitor.AccountQuota
	found := false
	for _, a := range st.last {
		if a.AccountKey == key {
			account = cloneAccounts([]quotamonitor.AccountQuota{a})[0]
			found = true
			break
		}
	}
	if !found {
		return fail(404, "account not found")
	}
	if !probeEligible(account) {
		return fail(422, "account refresh unavailable")
	}
	if st.accountRefreshAt == nil {
		st.accountRefreshAt = make(map[string]time.Time)
	}
	at := now()
	if last := st.accountRefreshAt[key]; !last.IsZero() && at.Sub(last) < forceCooldown {
		return fail(429, "refresh cooldown")
	}
	if st.flight != nil {
		return fail(409, "collection in progress")
	}
	// Prune removed identities; reservations include failed upstream probes.
	live := make(map[string]bool, len(st.last))
	for _, a := range st.last {
		live[a.AccountKey] = true
	}
	for k := range st.accountRefreshAt {
		if !live[k] {
			delete(st.accountRefreshAt, k)
		}
	}
	st.accountRefreshAt[key] = at
	ctx, cancel := context.WithTimeout(context.Background(), collectionTimeout)
	defer cancel()
	st.runSeq++
	flight := &collectionFlight{generation: st.generation, runID: st.runSeq, hadComplete: st.snapshotComplete, force: true, accountKey: key, done: make(chan struct{}), cancel: cancel}
	st.flight, st.runID, st.inProgress = flight, flight.runID, true
	st.progress = collectionProgress{Total: 1, StartedAt: at, UpdatedAt: at}
	client := st.client
	st.mu.Unlock()

	accounts, attempts, truncated := probeAccounts(ctx, client, []quotamonitor.AccountQuota{account}, []quotamonitor.AccountQuota{account}, nil, true)
	result := collectionResult{accounts: accounts, generation: flight.generation, category: "success", truncated: truncated}
	attempt, attempted := attempts[key]
	success := attempted && !attempt.LastSuccess.IsZero()
	if !success {
		result.err = errors.New("quota lookup failed")
		result.category = "account_refresh_failed"
	}
	if ctx.Err() != nil {
		setCollectionContextError(&result, ctx.Err())
	}

	st.mu.Lock()
	if st.generation != flight.generation || st.runID != flight.runID || st.flight != flight {
		result.err = errors.New("configuration changed")
		result.category = "configuration_changed"
		result.accounts = []quotamonitor.AccountQuota{}
		result.stale = true
	} else {
		// Failure retains last-good windows and marks this account alone as stale.
		if len(accounts) == 1 {
			for i := range st.last {
				if st.last[i].AccountKey == key {
					st.last[i] = cloneAccounts(accounts)[0]
					break
				}
			}
		}
		recordProbeAttempts(st, st.last, attempts)
		st.current = currentAccountSelection(st.last)
		st.latest.accounts = cloneAccounts(st.last)
		st.inProgress = false
		st.progress.Completed = 1
		st.progress.UpdatedAt = now()
		result.accounts = cloneAccounts(st.last)
	}
	flight.result = copyResult(result)
	if st.flight == flight {
		st.flight = nil
	}
	close(flight.done)
	st.mu.Unlock()
	body := map[string]any{"refreshed": result.err == nil, "count": 1, "generation": result.generation, "reason": result.category, "truncated": result.truncated, "stale": result.err != nil}
	if result.err != nil {
		body["error"] = "account refresh failed"
	}
	return 200, body
}
