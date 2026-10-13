package plugin

import (
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestCurrentSelectionRequiresAuditedTokenUse(t *testing.T) {
	reset(t)
	recent := now().Add(-time.Minute)
	older := now().Add(-2 * time.Minute)
	accounts := []quotamonitor.AccountQuota{
		{AccountKey: "request-only", Provider: "codex", Priority: 100, Operational: &quotamonitor.OperationalStatus{RecentRequestLatest: &recent}},
		{AccountKey: "token-user", Provider: "codex", LastUsedAt: &older},
	}
	got := currentAccountSelection(accounts)["codex"]
	if got.accountKey != "token-user" || got.kind != "recent" {
		t.Fatalf("operational request counter must not override audit: %+v", got)
	}
	blocked := true
	accounts[1].Operational = &quotamonitor.OperationalStatus{Unavailable: &blocked}
	got = currentAccountSelection(accounts)["codex"]
	if _, ok := currentAccountSelection(accounts)["codex"]; ok {
		t.Fatalf("ineligible token user must not cause fallback: %+v", got)
	}
	accounts[1].Operational = nil
	accounts[1].LastUsedAt = nil
	if selection := currentAccountSelection(accounts); len(selection) != 0 {
		t.Fatalf("without audited token use expected no selection, got %+v", selection)
	}
}

func TestLegacyCandidateSelectionCannotMarkCurrent(t *testing.T) {
	reset(t)
	accounts := []quotamonitor.AccountQuota{{AccountKey: "healthy", Provider: "codex", Account: "user@example.com"}}
	for _, reason := range []string{"priority_candidate", "fallback_healthy"} {
		legacy := map[string]currentChoice{"codex": {accountKey: "healthy", kind: "candidate", reason: reason}}
		if keys := currentSelectionKeys(legacy); len(keys) != 0 {
			t.Errorf("legacy candidate must not drive current probe tier: %v", keys)
		}
		safe := sanitizeAccountsWithSelection(accounts, legacy)
		if safe[0].CurrentAccount || safe[0].CurrentKind != "" || safe[0].CurrentReason != "" || safe[0].SelectedBy != "" {
			t.Errorf("legacy candidate must not be published as current: %+v", safe[0])
		}
	}
}
