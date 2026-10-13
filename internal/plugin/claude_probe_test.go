package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestClaudeWindowAliasesMerge(t *testing.T) {
	for _, names := range [][]string{{"five-hour", "five_hour", "5h", "5_hours"}, {"weekly", "seven-day", "seven_day", "7d"}, {"weekly-sonnet", "seven-day-sonnet"}} {
		want := canonicalWindowKey(quotamonitor.QuotaWindow{Name: names[0]})
		for _, name := range names[1:] {
			if got := canonicalWindowKey(quotamonitor.QuotaWindow{Name: name}); got != want {
				t.Errorf("alias %q key %q, want %q", name, got, want)
			}
		}
	}
}

func TestClaudeProbeEligibilityAndRecovery(t *testing.T) {
	used, remaining, blocked, reached := 100.0, 0.0, false, true
	fiveHours, week := 300, 10080
	account := quotamonitor.AccountQuota{
		AccountKey: "claude-key", AuthIndex: "claude-auth", Provider: "claude",
		Status: quotamonitor.StatusKnown,
		Windows: []quotamonitor.QuotaWindow{
			{Name: "five-hour", WindowMinutes: &fiveHours, UsedPercent: &used, RemainingPercent: &remaining, Allowed: &blocked, LimitReached: &reached},
			{Name: "seven-day", WindowMinutes: &week, UsedPercent: &used, RemainingPercent: &remaining, Allowed: &blocked, LimitReached: &reached},
		},
	}
	if !probeEligible(account) {
		t.Fatal("official Claude must be eligible for active collection")
	}
	calls := 0
	fail := false
	inventory := `"cedar_ember":{"eligible":true,"grants":[{"resets_left":2,"paused":false,"ends_at":"2099-01-01T00:00:00Z","id":"secret-handle"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		status, body := 200, `{"five_hour":{"utilization":10,"resets_at":"2026-10-09T01:00:00Z"},"seven_day":{"utilization":25,"resets_at":"2026-10-12T01:00:00Z"}}`
		if fail {
			status, body = 401, "sensitive-provider-body"
		} else {
			body = body[:len(body)-1] + "," + inventory + "}"
		}
		_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{StatusCode: status, Body: body})
	}))
	defer server.Close()
	client := quotamonitor.NewClient(server.URL, "management-key")
	got, attempts, truncated := probeAccounts(context.Background(), client, []quotamonitor.AccountQuota{account}, nil, nil, true)
	if truncated || calls != 1 || len(attempts) != 1 || len(got) != 1 || got[0].Status != quotamonitor.StatusKnown || got[0].Source != "active" || len(got[0].Windows) != 2 {
		t.Fatalf("Claude collection/recovery failed: %#v attempts=%#v calls=%d truncated=%v", got, attempts, calls, truncated)
	}
	if got[0].ResetCredits == nil || got[0].ResetCredits.AvailableCount == nil || *got[0].ResetCredits.AvailableCount != 2 || got[0].ResetCredits.NearestExpiry == nil {
		t.Fatalf("Claude reset inventory not collected: %#v", got[0].ResetCredits)
	}
	for _, window := range got[0].Windows {
		if window.Allowed == nil || !*window.Allowed || window.LimitReached == nil || *window.LimitReached || window.RemainingPercent == nil || *window.RemainingPercent <= 0 {
			t.Fatalf("passive exhausted window not recovered: %#v", window)
		}
	}
	// Backoff and disabled accounts remain respected even for the new provider.
	_, skipped, _ := probeAccounts(context.Background(), client, []quotamonitor.AccountQuota{account}, got, map[string]time.Time{"claude-key": time.Now().Add(time.Hour)}, false)
	if len(skipped) != 0 || calls != 1 {
		t.Fatal("Claude backoff ignored")
	}
	disabled := true
	account.Operational = &quotamonitor.OperationalStatus{Disabled: &disabled}
	if probeEligible(account) {
		t.Fatal("disabled Claude eligible")
	}
	// A complete zero inventory clears both an old positive count and expiry.
	account.Operational = nil
	inventory = `"cedar_ember":{"eligible":true,"grants":[]}`
	cleared, _, _ := probeAccounts(context.Background(), client, []quotamonitor.AccountQuota{account}, got, nil, true)
	if len(cleared) != 1 || cleared[0].ResetCredits == nil || cleared[0].ResetCredits.AvailableCount == nil || *cleared[0].ResetCredits.AvailableCount != 0 || cleared[0].ResetCredits.NearestExpiry != nil {
		t.Fatalf("Claude zero inventory retained stale rights: %#v", cleared)
	}
	// A provider failure keeps last-good quotas account-locally and sanitized.
	account.Operational = nil
	fail = true
	stale, _, _ := probeAccounts(context.Background(), client, []quotamonitor.AccountQuota{account}, got, nil, true)
	if len(stale) != 1 || stale[0].Status != quotamonitor.StatusStale || !stale[0].Stale || stale[0].ErrorKind != "auth" || stale[0].Error != "quota lookup failed" || stale[0].ResetCredits == nil || *stale[0].ResetCredits.AvailableCount != 2 || len(stale[0].Windows) != 2 || *stale[0].Windows[0].RemainingPercent != 90 {
		t.Fatalf("last-good Claude quota lost on failure: %#v", stale)
	}
}
