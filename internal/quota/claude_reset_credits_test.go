package quotamonitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var claudeResetCreditsClock = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func claudeResetCreditsPayload(grants string) []byte {
	return []byte(`{"five_hour":{"utilization":12},"cedar_ember":{"eligible":true,"grants":[` + grants + `]}}`)
}

func TestParseClaudeResetCreditsOwnershipAndPrivacy(t *testing.T) {
	data := claudeResetCreditsPayload(`
		{"id":"private-redemption-handle","label":"private grant","resets_left":2,"resets_total":3,"paused":false,"usable_now":false,"starts_at":"2026-10-08T12:00:00Z","ends_at":"2026-10-10T12:00:00Z"},
		{"resets_left":1,"paused":false,"ends_at":"2026-10-09T14:00:00+02:00"},
		{"resets_left":3,"paused":false},
		{"resets_left":1,"paused":true,"ends_at":"2026-10-08T13:00:00Z"},
		{"resets_left":1,"paused":false,"ends_at":"2026-10-08T12:00:00Z"},
		{"resets_left":1,"paused":false,"starts_at":"2026-10-08T12:00:00.000000001Z"}`)
	got, err := ParseClaudeResetCredits(data, claudeResetCreditsClock)
	if err != nil || got == nil || got.AvailableCount == nil || *got.AvailableCount != 6 {
		t.Fatalf("credits=%#v err=%v", got, err)
	}
	wantExpiry := claudeResetCreditsClock.Add(24 * time.Hour)
	if got.NearestExpiry == nil || !got.NearestExpiry.Equal(wantExpiry) || got.ApplicableAvailableCount != nil {
		t.Fatalf("expiry=%v applicable=%v", got.NearestExpiry, got.ApplicableAvailableCount)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"available_count":6,"nearest_expiry":"2026-10-09T12:00:00Z"}`
	if string(encoded) != wantJSON {
		t.Fatalf("unsafe or unexpected DTO: %s", encoded)
	}
	for _, secret := range []string{"private-redemption-handle", "private grant", "cedar_ember", "grants", "usable_now", "resets_left"} {
		if strings.Contains(fmt.Sprintf("%+v %s", got, encoded), secret) {
			t.Errorf("retained private/raw field %q", secret)
		}
	}
}

func TestParseClaudeResetCreditsUnknownInventory(t *testing.T) {
	for _, block := range []string{
		`null`, `{}`, `false`, `[]`, `"private-inventory"`,
		`{"eligible":false,"grants":[]}`, `{"eligible":null,"grants":[]}`,
		`{"eligible":"true","grants":[]}`, `{"eligible":1,"grants":[]}`,
		`{"eligible":true}`, `{"eligible":true,"grants":null}`,
		`{"eligible":true,"grants":{}}`, `{"eligible":true,"grants":"private-grants"}`,
	} {
		t.Run(block, func(t *testing.T) {
			data := []byte(`{"five_hour":{"utilization":12},"cedar_ember":` + block + `}`)
			got, err := ParseClaudeResetCredits(data, claudeResetCreditsClock)
			if got != nil || err != nil {
				t.Fatalf("unknown inventory credits=%#v err=%v", got, err)
			}
			windows, err := ParseClaudeQuota(data)
			if err != nil || len(windows) != 1 || *windows[0].UsedPercent != 12 {
				t.Fatalf("ordinary usage lost: windows=%#v err=%v", windows, err)
			}
		})
	}
	got, err := ParseClaudeResetCredits([]byte(`{"five_hour":{"utilization":12}}`), claudeResetCreditsClock)
	if got != nil || err != nil {
		t.Fatalf("absent inventory credits=%#v err=%v", got, err)
	}
}

func TestParseClaudeResetCreditsSanitizedParseError(t *testing.T) {
	for _, body := range []string{``, `null`, `[]`, `true`, `"private-body"`, `{"cedar_ember":"private-body"`, `{} {}`, `{"secret":NaN}`} {
		t.Run(body, func(t *testing.T) {
			got, err := ParseClaudeResetCredits([]byte(body), claudeResetCreditsClock)
			var apiErr *APICallError
			if got != nil || !errors.As(err, &apiErr) || apiErr.Kind != "parse" || err.Error() != "parse" {
				t.Fatalf("credits=%#v err=%v, want sanitized parse APICallError", got, err)
			}
		})
	}
}

func TestParseClaudeResetCreditsMalformedGrantsAreDropped(t *testing.T) {
	for _, grant := range []string{
		`null`, `{}`, `[]`, `true`, `1`, `"private-grant"`,
		`{"paused":false}`, `{"resets_left":null,"paused":false}`,
		`{"resets_left":"1","paused":false}`, `{"resets_left":true,"paused":false}`,
		`{"resets_left":1.5,"paused":false}`, `{"resets_left":1.0,"paused":false}`,
		`{"resets_left":1e0,"paused":false}`, `{"resets_left":1e999,"paused":false}`,
		`{"resets_left":-1,"paused":false}`, `{"resets_left":9223372036854775808,"paused":false}`,
		`{"resets_left":1}`, `{"resets_left":1,"paused":null}`,
		`{"resets_left":1,"paused":"false"}`, `{"resets_left":1,"paused":0}`,
		`{"resets_left":1,"paused":false,"resets_total":0}`,
		`{"resets_left":1,"paused":false,"resets_total":-1}`,
		`{"resets_left":1,"paused":false,"resets_total":"2"}`,
		`{"resets_left":1,"paused":false,"resets_total":true}`,
		`{"resets_left":1,"paused":false,"resets_total":2.5}`,
		`{"resets_left":1,"paused":false,"starts_at":"not-a-date"}`,
		`{"resets_left":1,"paused":false,"ends_at":"2026-10-09"}`,
		`{"resets_left":1,"paused":false,"starts_at":42}`,
		`{"resets_left":1,"paused":false,"ends_at":false}`,
		`{"resets_left":1,"paused":false,"ends_at":{}}`,
		`{"resets_left":1,"paused":false,"starts_at":[]}`,
	} {
		t.Run(grant, func(t *testing.T) {
			got, err := ParseClaudeResetCredits(claudeResetCreditsPayload(grant+`,{"resets_left":2,"paused":false}`), claudeResetCreditsClock)
			if err != nil || got == nil || got.AvailableCount == nil || *got.AvailableCount != 2 || got.NearestExpiry != nil || got.ApplicableAvailableCount != nil {
				t.Fatalf("malformed grant affected valid neighbor: credits=%#v err=%v", got, err)
			}
		})
	}
}

func TestParseClaudeResetCreditsKnownZero(t *testing.T) {
	for _, grants := range []string{
		``, `{"resets_left":0,"resets_total":0,"paused":false}`,
		`{"resets_left":1,"paused":true}`,
		`{"resets_left":1,"paused":false,"ends_at":"2026-10-08T12:00:00Z"}`,
		`{"resets_left":1,"paused":false,"ends_at":"2020-01-01T00:00:00Z"}`,
		`{"resets_left":1,"paused":false,"starts_at":"2026-10-08T12:00:00.000000001Z"}`,
		`{},null,{"resets_left":1,"paused":null}`,
	} {
		t.Run(grants, func(t *testing.T) {
			got, err := ParseClaudeResetCredits(claudeResetCreditsPayload(grants), claudeResetCreditsClock)
			if err != nil || got == nil || got.AvailableCount == nil || *got.AvailableCount != 0 || got.NearestExpiry != nil || got.ApplicableAvailableCount != nil {
				t.Fatalf("known empty must clear stale count: credits=%#v err=%v", got, err)
			}
			encoded, err := json.Marshal(got)
			if err != nil || string(encoded) != `{"available_count":0}` {
				t.Fatalf("zero omitted: json=%s err=%v", encoded, err)
			}
		})
	}
}

func TestParseClaudeResetCreditsNullAndIgnoredFields(t *testing.T) {
	got, err := ParseClaudeResetCredits(claudeResetCreditsPayload(`
		{"resets_left":2,"resets_total":null,"paused":false,"starts_at":null,"ends_at":null,"usable_now":false},
		{"resets_left":1,"paused":false,"usable_now":"ignored","id":{"private":"handle"}},
		{"resets_left":1,"resets_total":1,"paused":false,"starts_at":"2026-10-08T14:00:00+02:00","ends_at":"2026-10-08T12:00:00.000000001Z"}`), claudeResetCreditsClock)
	if err != nil || got == nil || got.AvailableCount == nil || *got.AvailableCount != 4 {
		t.Fatalf("credits=%#v err=%v", got, err)
	}
	if got.NearestExpiry == nil || !got.NearestExpiry.Equal(claudeResetCreditsClock.Add(time.Nanosecond)) {
		t.Fatalf("fractional expiry lost: %v", got.NearestExpiry)
	}
}

func TestParseClaudeResetCreditsBounds(t *testing.T) {
	for _, records := range []int{200, 201} {
		t.Run(fmt.Sprintf("records_%d", records), func(t *testing.T) {
			grants := `{"resets_left":1,"paused":false}` + strings.Repeat(`,null`, records-1)
			got, err := ParseClaudeResetCredits(claudeResetCreditsPayload(grants), claudeResetCreditsClock)
			if err != nil {
				t.Fatal(err)
			}
			if records == 200 {
				if got == nil || got.AvailableCount == nil || *got.AvailableCount != 1 {
					t.Fatalf("200 records rejected: %#v", got)
				}
			} else if got != nil {
				t.Fatalf("malformed records did not count toward cap: %#v", got)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		grants string
		count  int // -1 means unknown inventory
	}{
		{"exact_total", `{"resets_left":50,"paused":false}`, 50},
		{"exact_aggregate", `{"resets_left":25,"paused":false},{"resets_left":25,"paused":false}`, 50},
		{"large_total_metadata", `{"resets_left":1,"resets_total":1000,"paused":false}`, 1},
		{"excess_total", `{"resets_left":51,"paused":false}`, -1},
		{"excess_aggregate", `{"resets_left":50,"paused":false},{"resets_left":1,"paused":false}`, -1},
		{"overflow_safe", `{"resets_left":1,"paused":false},{"resets_left":9223372036854775807,"paused":false}`, -1},
		{"only_owned_count", `{"resets_left":50,"paused":false},{"resets_left":51,"paused":true},{"resets_left":51,"paused":false,"starts_at":"2099-01-01T00:00:00Z"},{"resets_left":51,"paused":false,"ends_at":"2020-01-01T00:00:00Z"}`, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseClaudeResetCredits(claudeResetCreditsPayload(tc.grants), claudeResetCreditsClock)
			if err != nil {
				t.Fatal(err)
			}
			if tc.count < 0 {
				if got != nil {
					t.Fatalf("oversize inventory retained: %#v", got)
				}
			} else if got == nil || got.AvailableCount == nil || *got.AvailableCount != tc.count {
				t.Fatalf("credits=%#v, want %d", got, tc.count)
			}
		})
	}
}
