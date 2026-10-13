package plugin

import (
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestRecentBorderSelectionAcrossProviders(t *testing.T) {
	reset(t)
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	previousNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = previousNow })
	recent, older, expired := clock.Add(-time.Minute), clock.Add(-2*time.Minute), clock.Add(-currentAccountWindow-time.Second)
	healthy, empty := 80.0, 0.0
	blocked := true

	for _, provider := range []string{"codex", "antigravity", "claude"} {
		t.Run(provider, func(t *testing.T) {
			windowName := "primary"
			if provider == "antigravity" {
				windowName = "Gemini five hour"
			}
			base := quotamonitor.AccountQuota{AccountKey: "latest", Provider: provider, Status: quotamonitor.StatusKnown, LastUsedAt: &recent,
				Windows: []quotamonitor.QuotaWindow{{Name: windowName, RemainingPercent: &healthy}}}
			cases := []struct {
				name   string
				mutate func(*quotamonitor.AccountQuota)
				want   string
			}{
				{"latest", func(a *quotamonitor.AccountQuota) {}, "latest"},
				{"expired", func(a *quotamonitor.AccountQuota) { a.LastUsedAt = &expired }, "older"},
				{"request-only", func(a *quotamonitor.AccountQuota) {
					a.LastUsedAt = nil
					a.Operational = &quotamonitor.OperationalStatus{RecentRequestLatest: &recent, RecentRequestCount: 10}
				}, "older"},
				{"disabled", func(a *quotamonitor.AccountQuota) {
					a.Operational = &quotamonitor.OperationalStatus{Disabled: &blocked}
				}, "older"},
				{"unavailable", func(a *quotamonitor.AccountQuota) {
					a.Operational = &quotamonitor.OperationalStatus{Unavailable: &blocked}
				}, "older"},
				{"error", func(a *quotamonitor.AccountQuota) { a.Status = quotamonitor.StatusError }, "older"},
				{"auth-error", func(a *quotamonitor.AccountQuota) { a.ErrorKind = "auth" }, "older"},
				{"exhausted", func(a *quotamonitor.AccountQuota) { a.Windows[0].RemainingPercent = &empty }, "older"},
				{"limit-reached", func(a *quotamonitor.AccountQuota) { a.Windows[0].LimitReached = &blocked }, "older"},
				{"tie-break", func(a *quotamonitor.AccountQuota) { a.LastUsedAt = &older }, "latest"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					a := cloneAccounts([]quotamonitor.AccountQuota{base})[0]
					tc.mutate(&a)
					accounts := []quotamonitor.AccountQuota{a, {AccountKey: "older", Provider: provider, LastUsedAt: &older}}
					if got := currentAccountKeys(accounts)[provider]; got != tc.want {
						t.Fatalf("got %q, want %q", got, tc.want)
					}
					safe := sanitizeAccounts(accounts)
					count := 0
					for _, account := range safe {
						if account.CurrentAccount {
							count++
							if account.AccountKey != tc.want || account.CurrentKind != "recent" || account.CurrentReason != "recent_usage" {
								t.Fatalf("unexpected border metadata: %+v", account)
							}
						}
					}
					if count != 1 {
						t.Fatalf("expected exactly one border, got %d", count)
					}
				})
			}
		})
	}
}

func TestResourceRecentBorderExpiresWithoutCollection(t *testing.T) {
	reset(t)
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	previousNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = previousNow })
	used := clock.Add(-currentAccountWindow)
	accounts := []quotamonitor.AccountQuota{{AccountKey: "recent", Provider: "codex", LastUsedAt: &used}}
	state.mu.Lock()
	state.last = cloneAccounts(accounts)
	state.current = currentAccountSelection(accounts)
	state.cachedAt = clock
	state.mu.Unlock()
	if !resourceSnapshot()["accounts"].([]safeAccount)[0].CurrentAccount {
		t.Fatal("inclusive 15-minute boundary should remain recent")
	}
	clock = clock.Add(time.Second)
	if resourceSnapshot()["accounts"].([]safeAccount)[0].CurrentAccount {
		t.Fatal("cached selection must not keep blue border beyond audited 15-minute window")
	}
}

func TestFrozenRecentBorderClearsWhenAccountBecomesBlocked(t *testing.T) {
	reset(t)
	recent := now().Add(-time.Minute)
	blocked := true
	empty := 0.0
	for _, provider := range []string{"codex", "antigravity", "claude"} {
		t.Run(provider, func(t *testing.T) {
			windowName := "primary"
			if provider == "antigravity" {
				windowName = "Gemini five hour"
			}
			accounts := []quotamonitor.AccountQuota{
				{AccountKey: "recent", Provider: provider, LastUsedAt: &recent},
				{AccountKey: "alternate", Provider: provider, LastUsedAt: &recent},
			}
			selection := map[string]currentChoice{provider: {accountKey: "recent", kind: "recent", reason: "recent_usage"}}
			for _, mutation := range []struct {
				name  string
				apply func(*quotamonitor.AccountQuota)
			}{
				{"disabled", func(a *quotamonitor.AccountQuota) {
					a.Operational = &quotamonitor.OperationalStatus{Disabled: &blocked}
				}},
				{"unavailable", func(a *quotamonitor.AccountQuota) {
					a.Operational = &quotamonitor.OperationalStatus{Unavailable: &blocked}
				}},
				{"exhausted", func(a *quotamonitor.AccountQuota) {
					a.Windows = []quotamonitor.QuotaWindow{{Name: windowName, RemainingPercent: &empty}}
				}},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					updated := cloneAccounts(accounts)
					mutation.apply(&updated[0])
					for _, account := range sanitizeAccountsWithSelection(updated, selection) {
						if account.CurrentAccount || account.CurrentKind != "" || account.CurrentReason != "" || account.SelectedBy != "" {
							t.Fatalf("invalid frozen selection must clear without moving to alternate: %+v", account)
						}
					}
				})
			}
		})
	}
}
