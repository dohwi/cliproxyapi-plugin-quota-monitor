package overview

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func summaryTestPercent(value float64) *float64 { return &value }
func summaryTestBool(value bool) *bool          { return &value }
func summaryTestMinutes(value int) *int         { return &value }

func TestBuildAllAccountsConservative(t *testing.T) {
	early := time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	accounts := []quotamonitor.AccountQuota{
		{Provider: "codex", Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: summaryTestPercent(80), ResetAt: &early}, {Name: "secondary", UsedPercent: summaryTestPercent(90), ResetAt: &late}}},
		// Never used, stale, and unavailable accounts are still included.
		{Provider: "GPT", Status: quotamonitor.StatusStale, Operational: &quotamonitor.OperationalStatus{Unavailable: summaryTestBool(true)}, Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: summaryTestPercent(30), ResetAt: &early}}},
		{Provider: "codex", Status: quotamonitor.StatusError},
		{Provider: "codex", Operational: &quotamonitor.OperationalStatus{Disabled: summaryTestBool(true)}, Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: summaryTestPercent(0)}}},
		{Provider: "codex", Operational: &quotamonitor.OperationalStatus{Status: "disabled"}},
	}
	before, _ := json.Marshal(accounts)
	out := Build(accounts, false)
	got := out[1]
	if !got.Stale || got.AccountCount != 3 || got.KnownCount != 2 || got.UnknownCount != 1 || got.StaleCount != 1 {
		t.Fatalf("overview=%+v summary=%+v", out, got)
	}
	if *got.RemainingPercent != 10 || *got.UsedPercent != 90 || !got.ResetAt.Equal(late) {
		t.Fatalf("worst known=%+v", got)
	}
	if len(got.Limits) != 2 || got.Limits[0].Name != "five-hour" || *got.Limits[0].RemainingPercent != 30 || got.Limits[0].KnownCount != 2 || got.Limits[1].Name != "weekly" || got.Limits[1].UnknownCount != 2 {
		t.Fatalf("limits=%+v", got.Limits)
	}
	stale := Build(accounts, true)[1]
	if stale.StaleCount != 3 || stale.KnownCount != 2 {
		t.Fatalf("snapshot stale=%+v", stale)
	}
	after, _ := json.Marshal(accounts)
	if string(before) != string(after) {
		t.Fatal("overview mutated accounts")
	}
	*got.RemainingPercent = 99
	*got.ResetAt = early
	if *accounts[0].Windows[1].UsedPercent != 90 || !accounts[0].Windows[1].ResetAt.Equal(late) {
		t.Fatal("overview aliases account pointers")
	}
}

