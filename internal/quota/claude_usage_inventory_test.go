package quotamonitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchClaudeInventoryAndFallback(t *testing.T) {
	for _, status := range []int{200, 400, 403, 401, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var p struct {
					URL    string            `json:"url"`
					Header map[string]string `json:"header"`
				}
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				wantURL, wantUA := claudeResetUsageURL, "claude-cli/2.1.0 (external, cli)"
				responseStatus := status
				if calls == 2 {
					wantURL, wantUA = claudeQuotaURL, "claude-code/2.1.0"
					responseStatus = 200
				}
				if p.URL != wantURL || p.Header["User-Agent"] != wantUA || p.Header["Authorization"] != "Bearer $TOKEN$" {
					t.Errorf("incorrect fixed inventory/fallback request")
				}
				body := `{"five_hour":{"utilization":10},"cedar_ember":{"eligible":true,"grants":[{"resets_left":2,"paused":false,"ends_at":"2099-01-01T00:00:00Z","id":"private-redemption"}]}}`
				if calls == 2 {
					body = `{"five_hour":{"utilization":10}}`
				}
				_ = json.NewEncoder(w).Encode(APICallResponse{StatusCode: responseStatus, Body: body})
			}))
			defer server.Close()
			windows, reset, err := NewClient(server.URL, "key").FetchClaudeUsage(context.Background(), AccountQuota{Provider: "claude", AuthIndex: "c"})
			switch status {
			case 200:
				if err != nil || len(windows) != 1 || calls != 1 || reset == nil || reset.AvailableCount == nil || *reset.AvailableCount != 2 || reset.NearestExpiry == nil {
					t.Fatalf("inventory: windows=%d reset=%#v calls=%d err=%v", len(windows), reset, calls, err)
				}
			case 400, 403:
				if err != nil || len(windows) != 1 || calls != 2 || reset != nil {
					t.Fatalf("fallback: windows=%d reset=%#v calls=%d err=%v", len(windows), reset, calls, err)
				}
			default:
				if err == nil || calls != 1 {
					t.Fatalf("terminal failure retried/lost: calls=%d err=%v", calls, err)
				}
			}
		})
	}
}

func TestFetchClaudeMalformedInventoryKeepsUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(APICallResponse{StatusCode: 200, Body: `{"five_hour":{"utilization":15},"cedar_ember":{"eligible":true,"grants":"private-invalid"}}`})
	}))
	defer server.Close()
	windows, reset, err := NewClient(server.URL, "key").FetchClaudeUsage(context.Background(), AccountQuota{AuthIndex: "c"})
	if err != nil || len(windows) != 1 || reset != nil {
		t.Fatalf("optional malformed inventory must not break usage: %#v %#v %v", windows, reset, err)
	}
}
