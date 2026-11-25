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

func TestComputeCodexWeeklyOnlyAvailability(t *testing.T) {
	for _, tc := range []struct {
		name    string
		windows []quotamonitor.QuotaWindow
		want    float64
	}{
		{"weekly in primary with positional duplicate", []quotamonitor.QuotaWindow{availWin("primary", 10080, 65), {Name: "secondary", RemainingPercent: new(float64)}}, 65},
		{"weekly in secondary", []quotamonitor.QuotaWindow{availWin("secondary", 10080, 70)}, 70},
		{"weekly exhausted", []quotamonitor.QuotaWindow{availWin("primary", 10080, 0)}, 0},
		{"two distinct windows", []quotamonitor.QuotaWindow{availWin("primary", 300, 50), availWin("secondary", 10080, 70)}, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := quotamonitor.AccountQuota{Provider: "codex", Status: quotamonitor.StatusKnown, Windows: tc.windows}
			if got := availEntry(t, Compute([]quotamonitor.AccountQuota{a}), "codex"); got.RemainingPercent != tc.want || got.Accounts != 1 {
				t.Fatalf("codex = %+v, want %v%% over 1 account", got, tc.want)
			}
		})
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

func TestWeeklyOnlyAvailabilityOutsideCodex(t *testing.T) {
	for _, tc := range []struct {
		provider, key, short, weekly string
	}{
		{"claude", "claude", "five-hour", "weekly"},
		{"antigravity", "antigravity_gemini", "Gemini Models / 5h", "Gemini Models / weekly"},
		{"antigravity", "antigravity_claude", "Claude and GPT models / 5h", "Claude and GPT models / weekly"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			for _, remaining := range []float64{42, 0} {
				accounts := []quotamonitor.AccountQuota{{Provider: tc.provider, Windows: []quotamonitor.QuotaWindow{
					{Name: tc.short, WindowMinutes: availWin(tc.short, 300, 0).WindowMinutes},
					availWin(tc.weekly, 10080, remaining),
				}}}
				got := availEntry(t, Compute(accounts), tc.key)
				if got.RemainingPercent != remaining || got.Accounts != 1 {
					t.Fatalf("want measured weekly quota %v%%, got %+v", remaining, got)
				}
				accounts[0].Windows = accounts[0].Windows[:1]
				if got := Compute(accounts); len(got) != 0 {
					t.Fatalf("metadata-only 5h window invented availability: %+v", got)
				}
			}
		})
	}
}

func TestComputeEmptyWhenNothingMeasurable(t *testing.T) {
	if got := Compute(nil); len(got) != 0 {
		t.Fatalf("empty fleet produced %+v", got)
	}
}

// A model-scoped weekly window (weekly-sonnet) shares the weekly duration but
// meters one model only, so it must not stand in for the account-wide weekly
// window when availability is computed.
func TestModelScopedWeeklyDoesNotReplaceAccountWindow(t *testing.T) {
	account := quotamonitor.AccountQuota{
		Provider: "claude", Status: quotamonitor.StatusKnown,
		Windows: []quotamonitor.QuotaWindow{
			availWin("five-hour", 300, 80),
			availWin("weekly-sonnet", 10080, 0),
		},
	}
	entries := Compute([]quotamonitor.AccountQuota{account})
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one Claude entry", entries)
	}
	if entries[0].RemainingPercent != 80 {
		t.Errorf("remaining = %v, want the 5h window's 80 (model-scoped weekly is not the account window)", entries[0].RemainingPercent)
	}
}

