package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestRefreshRollbackRealCollection(t *testing.T) {
	reset(t)
	clock := time.Now().UTC().Truncate(time.Second)
	oldObservation := clock.Add(-time.Minute)
	previousNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = previousNow })
	var phase, probes, lists atomic.Int32
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			lists.Add(1)
			fmt.Fprintf(w, `[{"provider":"codex","auth_index":"7","account":"a@x","status":"unavailable","unavailable":true,"quota":{"observed_at":%q,"signals":{"x-codex-primary-used-percent":"100","x-codex-primary-limit-reached":"true","x-codex-primary-allowed":"false","x-codex-primary-reset-after-seconds":"3600","x-codex-secondary-used-percent":"70","x-codex-secondary-reset-after-seconds":"7200","x-codex-plan-type":"free"}}}]`, oldObservation.Format(time.RFC3339))
		case "/management/api-call":
			probes.Add(1)
			if phase.Load() == 3 {
				http.Error(w, "failed", http.StatusBadGateway)
				return
			}
			body := `{"plan_type":"team","primary_window":{"used_percent":0,"reset_after_seconds":600},"secondary_window":{"used_percent":20,"reset_after_seconds":7200},"credits":{"has_credits":true,"balance":12},"rate_limit_reset_credits":{"available_count":0}}`
			if phase.Load() == 0 || phase.Load() == 2 {
				body = `{"plan_type":"team","primary_window":{"used_percent":100,"limit_reached":true,"allowed":false,"reset_after_seconds":600},"secondary_window":{"used_percent":20,"reset_after_seconds":7200}}`
			}
			json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": body})
		default:
			w.Write([]byte(`[]`))
		}
	})
	initial := collectAccounts(context.Background(), false)
	if initial.err != nil || len(initial.accounts) != 1 {
		t.Fatalf("initial: %+v", initial)
	}
	phase.Store(1)
	recovered := collectAccounts(context.Background(), true)
	if recovered.err != nil || probes.Load() != 2 {
		t.Fatalf("forced: %+v probes=%d", recovered, probes.Load())
	}
	good := recovered.accounts[0]
	primary := rollbackWindow(t, good, "codex:primary")
	if primary.UsedPercent == nil || *primary.UsedPercent != 0 || primary.RemainingPercent == nil || *primary.RemainingPercent != 100 || primary.LimitReached == nil || *primary.LimitReached || primary.Allowed == nil || !*primary.Allowed {
		t.Fatalf("recovery retained exhausted fields: %+v", primary)
	}
	if primary.ResetAt == nil || !primary.ResetAt.Equal(clock.Add(600*time.Second)) || good.ObservedAt == nil || !good.ObservedAt.Equal(clock) || good.LastSuccessAt == nil || !good.FetchedAt.Equal(clock) {
		t.Fatalf("active anchors: %+v window=%+v", good, primary)
	}
	if good.Operational == nil || good.Operational.Unavailable == nil || *good.Operational.Unavailable || good.PlanType != "team" {
		t.Fatalf("recovery state: %+v", good)
	}
	clock = clock.Add(cacheTTL + time.Second)
	// Keep this case explicitly not due, independently of tier scheduling policy.
	state.mu.Lock()
	state.probeDue[good.AccountKey] = clock.Add(time.Hour)
	state.mu.Unlock()
	skipped := collectAccounts(context.Background(), false)
	if skipped.err != nil || probes.Load() != 2 || lists.Load() != 3 {
		t.Fatalf("passive did not collect/skip: %+v probes=%d lists=%d", skipped, probes.Load(), lists.Load())
	}
	got := skipped.accounts[0]
	if !reflect.DeepEqual(got.Windows, good.Windows) || !reflect.DeepEqual(got.APICredits, good.APICredits) || !reflect.DeepEqual(got.ResetCredits, good.ResetCredits) || got.PlanType != good.PlanType || !got.ObservedAt.Equal(*good.ObservedAt) || !got.LastSuccessAt.Equal(*good.LastSuccessAt) || !got.FetchedAt.Equal(good.FetchedAt) || *got.Operational.Unavailable || got.Operational.Status != "active" {
		t.Fatalf("passive rolled back: good=%+v got=%+v", good, got)
	}
	phase.Store(2)
	exhausted := collectAccounts(context.Background(), true)
	w := rollbackWindow(t, exhausted.accounts[0], "codex:primary")
	if w.UsedPercent == nil || *w.UsedPercent != 100 || w.LimitReached == nil || !*w.LimitReached || isAccountRoutableHealthy(exhausted.accounts[0]) {
		t.Fatalf("new exhaustion hidden: %+v", exhausted.accounts[0])
	}
	phase.Store(3)
	clock = clock.Add(forceCooldown + time.Second)
	failed := collectAccounts(context.Background(), true)
	if failed.err != nil || failed.stale || !failed.accounts[0].Stale || failed.accounts[0].ErrorKind != "active" || !reflect.DeepEqual(failed.accounts[0].Windows, exhausted.accounts[0].Windows) {
		t.Fatalf("failure lost quota/local stale: %+v", failed)
	}
}