func TestOverviewWindowRemaining(t *testing.T) {
	for _, tc := range []struct {
		name   string
		window quotamonitor.QuotaWindow
		want   *float64
	}{
		{"missing", quotamonitor.QuotaWindow{}, nil},
		{"used zero", quotamonitor.QuotaWindow{UsedPercent: summaryTestPercent(0)}, summaryTestPercent(100)},
		{"remaining zero", quotamonitor.QuotaWindow{RemainingPercent: summaryTestPercent(0)}, summaryTestPercent(0)},
		{"used full", quotamonitor.QuotaWindow{UsedPercent: summaryTestPercent(100)}, summaryTestPercent(0)},
		{"inconsistent conservative", quotamonitor.QuotaWindow{UsedPercent: summaryTestPercent(80), RemainingPercent: summaryTestPercent(90)}, summaryTestPercent(20)},
		{"remaining more restrictive", quotamonitor.QuotaWindow{UsedPercent: summaryTestPercent(10), RemainingPercent: summaryTestPercent(20)}, summaryTestPercent(20)},
		{"invalid negative", quotamonitor.QuotaWindow{RemainingPercent: summaryTestPercent(-1)}, nil},
		{"invalid over 100", quotamonitor.QuotaWindow{UsedPercent: summaryTestPercent(101)}, nil},
		{"invalid nan", quotamonitor.QuotaWindow{UsedPercent: summaryTestPercent(math.NaN())}, nil},
		{"invalid infinity", quotamonitor.QuotaWindow{RemainingPercent: summaryTestPercent(math.Inf(1))}, nil},
		{"invalid minus infinity", quotamonitor.QuotaWindow{RemainingPercent: summaryTestPercent(math.Inf(-1))}, nil},
		{"valid fallback", quotamonitor.QuotaWindow{UsedPercent: summaryTestPercent(40), RemainingPercent: summaryTestPercent(200)}, summaryTestPercent(60)},
		{"blocked overrides", quotamonitor.QuotaWindow{Allowed: summaryTestBool(false), RemainingPercent: summaryTestPercent(99)}, summaryTestPercent(0)},
		{"limited without numbers", quotamonitor.QuotaWindow{LimitReached: summaryTestBool(true)}, summaryTestPercent(0)},
		{"allowed not full", quotamonitor.QuotaWindow{Allowed: summaryTestBool(true), LimitReached: summaryTestBool(false)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := overviewWindowRemaining(tc.window); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("remaining=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestBuildFamiliesPrivacyAndIgnoredWindows(t *testing.T) {
	accounts := []quotamonitor.AccountQuota{
		{Provider: "Claude", Windows: []quotamonitor.QuotaWindow{{Name: "five_hour", RemainingPercent: summaryTestPercent(70)}, {Name: "seven_day_sonnet", RemainingPercent: summaryTestPercent(40)}, {Name: "overage", Allowed: summaryTestBool(false)}}},
		{Provider: "codex", Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: summaryTestPercent(90)}, {Name: "code_review", LimitReached: summaryTestBool(true)}}},
		{Provider: "gemini", AccountKey: "secret-key", AuthIndex: "secret-auth", Account: "person@example.com", Error: "secret-error", Windows: []quotamonitor.QuotaWindow{{Name: "person@example.com secret-key", LimitName: "secret-auth", WindowMinutes: summaryTestMinutes(1440), RemainingPercent: summaryTestPercent(60)}}},
		{Provider: "antigravity", Windows: []quotamonitor.QuotaWindow{{Name: "Gemini models / Five Hour Limit Remaining", RemainingPercent: summaryTestPercent(80)}, {Name: "Claude and GPT models / Weekly Limit Remaining", LimitReached: summaryTestBool(true)}, {Name: "unclassified", RemainingPercent: summaryTestPercent(0)}}},
		{Provider: "not-supported-secret", AccountKey: "another-secret"},
	}
	out := Build(accounts, false)
	for i, want := range []float64{40, 90, 60, 80, 0} {
		if got := out[i]; got.RemainingPercent == nil || *got.RemainingPercent != want || got.AccountCount != 1 {
			t.Fatalf("summary %d=%+v, want remaining %v", i, got, want)
		}
	}
	if out[3].Family != "gemini" || out[4].Family != "claude" || len(out[0].Limits) != 2 || out[0].Limits[1].Name != "weekly-sonnet" || len(out[1].Limits) != 1 {
		t.Fatalf("families/limits=%+v", out)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret", "example.com", "account_key", "auth_index", "code-review", "overage"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("overview leaked %q: %s", forbidden, raw)
		}
	}
}

func TestBuildUnknownAndDuplicateLimits(t *testing.T) {
	out := Build(nil, true)
	if len(out) != 5 {
		t.Fatalf("empty summaries=%+v", out)
	}
	for _, summary := range out {
		if summary.AccountCount != 0 || summary.RemainingPercent != nil || summary.UsedPercent != nil || summary.ResetAt != nil || summary.Limits == nil {
			t.Fatalf("empty summary=%+v", summary)
		}
	}
	accounts := []quotamonitor.AccountQuota{
		{Provider: "claude", Windows: []quotamonitor.QuotaWindow{{Name: "five-hour", UsedPercent: summaryTestPercent(math.NaN())}}},
		{Provider: "claude", Stale: true, Windows: []quotamonitor.QuotaWindow{{Name: "5h", RemainingPercent: summaryTestPercent(80)}, {Name: "five_hour", RemainingPercent: summaryTestPercent(30)}}},
		{Provider: "antigravity", Windows: []quotamonitor.QuotaWindow{{Name: "gemini weekly", RemainingPercent: summaryTestPercent(50)}}},
	}
	out = Build(accounts, false)
	got := out[0]
	if got.KnownCount != 1 || got.UnknownCount != 1 || got.StaleCount != 1 || len(got.Limits) != 1 || got.Limits[0].KnownCount != 1 || *got.RemainingPercent != 30 {
		t.Fatalf("duplicate/unknown counts=%+v", got)
	}
	if out[4].UnknownCount != 1 || out[4].RemainingPercent != nil {
		t.Fatalf("missing AG family=%+v", out[4])
	}
	allUnknown := Build(accounts[:1], false)[0]
	if allUnknown.RemainingPercent != nil || allUnknown.UsedPercent != nil || allUnknown.Limits[0].RemainingPercent != nil {
		t.Fatalf("invalid numeric became known=%+v", allUnknown)
	}
	raw, err := json.Marshal(allUnknown)
	if err != nil || !strings.Contains(string(raw), `"remaining_percent":null`) {
		t.Fatalf("unknown JSON=%s err=%v", raw, err)
	}
}

func TestBuildLimitingResetTies(t *testing.T) {
	early := time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	for _, tc := range []struct {
		name   string
		resets []*time.Time
		want   *time.Time
	}{
		{"latest tied reset", []*time.Time{&early, &late}, &late},
		{"latest independent of order", []*time.Time{&late, &early}, &late},
		{"missing reset", []*time.Time{&late, nil}, nil},
		{"missing first", []*time.Time{nil, &late}, nil},
		{"zero reset", []*time.Time{&late, {}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var accounts []quotamonitor.AccountQuota
			for _, reset := range tc.resets {
				accounts = append(accounts, quotamonitor.AccountQuota{Provider: "codex", Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: summaryTestPercent(0), ResetAt: reset}}})
			}
			got := Build(accounts, false)[1]
			if !reflect.DeepEqual(got.ResetAt, tc.want) || !reflect.DeepEqual(got.Limits[0].ResetAt, tc.want) {
				t.Fatalf("reset=%v limit reset=%v want=%v", got.ResetAt, got.Limits[0].ResetAt, tc.want)
			}
		})
	}
}
