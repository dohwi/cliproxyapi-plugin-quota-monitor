package quotamonitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizePlanTypesAcrossProviders(t *testing.T) {
	claudeCases := []struct {
		raw  string
		want string
	}{
		{"default_claude_max_20x", "max_20x"},
		{"claude_max_5x", "max_5x"},
		{"claude_max", "max"},
		{"claude_pro", "pro"},
		{"claude_team", "team"},
		{"claude_enterprise", "enterprise"},
		{"claude_free", "free"},
		{"pro", "pro"},
		{"", ""},
		{"<script>alert(1)</script>", ""},
		{"unknown_tier", ""},
	}
	for _, tc := range claudeCases {
		if got := NormalizePlanType("claude", tc.raw); got != tc.want {
			t.Errorf("NormalizePlanType(claude, %q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	agCases := []struct {
		raw  string
		want string
	}{
		{"g1-ultra-tier", "ultra"},
		{"Google AI Ultra", "ultra"},
		{"g1-pro-tier", "pro"},
		{"standard-tier", "standard"},
		{"free-tier", "free"},
		{"legacy-tier", "free"},
		{"", ""},
		{"<img onerror=1>", ""},
		{"custom-tier", ""},
	}
	for _, tc := range agCases {
		if got := NormalizePlanType("antigravity", tc.raw); got != tc.want {
			t.Errorf("NormalizePlanType(antigravity, %q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	codexCases := []struct {
		raw  string
		want string
	}{
		{"prolite", "pro_lite"},
		{"pro_lite", "pro_lite"},
		{"pro_5x", "pro_lite"},
		{"pro_20x", "pro_20x"},
		{"pro", "pro"},
		{"ChatGPT_Business", "team"},
		{"edu", "edu"},
		{"go", "go"},
	}
	for _, tc := range codexCases {
		if got := NormalizePlanType("codex", tc.raw); got != tc.want {
			t.Errorf("NormalizePlanType(codex, %q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	if got := NormalizePlanType("other", "pro"); got != "" {
		t.Errorf("NormalizePlanType(other, pro) = %q, want empty", got)
	}
}

func TestParseClaudePlanType(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"rate_limit_tier wins over generic org", `{"organization":{"organization_type":"claude_max","rate_limit_tier":"default_claude_max_20x"}}`, "max_20x"},
		{"max 5x tier", `{"organization":{"organization_type":"claude_max","rate_limit_tier":"default_claude_max_5x"}}`, "max_5x"},
		{"org type only", `{"organization":{"organization_type":"claude_team"}}`, "team"},
		{"account bool max", `{"account":{"has_claude_max":true,"has_claude_pro":true}}`, "max"},
		{"account bool pro", `{"account":{"has_claude_max":false,"has_claude_pro":true}}`, "pro"},
		{"account explicit free", `{"account":{"has_claude_max":false,"has_claude_pro":false},"organization":{"organization_type":"individual"}}`, "free"},
		{"account false with unknown org does not assume free", `{"account":{"has_claude_max":false,"has_claude_pro":false},"organization":{"organization_type":"api_workspace"}}`, ""},
		{"empty object", `{}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseClaudePlanType([]byte(tc.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ParseClaudePlanType(%s) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
	if _, err := ParseClaudePlanType([]byte("not-json")); err == nil {
		t.Fatal("expected parse error for invalid JSON")
	}
}

func TestParseAntigravityPlanType(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"paidTier wins over currentTier", `{"currentTier":{"id":"standard-tier"},"paidTier":{"id":"g1-ultra-tier","name":"Google AI Ultra"}}`, "ultra"},
		{"paidTier pro", `{"paidTier":{"id":"g1-pro-tier"}}`, "pro"},
		{"currentTier free", `{"currentTier":{"id":"free-tier"}}`, "free"},
		{"currentTier legacy", `{"currentTier":{"id":"legacy-tier"}}`, "free"},
		{"snake_case paid_tier", `{"paid_tier":{"id":"g1-pro-tier"}}`, "pro"},
		{"name fallback", `{"currentTier":{"id":"unknown","name":"Google AI Ultra"}}`, "ultra"},
		{"empty object", `{}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAntigravityPlanType([]byte(tc.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ParseAntigravityPlanType(%s) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
	if _, err := ParseAntigravityPlanType([]byte("{bad")); err == nil {
		t.Fatal("expected parse error for invalid JSON")
	}
}

func TestFetchClaudeAndAntigravityPlan(t *testing.T) {
	var seenURLs []string
	var seenBodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL  string `json:"url"`
			Data string `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seenURLs = append(seenURLs, req.URL)
		seenBodies = append(seenBodies, req.Data)
		switch {
		case req.URL == claudeProfileURL:
			_ = json.NewEncoder(w).Encode(APICallResponse{
				StatusCode: 200,
				Body:       `{"organization":{"organization_type":"claude_max","rate_limit_tier":"default_claude_max_5x"}}`,
			})
		case req.URL == antigravityLoadCodeAssistDailyURL:
			// Simulate empty tier on daily host so fallback tries sandbox/prod.
			_ = json.NewEncoder(w).Encode(APICallResponse{StatusCode: 200, Body: `{}`})
		case req.URL == antigravityLoadCodeAssistSandboxURL:
			_ = json.NewEncoder(w).Encode(APICallResponse{
				StatusCode: 200,
				Body:       `{"paidTier":{"id":"g1-ultra-tier"}}`,
			})
		default:
			_ = json.NewEncoder(w).Encode(APICallResponse{StatusCode: 404, Body: `{}`})
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "mgmt-key")

	// Empty auth index must not hit network.
	if plan, err := client.FetchClaudePlan(context.Background(), AccountQuota{Provider: "claude"}); err != nil || plan != "" || len(seenURLs) != 0 {
		t.Fatalf("empty claude auth_index: plan=%q err=%v calls=%d", plan, err, len(seenURLs))
	}
	if plan, err := client.FetchAntigravityPlan(context.Background(), AccountQuota{Provider: "antigravity"}); err != nil || plan != "" || len(seenURLs) != 0 {
		t.Fatalf("empty ag auth_index: plan=%q err=%v calls=%d", plan, err, len(seenURLs))
	}

	cPlan, err := client.FetchClaudePlan(context.Background(), AccountQuota{Provider: "claude", AuthIndex: "c1"})
	if err != nil || cPlan != "max_5x" {
		t.Fatalf("FetchClaudePlan = (%q, %v), want (max_5x, nil)", cPlan, err)
	}

	agPlan, err := client.FetchAntigravityPlan(context.Background(), AccountQuota{Provider: "antigravity", AuthIndex: "a1", projectID: "proj-1"})
	if err != nil || agPlan != "ultra" {
		t.Fatalf("FetchAntigravityPlan = (%q, %v), want (ultra, nil)", agPlan, err)
	}
	if len(seenBodies) < 2 || !strings.Contains(seenBodies[1], `"cloudaicompanionProject":"proj-1"`) {
		t.Fatalf("expected project in loadCodeAssist body, got %v", seenBodies)
	}
}
