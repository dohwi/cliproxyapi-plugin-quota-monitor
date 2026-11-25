package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	usageCalls := 0
	profileCalls := 0
	fail := false
	inventory := `"cedar_ember":{"eligible":true,"grants":[{"resets_left":2,"paused":false,"ends_at":"2099-01-01T00:00:00Z","id":"secret-handle"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.URL, "/api/oauth/profile") {
			profileCalls++
			_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{
				StatusCode: 200,
				Body:       `{"organization":{"organization_type":"claude_max","rate_limit_tier":"default_claude_max_20x"}}`,
			})
			return
		}
		usageCalls++
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
	if truncated || usageCalls != 1 || profileCalls != 1 || len(attempts) != 1 || len(got) != 1 || got[0].Status != quotamonitor.StatusKnown || got[0].Source != "active" || len(got[0].Windows) != 2 || got[0].PlanType != "max_20x" {
		t.Fatalf("Claude collection/recovery failed: %#v attempts=%#v usageCalls=%d profileCalls=%d truncated=%v", got, attempts, usageCalls, profileCalls, truncated)
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
	if len(skipped) != 0 || calls != 2 {
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

func TestClaudeProbeExplicitlyAbsentWindow(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		count            int
	}{
		{"weekly only", `{"five_hour":null,"seven_day":{"utilization":25}}`, "weekly", 1},
		{"five hours only", `{"five_hour":{"utilization":10},"seven_day":null}`, "five-hour", 1},
		{"neither window", `{"five_hour":null,"seven_day":null}`, "", 0},
		{"partial response", `{"seven_day":{"utilization":25}}`, "weekly", 2},
		{"unknown utilization", `{"five_hour":{"utilization":null},"seven_day":{"utilization":25}}`, "weekly", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, status := tc.body, http.StatusOK
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{StatusCode: status, Body: body})
			}))
			defer server.Close()
			old := time.Now().Add(-time.Minute)
			used := 100.0
			account := quotamonitor.AccountQuota{Provider: "claude", AccountKey: "c", AuthIndex: "1", ObservedAt: &old, Windows: []quotamonitor.QuotaWindow{
				{Name: "5h", UsedPercent: &used}, {Name: "7d", UsedPercent: &used},
			}}
			client := quotamonitor.NewClient(server.URL, "fixture-key")
			got := probeActive(client, []quotamonitor.AccountQuota{account}, nil)
			if len(got[0].Windows) != tc.count || (tc.count > 0 && got[0].Windows[0].Name != tc.want) || got[0].LastSuccessAt == nil {
				t.Fatalf("expected %d window(s) starting with %s, got %+v", tc.count, tc.want, got[0].Windows)
			}
			for _, at := range []time.Time{old, got[0].LastSuccessAt.Add(time.Second)} {
				account.ObservedAt = &at
				merged := applyLastGoodWindows([]quotamonitor.AccountQuota{account}, got)[0]
				if len(merged.Windows) != tc.count {
					t.Fatalf("older/partial passive signal resurrected an absent window: %+v", merged.Windows)
				}
			}
			status = http.StatusBadGateway
			stale := probeActive(client, []quotamonitor.AccountQuota{account}, got)[0]
			if !stale.Stale || len(stale.Windows) != tc.count || len(stale.ClaudeAbsentWindows) != len(got[0].ClaudeAbsentWindows) {
				t.Fatalf("failed probe changed known window presence: %+v", stale)
			}
			status, body = http.StatusOK, `{"five_hour":{"utilization":15},"seven_day":{"utilization":25}}`
			recovered := probeActive(client, []quotamonitor.AccountQuota{account}, got)[0]
			if len(recovered.Windows) != 2 || len(recovered.ClaudeAbsentWindows) != 0 || recovered.Stale {
				t.Fatalf("a later measured window must restore the row: %+v", recovered)
			}
		})
	}
}

// A unified header window and its OAuth equivalent use the same merge key.
func TestClaudeUnifiedWindowKeysMerge(t *testing.T) {
	for _, names := range [][]string{
		{"weekly-sonnet", "7d_sonnet", "seven_day_sonnet"},
		{"weekly-opus", "7d_opus", "seven_day_opus"},
		{"weekly", "7d", "seven_day"},
		{"five-hour", "5h", "five_hour"},
	} {
		minutes := 10080
		if names[0] == "five-hour" {
			minutes = 300
		}
		want := claudeMergeKey(quotamonitor.QuotaWindow{Name: names[0], WindowMinutes: &minutes})
		for _, name := range names[1:] {
			if got := claudeMergeKey(quotamonitor.QuotaWindow{Name: name}); got != want {
				t.Errorf("alias %q key %q, want %q", name, got, want)
			}
		}
	}
}
