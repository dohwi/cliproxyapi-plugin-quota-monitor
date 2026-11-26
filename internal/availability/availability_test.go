package availability

import (
	"encoding/json"
	"strings"
	"testing"

	quotamonitor "quota-monitor/internal/quota"
)

func availWin(name string, minutes int, remaining float64) quotamonitor.QuotaWindow {
	return quotamonitor.QuotaWindow{Name: name, WindowMinutes: &minutes, RemainingPercent: &remaining}
}

func assertOptFloat(t *testing.T, label string, got *float64, want *float64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("%s = %v, want nil", label, *got)
		}
		return
	}
	if got == nil || *got != *want {
		t.Fatalf("%s = %v, want %v", label, got, *want)
	}
}

func fptr(v float64) *float64 { return &v }

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
		// Weekly depleted: counts as 0% across effective, short, and weekly even though 5h is full.
		{Provider: "claude", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 100), availWin("weekly", 10080, 0)}},
		// Disabled accounts are excluded from the denominator.
		{Provider: "claude", Status: quotamonitor.StatusKnown, Operational: &quotamonitor.OperationalStatus{Disabled: &yes}, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 10)}},
		// Overage-only account has no 5h window and is ignored.
		{Provider: "claude", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("overage", 0, 100)}},
		{Provider: "codex", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 60), availWin("secondary", 10080, 90)}},
	}
	entries := Compute(accounts)
	claude := availEntry(t, entries, "claude")
	// Account 1: effective=min(80,50)=50, short=80, weekly=50; Account 2: effective=0, short=0, weekly=0
	if claude.RemainingPercent != 25 || claude.Accounts != 2 {
		t.Fatalf("claude = %+v, want effective 25%% over 2 accounts", claude)
	}
	assertOptFloat(t, "claude.short", claude.ShortRemainingPercent, fptr(40))
	assertOptFloat(t, "claude.weekly", claude.WeeklyRemainingPercent, fptr(25))

	gpt := availEntry(t, entries, "codex")
	if gpt.RemainingPercent != 60 || gpt.Accounts != 1 {
		t.Fatalf("codex = %+v, want 60%% over 1 account", gpt)
	}
	assertOptFloat(t, "codex.short", gpt.ShortRemainingPercent, fptr(60))
	assertOptFloat(t, "codex.weekly", gpt.WeeklyRemainingPercent, fptr(90))
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
	g := availEntry(t, entries, "antigravity_gemini")
	// Account 1: (90, 40); Account 2: (50, 60) -> short 70%, weekly 50%, effective min(70, 50) = 50%.
	if g.RemainingPercent != 50 || g.Accounts != 2 {
		t.Fatalf("gemini family = %+v, want effective 50%% over 2 accounts", g)
	}
	assertOptFloat(t, "gemini.short", g.ShortRemainingPercent, fptr(70))
	assertOptFloat(t, "gemini.weekly", g.WeeklyRemainingPercent, fptr(50))

	// Weekly depleted blocks the Claude/GPT family even though 5h has 20% left.
	c := availEntry(t, entries, "antigravity_claude")
	if c.RemainingPercent != 0 || c.Accounts != 1 {
		t.Fatalf("claude/gpt family = %+v, want 0%% over 1 account", c)
	}
	assertOptFloat(t, "claude.short", c.ShortRemainingPercent, fptr(0))
	assertOptFloat(t, "claude.weekly", c.WeeklyRemainingPercent, fptr(0))
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

func TestComputeUnifiesAntigravityWithTierWeights(t *testing.T) {
	accounts := []quotamonitor.AccountQuota{
		{
			Provider: "antigravity", PlanType: "ultra", ModelTier: quotamonitor.AntigravityTier4, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("Gemini Models / Five Hour Limit Remaining", 300, 100),
				availWin("Gemini Models / Weekly Limit Remaining", 10080, 100),
				availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 100),
				availWin("Claude and GPT models / Weekly Limit Remaining", 10080, 100),
			},
		},
		{
			Provider: "antigravity", PlanType: "pro", ModelTier: quotamonitor.AntigravityTier4, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("Gemini Models / Five Hour Limit Remaining", 300, 40),
				availWin("Gemini Models / Weekly Limit Remaining", 10080, 40),
				availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 0),
				availWin("Claude and GPT models / Weekly Limit Remaining", 10080, 0),
			},
		},
	}

	entries := Compute(accounts)
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want 2 unified AGY entries (gemini + claude)", entries)
	}
	// Ultra (weight 5) at 100% + Pro (weight 1) at 40% -> 540/6 = 90%
	if e := availEntry(t, entries, "antigravity_gemini"); e.RemainingPercent != 90 || e.Accounts != 2 || e.Label != "AGY · Gemini" || e.Short != "AG" || len(e.Tiers) != 2 {
		t.Fatalf("antigravity_gemini = %+v", e)
	}
	// Ultra (weight 5) at 100% + Pro (weight 1) at 0% -> 500/6 = 83.3%
	if e := availEntry(t, entries, "antigravity_claude"); e.RemainingPercent != 83.3 || e.Accounts != 2 || e.Label != "AGY · Claude/GPT" || e.Short != "AG" || len(e.Tiers) != 2 {
		t.Fatalf("antigravity_claude = %+v", e)
	}
}

