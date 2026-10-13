package quotamonitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchRecentUsageAuthsPagesAndFiltersTokenConsumers(t *testing.T) {
	now := time.Now().UTC()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/management/plugins/usage-audit/records" || r.URL.Query().Get("status") != "success" || r.URL.Query().Get("from") == "" || r.URL.Query().Get("limit") != "200" {
			t.Errorf("unexpected records request: %s", r.URL.String())
		}
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"records": []map[string]any{
					{"provider": "codex", "auth_index": "failed", "requested_at": now, "failed": true, "generate": true, "total_tokens": 99},
					{"provider": "codex", "auth_index": "count-only", "requested_at": now, "generate": false, "total_tokens": 99},
					{"provider": "codex", "auth_index": "zero", "requested_at": now, "generate": true, "total_tokens": 0},
					{"provider": "codex", "auth_index": "1", "requested_at": now.Add(-time.Minute), "generate": true, "total_tokens": 4},
				}, "has_more": true, "next_cursor": "second page",
			})
			return
		}
		if r.URL.Query().Get("cursor") != "second page" {
			t.Errorf("cursor not escaped/decoded correctly: %q", r.URL.Query().Get("cursor"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"records": []map[string]any{
				{"provider": "CoDeX", "auth_index": "1", "t": now.Add(-2 * time.Minute), "generate": true, "total_tokens": 7},
				{"provider": "antigravity", "auth_index": "2", "t": now.Add(-3 * time.Minute), "generate": true, "total_tokens": 5},
			}, "has_more": false,
		})
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL}
	got, err := client.FetchRecentUsageAuths(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(got) != 2 || !got["codex\x001"].Equal(now.Add(-time.Minute)) || !got["antigravity\x002"].Equal(now.Add(-3*time.Minute)) {
		t.Fatalf("calls=%d result=%v", calls, got)
	}
}

func TestFetchRecentUsageAuthsRejectsPartialPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"records":  []map[string]any{{"provider": "codex", "auth_index": "1", "t": time.Now().UTC(), "generate": true, "total_tokens": 10}},
				"has_more": true, "next_cursor": "bad",
			})
			return
		}
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()
	got, err := (&Client{BaseURL: server.URL}).FetchRecentUsageAuths(context.Background(), 200)
	if err == nil || got != nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("partial result must not be published: got=%v err=%v", got, err)
	}
}
