package plugin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	quotamonitor "quota-monitor/internal/quota"
)

func TestFilterQuotaProvidersKeepsOnlyMeasurable(t *testing.T) {
	in := []quotamonitor.AccountQuota{
		{Provider: "claude", AccountKey: "a"},
		{Provider: "Codex", AccountKey: "b"},         // case-insensitive
		{Provider: " antigravity ", AccountKey: "c"}, // whitespace tolerant
		{Provider: "opencode-free", AccountKey: "d"}, // other plugin provider
		{Provider: "openai", AccountKey: "e"},
		{Provider: "", AccountKey: "f"},
	}
	got := filterQuotaProviders(in)
	if len(got) != 3 || got[0].AccountKey != "a" || got[1].AccountKey != "b" || got[2].AccountKey != "c" {
		t.Fatalf("filtered = %+v, want a,b,c in order", got)
	}
	if len(in) != 6 {
		t.Fatalf("input must not be modified, len = %d", len(in))
	}
	if out := filterQuotaProviders(nil); out == nil || len(out) != 0 {
		t.Fatalf("nil input must yield an empty non-nil slice, got %#v", out)
	}
}

func TestIsQuotaProvider(t *testing.T) {
	for _, p := range []string{"claude", "CODEX", "Antigravity"} {
		if !isQuotaProvider(p) {
			t.Errorf("%q must be a quota provider", p)
		}
	}
	for _, p := range []string{"opencode-free", "gemini-cli", "kimi", "openai", "unsupported", ""} {
		if isQuotaProvider(p) {
			t.Errorf("%q must NOT be a quota provider", p)
		}
	}
}

// End to end: an unsupported provider in the host's auth-files list must not
// show up in the collected accounts, the count, or the dashboard snapshot.
func TestCollectionDropsUnsupportedProviders(t *testing.T) {
	reset(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "auth-files") {
			_, _ = w.Write([]byte(`[
				{"provider":"claude","name":"claude-a.json","auth_index":"1"},
				{"provider":"opencode-free","name":"opencode-free-direct.json","auth_index":"2"},
				{"provider":"opencode-free","name":"opencode-free-warp1.json","auth_index":"3"}
			]`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	t.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "test-key")
	configureFromEnv(nil)

	accounts, _, err := getAccounts()
	if err != nil {
		t.Fatalf("getAccounts: %v", err)
	}
	for _, a := range accounts {
		if !isQuotaProvider(a.Provider) {
			t.Errorf("unsupported provider leaked into collection: %q (%s)", a.Provider, a.AccountKey)
		}
	}
	if len(accounts) != 1 || !strings.EqualFold(accounts[0].Provider, "claude") {
		t.Fatalf("want exactly the claude account, got %d: %+v", len(accounts), accounts)
	}
	snap := resourceSnapshot()
	if n, _ := snap["count"].(int); n != 1 {
		t.Errorf("snapshot count = %v, want 1", snap["count"])
	}
}
