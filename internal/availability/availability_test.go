package availability

import (
	"testing"

	quotamonitor "quota-monitor/internal/quota"
)

func availWin(name string, minutes int, remaining float64) quotamonitor.QuotaWindow {
	return quotamonitor.QuotaWindow{Name: name, WindowMinutes: &minutes, RemainingPercent: &remaining}
}

func availEntry(t *testing.T, entries []Entry, key string) Entry {
	t.Helper()
	for _, e := range entries {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("availability entry %q missing in %+v", key, entries)
	return Entry{}
}

func TestComputeAveragesAccountsAndHonorsWeekly(t *testing.T) {
	yes := true
	accounts := []quotamonitor.AccountQuota{
		{Provider: "claude", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 80), availWin("weekly", 10080, 50)}},
		// Weekly depleted: counts as 0% even though 5h is full.
		{Provider: "claude", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 100), availWin("weekly", 10080, 0)}},
		// Disabled accounts are excluded from the denominator.
		{Provider: "claude", Status: quotamonitor.StatusKnown, Operational: &quotamonitor.OperationalStatus{Disabled: &yes}, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 10)}},
		// Overage-only account has no 5h window and is ignored.
		{Provider: "claude", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("overage", 0, 100)}},
		{Provider: "codex", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 60), availWin("secondary", 10080, 90)}},
	}
	entries := Compute(accounts)
	claude := availEntry(t, entries, "claude")
	if claude.RemainingPercent != 40 || claude.Accounts != 2 {
		t.Fatalf("claude = %+v, want 40%% over 2 accounts", claude)
	}
	if gpt := availEntry(t, entries, "codex"); gpt.RemainingPercent != 60 || gpt.Accounts != 1 {
		t.Fatalf("codex = %+v, want 60%% over 1 account", gpt)
	}
}

func TestComputeKeepsExhaustedUnavailableAccountAtZero(t *testing.T) {
	yes := true
	accounts := []quotamonitor.AccountQuota{
		{Provider: "claude", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 100)}},
		{Provider: "claude", Status: quotamonitor.StatusError, Operational: &quotamonitor.OperationalStatus{Unavailable: &yes}, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 0)}},
		// Unavailable without a known zero is unknown, so it is dropped rather than guessed.
		{Provider: "claude", Status: quotamonitor.StatusError, Operational: &quotamonitor.OperationalStatus{Unavailable: &yes}, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 70)}},
	}
	if got := availEntry(t, Compute(accounts), "claude"); got.RemainingPercent != 50 || got.Accounts != 2 {
		t.Fatalf("claude = %+v, want 50%% over 2 accounts", got)
	}
}

func TestComputeSplitsAntigravityFamilies(t *testing.T) {
	accounts := []quotamonitor.AccountQuota{
		{Provider: "antigravity", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{
			availWin("Gemini Models / 5h", 300, 90), availWin("Gemini Models / weekly", 10080, 40),
			availWin("Claude and GPT models / 5h", 300, 20), availWin("Claude and GPT models / weekly", 10080, 0),
		}},
		{Provider: "antigravity", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{
			availWin("Gemini Models / 5h", 300, 50), availWin("Gemini Models / weekly", 10080, 60),
		}},
	}
	entries := Compute(accounts)
	if g := availEntry(t, entries, "antigravity_gemini"); g.RemainingPercent != 70 || g.Accounts != 2 {
		t.Fatalf("gemini family = %+v, want 70%% over 2 accounts", g)
	}
	// Weekly depleted blocks the Claude/GPT family even though 5h has 20% left.
	if c := availEntry(t, entries, "antigravity_claude"); c.RemainingPercent != 0 || c.Accounts != 1 {
		t.Fatalf("claude/gpt family = %+v, want 0%% over 1 account", c)
	}
}

func TestComputeEmptyWhenNothingMeasurable(t *testing.T) {
	if got := Compute(nil); len(got) != 0 {
		t.Fatalf("empty fleet produced %+v", got)
	}
}