func TestComputeAutoSplitsAntigravityWhenModelPoolsDiffer(t *testing.T) {
	accounts := []quotamonitor.AccountQuota{
		{
			Provider: "antigravity", PlanType: "ultra", ModelTier: quotamonitor.AntigravityTierFull, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("Gemini Models / Five Hour Limit Remaining", 300, 99),
				availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 100),
			},
		},
		{
			Provider: "antigravity", PlanType: "pro", ModelTier: quotamonitor.AntigravityTierLite, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("Gemini Models / Five Hour Limit Remaining", 300, 80),
				availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 20),
			},
		},
		{
			Provider: "antigravity", PlanType: "pro", ModelTier: quotamonitor.AntigravityTierLite, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				availWin("Gemini Models / Five Hour Limit Remaining", 300, 60),
				availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 40),
			},
		},
	}

	entries := Compute(accounts)
	if len(entries) != 4 {
		t.Fatalf("entries = %#v, want 4 split AGY entries (full + lite)", entries)
	}
	if e := availEntry(t, entries, "antigravity_gemini"); e.RemainingPercent != 99 || e.Accounts != 1 || e.Label != "AGY · Gemini" {
		t.Fatalf("antigravity_gemini = %+v", e)
	}
	if e := availEntry(t, entries, "antigravity_claude"); e.RemainingPercent != 100 || e.Accounts != 1 || e.Label != "AGY · Claude/GPT" {
		t.Fatalf("antigravity_claude = %+v", e)
	}
	if e := availEntry(t, entries, "antigravity_lite_gemini"); e.RemainingPercent != 70 || e.Accounts != 2 || e.Label != "AGY (AI Pro Trial) · Gemini" || e.Short != "AGT" || len(e.Tiers) != 1 || e.Tiers[0].Label != "AI Pro Trial" {
		t.Fatalf("antigravity_lite_gemini = %+v", e)
	}
	if e := availEntry(t, entries, "antigravity_lite_claude"); e.RemainingPercent != 30 || e.Accounts != 2 || e.Label != "AGY (AI Pro Trial) · Claude/GPT" || e.Short != "AGT" || len(e.Tiers) != 1 || e.Tiers[0].Label != "AI Pro Trial" {
		t.Fatalf("antigravity_lite_claude = %+v", e)
	}

	// Unsplit lite-only pro accounts must also label their tier as AI Pro Trial (matching account cards).
	liteOnly := Compute(accounts[1:])
	if len(liteOnly) != 2 {
		t.Fatalf("liteOnly entries = %#v, want 2 unified AGY entries", liteOnly)
	}
	if e := availEntry(t, liteOnly, "antigravity_gemini"); len(e.Tiers) != 1 || e.Tiers[0].Label != "AI Pro Trial" {
		t.Fatalf("liteOnly antigravity_gemini = %+v, want AI Pro Trial tier", e)
	}
	if e := availEntry(t, liteOnly, "antigravity_claude"); len(e.Tiers) != 1 || e.Tiers[0].Label != "AI Pro Trial" {
		t.Fatalf("liteOnly antigravity_claude = %+v, want AI Pro Trial tier", e)
	}
}

