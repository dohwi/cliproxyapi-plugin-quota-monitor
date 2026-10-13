package quotamonitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCodexPlanVariantsNormalizeWithoutOrganizationGuessing(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"account snake", `{"account":{"plan_type":"CHATGPT_TEAM"}}`, "team"},
		{"account camel", `{"account":{"chatgptPlanType":"business"}}`, "team"},
		{"root chatgpt", `{"chatgpt_plan_type":"Plus"}`, "plus"},
		{"subscription tier", `{"subscription":{"tier":"team"}}`, "team"},
		{"organization subscription", `{"organization":{"id":"org-fixture","subscription":{"plan":"chatgpt_business"}}}`, "team"},
		{"workspace explicit type", `{"workspace":{"id":"ws-fixture","type":"TEAM"}}`, "team"},
		{"plural workspace", `{"workspaces":[{"id":"ws-fixture","plan_type":"pro"}]}`, "pro"},
		{"organization only", `{"organization":{"id":"org-fixture","name":"Team Alpha","role":"member"}}`, ""},
		{"workspace only", `{"workspace":{"id":"ws-fixture","name":"team","role":"owner"}}`, ""},
		{"unrelated type", `{"type":"team","provider":"codex"}`, ""},
		{"unknown paid", `{"account":{"plan_type":"premium"}}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCodexPlanType([]byte(tc.raw))
			if err != nil || got != tc.want {
				t.Fatalf("plan=%q want=%q err=%v", got, tc.want, err)
			}
		})
	}
}

func TestPassiveCodexTeamPlanVariantsAndPrecedence(t *testing.T) {
	adapter := NewAdapter(WithClock(func() time.Time { return time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC) }))
	accounts, err := adapter.ParseAuthFilesJSON([]byte(`[
		{"provider":"codex","alias":"team-account","account":{"plan_type":"team"},"organization":{"id":"org-fixture"}},
		{"provider":"codex","alias":"workspace-account","workspace":{"id":"ws-fixture","subscription":{"type":"business"}}},
		{"provider":"codex","alias":"org-only","organization":{"id":"org-fixture","name":"Team Alpha"}},
		{"provider":"codex","alias":"signal-wins","plan_type":"free","quota":{"signals":{"x-codex-plan-type":"PRO"}}}
	]`))
	if err != nil || len(accounts) != 4 {
		t.Fatalf("accounts=%#v err=%v", accounts, err)
	}
	want := []string{"team", "team", "", "pro"}
	for i := range want {
		if accounts[i].PlanType != want[i] {
			t.Errorf("account %d plan=%q want=%q", i, accounts[i].PlanType, want[i])
		}
	}
}

func TestPassiveCodexTeamFilenameRequiresVerifiedResolver(t *testing.T) {
	payload := []byte(`[
		{"provider":"codex","name":"raw-team.json","alias":"raw-name-only"},
		{"provider":"codex","name":"explicit-team.json","alias":"explicit","plan_type":"team"},
		{"provider":"codex","name":"workspace-team.json","alias":"workspace","workspace":{"subscription":{"type":"business"}}}
	]`)
	accounts, err := NewAdapter(WithClock(func() time.Time { return time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC) })).ParseAuthFilesJSON(payload)
	if err != nil || len(accounts) != 3 {
		t.Fatalf("accounts=%#v err=%v", accounts, err)
	}
	want := []string{"", "team", "team"}
	for i := range want {
		if accounts[i].PlanType != want[i] {
			t.Errorf("account %d plan=%q want=%q", i, accounts[i].PlanType, want[i])
		}
	}
}

func TestNormalizeCodexPlanTypePreservesExistingLabels(t *testing.T) {
	for raw, want := range map[string]string{
		"Plus": "plus", "CHATGPT_TEAM": "team", "business": "team",
		"Free": "free", "Pro": "pro", "k-12": "k12",
	} {
		if got := NormalizeCodexPlanType(raw); got != want {
			t.Errorf("NormalizeCodexPlanType(%q)=%q want=%q", raw, got, want)
		}
	}
	for _, raw := range []string{"", "premium", "my-team", "org_team_alpha"} {
		if got := NormalizeCodexPlanType(raw); got != "" {
			t.Errorf("guessy value %q normalized to %q", raw, got)
		}
	}
}

func TestParseCodexUsageExtrasV53(t *testing.T) {
	api, reset, err := ParseCodexUsageExtras([]byte(`{
		"credits":{"has_credits":true,"unlimited":false,"balance":12.5},
		"rate_limit_reset_credits":{"available_count":3,"applicable_available_count":2,"id":"never-keep"}
	}`))
	if err != nil || api == nil || reset == nil || api.Balance == nil || *api.Balance != 12.5 || reset.AvailableCount == nil || *reset.AvailableCount != 3 || reset.ApplicableAvailableCount == nil || *reset.ApplicableAvailableCount != 2 {
		t.Fatalf("extras=%#v %#v err=%v", api, reset, err)
	}
	serialized := string(mustJSON(t, struct {
		A *APICredits   `json:"api"`
		R *ResetCredits `json:"reset"`
	}{api, reset}))
	for _, banned := range []string{"never-keep", "id", "granted", "raw"} {
		if strings.Contains(serialized, banned) {
			t.Fatalf("unsafe field leaked: %s", serialized)
		}
	}
}

func TestParseCodexUsageExtrasMissingZeroMalformedV53(t *testing.T) {
	api, reset, err := ParseCodexUsageExtras([]byte(`{"credits":{"has_credits":"invalid","balance":"NaN"},"rate_limit_reset_credits":{"available_count":0,"applicable_available_count":0}}`))
	if err != nil || api != nil || reset == nil || reset.AvailableCount == nil || *reset.AvailableCount != 0 {
		t.Fatalf("zero/malformed: %#v %#v %v", api, reset, err)
	}
	api, reset, err = ParseCodexUsageExtras([]byte(`{}`))
	if err != nil || api != nil || reset != nil {
		t.Fatalf("missing: %#v %#v %v", api, reset, err)
	}
	if _, _, err = ParseCodexUsageExtras([]byte(`{`)); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}

func TestParseCodexResetCreditsExpiryV53(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	expiry, err := ParseCodexResetCreditsExpiry([]byte(`{"credits":[
		{"id":"old","status":"available","expires_at":"2026-03-30T11:00:00Z"},
		{"id":"used","status":"used","expires_at":"2026-03-30T12:05:00Z"},
		{"id":"later","status":"available","expires_at":"2026-03-30T14:00:00Z"},
		{"id":"nearest","status":"available","expires_at":"2026-03-30T13:00:00Z"}
	]}`), now)
	if err != nil || expiry == nil || !expiry.Equal(time.Date(2026, 3, 30, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("expiry=%v err=%v", expiry, err)
	}
}

func TestV53CodexActiveScopeFlagsWindowsAndEmptyObjects(t *testing.T) {
	windows, err := ParseCodexQuota([]byte(`{
		"rate_limit": {
			"allowed": false,
			"limit_reached": true,
			"limit_window_seconds": "301",
			"primary_window": {"remaining_percent":"77"},
			"secondary_window": {},
			"ignored_window": {}
		}
	}`))
	if err != nil || len(windows) != 1 {
		t.Fatalf("windows=%#v err=%v", windows, err)
	}
	w := windows[0]
	if w.Name != "primary" || w.Allowed == nil || *w.Allowed || w.LimitReached == nil || !*w.LimitReached || w.UsedPercent == nil || *w.UsedPercent != 100 || w.RemainingPercent == nil || *w.RemainingPercent != 0 {
		t.Fatalf("scope admission flags not canonicalized: %#v", w)
	}
	if w.WindowMinutes == nil || *w.WindowMinutes != 6 {
		t.Fatalf("limit_window_seconds not converted safely: %#v", w.WindowMinutes)
	}
}

func TestV53AntigravityFieldUnitsAndTimestampSafety(t *testing.T) {
	windows, err := ParseAntigravityQuota([]byte(`{"groups":[{"buckets":[
		{"displayName":"fraction string","remainingFraction":"0.25"},
		{"displayName":"percent string","remaining_percent":"25"},
		{"displayName":"invalid fraction","remainingFraction":"25"}
	]}]}`))
	if err != nil || len(windows) != 3 {
		t.Fatalf("windows=%#v err=%v", windows, err)
	}
	for i, want := range []float64{25, 25} {
		if windows[i].RemainingPercent == nil || *windows[i].RemainingPercent != want || windows[i].UsedPercent == nil || *windows[i].UsedPercent != 100-want {
			t.Errorf("window %d unit parsing = %#v", i, windows[i])
		}
	}
	if windows[2].RemainingPercent != nil || windows[2].UsedPercent != nil {
		t.Errorf("out-of-range fraction must not be treated as percent: %#v", windows[2])
	}

	for _, raw := range []string{"1700000000", "1700000000000"} {
		tm, ok := parseTimestamp(raw)
		if !ok || tm.Unix() != 1700000000 {
			t.Errorf("epoch %q = %v, %v", raw, tm, ok)
		}
	}
	for _, raw := range []string{"253402300800000", "999999999999999999", "1969-12-31T23:59:59Z"} {
		if _, ok := parseTimestamp(raw); ok {
			t.Errorf("unsafe/out-of-range time accepted: %q", raw)
		}
	}
}

func TestV53ResetAtNormalizationAndAbsolutePrecedence(t *testing.T) {
	observed := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	got, ok := ResetAtFromObservedAt(observed, 90)
	if !ok || !got.Equal(observed.Add(90*time.Second)) {
		t.Fatalf("derived reset=%v ok=%v", got, ok)
	}
	adapter := NewAdapter(WithClock(func() time.Time { return observed }))
	accounts, err := adapter.ParseAuthFilesJSON([]byte(`[{"provider":"codex","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-primary-used-percent":"1","x-codex-primary-reset-after-seconds":"90"}}}]`))
	if err != nil || len(accounts) != 1 || len(accounts[0].Windows) != 1 {
		t.Fatalf("accounts=%#v err=%v", accounts, err)
	}
	if reset := accounts[0].Windows[0].ResetAt; reset == nil || !reset.Equal(observed.Add(90*time.Second)) {
		t.Fatalf("passive relative reset was not normalized: %#v", accounts[0].Windows[0])
	}
	accounts, err = adapter.ParseAuthFilesJSON([]byte(`[{"provider":"codex","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-primary-used-percent":"1","x-codex-primary-reset-after-seconds":"90","x-codex-primary-reset-at":"2026-03-31T00:00:00Z"}}}]`))
	if err != nil || len(accounts) != 1 || len(accounts[0].Windows) != 1 {
		t.Fatalf("accounts=%#v err=%v", accounts, err)
	}
	if reset := accounts[0].Windows[0].ResetAt; reset == nil || !reset.Equal(time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("absolute reset_at must win: %#v", accounts[0].Windows[0])
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
