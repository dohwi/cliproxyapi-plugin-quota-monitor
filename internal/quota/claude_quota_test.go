package quotamonitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const claudeUsageFixture = `{"five_hour":{"utilization":0.5,"resets_at":"2026-10-08T18:00:00Z"},"seven_day":{"utilization":42,"resets_at":"2026-10-12T18:00:00.123456+00:00"},"seven_day_opus":null,"seven_day_sonnet":{"utilization":100,"resets_at":null},"seven_day_oauth_apps":{"utilization":0,"resets_at":null},"extra_usage":{"is_enabled":true,"used_credits":123,"account_id":"secret-body"},"access_token":"secret-body"}`

func TestParseClaudeQuota(t *testing.T) {
	windows, err := ParseClaudeQuota([]byte(claudeUsageFixture))
	if err != nil || len(windows) != 4 {
		t.Fatalf("windows=%#v err=%v", windows, err)
	}
	for i, want := range []struct {
		name    string
		used    float64
		minutes int
	}{{"five-hour", 0.5, 300}, {"weekly", 42, 10080}, {"weekly-sonnet", 100, 10080}, {"weekly-oauth-apps", 0, 10080}} {
		got := windows[i]
		if got.Name != want.name || got.UsedPercent == nil || *got.UsedPercent != want.used || got.RemainingPercent == nil || *got.RemainingPercent != 100-want.used || got.WindowMinutes == nil || *got.WindowMinutes != want.minutes {
			t.Errorf("window %d = %#v, want %+v", i, got, want)
		}
	}
	if windows[0].ResetAt == nil || windows[1].ResetAt == nil || windows[2].ResetAt != nil {
		t.Error("reset deadlines not correctly parsed")
	}
	encoded, _ := json.Marshal(windows)
	if strings.Contains(string(encoded), "secret-body") || strings.Contains(string(encoded), "extra_usage") {
		t.Fatalf("unsafe fields escaped parser: %s", encoded)
	}
}

func TestParseClaudeQuotaMissingInvalidAndBounds(t *testing.T) {
	for _, body := range []string{`{}`, `{"five_hour":null}`, `{"five_hour":{"resets_at":"2026-10-08T00:00:00Z"}}`, `{"five_hour":{"utilization":null}}`, `{"unknown":{"utilization":80}}`} {
		windows, err := ParseClaudeQuota([]byte(body))
		if err != nil || len(windows) != 0 {
			t.Errorf("missing utilization must not imply zero: body=%s windows=%#v err=%v", body, windows, err)
		}
	}
	for _, body := range []string{`null`, `[]`, `invalid`, `{"five_hour":false}`, `{"five_hour":{"utilization":"secret-body"}}`, `{"five_hour":{"utilization":1e999}}`} {
		_, err := ParseClaudeQuota([]byte(body))
		if err == nil || strings.Contains(err.Error(), "secret-body") {
			t.Errorf("body=%s error=%v, want sanitized parse error", body, err)
		}
	}
	windows, err := ParseClaudeQuota([]byte(`{"five_hour":{"utilization":-10,"resets_at":"bad"},"seven_day":{"utilization":150,"resets_at":null}}`))
	if err != nil || len(windows) != 2 || *windows[0].UsedPercent != 0 || *windows[1].RemainingPercent != 0 || windows[0].ResetAt != nil {
		t.Fatalf("out of range/invalid timestamp: %#v, %v", windows, err)
	}
}

func TestFetchClaudeQuotaManagementContract(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/management/api-call" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer management-secret" {
			t.Error("wrong management request")
		}
		var payload struct {
			AuthIndex string            `json:"auth_index"`
			Method    string            `json:"method"`
			URL       string            `json:"url"`
			Header    map[string]string `json:"header"`
			Data      string            `json:"data"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid api-call payload")
		}
		if payload.AuthIndex != "claude-auth" || payload.URL != claudeResetUsageURL || payload.Method != http.MethodGet || payload.Data != "" || payload.Header["Authorization"] != "Bearer $TOKEN$" || payload.Header["Anthropic-Beta"] != "oauth-2025-04-20" {
			t.Errorf("wrong provider request: %#v", payload)
		}
		for _, value := range payload.Header {
			if strings.Contains(value, "management-secret") {
				t.Error("management credential leaked to provider")
			}
		}
		_ = json.NewEncoder(w).Encode(APICallResponse{StatusCode: 200, Body: claudeUsageFixture})
	}))
	defer server.Close()
	windows, err := NewClient(server.URL, "management-secret").FetchActiveQuota(context.Background(), AccountQuota{Provider: "claude", AuthIndex: "claude-auth"})
	if err != nil || len(windows) != 4 || calls != 1 {
		t.Fatalf("windows=%#v calls=%d err=%v", windows, calls, err)
	}
	if !isAllowedEndpoint(claudeResetUsageURL) {
		t.Fatal("fixed reset inventory query must be allowed")
	}
	for _, endpoint := range []string{claudeResetUsageURL + "&token=secret", claudeQuotaURL + "?cedar_ember=2", claudeQuotaURL + "?token=secret", "https://api.anthropic.com/api/oauth/profile", "https://evil.example/api/oauth/usage"} {
		if isAllowedEndpoint(endpoint) {
			t.Errorf("unapproved endpoint allowed: %s", endpoint)
		}
	}
}

func TestFetchClaudeQuotaFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		kind   string
	}{{401, "secret-body", "auth"}, {403, "secret-body", "upstream status"}, {429, "secret-body", "upstream status"}, {200, "secret-body", "parse"}, {200, `{}`, "empty"}} {
		t.Run(tc.kind+http.StatusText(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(APICallResponse{StatusCode: tc.status, Body: tc.body})
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "key").FetchActiveQuota(context.Background(), AccountQuota{Provider: "claude", AuthIndex: "c"})
			ae, ok := err.(*APICallError)
			if !ok || ae.Kind != tc.kind || strings.Contains(err.Error(), "secret-body") {
				t.Fatalf("error=%v, want safe %s", err, tc.kind)
			}
		})
	}
}
