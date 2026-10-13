package plugin

import (
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestClaudeRecentUsageIgnoresBillingOverage(t *testing.T) {
	reset(t)
	recent := now().Add(-time.Minute)
	older := now().Add(-2 * time.Minute)
	healthy, zero := 80.0, 0.0
	allowed, blocked := true, false
	week := 10080

	for _, name := range []string{"overage", "overage-limit", "overage_limit", "Overage Limit"} {
		t.Run(name, func(t *testing.T) {
			accounts := []quotamonitor.AccountQuota{
				{
					AccountKey: "claude-latest", Provider: " Claude ", Status: quotamonitor.StatusKnown,
					LastUsedAt:  &recent,
					Operational: &quotamonitor.OperationalStatus{Status: "active"},
					Windows: []quotamonitor.QuotaWindow{
						{Name: "five-hour", RemainingPercent: &healthy, Allowed: &allowed},
						{Name: "weekly", WindowMinutes: &week, RemainingPercent: &healthy, Allowed: &allowed},
						{Name: name, RemainingPercent: &zero, Allowed: &blocked},
					},
				},
				{AccountKey: "claude-older", Provider: "claude", Status: quotamonitor.StatusKnown, LastUsedAt: &older},
			}
			if got := currentAccountKeys(accounts)["claude"]; got != "claude-latest" {
				t.Fatalf("billing overage must not suppress latest audited Claude use: got %q", got)
			}
			safe := sanitizeAccounts(accounts)
			if !safe[0].CurrentAccount || safe[0].CurrentKind != "recent" || safe[0].CurrentReason != "recent_usage" || safe[0].SelectedBy != "recent_usage" || safe[1].CurrentAccount {
				t.Fatalf("expected only latest Claude account to carry border metadata: %+v", safe)
			}

			// Actual subscription exhaustion still disqualifies the account.
			accounts[0].Windows[1].RemainingPercent = &zero
			if got := currentAccountKeys(accounts)["claude"]; got != "claude-older" {
				t.Fatalf("exhausted subscription must remain ineligible: got %q", got)
			}

			// No other provider gets the Claude-specific billing exception.
			accounts[0].Provider = "codex"
			accounts[0].Windows[1].RemainingPercent = &healthy
			if got := currentAccountKeys(accounts)["codex"]; got != "" {
				t.Fatalf("Codex exhausted windows must remain ineligible: got %q", got)
			}
		})
	}
}

func TestClaudeBillingOverageDoesNotProveRecentUsage(t *testing.T) {
	reset(t)
	zero := 0.0
	account := quotamonitor.AccountQuota{
		AccountKey: "claude-unused", Provider: "claude", Status: quotamonitor.StatusKnown,
		Operational: &quotamonitor.OperationalStatus{Status: "active", RecentRequestCount: 10},
		Windows:     []quotamonitor.QuotaWindow{{Name: "overage", RemainingPercent: &zero}},
	}
	if got := currentAccountKeys([]quotamonitor.AccountQuota{account}); len(got) != 0 {
		t.Fatalf("ignoring billing overage cannot invent audited usage: %v", got)
	}
}