func rollbackWindow(t *testing.T, a quotamonitor.AccountQuota, key string) quotamonitor.QuotaWindow {
	t.Helper()
	for _, w := range a.Windows {
		if canonicalWindowKey(w) == key {
			return w
		}
	}
	t.Fatalf("missing window %s: %+v", key, a.Windows)
	return quotamonitor.QuotaWindow{}
}

func TestRefreshRollbackActivePartialRecovery(t *testing.T) {
	used, remaining, over := 100.0, 0.0, 10.0
	yes, no := true, false
	reset := time.Now().Add(-time.Hour)
	old := []quotamonitor.QuotaWindow{{Name: "primary", UsedPercent: &used, RemainingPercent: &remaining, LimitReached: &yes, Allowed: &no, OverLimitPercent: &over, ResetAt: &reset}, {Name: "secondary", UsedPercent: &used, LimitReached: &yes, Allowed: &no, ResetAt: &reset}}
	zero := 0.0
	for _, explicit := range []bool{false, true} {
		fresh := quotamonitor.QuotaWindow{Name: "primary", UsedPercent: &zero}
		if explicit {
			fresh.LimitReached, fresh.Allowed = &no, &yes
		}
		got := mergeWindows(old, []quotamonitor.QuotaWindow{fresh})
		if *got[0].LimitReached || !*got[0].Allowed || *got[0].RemainingPercent != 100 || *got[0].OverLimitPercent != 0 || got[0].ResetAt == nil || !reflect.DeepEqual(got[1], old[1]) {
			t.Fatalf("partial recovery explicit=%v: %+v", explicit, got)
		}
	}
	contradictory := mergeWindows(old, []quotamonitor.QuotaWindow{{Name: "primary", UsedPercent: &zero, LimitReached: &yes}})
	if !*contradictory[0].LimitReached {
		t.Fatal("explicit exhausted flag cleared")
	}
	deadlineOnly := mergeWindows(old, []quotamonitor.QuotaWindow{{Name: "primary", ResetAt: &reset}})
	if !*deadlineOnly[0].LimitReached || *deadlineOnly[0].Allowed {
		t.Fatal("deadline alone unlocked quota")
	}
}

func TestRefreshRollbackPreservesFreshAuthMetadata(t *testing.T) {
	observed := time.Now().UTC()
	yes, no := true, false
	used := 20.0
	previous := []quotamonitor.AccountQuota{{AccountKey: "a", Source: "active", LastSuccessAt: &observed, ObservedAt: &observed, FetchedAt: observed, Windows: []quotamonitor.QuotaWindow{{Name: "primary", UsedPercent: &used}}, Operational: &quotamonitor.OperationalStatus{Status: "active", Unavailable: &no}}}
	for _, status := range []string{"disabled", "error", "unavailable"} {
		fresh := quotamonitor.AccountQuota{AccountKey: "a", AuthIndex: "new", LastUsedAt: &observed, Operational: &quotamonitor.OperationalStatus{Status: status, Unavailable: &yes, RecentRequestCount: 7}}
		if status == "disabled" {
			fresh.Operational.Disabled = &yes
		}
		if status == "unavailable" {
			newer := observed.Add(time.Minute)
			fresh.Operational.LastRefresh = &newer
		}
		got := applyLastGoodWindows([]quotamonitor.AccountQuota{fresh}, previous)[0]
		if got.AuthIndex != "new" || got.LastUsedAt == nil || got.Operational.Status != status || !*got.Operational.Unavailable || got.Operational.RecentRequestCount != 7 {
			t.Fatalf("fresh auth state overwritten: %+v", got)
		}
	}
}