func TestComputeWeightsFleetAvailabilityByTierCapacity(t *testing.T) {
	accounts := []quotamonitor.AccountQuota{
		// Claude: Ultra (weight 10) at 0% + Pro (weight 1) at 100% -> 100/11 = 9.1%
		{
			Provider: "claude", PlanType: "ultra", Status: quotamonitor.StatusKnown,
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
		// Codex: Pro (weight 20) at 0% + Pro 100 (weight 5) at 100% + Plus (weight 1) at 100% -> 600/26 = 23.1%
		{
			Provider: "codex", PlanType: "pro", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 0)},
		},
		{
			Provider: "codex", PlanType: "pro_100", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 100)},
		},
		{
			Provider: "codex", PlanType: "plus", Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 100)},
		},
		// Antigravity paid: AI Ultra (weight 5) at 0% + AI Pro (weight 1) at 100% -> 100/6 = 16.7%
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
	if cl.RemainingPercent != 9.1 || cl.Accounts != 2 || len(cl.Tiers) != 2 {
		t.Fatalf("claude weighted = %+v, want 9.1%% with 2 tiers", cl)
	}
	if cl.Tiers[0].Tier != "ultra" || cl.Tiers[0].Label != "Ultra" || cl.Tiers[0].RemainingPercent != 0 || cl.Tiers[1].Tier != "pro" || cl.Tiers[1].RemainingPercent != 100 {
		t.Fatalf("claude tiers = %+v", cl.Tiers)
	}

	cx := availEntry(t, entries, "codex")
	if cx.RemainingPercent != 23.1 || cx.Accounts != 3 || len(cx.Tiers) != 3 {
		t.Fatalf("codex weighted = %+v, want 23.1%% with 3 tiers", cx)
	}
	if cx.Tiers[0].Label != "Pro" || cx.Tiers[1].Label != "Pro 100" || cx.Tiers[2].Label != "Plus" {
		t.Fatalf("codex tier labels = %+v", cx.Tiers)
	}

	ag := availEntry(t, entries, "antigravity_gemini")
	if ag.RemainingPercent != 16.7 || ag.Accounts != 2 || len(ag.Tiers) != 2 {
		t.Fatalf("antigravity weighted = %+v, want 16.7%% with 2 tiers", ag)
	}
	if ag.Tiers[0].Label != "AI Ultra" || ag.Tiers[1].Label != "AI Pro" {
		t.Fatalf("antigravity tier labels = %+v", ag.Tiers)
	}
}

