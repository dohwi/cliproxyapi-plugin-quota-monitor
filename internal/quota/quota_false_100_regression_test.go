package quotamonitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodexActiveScopeFlagsDoNotOverwriteHealthyPrimaryWhenSecondaryExhausted(t *testing.T) {
	body := `{
		"rate_limit": {
			"allowed": false,
			"limit_reached": true,
			"primary_window": {"used_percent": 12, "limit_window_seconds": 18000},
			"secondary_window": {"used_percent": 100, "limit_window_seconds": 604800}
		}
	}`
	windows, err := ParseCodexQuota([]byte(body))
	if err != nil {
		t.Fatalf("ParseCodexQuota failed: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("len(windows) = %d, want 2: %#v", len(windows), windows)
	}
	var primary, secondary *QuotaWindow
	for i := range windows {
		switch windows[i].Name {
		case "primary":
			primary = &windows[i]
		case "secondary":
			secondary = &windows[i]
		}
	}
	if primary == nil || secondary == nil {
		t.Fatalf("expected primary and secondary windows, got %#v", windows)
	}
	if primary.UsedPercent == nil || *primary.UsedPercent != 12 || primary.RemainingPercent == nil || *primary.RemainingPercent != 88 {
		t.Fatalf("primary overwritten to false 100%%: %#v", *primary)
	}
	if primary.LimitReached != nil && *primary.LimitReached {
		t.Fatalf("primary unexpectedly marked limit_reached: %#v", *primary)
	}
	if primary.Allowed != nil && !*primary.Allowed {
		t.Fatalf("primary unexpectedly marked allowed=false: %#v", *primary)
	}
	if secondary.UsedPercent == nil || *secondary.UsedPercent != 100 || secondary.RemainingPercent == nil || *secondary.RemainingPercent != 0 {
		t.Fatalf("secondary should remain exhausted: %#v", *secondary)
	}
}

func TestCodexPassiveScopeFlagsDoNotOverwriteHealthyPrimaryWhenSecondaryExhausted(t *testing.T) {
	payload := `[{
		"provider": "codex",
		"quota": {
			"signals": {
				"x-codex-allowed": "false",
				"x-codex-limit-reached": "true",
				"x-codex-primary-window-minutes": "300",
				"x-codex-primary-used-percent": "15",
				"x-codex-secondary-window-minutes": "10080",
				"x-codex-secondary-used-percent": "100"
			}
		}
	}]`
	accounts, err := NewAdapter().ParseAuthFilesJSON([]byte(payload))
	if err != nil || len(accounts) != 1 {
		t.Fatalf("ParseAuthFilesJSON = %#v, err=%v", accounts, err)
	}
	var primary, secondary *QuotaWindow
	for i := range accounts[0].Windows {
		switch accounts[0].Windows[i].Name {
		case "primary":
			primary = &accounts[0].Windows[i]
		case "secondary":
			secondary = &accounts[0].Windows[i]
		}
	}
	if primary == nil || secondary == nil {
		t.Fatalf("expected primary and secondary windows, got %#v", accounts[0].Windows)
	}
	if primary.UsedPercent == nil || *primary.UsedPercent != 15 || primary.RemainingPercent == nil || *primary.RemainingPercent != 85 {
		t.Fatalf("passive primary overwritten to false 100%%: %#v", *primary)
	}
	if secondary.UsedPercent == nil || *secondary.UsedPercent != 100 || secondary.RemainingPercent == nil || *secondary.RemainingPercent != 0 {
		t.Fatalf("passive secondary should remain exhausted: %#v", *secondary)
	}
}

func TestAntigravityInvalidFractionFallsBackToRemainingPercent(t *testing.T) {
	body := `{"groups":[{"displayName":"Gemini Models","buckets":[
		{"displayName":"Five Hour Limit Remaining","remainingFraction":"","remaining_percent":65},
		{"displayName":"Weekly Limit Remaining","remainingFraction":25,"remaining_percent":"40"}
	]}]}`
	ws, err := ParseAntigravityQuota([]byte(body))
	if err != nil || len(ws) != 2 {
		t.Fatalf("ParseAntigravityQuota = %#v, err=%v", ws, err)
	}
	if ws[0].RemainingPercent == nil || *ws[0].RemainingPercent != 65 || ws[0].UsedPercent == nil || *ws[0].UsedPercent != 35 {
		t.Fatalf("bucket[0] fallback failed: %#v", ws[0])
	}
	if ws[1].RemainingPercent == nil || *ws[1].RemainingPercent != 40 || ws[1].UsedPercent == nil || *ws[1].UsedPercent != 60 {
		t.Fatalf("bucket[1] fallback failed: %#v", ws[1])
	}
}

func TestAntigravityUnmeasuredBucketsTriggerFallbackAndReplacement(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch req.URL {
		case antigravityDailyURL:
			// Returns Claude measured + unmeasured Gemini placeholder bucket.
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"groups\":[{\"displayName\":\"Claude and GPT models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\",\"remainingFraction\":0.9}]},{\"displayName\":\"Gemini Models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\",\"remainingFraction\":\"\"}]}]}"}`))
		case antigravitySandboxURL:
			// Unmeasured-only response should be treated as empty, not complete.
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"groups\":[{\"displayName\":\"Gemini Models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\"}]}]}"}`))
		case antigravityProdURL:
			// Returns real measured Gemini bucket replacing placeholder.
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"groups\":[{\"displayName\":\"Gemini Models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\",\"remainingFraction\":0.8}]}]}"}`))
		default:
			_, _ = w.Write([]byte(`{"status_code":500,"body":"unexpected"}`))
		}
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "k")
	ws, err := client.FetchActiveQuota(context.Background(), AccountQuota{AuthIndex: "1", Provider: "antigravity"})
	if err != nil {
		t.Fatalf("FetchActiveQuota failed: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (daily -> sandbox -> prod)", calls)
	}
	var gemini *QuotaWindow
	for i := range ws {
		if ws[i].Name == "Gemini Models / Five Hour Limit Remaining" {
			gemini = &ws[i]
		}
	}
	if gemini == nil || gemini.RemainingPercent == nil || *gemini.RemainingPercent != 80 {
		t.Fatalf("expected replaced measured Gemini window at 80%%, got %#v", ws)
	}
}

func TestAntigravityAllUnmeasuredReturnsEmptyAndUpstreamPreservesStatusCode(t *testing.T) {
	tsEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"groups\":[{\"displayName\":\"Gemini Models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\"}]}]}"}`))
	}))
	defer tsEmpty.Close()

	_, err := NewClient(tsEmpty.URL, "k").FetchActiveQuota(context.Background(), AccountQuota{AuthIndex: "1", Provider: "antigravity"})
	var ae *APICallError
	if !errors.As(err, &ae) || ae.Kind != "empty" {
		t.Fatalf("unmeasured buckets should return empty error, got %v", err)
	}

	ts429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":429,"body":"rate limited"}`))
	}))
	defer ts429.Close()

	_, err = NewClient(ts429.URL, "k").FetchActiveQuota(context.Background(), AccountQuota{AuthIndex: "1", Provider: "antigravity"})
	ae = nil
	if !errors.As(err, &ae) || ae.Kind != "upstream" || ae.StatusCode != 429 {
		t.Fatalf("429 upstream error lost status code: %#v", err)
	}
}