func TestRefreshRollbackObservationProvenance(t *testing.T) {
	observed := time.Now().UTC().Truncate(time.Second)
	oldObserved, fetched := observed.Add(-time.Minute), observed.Add(5*time.Minute)
	activeUsed, passiveUsed := 20.0, 100.0
	activeBalance, passiveBalance := 12.0, 0.0
	activeCount, passiveCount := 2, 0
	relative := int64(600)
	yes, no := true, false
	for _, source := range []string{"active", "both"} {
		previous := []quotamonitor.AccountQuota{{AccountKey: "a", Source: source, Status: quotamonitor.StatusStale, Stale: true, ErrorKind: "active", Error: "quota lookup failed", LastSuccessAt: &observed, ObservedAt: &observed, FetchedAt: observed, PlanType: "team", Windows: []quotamonitor.QuotaWindow{{Name: "primary", UsedPercent: &activeUsed, ResetAfterSec: &relative}}, APICredits: &quotamonitor.APICredits{Balance: &activeBalance, HasCredits: &yes}, ResetCredits: &quotamonitor.ResetCredits{AvailableCount: &activeCount}, Credits: map[string]string{"balance": "12"}}}
		fresh := quotamonitor.AccountQuota{AccountKey: "a", Source: "passive", ObservedAt: &oldObserved, FetchedAt: fetched, PlanType: "free", Windows: []quotamonitor.QuotaWindow{{Name: "primary", UsedPercent: &passiveUsed, ResetAfterSec: &relative}}, APICredits: &quotamonitor.APICredits{Balance: &passiveBalance, HasCredits: &no}, ResetCredits: &quotamonitor.ResetCredits{AvailableCount: &passiveCount}, Credits: map[string]string{"balance": "0"}}
		got := applyLastGoodWindows(cloneAccounts([]quotamonitor.AccountQuota{fresh}), previous)[0]
		if *got.Windows[0].UsedPercent != activeUsed || got.Windows[0].ResetAt == nil || !got.Windows[0].ResetAt.Equal(observed.Add(600*time.Second)) || *got.APICredits.Balance != activeBalance || !*got.APICredits.HasCredits || *got.ResetCredits.AvailableCount != activeCount || got.Credits["balance"] != "12" || got.PlanType != "team" || !got.Stale || got.ErrorKind != "active" || !got.ObservedAt.Equal(observed) || !got.FetchedAt.Equal(observed) {
			t.Fatalf("source=%s lost active provenance: %+v", source, got)
		}
		// Reapplying a baseline must not move deadlines or change retained fields.
		again := applyLastGoodWindows(cloneAccounts([]quotamonitor.AccountQuota{got}), previous)[0]
		if !reflect.DeepEqual(got, again) {
			t.Fatalf("source=%s baseline merge not idempotent", source)
		}
		previous[0].Stale, previous[0].ErrorKind, previous[0].Error = false, "", ""
		previous[0].Status = quotamonitor.StatusKnown
		fresh.ObservedAt = &fetched
		newer := applyLastGoodWindows(cloneAccounts([]quotamonitor.AccountQuota{fresh}), previous)[0]
		safe := sanitizeAccounts([]quotamonitor.AccountQuota{newer})[0]
		if safe.ObservedAt == nil || !safe.ObservedAt.Equal(fetched) || safe.LastSuccess == nil || !safe.LastSuccess.Equal(observed) {
			t.Fatal("DTO must distinguish newer passive observation from last active success")
		}
		if *newer.Windows[0].UsedPercent != passiveUsed || !newer.ObservedAt.Equal(fetched) || newer.PlanType != "free" || *newer.APICredits.Balance != passiveBalance {
			t.Fatalf("source=%s newer passive observation hidden: %+v", source, newer)
		}
	}
}