func TestComputeDualWindowEdgeCasesAndJSONSerialization(t *testing.T) {
	// 1. Nearly depleted weekly caps effective remaining (no 100% cliff) while keeping short/weekly separate.
	cliff := Compute([]quotamonitor.AccountQuota{{
		Provider: "claude", PlanType: "pro", Status: quotamonitor.StatusKnown,
		Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 100), availWin("weekly", 10080, 5)},
	}})
	cl := availEntry(t, cliff, "claude")
	if cl.RemainingPercent != 5 {
		t.Fatalf("cliff effective = %v, want 5", cl.RemainingPercent)
	}
	assertOptFloat(t, "cliff.short", cl.ShortRemainingPercent, fptr(100))
	assertOptFloat(t, "cliff.weekly", cl.WeeklyRemainingPercent, fptr(5))
	assertOptFloat(t, "cliff.tier.short", cl.Tiers[0].ShortRemainingPercent, fptr(100))
	assertOptFloat(t, "cliff.tier.weekly", cl.Tiers[0].WeeklyRemainingPercent, fptr(5))

	// 2. Short-limited (5h=0%) with healthy weekly (7d=90%): effective=0, short=0 (serialized!), weekly=90.
	shortLimited := Compute([]quotamonitor.AccountQuota{{
		Provider: "claude", PlanType: "pro", Status: quotamonitor.StatusKnown,
		Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 0), availWin("weekly", 10080, 90)},
	}})
	sl := availEntry(t, shortLimited, "claude")
	if sl.RemainingPercent != 0 {
		t.Fatalf("shortLimited effective = %v, want 0", sl.RemainingPercent)
	}
	assertOptFloat(t, "shortLimited.short", sl.ShortRemainingPercent, fptr(0))
	assertOptFloat(t, "shortLimited.weekly", sl.WeeklyRemainingPercent, fptr(90))
	raw, err := json.Marshal(sl)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if !strings.Contains(string(raw), `"short_remaining_percent":0`) || !strings.Contains(string(raw), `"weekly_remaining_percent":90`) {
		t.Fatalf("JSON must keep explicit 0 for short_remaining_percent: %s", raw)
	}

	// 3. Mixed fleet: one account has 5h+7d, one has 5h-only -> weekly averages only accounts that reported 7d,
	// and effective remaining matches min(short, weekly) so summary gauges match the tighter displayed window.
	mixed := Compute([]quotamonitor.AccountQuota{
		{Provider: "codex", PlanType: "plus", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 80), availWin("secondary", 10080, 40)}},
		{Provider: "codex", PlanType: "plus", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 60)}},
	})
	mx := availEntry(t, mixed, "codex")
	// short=(80+60)/2=70; weekly=40/1=40 -> effective min(70,40)=40.
	if mx.RemainingPercent != 40 {
		t.Fatalf("mixed effective = %v, want 40", mx.RemainingPercent)
	}
	assertOptFloat(t, "mixed.short", mx.ShortRemainingPercent, fptr(70))
	assertOptFloat(t, "mixed.weekly", mx.WeeklyRemainingPercent, fptr(40))
	assertOptFloat(t, "mixed.tier.short", mx.Tiers[0].ShortRemainingPercent, fptr(70))
	assertOptFloat(t, "mixed.tier.weekly", mx.Tiers[0].WeeklyRemainingPercent, fptr(40))

	// 3b. Complementary accounts across tiers (Max 0/90 + Pro 100/5): effective matches min(fleetShort, fleetWeekly).
	comp := Compute([]quotamonitor.AccountQuota{
		{Provider: "claude", PlanType: "max", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 0), availWin("weekly", 10080, 90)}},
		{Provider: "claude", PlanType: "pro", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 100), availWin("weekly", 10080, 5)}},
	})
	cp := availEntry(t, comp, "claude")
	// short = (0*5 + 100*1)/6 = 16.7, weekly = (90*5 + 5*1)/6 = 75.8 -> effective = 16.7.
	if cp.RemainingPercent != 16.7 {
		t.Fatalf("comp effective = %v, want 16.7", cp.RemainingPercent)
	}
	assertOptFloat(t, "comp.short", cp.ShortRemainingPercent, fptr(16.7))
	assertOptFloat(t, "comp.weekly", cp.WeeklyRemainingPercent, fptr(75.8))

	// 4. Single-window account omits the unmeasured field in JSON.
	single := Compute([]quotamonitor.AccountQuota{{
		Provider: "codex", Status: quotamonitor.StatusKnown,
		Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 75)},
	}})
	sn := availEntry(t, single, "codex")
	assertOptFloat(t, "single.short", sn.ShortRemainingPercent, fptr(75))
	assertOptFloat(t, "single.weekly", sn.WeeklyRemainingPercent, nil)
	rawSingle, _ := json.Marshal(sn)
	if strings.Contains(string(rawSingle), "weekly_remaining_percent") {
		t.Fatalf("single-window JSON must omit weekly_remaining_percent: %s", rawSingle)
	}

	// 5. Antigravity name fallback when WindowMinutes is omitted.
	rem80, rem30 := 80.0, 30.0
	agFallback := Compute([]quotamonitor.AccountQuota{{
		Provider: "antigravity", Status: quotamonitor.StatusKnown,
		Windows: []quotamonitor.QuotaWindow{
			{Name: "Gemini Models / Five Hour Limit Remaining", RemainingPercent: &rem80},
			{Name: "Gemini Models / Weekly Limit Remaining", RemainingPercent: &rem30},
		},
	}})
	agf := availEntry(t, agFallback, "antigravity_gemini")
	if agf.RemainingPercent != 30 {
		t.Fatalf("agFallback effective = %v, want 30", agf.RemainingPercent)
	}
	assertOptFloat(t, "agFallback.short", agf.ShortRemainingPercent, fptr(80))
	assertOptFloat(t, "agFallback.weekly", agf.WeeklyRemainingPercent, fptr(30))
}