func TestComputeSplitsAntigravityByPlanAndFallbackTier(t *testing.T) {
	accounts := []quotamonitor.AccountQuota{
		{
			Provider: "antigravity", PlanType: "ultra", ModelTier: quotamonitor.AntigravityTier4, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("Gemini Models / Five Hour Limit Remaining", 300, 99),
				availWin("Gemini Models / Weekly Limit Remaining", 10080, 100),
				availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 100),
				availWin("Claude and GPT models / Weekly Limit Remaining", 10080, 100),
			},
		},
		{
			Provider: "antigravity", PlanType: "free", ModelTier: quotamonitor.AntigravityTier4, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("Gemini Models / Five Hour Limit Remaining", 300, 96),
				availWin("Gemini Models / Weekly Limit Remaining", 10080, 94),
				availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 100),
				availWin("Claude and GPT models / Weekly Limit Remaining", 10080, 0),
			},
		},
		{
			// Legacy account without PlanType falls back to ModelTier 3.8 -> Free bucket.
			Provider: "antigravity", ModelTier: quotamonitor.AntigravityTier38, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("Gemini Models / Five Hour Limit Remaining", 300, 100),
				availWin("Gemini Models / Weekly Limit Remaining", 10080, 96),
				availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 100),
				availWin("Claude and GPT models / Weekly Limit Remaining", 10080, 0),
			},
		},
	}

	entries := Compute(accounts)
	if len(entries) != 4 {
		t.Fatalf("entries = %#v, want 4 split AGY entries (paid + free)", entries)
	}
	if e := availEntry(t, entries, "antigravity_gemini"); e.RemainingPercent != 99 || e.Accounts != 1 || e.Label != "AGY · Gemini" || e.Short != "AG" {
		t.Fatalf("antigravity_gemini = %+v", e)
	}
	if e := availEntry(t, entries, "antigravity_claude"); e.RemainingPercent != 100 || e.Accounts != 1 || e.Label != "AGY · Claude/GPT" || e.Short != "AG" {
		t.Fatalf("antigravity_claude = %+v", e)
	}
	if e := availEntry(t, entries, "antigravity_free_gemini"); e.Accounts != 2 || e.Label != "AGY (Free) · Gemini" || e.Short != "AGF" {
		t.Fatalf("antigravity_free_gemini = %+v", e)
	}
	if e := availEntry(t, entries, "antigravity_free_claude"); e.RemainingPercent != 0 || e.Accounts != 2 || e.Label != "AGY (Free) · Claude/GPT" || e.Short != "AGF" {
		t.Fatalf("antigravity_free_claude = %+v", e)
	}
}

func TestComputeWeightsFleetAvailabilityByTierCapacity(t *testing.T) {
	accounts := []quotamonitor.AccountQuota{
		// Claude: Max 20x (weight 20) at 0% + Pro (weight 1) at 100% -> 100/21 = 4.8%
		{
			Provider: "claude", PlanType: "max_20x", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("five-hour", 300, 0),
				availWin("weekly", 10080, 0),
			},
		},
		{
			Provider: "claude", PlanType: "pro", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("five-hour", 300, 100),
				availWin("weekly", 10080, 100),
			},
		},
		// Codex: Pro (weight 20) at 0% + Pro 5x (weight 5) at 100% + Plus (weight 1) at 100% -> 600/26 = 23.1%
		{
			Provider: "codex", PlanType: "pro", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 0)},
		},
		{
			Provider: "codex", PlanType: "pro_lite", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 100)},
		},
		{
			Provider: "codex", PlanType: "plus", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 100)},
		},
		// Antigravity paid: Ultra (weight 5) at 0% + Pro (weight 1) at 100% -> 100/6 = 16.7%
		{
			Provider: "antigravity", PlanType: "ultra", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 0)},
		},
		{
			Provider: "antigravity", PlanType: "pro", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 100)},
		},
	}

	entries := Compute(accounts)

	cl := availEntry(t, entries, "claude")
	if cl.RemainingPercent != 4.8 || cl.Accounts != 2 || len(cl.Tiers) != 2 {
		t.Fatalf("claude weighted = %+v, want 4.8%% with 2 tiers", cl)
	}
	if cl.Tiers[0].Tier != "max_20x" || cl.Tiers[0].RemainingPercent != 0 || cl.Tiers[1].Tier != "pro" || cl.Tiers[1].RemainingPercent != 100 {
		t.Fatalf("claude tiers = %+v", cl.Tiers)
	}

	cx := availEntry(t, entries, "codex")
	if cx.RemainingPercent != 23.1 || cx.Accounts != 3 || len(cx.Tiers) != 3 {
		t.Fatalf("codex weighted = %+v, want 23.1%% with 3 tiers", cx)
	}
	if cx.Tiers[0].Label != "Pro" || cx.Tiers[1].Label != "Pro 5x" || cx.Tiers[2].Label != "Plus" {
		t.Fatalf("codex tier labels = %+v", cx.Tiers)
	}

	ag := availEntry(t, entries, "antigravity_gemini")
	if ag.RemainingPercent != 16.7 || ag.Accounts != 2 || len(ag.Tiers) != 2 {
		t.Fatalf("antigravity weighted = %+v, want 16.7%% with 2 tiers", ag)
	}
}