func TestComputeDynamicModelSetSplitAndMerge(t *testing.T) {
	fullCodex := []string{"gpt-6.2-astra", "gpt-6.2-sol", "gpt-6.5-pro"}
	baseCodex := []string{"gpt-6.2-astra", "gpt-6.2-sol"}

	// 1. Codex Pro 200 has an exclusive model while Pro 100 & Plus share the base set -> auto-splits into 2 pools.
	split := Compute([]quotamonitor.AccountQuota{
		{Provider: "codex", PlanType: "pro_200", Models: fullCodex, Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 90), availWin("secondary", 10080, 80)}},
		{Provider: "codex", PlanType: "pro_100", Models: baseCodex, Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 50), availWin("secondary", 10080, 40)}},
		{Provider: "codex", PlanType: "plus", Models: baseCodex, Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 20), availWin("secondary", 10080, 10)}},
	})
	if len(split) != 2 {
		t.Fatalf("expected 2 split codex pools, got %d: %+v", len(split), split)
	}
	if split[0].Key != "codex" || split[0].Label != "GPT · Pro 200" || split[0].Accounts != 1 || split[0].RemainingPercent != 80 {
		t.Fatalf("pool[0] = %+v, want codex / GPT · Pro 200 / 1 / 80", split[0])
	}
	if !strings.HasPrefix(split[1].Key, "codex_pool_") || split[1].Label != "GPT · Pro 100/Plus" || split[1].Accounts != 2 {
		t.Fatalf("pool[1] = %+v, want codex_pool_* / GPT · Pro 100/Plus / 2", split[1])
	}

	// 2. When Pro 100 & Plus receive the same model catalog as Pro 200 (order-independent) -> auto-merges into 1 GPT pool.
	reordered := []string{"openai/gpt-6.5-pro", "gpt-6.2-sol", "GPT-6.2-ASTRA"}
	merged := Compute([]quotamonitor.AccountQuota{
		{Provider: "codex", PlanType: "pro_200", Models: fullCodex, Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 90), availWin("secondary", 10080, 80)}},
		{Provider: "codex", PlanType: "pro_100", Models: reordered, Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 50), availWin("secondary", 10080, 40)}},
	})
	if len(merged) != 1 || merged[0].Key != "codex" || merged[0].Label != "GPT" || merged[0].Accounts != 2 {
		t.Fatalf("expected 1 merged GPT pool, got %+v", merged)
	}

	// 3. Claude Max vs Pro with differing model sets -> auto-splits into Claude · Max and Claude · Pro.
	claudeSplit := Compute([]quotamonitor.AccountQuota{
		{Provider: "claude", PlanType: "max", Models: []string{"claude-opus-5.5", "claude-sonnet-5.5", "claude-haiku-5.5"}, Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 70)}},
		{Provider: "claude", PlanType: "pro", Models: []string{"claude-sonnet-5.5", "claude-haiku-5.5"}, Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("five-hour", 300, 30)}},
	})
	if len(claudeSplit) != 2 || claudeSplit[0].Key != "claude" || claudeSplit[0].Label != "Claude · Max" || !strings.HasPrefix(claudeSplit[1].Key, "claude_pool_") || claudeSplit[1].Label != "Claude · Pro" {
		t.Fatalf("claudeSplit = %+v", claudeSplit)
	}

	// 4. Same plan with differing model sets disambiguates cleanly by model count.
	samePlan := Compute([]quotamonitor.AccountQuota{
		{Provider: "codex", PlanType: "plus", Models: fullCodex, Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 80)}},
		{Provider: "codex", PlanType: "plus", Models: baseCodex, Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("primary", 300, 40)}},
	})
	if len(samePlan) != 2 || samePlan[0].Label != "GPT · Plus (3)" || samePlan[1].Label != "GPT · Plus (2)" {
		t.Fatalf("samePlan disambiguation = %+v", samePlan)
	}

	// 5. Antigravity spanning 3 distinct model pools (Ultra full, Pro full, Free lite) emits 3 paired pool entries.
	ag3 := Compute([]quotamonitor.AccountQuota{
		{Provider: "antigravity", PlanType: "ultra", ModelTier: "full", Models: []string{"gemini-4-argon", "gemini-4-neon", "gemini-4-hydrogen", "gemini-claude-opus-5.5"}, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 90), availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 95)}},
		{Provider: "antigravity", PlanType: "pro", ModelTier: "full", Models: []string{"gemini-4-argon", "gemini-4-hydrogen", "gemini-claude-sonnet-5.5"}, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 60), availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 65)}},
		{Provider: "antigravity", PlanType: "free", ModelTier: "lite", Models: []string{"gemini-3.8-flash", "gemini-4-hydrogen"}, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 20), availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 10)}},
	})
	if len(ag3) != 6 {
		t.Fatalf("expected 6 AG entries (3 pools x 2 families), got %d: %+v", len(ag3), ag3)
	}
	if ag3[0].Key != "antigravity_gemini" || ag3[1].Key != "antigravity_claude" {
		t.Fatalf("ag3 primary keys = %q, %q", ag3[0].Key, ag3[1].Key)
	}
	if !strings.HasPrefix(ag3[2].Key, "antigravity_pool_") || !strings.HasSuffix(ag3[2].Key, "_gemini") || ag3[2].Label != "AGY (AI Pro) · Gemini" {
		t.Fatalf("ag3 mid pool gemini = %+v", ag3[2])
	}
	if ag3[4].Key != "antigravity_lite_gemini" || ag3[5].Key != "antigravity_lite_claude" {
		t.Fatalf("ag3 lite keys = %q, %q", ag3[4].Key, ag3[5].Key)
	}
	if ag3[0].ModelCount != 4 || ag3[2].ModelCount != 3 || ag3[4].ModelCount != 2 {
		t.Fatalf("ag3 model counts = [%d, %d, %d], want [4, 3, 2]", ag3[0].ModelCount, ag3[2].ModelCount, ag3[4].ModelCount)
	}

	// 6. Antigravity pools with identical tier mix (Ultra+Pro in both) disambiguate BOTH Pool 0 and Pool 1 by model count.
	agSameTiers := Compute([]quotamonitor.AccountQuota{
		{Provider: "antigravity", PlanType: "ultra", Models: []string{"gemini-4-argon-high", "gemini-4-carbon-high", "gemini-4-neon-xhigh", "claude-opus-5-5-high"}, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 98), availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 100)}},
		{Provider: "antigravity", PlanType: "pro", Models: []string{"gemini-4-argon-high", "gemini-4-carbon-high", "gemini-4-neon-xhigh", "claude-opus-5-5-high"}, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 92), availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 100)}},
		{Provider: "antigravity", PlanType: "ultra", Models: []string{"gemini-4-argon-high", "gemini-4-hydrogen-xhigh", "claude-sonnet-5-5-high"}, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 70), availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 80)}},
		{Provider: "antigravity", PlanType: "pro", Models: []string{"gemini-4-argon-high", "gemini-4-hydrogen-xhigh", "claude-sonnet-5-5-high"}, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 60), availWin("Claude and GPT models / Five Hour Limit Remaining", 300, 50)}},
	})
	if len(agSameTiers) != 4 {
		t.Fatalf("expected 4 AG entries (2 pools x 2 families), got %d: %+v", len(agSameTiers), agSameTiers)
	}
	if agSameTiers[0].Key != "antigravity_gemini" || agSameTiers[0].Label != "AGY (AI Ultra/AI Pro (4)) · Gemini" || agSameTiers[0].ModelCount != 4 || agSameTiers[0].Accounts != 2 {
		t.Fatalf("agSameTiers pool[0] gemini = %+v", agSameTiers[0])
	}
	if !strings.HasPrefix(agSameTiers[2].Key, "antigravity_pool_") || agSameTiers[2].Label != "AGY (AI Ultra/AI Pro (3)) · Gemini" || agSameTiers[2].ModelCount != 3 || agSameTiers[2].Accounts != 2 {
		t.Fatalf("agSameTiers pool[1] gemini = %+v", agSameTiers[2])
	}

	// 7. ModelTier-only fallback must NOT emit a synthetic ModelCount (e.g. 1000).
	tierOnly := Compute([]quotamonitor.AccountQuota{
		{Provider: "antigravity", PlanType: "ultra", ModelTier: "full", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 90)}},
		{Provider: "antigravity", PlanType: "pro", ModelTier: "lite", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 50)}},
	})
	for _, e := range tierOnly {
		if e.ModelCount != 0 {
			t.Fatalf("tier-only entry %q leaked synthetic ModelCount=%d", e.Key, e.ModelCount)
		}
	}

	// 8. Two all-Lite Antigravity pools with distinct model sets must label Pool 0 explicitly instead of bare "AGY · Gemini".
	agAllLite := Compute([]quotamonitor.AccountQuota{
		{Provider: "antigravity", PlanType: "pro", Models: []string{"gemini-3.8-flash-high", "gemini-4-hydrogen-xhigh", "claude-opus-4-6-thinking"}, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 80)}},
		{Provider: "antigravity", PlanType: "free", Models: []string{"gemini-3.8-flash-high", "gemini-4-hydrogen-xhigh"}, Status: quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{availWin("Gemini Models / Five Hour Limit Remaining", 300, 40)}},
	})
	if len(agAllLite) != 2 {
		t.Fatalf("expected 2 all-Lite AG entries, got %d: %+v", len(agAllLite), agAllLite)
	}
	if agAllLite[0].Label != "AGY (AI Pro Trial) · Gemini" || agAllLite[0].Short != "AGT" || agAllLite[0].ModelCount != 3 {
		t.Fatalf("agAllLite[0] = %+v", agAllLite[0])
	}
	if agAllLite[1].Label != "AGY (Free) · Gemini" || agAllLite[1].Short != "AGT" || agAllLite[1].ModelCount != 2 {
		t.Fatalf("agAllLite[1] = %+v", agAllLite[1])
	}
}
