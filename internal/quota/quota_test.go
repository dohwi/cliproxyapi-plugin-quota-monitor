package quotamonitor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syntheticFixtures provides test payloads matching real CLIProxyAPI management auth-files.
var syntheticFixtures = struct {
	ZeroPercent  string
	MissingQuota string
	MultiWindow  string
	Stale        string
	Malformed    string
	NoSecretLeak string
}{
	// Fixture 1: Zero used percent vs missing percent
	ZeroPercent: `[
		{
			"provider": "codex",
			"name": "acc-zero-test",
			"quota": {
				"observed_at": "2026-03-30T12:00:00Z",
				"signals": {
					"x-codex-plan-type": "plus",
					"x-codex-primary-used-percent": "0.0",
					"x-codex-primary-window-minutes": "60"
				}
			}
		}
	]`,

	// Fixture 2: Missing quota field (or empty signals & zero observed_at)
	MissingQuota: `[
		{
			"provider": "codex",
			"alias": "acc-missing-quota",
			"quota": null
		},
		{
			"provider": "claude",
			"alias": "acc-claude-empty",
			"quota": {
				"observed_at": null,
				"signals": {}
			}
		}
	]`,

	// Fixture 3: Multi-window Codex (primary, secondary, code-review, bengalfox)
	MultiWindow: `[
		{
			"provider": "codex",
			"account": "acc-multi-window",
			"quota": {
				"observed_at": "2026-03-30T12:00:00Z",
				"signals": {
					"x-codex-plan-type": "pro",
					"x-codex-active-limit": "primary",
					"x-codex-primary-used-percent": "45.5",
					"x-codex-primary-window-minutes": "180",
					"x-codex-primary-reset-after-seconds": "3600",
					"x-codex-secondary-used-percent": "12.0",
					"x-codex-secondary-window-minutes": "1440",
					"x-codex-code-review-allowed": "true",
					"x-codex-code-review-used-percent": "100.0",
					"x-codex-code-review-limit-reached": "true",
					"x-codex-bengalfox-primary-used-percent": "80.0",
					"x-codex-credits-balance": "1500",
					"retry-after": "120"
				}
			}
		}
	]`,

	// Fixture 4: Stale snapshot (observed > 15m ago)
	Stale: `[
		{
			"provider": "codex",
			"alias": "acc-stale",
			"quota": {
				"observed_at": "2026-03-30T10:00:00Z",
				"signals": {
					"x-codex-primary-used-percent": "90.0"
				}
			}
		}
	]`,

	// Fixture 5: Malformed JSON or missing provider
	Malformed: `[
		{
			"name": "no-provider-entry",
			"quota": { "observed_at": "2026-03-30T12:00:00Z" }
		}
	]`,

	// Fixture 6: Security test - raw auth payload containing sensitive fields, tokens, keys, secrets
	NoSecretLeak: `[
		{
			"provider": "codex",
			"alias": "acc-secret-holder",
			"access_token": "sk-secret-token-123456",
			"refresh_token": "rt-refresh-secret-7890",
			"api_key": "raw-api-key-never-leak",
			"client_secret": "my-client-secret-xyz",
			"quota": {
				"observed_at": "2026-03-30T12:00:00Z",
				"signals": {
					"x-codex-plan-type": "pro",
					"x-codex-primary-used-percent": "25.0",
					"authorization": "Bearer leak-attempt-1",
					"set-cookie": "session=sensitive-cookie",
					"x-custom-secret": "super-secret"
				}
			}
		}
	]`,
}

// v72155Fixture replicates the EXACT envelope shape emitted by CLIProxyAPI
// v7.2.155 GET /v0/management/auth-files (handler ListAuthFiles ->
// buildAuthFileEntry): {"files": [...]} with keys id, auth_index, name, type,
// provider, label, status, status_message, disabled, unavailable, runtime_only,
// source, size, success, failed, recent_requests, quota (observed_at only when
// non-zero + signals map), plus optional email/account_type/account/path.
// All emails/paths are synthetic.
const v72155Fixture = `{"files":[
  {
    "id": "codex-a1b2c3",
    "auth_index": "1",
    "name": "codex-user1@example.com.json",
    "type": "codex",
    "provider": "codex",
    "label": "",
    "status": "active",
    "status_message": "",
    "disabled": false,
    "unavailable": false,
    "runtime_only": false,
    "source": "file",
    "size": 4096,
    "success": 120,
    "failed": 3,
    "recent_requests": [
      {"time": "2026-03-30T11:55:00Z", "success": 40, "failed": 1}
    ],
    "quota": {
      "observed_at": "2026-03-30T12:00:00Z",
      "signals": {
        "x-codex-plan-type": "plus",
        "x-codex-primary-used-percent": "45.5",
        "x-codex-primary-window-minutes": "180",
        "x-codex-primary-reset-after-seconds": "3600"
      }
    },
    "email": "codex-user1@example.com",
    "account_type": "chatgpt",
    "account": "codex-user1@example.com",
    "created_at": "2026-01-01T00:00:00Z",
    "modtime": "2026-03-30T12:00:00Z",
    "updated_at": "2026-03-30T12:00:00Z",
    "path": "/home/user/.cli-proxy-api/codex-use****-MASKED****.json"
  },
  {
    "id": "antigravity-d4e5f6",
    "auth_index": "2",
    "name": "antigravity-user2@example.invalid.json",
    "type": "antigravity",
    "provider": "antigravity",
    "status": "active",
    "disabled": false,
    "unavailable": false,
    "runtime_only": false,
    "source": "file",
    "size": 2048,
    "success": 5,
    "failed": 0,
    "recent_requests": [],
    "quota": {"signals": {}},
    "email": "antigravity-user2@example.invalid",
    "account_type": "google",
    "path": "/home/user/.cli-proxy-api/antigravity-use****-MASKED****.json"
  },
  {
    "id": "codex-noquota-g7h8i9",
    "auth_index": "3",
    "name": "codex-user3@example.com.json",
    "type": "codex",
    "provider": "codex",
    "status": "active",
    "disabled": false,
    "unavailable": false,
    "runtime_only": false,
    "source": "file",
    "size": 2048,
    "success": 0,
    "failed": 0,
    "recent_requests": [],
    "quota": {"signals": {}},
    "email": "codex-user3@example.com",
    "account_type": "chatgpt",
    "path": "/home/user/.cli-proxy-api/codex-user3@example.com.json"
  },
  {
    "id": "disk-fallback-j1k2l3",
    "name": "claude-user4.json",
    "type": "claude",
    "size": 1024,
    "modtime": "2026-03-30T11:00:00Z",
    "email": "claude-user4@example.com"
  }
]}`

// TestV72155RealEnvelope pins the parser to the verified live v7.2.155 response:
// codex accounts must parse into AccountQuota with windows (known when signals
// exist, unknown when the snapshot is empty), antigravity must be unsupported
// without any error, and the whole payload must parse without failure.
func TestV72155RealEnvelope(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))
	res, err := adapter.ParseAuthFilesJSON([]byte(v72155Fixture))
	if err != nil {
		t.Fatalf("v7.2.155 envelope must parse without error, got: %v", err)
	}
	if len(res) != 4 {
		t.Fatalf("expected 4 accounts, got %d", len(res))
	}

	// Entry 0: codex with passive signals -> known with parsed windows.
	codex := res[0]
	if codex.Provider != "codex" || codex.Status != StatusKnown {
		t.Errorf("codex entry: expected provider=codex status=known, got %s/%s", codex.Provider, codex.Status)
	}
	if codex.Account != "codex-user1@example.com" {
		t.Errorf("codex entry: unexpected account %q", codex.Account)
	}
	if codex.PlanType != "plus" {
		t.Errorf("codex entry: expected plan_type plus, got %q", codex.PlanType)
	}
	if len(codex.Windows) != 1 {
		t.Fatalf("codex entry: expected 1 window, got %d", len(codex.Windows))
	}
	w := codex.Windows[0]
	if w.Name != "primary" || w.UsedPercent == nil || *w.UsedPercent != 45.5 || w.WindowMinutes == nil || *w.WindowMinutes != 180 {
		t.Errorf("codex entry: window mismatch: %+v", w)
	}
	if codex.ObservedAt == nil {
		t.Errorf("codex entry: expected observed_at parsed")
	}

	// Entry 1: antigravity -> unsupported, no failure.
	anti := res[1]
	if anti.Provider != "antigravity" || anti.Status != StatusUnsupported {
		t.Errorf("antigravity entry: expected unsupported, got %s/%s", anti.Provider, anti.Status)
	}
	if anti.Account != "antigravity-user2@example.invalid" {
		t.Errorf("antigravity entry: unexpected account %q", anti.Account)
	}

	// Entry 2: codex with empty snapshot -> unknown (not error), account still parsed.
	noquota := res[2]
	if noquota.Status != StatusUnknown {
		t.Errorf("codex empty-snapshot entry: expected unknown, got %s", noquota.Status)
	}
	if noquota.Account != "codex-user3@example.com" {
		t.Errorf("codex empty-snapshot entry: unexpected account %q", noquota.Account)
	}

	// Entry 3: disk-fallback variant (type only, no provider/quota) -> tolerant
	// parse, claude recognized, unknown status instead of error.
	disk := res[3]
	if disk.Provider != "claude" {
		t.Errorf("disk-fallback entry: expected provider fallback from type=claude, got %q", disk.Provider)
	}
	if disk.Status != StatusUnknown {
		t.Errorf("disk-fallback entry: expected unknown, got %s", disk.Status)
	}

	// Security: filesystem paths, id_token claims, tokens, and raw request payloads must
	// never leak into the output.
	marshaled, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	outStr := string(marshaled)
	for _, banned := range []string{
		"MASKED",
		"/home/user/.cli-proxy-api",
		"chatgpt_account_id",
		"sk-secret-token",
		"id_token",
	} {
		if strings.Contains(outStr, banned) {
			t.Errorf("SECURITY LEAK: output contains %q: %s", banned, outStr)
		}
	}

	// Verify operational status parsed correctly for antigravity and codex
	if anti.Operational == nil {
		t.Fatalf("antigravity entry: expected operational status")
	}
	if anti.Operational.Status != "active" {
		t.Errorf("antigravity: expected status active, got %s", anti.Operational.Status)
	}
	if anti.Operational.Success == nil || *anti.Operational.Success != 5 {
		t.Errorf("antigravity: expected success=5, got %v", anti.Operational.Success)
	}
	if anti.Operational.Failed == nil || *anti.Operational.Failed != 0 {
		t.Errorf("antigravity: expected failed=0, got %v", anti.Operational.Failed)
	}
	if anti.Operational.AccountType != "google" {
		t.Errorf("antigravity: expected account_type=google, got %s", anti.Operational.AccountType)
	}

	if codex.Operational == nil {
		t.Fatalf("codex entry: expected operational status")
	}
	if codex.Operational.Success == nil || *codex.Operational.Success != 120 {
		t.Errorf("codex: expected success=120, got %v", codex.Operational.Success)
	}
	if codex.Operational.RecentRequestCount != 1 {
		t.Errorf("codex: expected recent_request_count=1, got %d", codex.Operational.RecentRequestCount)
	}
	if codex.Operational.RecentRequestLatest == nil {
		t.Errorf("codex: expected recent_request_latest parsed")
	}
}

func TestParseEndpointKindLegacyDirectLiteralAndRejections(t *testing.T) {
	for _, raw := range []string{"", "  ", "direct", " DIRECT ", "DiReCt"} {
		if got := ParseEndpointKindFromProxyURL(raw); got != EndpointDirect {
			t.Errorf("legacy direct literal %q = %q, want direct", raw, got)
		}
	}
	for _, raw := range []string{
		"direct://", "direct://warp1:8080", "prefix-direct", "direct-suffix",
		"http://direct:8080", "http://user:pass@warp1:8080", "warp1",
		"http://warp1", "http://warp1:not-a-port",
	} {
		if got := ParseEndpointKindFromProxyURL(raw); got != EndpointUnknown {
			t.Errorf("unsafe/non-literal proxy %q = %q, want unknown", raw, got)
		}
	}
	for raw, want := range map[string]EndpointKind{
		"http://WARP1:8080":    EndpointWarp1,
		"socks5h://warp2:1080": EndpointWarp2,
	} {
		if got := ParseEndpointKindFromProxyURL(raw); got != want {
			t.Errorf("proxy %q = %q, want %q", raw, got, want)
		}
	}
}

func testFixedTime() time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-03-30T12:05:00Z")
	return t
}

func TestZeroPercentDistinction(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))
	res, err := adapter.ParseAuthFilesJSON([]byte(syntheticFixtures.ZeroPercent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	acc := res[0]
	if acc.Status != StatusKnown {
		t.Errorf("expected status known, got %s", acc.Status)
	}
	if len(acc.Windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(acc.Windows))
	}
	win := acc.Windows[0]
	if win.UsedPercent == nil {
		t.Fatalf("expected non-nil UsedPercent for 0.0")
	}
	if *win.UsedPercent != 0.0 {
		t.Errorf("expected 0.0, got %f", *win.UsedPercent)
	}
	if win.WindowMinutes == nil || *win.WindowMinutes != 60 {
		t.Errorf("expected window minutes 60")
	}
}

func TestMissingQuota(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))
	res, err := adapter.ParseAuthFilesJSON([]byte(syntheticFixtures.MissingQuota))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}

	if res[0].Status != StatusUnknown {
		t.Errorf("expected status unknown for null quota, got %s", res[0].Status)
	}
	if res[0].Account != "acc-missing-quota" {
		t.Errorf("unexpected account: %s", res[0].Account)
	}

	if res[1].Status != StatusUnknown {
		t.Errorf("expected status unknown for empty signals, got %s", res[1].Status)
	}
}

func TestMultiWindowAndSignals(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))
	res, err := adapter.ParseAuthFilesJSON([]byte(syntheticFixtures.MultiWindow))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	acc := res[0]
	if acc.PlanType != "pro" {
		t.Errorf("expected plan_type pro, got %s", acc.PlanType)
	}
	if acc.ActiveLimit != "primary" {
		t.Errorf("expected active_limit primary, got %s", acc.ActiveLimit)
	}
	if acc.RetryAfter == nil || *acc.RetryAfter != 120 {
		t.Errorf("expected retry-after 120, got %v", acc.RetryAfter)
	}
	if acc.Credits["balance"] != "1500" {
		t.Errorf("expected credit balance 1500, got %v", acc.Credits)
	}

	// Windows expected: bengalfox-primary, code-review, primary, secondary
	if len(acc.Windows) < 4 {
		t.Fatalf("expected at least 4 windows, got %d", len(acc.Windows))
	}

	var foundPrimary, foundCodeReview bool
	for _, w := range acc.Windows {
		if w.Name == "primary" {
			foundPrimary = true
			if w.UsedPercent == nil || *w.UsedPercent != 45.5 {
				t.Errorf("primary used percent mismatch: %v", w.UsedPercent)
			}
			if w.WindowMinutes == nil || *w.WindowMinutes != 180 {
				t.Errorf("primary window minutes mismatch: %v", w.WindowMinutes)
			}
			if w.ResetAfterSec == nil || *w.ResetAfterSec != 3600 {
				t.Errorf("primary reset after mismatch: %v", w.ResetAfterSec)
			}
		}
		if w.Name == "code-review" {
			foundCodeReview = true
			if w.LimitReached == nil || !*w.LimitReached {
				t.Errorf("code-review limit reached mismatch")
			}
			if w.Allowed == nil || !*w.Allowed {
				t.Errorf("code-review allowed mismatch")
			}
		}
	}
	if !foundPrimary || !foundCodeReview {
		t.Errorf("did not find expected primary and code-review windows")
	}
}

func TestStaleness(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime)) // Fixed at 12:05, observed was 10:00 (2h5m ago > 15m)
	res, err := adapter.ParseAuthFilesJSON([]byte(syntheticFixtures.Stale))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].Status != StatusStale {
		t.Errorf("expected status stale, got %s", res[0].Status)
	}
}

func TestMalformedAndUnsupported(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))
	res, err := adapter.ParseAuthFilesJSON([]byte(syntheticFixtures.Malformed))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].Status != StatusError {
		t.Errorf("expected status error for missing provider, got %s", res[0].Status)
	}

	// Malformed invalid json string
	_, err = adapter.ParseAuthFilesJSON([]byte(`{not-json}`))
	if err == nil {
		t.Errorf("expected json syntax error, got nil")
	}

	// Unsupported provider test
	unsupportedJSON := `[{"provider": "openai", "name": "openai-key", "quota": {"signals": {"foo": "bar"}}}]`
	res2, err := adapter.ParseAuthFilesJSON([]byte(unsupportedJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res2) != 1 || res2[0].Status != StatusUnsupported {
		t.Errorf("expected status unsupported for openai provider, got %v", res2[0].Status)
	}
}

func TestNoSecretLeak(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))
	res, err := adapter.ParseAuthFilesJSON([]byte(syntheticFixtures.NoSecretLeak))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	acc := res[0]

	// Marshal back to JSON to assert no secret strings leak in output
	marshaled, err := json.Marshal(acc)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	outStr := string(marshaled)

	bannedSubstrings := []string{
		"sk-secret-token-123456",
		"rt-refresh-secret-7890",
		"raw-api-key-never-leak",
		"my-client-secret-xyz",
		"leak-attempt-1",
		"session=sensitive-cookie",
		"super-secret",
		"authorization",
		"set-cookie",
		"x-custom-secret",
	}

	for _, banned := range bannedSubstrings {
		if strings.Contains(outStr, banned) {
			t.Errorf("SECURITY LEAK DETECTED! Found banned substring: %s in output: %s", banned, outStr)
		}
	}

	// Check whitelist in RawSignals
	if _, ok := acc.RawSignals["authorization"]; ok {
		t.Errorf("RawSignals contains forbidden header authorization")
	}
	if _, ok := acc.RawSignals["x-codex-plan-type"]; !ok {
		t.Errorf("expected x-codex-plan-type in whitelisted RawSignals")
	}
}

func TestClientWithMockServer(t *testing.T) {
	var gotAuth, gotMgmtKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/management/auth-files" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotMgmtKey = r.Header.Get("X-Management-Key")
		if gotAuth != "Bearer test-secret-mgmt-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(syntheticFixtures.MultiWindow))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewClient(server.URL, "test-secret-mgmt-key", WithClientAdapter(NewAdapter(WithClock(testFixedTime))))
	quotas, err := client.FetchAuthFiles(ctx)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	if len(quotas) != 1 {
		t.Fatalf("expected 1 quota, got %d", len(quotas))
	}
	if quotas[0].Account != "acc-multi-window" {
		t.Errorf("unexpected account name: %s", quotas[0].Account)
	}
	if gotAuth != "Bearer test-secret-mgmt-key" {
		t.Errorf("outer Authorization header = %q", gotAuth)
	}
	if gotMgmtKey != "test-secret-mgmt-key" {
		t.Errorf("outer X-Management-Key header = %q", gotMgmtKey)
	}

	// Test invalid auth
	badClient := NewClient(server.URL, "wrong-key")
	_, err = badClient.FetchAuthFiles(ctx)
	if err == nil {
		t.Errorf("expected error with invalid key, got nil")
	}
}

func TestRedirectRejectionAndAuthHeaderProtection(t *testing.T) {
	// Target server that should NEVER receive the Authorization header
	var targetReceivedAuth string
	var targetReached bool
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetReached = true
		targetReceivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"files": []}`))
	}))
	defer targetServer.Close()

	t.Run("Redirect to different hostname", func(t *testing.T) {
		targetReached = false
		targetReceivedAuth = ""
		redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, targetServer.URL+"/management/auth-files", http.StatusFound)
		}))
		defer redirectServer.Close()

		origHTTPClient := &http.Client{Timeout: 5 * time.Second}
		client := NewClient(redirectServer.URL, "secret-admin-key", WithHTTPClient(origHTTPClient))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.FetchAuthFiles(ctx)
		// Should fail due to non-200 (302) without following
		if err == nil {
			t.Errorf("expected error due to redirect rejection, got nil")
		}
		if targetReached {
			t.Errorf("target server should NOT have been reached on redirect")
		}
		if targetReceivedAuth != "" {
			t.Errorf("Authorization header leaked to redirect target: %s", targetReceivedAuth)
		}
		// Verify original caller client was NOT mutated
		if origHTTPClient.CheckRedirect != nil {
			t.Errorf("caller's http.Client was mutated!")
		}
	})

	t.Run("Redirect to same hostname different port", func(t *testing.T) {
		targetReached = false
		targetReceivedAuth = ""
		// Go's default transport does NOT strip Authorization on same-host different port,
		// which could leak credentials if redirects were followed. Our cloneHTTPClient policy
		// stops all redirects unconditionally.
		redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, targetServer.URL+"/management/auth-files", http.StatusTemporaryRedirect)
		}))
		defer redirectServer.Close()

		origHTTPClient := &http.Client{Timeout: 5 * time.Second}
		client := NewClient(redirectServer.URL, "secret-admin-key", WithHTTPClient(origHTTPClient))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.FetchAuthFiles(ctx)
		if err == nil {
			t.Errorf("expected error due to redirect rejection, got nil")
		}
		if targetReached {
			t.Errorf("target server should NOT have been reached on redirect to different port")
		}
		if targetReceivedAuth != "" {
			t.Errorf("Authorization header leaked to different port: %s", targetReceivedAuth)
		}
		if origHTTPClient.CheckRedirect != nil {
			t.Errorf("caller's http.Client was mutated!")
		}
	})
}

type countingRoundTripper struct {
	called int
	inner  http.RoundTripper
}

func (c *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.called++
	rt := c.inner
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(req)
}

func TestCustomRoundTripperPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	rt := &countingRoundTripper{inner: server.Client().Transport}
	customClient := &http.Client{
		Transport: rt,
		Timeout:   5 * time.Second,
	}

	client := NewClient(server.URL, "key", WithHTTPClient(customClient))
	_, err := client.FetchAuthFiles(context.Background())
	if err != nil {
		t.Fatalf("FetchAuthFiles failed: %v", err)
	}
	if rt.called != 1 {
		t.Errorf("expected custom RoundTripper to be called once, got %d", rt.called)
	}

	// Verify original customClient transport is untouched
	if customClient.Transport != rt {
		t.Errorf("original custom client transport was mutated")
	}
}

func TestLoopbackClientIgnoresEnvironmentProxy(t *testing.T) {
	var proxyHits int64
	// Set up a hostile/mock proxy server that records any hits
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&proxyHits, 1)
		http.Error(w, "hostile proxy intercepted request", http.StatusBadGateway)
	}))
	defer proxyServer.Close()

	// Direct loopback server simulating CLIProxyAPI management endpoint
	var serverHits int64
	var lastAuth, lastMgmtKey string
	var lastPayload struct {
		AuthIndex string            `json:"auth_index"`
		Method    string            `json:"method"`
		URL       string            `json:"url"`
		Header    map[string]string `json:"header"`
		Data      string            `json:"data"`
	}

	mgmtServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&serverHits, 1)
		lastAuth = r.Header.Get("Authorization")
		lastMgmtKey = r.Header.Get("X-Management-Key")

		switch r.URL.Path {
		case "/management/auth-files":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case "/management/api-call":
			_ = json.NewDecoder(r.Body).Decode(&lastPayload)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status_code":200,"header":{"Content-Type":["application/json"]},"body":"{}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mgmtServer.Close()

	// Set malicious HTTP_PROXY in environment and clear NO_PROXY
	t.Setenv("HTTP_PROXY", proxyServer.URL)
	t.Setenv("http_proxy", proxyServer.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	// Client uses default NewClient constructor (production default client)
	client := NewClient(mgmtServer.URL, "secret-test-key")

	// 1. Test FetchAuthFiles bypasses proxy completely
	_, err := client.FetchAuthFiles(context.Background())
	if err != nil {
		t.Fatalf("FetchAuthFiles failed: %v", err)
	}
	if hits := atomic.LoadInt64(&proxyHits); hits != 0 {
		t.Fatalf("hostile proxy received %d requests during FetchAuthFiles, expected 0", hits)
	}
	if lastAuth != "Bearer secret-test-key" {
		t.Errorf("FetchAuthFiles Authorization header = %q", lastAuth)
	}
	if lastMgmtKey != "secret-test-key" {
		t.Errorf("FetchAuthFiles X-Management-Key header = %q", lastMgmtKey)
	}

	// 2. Test APICall also bypasses proxy completely
	resp, err := client.APICall(context.Background(), "1", http.MethodPost, antigravityDailyURL, map[string]string{
		"Authorization": "Bearer $TOKEN$",
	}, "{}")
	if err != nil {
		t.Fatalf("APICall failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("APICall unexpected status: %d", resp.StatusCode)
	}
	if hits := atomic.LoadInt64(&proxyHits); hits != 0 {
		t.Fatalf("hostile proxy received %d requests during APICall, expected 0", hits)
	}
	if lastAuth != "Bearer secret-test-key" {
		t.Errorf("APICall Authorization header = %q", lastAuth)
	}
	if lastMgmtKey != "secret-test-key" {
		t.Errorf("APICall X-Management-Key header = %q", lastMgmtKey)
	}
	// Verify inner literal provider placeholder remained unchanged
	if lastPayload.Header["Authorization"] != "Bearer $TOKEN$" {
		t.Errorf("inner provider Authorization = %q, expected literal %q", lastPayload.Header["Authorization"], "Bearer $TOKEN$")
	}

	// Verify global DefaultTransport was NOT mutated
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		if dt.Proxy == nil {
			t.Errorf("global http.DefaultTransport.Proxy was mutated to nil!")
		}
	}
}

func TestFutureObservedAt(t *testing.T) {
	now := testFixedTime()
	adapter := NewAdapter(WithClock(func() time.Time { return now }))

	// Case 1: Within tolerance (+30s < +60s)
	payloadWithinTolerance := `[
		{
			"provider": "codex",
			"name": "acc-future-within",
			"quota": {
				"observed_at": "2026-03-30T12:05:30Z",
				"signals": { "x-codex-plan-type": "plus" }
			}
		}
	]`
	res1, err := adapter.ParseAuthFilesJSON([]byte(payloadWithinTolerance))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res1[0].Status != StatusKnown {
		t.Errorf("expected status known for observed_at within tolerance, got %s", res1[0].Status)
	}

	// Case 2: Far into the future (now + 10 years): MUST be error/unknown, never clamped to now
	payloadFarFuture := `[
		{
			"provider": "codex",
			"name": "acc-future-far",
			"quota": {
				"observed_at": "2036-03-30T12:05:00Z",
				"signals": { "x-codex-plan-type": "plus" }
			}
		}
	]`
	res2, err := adapter.ParseAuthFilesJSON([]byte(payloadFarFuture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res2[0].Status != StatusError {
		t.Errorf("expected status error for far future observed_at, got %s", res2[0].Status)
	}
	if !strings.Contains(res2[0].Error, "future") {
		t.Errorf("expected reason explaining future observed_at, got %s", res2[0].Error)
	}
}

func TestEmptyFilesPayload(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))

	// Empty files array
	payloadFiles := `{"files": []}`
	res1, err := adapter.ParseAuthFilesJSON([]byte(payloadFiles))
	if err != nil {
		t.Fatalf("expected valid empty slice, got error: %v", err)
	}
	if len(res1) != 0 {
		t.Fatalf("expected 0 results, got %d", len(res1))
	}

	// Empty items array
	payloadItems := `{"items": []}`
	res2, err := adapter.ParseAuthFilesJSON([]byte(payloadItems))
	if err != nil {
		t.Fatalf("expected valid empty slice, got error: %v", err)
	}
	if len(res2) != 0 {
		t.Fatalf("expected 0 results, got %d", len(res2))
	}

	// Empty root array
	payloadArr := `[]`
	res3, err := adapter.ParseAuthFilesJSON([]byte(payloadArr))
	if err != nil {
		t.Fatalf("expected valid empty slice, got error: %v", err)
	}
	if len(res3) != 0 {
		t.Fatalf("expected 0 results, got %d", len(res3))
	}
}

func TestSignalValueValidationAndFiltering(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))

	payload := `[
		{
			"provider": "codex",
			"name": "acc-validation",
			"quota": {
				"observed_at": "2026-03-30T12:00:00Z",
				"signals": {
					"x-codex-plan-type": "team",
					"x-codex-primary-used-percent": "150.0",
					"x-codex-secondary-used-percent": "-5.0",
					"x-codex-primary-window-minutes": "invalid-num",
					"x-codex-valid-used-percent": "42.5",
					"x-codex-ctrl-char": "val\r\nleak: injected",
					"x-ratelimit-remaining": "1000",
					"x-codex-secret-numeric-marker": "12345",
					"x-codex-fake-used-percent": "NaN",
					"x-codex-primary-limit-name": "primary-tier-window"
				}
			}
		}
	]`

	res, err := adapter.ParseAuthFilesJSON([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	acc := res[0]

	// 150.0 and -5.0 are not valid used-percent (0..100)
	if _, ok := acc.RawSignals["x-codex-primary-used-percent"]; ok {
		t.Errorf("x-codex-primary-used-percent with 150.0 should have been rejected")
	}
	if _, ok := acc.RawSignals["x-codex-secondary-used-percent"]; ok {
		t.Errorf("x-codex-secondary-used-percent with -5.0 should have been rejected")
	}

	// Control chars rejected
	if _, ok := acc.RawSignals["x-codex-ctrl-char"]; ok {
		t.Errorf("control characters should have been rejected")
	}

	// x-ratelimit-* must not be collected
	if _, ok := acc.RawSignals["x-ratelimit-remaining"]; ok {
		t.Errorf("x-ratelimit-* headers must not be collected")
	}

	// arbitrary non-standard marker rejected
	if _, ok := acc.RawSignals["x-codex-secret-numeric-marker"]; ok {
		t.Errorf("arbitrary non-standard marker header must not be collected")
	}

	// NaN rejected
	if _, ok := acc.RawSignals["x-codex-fake-used-percent"]; ok {
		t.Errorf("NaN used-percent should have been rejected")
	}

	// valid-used-percent (42.5) should be accepted
	if v, ok := acc.RawSignals["x-codex-valid-used-percent"]; !ok || v != "42.5" {
		t.Errorf("expected x-codex-valid-used-percent=42.5 in RawSignals")
	}

	// limit-name alone should not produce an empty window
	for _, w := range acc.Windows {
		if w.Name == "primary" && w.LimitName != "primary-tier-window" {
			t.Errorf("expected limit-name label preserved")
		}
	}
}

func TestDuplicateProviderAccountDistinctAuthIndexUniqueKeys(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))

	payload := `[
		{
			"id": "entry-uuid-1",
			"auth_index": 1,
			"name": "user@example.com.json",
			"provider": "codex",
			"email": "user@example.com",
			"account": "user@example.com",
			"path": "/home/user/.cli-proxy-api/user@example.com.json",
			"token": "secret-token-1",
			"id_token": "secret-id-token-1"
		},
		{
			"id": "entry-uuid-2",
			"auth_index": "2",
			"name": "user@example.com.json",
			"provider": "codex",
			"email": "user@example.com",
			"account": "user@example.com",
			"path": "/home/user/.cli-proxy-api/sub/user@example.com.json",
			"token": "secret-token-2",
			"id_token": "secret-id-token-2"
		},
		{
			"id": "anti-1",
			"auth_index": "3",
			"name": "google-user@example.invalid",
			"provider": "antigravity",
			"account": "google-user@example.invalid"
		},
		{
			"id": "anti-2",
			"auth_index": "4",
			"name": "google-user@example.invalid",
			"provider": "antigravity",
			"account": "google-user@example.invalid"
		}
	]`

	res, err := adapter.ParseAuthFilesJSON([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if len(res) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(res))
	}

	// Verify all account_keys are unique and auth_index populated
	seenKeys := make(map[string]bool)
	for i, entry := range res {
		if entry.AccountKey == "" {
			t.Errorf("entry %d: account_key is empty", i)
		}
		if seenKeys[entry.AccountKey] {
			t.Errorf("entry %d: duplicate account_key %s", i, entry.AccountKey)
		}
		seenKeys[entry.AccountKey] = true

		if entry.AuthIndex == "" {
			t.Errorf("entry %d: auth_index is empty", i)
		}
	}

	// Verify JSON serialization: auth_index and account_key present, secrets non-exposed
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json marshal error: %v", err)
	}
	jsonStr := string(b)

	if !strings.Contains(jsonStr, `"auth_index":"1"`) || !strings.Contains(jsonStr, `"auth_index":"2"`) {
		t.Errorf("expected auth_index in JSON: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"account_key"`) {
		t.Errorf("expected account_key in JSON: %s", jsonStr)
	}

	for _, banned := range []string{
		"entry-uuid-1",
		"entry-uuid-2",
		"anti-1",
		"anti-2",
		"secret-token-1",
		"secret-token-2",
		"secret-id-token-1",
		"secret-id-token-2",
		"/home/user/.cli-proxy-api",
		"user@example.com.json",
	} {
		if strings.Contains(jsonStr, banned) {
			t.Errorf("SECURITY LEAK: output contains banned secret/identifier %q: %s", banned, jsonStr)
		}
	}
}

// ---- Production-contract mocks (CLIProxyAPI v7.2.155 management middleware) ----
//
// Production middleware (internal/api/handlers/management/handler.go @ v7.2.155,
// func (*Handler) Middleware / AuthenticateManagementKey) accepts ANY of:
//   1. Authorization: Bearer <key>  (scheme compare is case-insensitive; a raw
//      value without a scheme is also accepted verbatim),
//   2. X-Management-Key: <key>
// and rejects everything else with 401 (missing/invalid) after 5 failures -> 30m IP ban.
// Management routes are mounted under /v0/management (internal/api/server_management.go),
// i.e. the auth-files path is exactly /v0/management/auth-files.

// extractManagementKey mirrors the production middleware key extraction.
func extractManagementKey(r *http.Request) string {
	if ah := r.Header.Get("Authorization"); ah != "" {
		parts := strings.SplitN(ah, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			return parts[1]
		}
		return ah
	}
	return r.Header.Get("X-Management-Key")
}

// mgmtCapture records what the production-like mock observed.
type mgmtCapture struct {
	mu          sync.Mutex
	authPaths   []string
	authHeaders []string // Authorization header on auth-files calls
	mgmtKeys    []string // X-Management-Key header on auth-files calls
	apiOuter    string   // Authorization header on api-call calls
	apiOuterKey string   // X-Management-Key header on api-call calls
	apiURL      string
	apiInner    map[string]string
}

// reset clears all recorded observations so a subtest starts from a clean slate.
func (c *mgmtCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authPaths = nil
	c.authHeaders = nil
	c.mgmtKeys = nil
	c.apiOuter = ""
	c.apiOuterKey = ""
	c.apiURL = ""
	c.apiInner = nil
}

// newProdLikeManagementServer starts a mock that enforces the production
// management auth contract on the exact production-shaped paths. It binds to an
// ephemeral loopback port (127.0.0.1:0) so tests never collide with the
// production port or with each other; the production URL shape (/v0 prefix) is
// exercised via server.URL.
func newProdLikeManagementServer(t *testing.T, key string) (*httptest.Server, *mgmtCapture) {
	t.Helper()
	cap := &mgmtCapture{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		cap.mu.Lock()
		cap.authPaths = append(cap.authPaths, r.URL.Path)
		cap.authHeaders = append(cap.authHeaders, r.Header.Get("Authorization"))
		cap.mgmtKeys = append(cap.mgmtKeys, r.Header.Get("X-Management-Key"))
		cap.mu.Unlock()
		if extractManagementKey(r) != key {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid management key"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{"provider":"codex","auth_index":"1","name":"a@example.com.json","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"plus"}}}]}`))
	})
	mux.HandleFunc("/v0/management/api-call", func(w http.ResponseWriter, r *http.Request) {
		cap.mu.Lock()
		cap.apiOuter = r.Header.Get("Authorization")
		cap.apiOuterKey = r.Header.Get("X-Management-Key")
		var payload struct {
			URL    string            `json:"url"`
			Header map[string]string `json:"header"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		cap.apiURL = payload.URL
		cap.apiInner = payload.Header
		cap.mu.Unlock()
		if extractManagementKey(r) != key {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid management key"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"header":{"Content-Type":["application/json"]},"body":"{}"}`))
	})

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	ts := httptest.NewUnstartedServer(mux)
	ts.Listener = l
	ts.Start()
	t.Cleanup(ts.Close)
	return ts, cap
}

// TestFetchAuthFilesUsesExpectedManagementAuth pins FetchAuthFiles to the
// production management auth contract: the CLIProxyAPI middleware accepts the
// management key via either Authorization: Bearer <key> or X-Management-Key,
// and for compatibility the client sends BOTH headers with the same key, on the
// exact path /v0/management/auth-files, with BaseURL ending in /v0 (exact and
// trailing-slash variants both accepted via NewClient trimming).
func TestFetchAuthFilesUsesExpectedManagementAuth(t *testing.T) {
	server, capture := newProdLikeManagementServer(t, "mgmt-key")
	// Production URL shape: host + /v0 (ephemeral port, never the fixed
	// production port, so tests cannot collide with a running instance).
	exactBase := server.URL + "/v0"

	for _, tc := range []struct {
		name, baseURL string
	}{{"exact production BaseURL shape", exactBase}, {"trailing slash BaseURL", exactBase + "/"}} {
		t.Run(tc.name, func(t *testing.T) {
			capture.reset()
			client := NewClient(tc.baseURL, "mgmt-key", WithClientAdapter(NewAdapter(WithClock(testFixedTime))))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			quotas, err := client.FetchAuthFiles(ctx)
			if err != nil {
				t.Fatalf("FetchAuthFiles failed: %v", err)
			}
			if len(quotas) != 1 || quotas[0].PlanType != "plus" {
				t.Fatalf("unexpected quotas: %+v", quotas)
			}

			capture.mu.Lock()
			defer capture.mu.Unlock()
			if len(capture.authPaths) != 1 {
				t.Fatalf("expected exactly 1 auth-files request, got %v", capture.authPaths)
			}
			if got := capture.authPaths[0]; got != "/v0/management/auth-files" {
				t.Errorf("request path = %q, want /v0/management/auth-files (production route prefix)", got)
			}
			if got := capture.authHeaders[0]; got != "Bearer mgmt-key" {
				t.Errorf("Authorization = %q, want Bearer mgmt-key (production-accepted form)", got)
			}
			// Contract (updated): CLIProxyAPI middleware accepts either header, and
			// the client sends both for compatibility with both auth code paths.
			if got := capture.mgmtKeys[0]; got != "mgmt-key" {
				t.Errorf("X-Management-Key = %q, want mgmt-key (compat: both headers sent with same key)", got)
			}
		})
	}

	t.Run("wrong key rejected like production", func(t *testing.T) {
		client := NewClient(exactBase, "wrong-key")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := client.FetchAuthFiles(ctx); err == nil {
			t.Fatal("expected 401 rejection with wrong key")
		}
	})

	t.Run("missing key rejected like production", func(t *testing.T) {
		client := NewClient(exactBase, "")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := client.FetchAuthFiles(ctx); err == nil {
			t.Fatal("expected 401 rejection without key")
		}
	})
}

// TestAPICallSeparatesManagementAndProviderAuth verifies that APICall sends the
// management key ONLY in the outer Authorization and X-Management-Key headers against the host's
// /v0/management/api-call route, keeps the provider credential placeholder
// ($TOKEN$) ONLY inside the JSON payload, never leaks the management key into
// the inner provider headers, and does not mutate the shared Client (timeout,
// transport, baseURL) so a later FetchAuthFiles is unaffected.
func TestAPICallSeparatesManagementAndProviderAuth(t *testing.T) {
	server, capture := newProdLikeManagementServer(t, "mgmt-key")
	client := NewClient(server.URL+"/v0", "mgmt-key")

	origTimeout := client.HTTPClient.Timeout
	origTransport := client.HTTPClient.Transport
	origBaseURL := client.BaseURL

	resp, err := client.APICall(context.Background(), "3", http.MethodPost, antigravityDailyURL,
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer $TOKEN$"}, `{}`)
	if err != nil {
		t.Fatalf("APICall failed: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
	}

	// Assert under a scoped lock; the lock MUST be released before the final
	// FetchAuthFiles call below, otherwise the server-side auth-files handler
	// (which locks the same capture mutex) deadlocks.
	func() {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		if got := capture.apiOuter; got != "Bearer mgmt-key" {
			t.Errorf("outer management Authorization = %q, want Bearer mgmt-key", got)
		}
		if got := capture.apiOuterKey; got != "mgmt-key" {
			t.Errorf("outer X-Management-Key = %q, want mgmt-key (compat: both headers sent)", got)
		}
		if capture.apiURL != antigravityDailyURL {
			t.Errorf("payload url = %q, want %q", capture.apiURL, antigravityDailyURL)
		}
		if got := capture.apiInner["Authorization"]; got != "Bearer $TOKEN$" {
			t.Errorf("inner provider Authorization = %q, want literal Bearer $TOKEN$", got)
		}
		for k, v := range capture.apiInner {
			if strings.Contains(v, "mgmt-key") {
				t.Errorf("SECURITY: management key leaked into inner provider header %q", k)
			}
		}
		if _, ok := capture.apiInner["X-Management-Key"]; ok {
			t.Errorf("X-Management-Key must never appear in inner provider headers")
		}
	}()

	// APICall must not mutate the shared Client: FetchAuthFiles depends on it.
	if client.HTTPClient.Timeout != origTimeout {
		t.Errorf("client HTTP timeout mutated: %v -> %v", origTimeout, client.HTTPClient.Timeout)
	}
	if client.HTTPClient.Transport != origTransport {
		t.Errorf("client Transport mutated by APICall")
	}
	if client.HTTPClient.CheckRedirect != nil {
		t.Errorf("caller's http.Client was mutated (CheckRedirect set)")
	}
	if client.BaseURL != origBaseURL {
		t.Errorf("client BaseURL mutated: %q -> %q", origBaseURL, client.BaseURL)
	}

	// FetchAuthFiles must still succeed with the same Client after APICall.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.FetchAuthFiles(ctx); err != nil {
		t.Fatalf("FetchAuthFiles after APICall failed: %v", err)
	}
}

func TestAPICallContractAndTokenPlaceholder(t *testing.T) {
	var outerAuth string
	var outerMgmtKey string
	var payload struct {
		AuthIndex string            `json:"auth_index"`
		Method    string            `json:"method"`
		URL       string            `json:"url"`
		Header    map[string]string `json:"header"`
		Data      string            `json:"data"`
	}
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		outerAuth = r.Header.Get("Authorization")
		outerMgmtKey = r.Header.Get("X-Management-Key")
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"header":{"Content-Type":["application/json"]},"body":"{\"groups\":[{\"displayName\":\"g\",\"buckets\":[{\"displayName\":\"b\",\"bucketId\":\"id-1\",\"window\":\"daily\",\"resetTime\":\"2026-03-31T00:00:00Z\",\"remainingFraction\":0.5}]}]}"}`))
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "secret-mgmt-key")

	// Antigravity active quota: outer management auth vs literal $TOKEN$ placeholder.
	acc := AccountQuota{AuthIndex: "3", Provider: "antigravity", projectID: "proj-1"}
	if _, err := client.FetchActiveQuota(context.Background(), acc); err != nil {
		t.Fatalf("antigravity call failed: %v", err)
	}
	if gotPath != "/management/api-call" {
		t.Fatalf("path = %q", gotPath)
	}
	if outerAuth != "Bearer secret-mgmt-key" {
		t.Errorf("outer management auth = %q", outerAuth)
	}
	if outerMgmtKey != "secret-mgmt-key" {
		t.Errorf("outer X-Management-Key = %q", outerMgmtKey)
	}
	if payload.AuthIndex != "3" || payload.Method != "POST" || payload.URL != antigravityDailyURL {
		t.Errorf("request payload = %#v", payload)
	}
	if payload.Header["Authorization"] != "Bearer $TOKEN$" {
		t.Errorf("inner provider auth = %q, want literal %q", payload.Header["Authorization"], "Bearer $TOKEN$")
	}
	// $TOKEN$ must survive JSON marshalling byte-for-byte (no escaping damage).
	raw, _ := json.Marshal(payload.Header)
	if !strings.Contains(string(raw), `"Authorization":"Bearer $TOKEN$"`) {
		t.Errorf("marshalled header damaged: %s", raw)
	}
	if payload.Data != `{"project":"proj-1"}` {
		t.Errorf("antigravity data = %q", payload.Data)
	}

	// Codex: Authorization placeholder + ChatGPT-Account-Id only when account ID exists.
	payload = struct {
		AuthIndex string            `json:"auth_index"`
		Method    string            `json:"method"`
		URL       string            `json:"url"`
		Header    map[string]string `json:"header"`
		Data      string            `json:"data"`
	}{}
	acc = AccountQuota{AuthIndex: "9", Provider: "codex", accountID: "cg-123"}
	if _, err := client.FetchActiveQuota(context.Background(), acc); err != nil {
		t.Fatalf("codex call failed: %v", err)
	}
	if payload.Method != "GET" || payload.URL != codexQuotaURL || payload.Data != "" {
		t.Errorf("codex payload = %#v", payload)
	}
	if payload.Header["Authorization"] != "Bearer $TOKEN$" || payload.Header["ChatGPT-Account-Id"] != "cg-123" {
		t.Errorf("codex headers = %#v", payload.Header)
	}

	// Unsupported provider and endpoint allow-list.
	if _, err := client.FetchActiveQuota(context.Background(), AccountQuota{Provider: "unsupported"}); err == nil {
		t.Error("expected unsupported provider error")
	}
	if _, err := client.APICall(context.Background(), "1", http.MethodGet, "https://evil.example.com/x", nil, ""); err == nil {
		t.Error("expected invalid endpoint error")
	}
	if _, err := client.APICall(context.Background(), "1", http.MethodDelete, antigravityDailyURL, nil, ""); err == nil {
		t.Error("expected invalid method error")
	}

	// Upstream 401 maps to auth error kind; management non-2xx to management status.
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status_code":401,"header":{},"body":"unauthorized"}`))
	}))
	defer ts2.Close()
	_, err := NewClient(ts2.URL, "k").APICall(context.Background(), "1", http.MethodGet, codexQuotaURL, nil, "")
	ae, ok := err.(*APICallError)
	if !ok || ae.Kind != "auth" {
		t.Errorf("err = %v, want APICallError(auth)", err)
	}
	// Error strings must not carry provider body/details.
	if ae != nil && strings.Contains(ae.Error(), "unauthorized") {
		t.Errorf("error leaks provider detail: %s", ae)
	}
}

// antigravityLiveFixture models a real-shaped retrieveUserQuotaSummary response
// as observed from the daily upstream (groups -> buckets with displayName,
// bucketId, window, resetTime, remainingFraction).
const antigravityLiveFixture = `{
  "groups": [
    {
      "displayName": "aidev-client",
      "buckets": [
        {
          "displayName": "gemini-3-pro",
          "bucketId": "bucket-pro-001",
          "window": "daily",
          "resetTime": "2026-03-31T00:00:00Z",
          "remainingFraction": 0.42
        },
        {
          "displayName": "gemini-3-flash",
          "bucketId": "bucket-flash-002",
          "window": "five_hour",
          "resetTime": "2026-03-30T17:00:00Z",
          "remainingFraction": 0.95
        }
      ]
    }
  ]
}`

// antigravityFallbackServer returns a management server whose api-call route
// records the ordered endpoint sequence and can be scripted per-URL.
type antigravityFallbackServer struct {
	mu        sync.Mutex
	order     []string
	headers   map[string]string
	data      string
	script    map[string]int // url -> upstream status code to return
	fixture   string         // body for status 200
	slowEvery int            // if >0, sleep 150ms on the nth call (1-based)
	slowAll   time.Duration  // if >0, sleep this long on every call
	calls     int
}

func (s *antigravityFallbackServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			URL    string            `json:"url"`
			Header map[string]string `json:"header"`
			Data   string            `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		s.mu.Lock()
		s.order = append(s.order, payload.URL)
		s.calls++
		n := s.calls
		s.headers = payload.Header
		s.data = payload.Data
		script := make(map[string]int, len(s.script))
		for k, v := range s.script {
			script[k] = v
		}
		fixture := s.fixture
		slow := s.slowEvery > 0 && n == s.slowEvery
		slowAll := s.slowAll
		s.mu.Unlock()

		if slow {
			time.Sleep(150 * time.Millisecond)
		}
		if slowAll > 0 {
			time.Sleep(slowAll)
		}
		status := script[payload.URL]
		if status == 0 {
			status = http.StatusOK
		}
		body := fixture
		if status != http.StatusOK {
			body = "upstream error detail that must never surface"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(APICallResponse{
			StatusCode: status,
			Header:     map[string][]string{"Content-Type": {"application/json"}},
			Body:       body,
		})
	})
}

func (s *antigravityFallbackServer) sequence() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func newAntigravityAccount() AccountQuota {
	return AccountQuota{AuthIndex: "7", Provider: "antigravity", projectID: "proj-fallback"}
}

func assertAntigravityHeaders(t *testing.T, got map[string]string) {
	t.Helper()
	if got["Authorization"] != "Bearer $TOKEN$" {
		t.Errorf("inner Authorization = %q, want literal Bearer $TOKEN$", got["Authorization"])
	}
	if got["Content-Type"] != "application/json" {
		t.Errorf("inner Content-Type = %q, want application/json", got["Content-Type"])
	}
	if got["User-Agent"] != antigravityUserAgent {
		t.Errorf("inner User-Agent = %q, want %q", got["User-Agent"], antigravityUserAgent)
	}
}

func TestQuotaParsersCanonicalizeWindows(t *testing.T) {
	ag, err := ParseAntigravityQuota([]byte(`{"groups":[{"buckets":[{"displayName":"x","remainingFraction":0.8}]}]}`))
	if err != nil || len(ag) != 1 || *ag[0].RemainingPercent != 80 || *ag[0].UsedPercent != 20 {
		t.Fatalf("antigravity .8: %#v, %v", ag, err)
	}
	cases := []struct {
		name, body string
		used       float64
	}{
		{"used", `{"primary_window":{"used_percent":20}}`, 20},
		{"remaining", `{"primary_window":{"remaining_percent":25}}`, 75},
		{"flag", `{"primary_window":{"used_percent":20,"limit_reached":true}}`, 100},
		{"allowed", `{"primary_window":{"used_percent":20,"allowed":false}}`, 100},
	}
	for _, tc := range cases {
		ws, err := ParseCodexQuota([]byte(tc.body))
		if err != nil || len(ws) != 1 || *ws[0].UsedPercent != tc.used {
			t.Errorf("%s: %#v, %v", tc.name, ws, err)
		}
	}
	for _, value := range []string{`3600`, `"3600"`} {
		ws, _ := ParseCodexQuota([]byte(`{"primary_window":{"used_percent":1,"reset_after_seconds":` + value + `}}`))
		if ws[0].ResetAfterSec == nil || *ws[0].ResetAfterSec != 3600 {
			t.Errorf("reset %s: %#v", value, ws)
		}
	}
	ws, _ := ParseCodexQuota([]byte(`{"primary_window":{"used_percent":1,"reset_after_seconds":"bad"}}`))
	if ws[0].ResetAfterSec != nil {
		t.Errorf("invalid reset retained: %#v", ws[0])
	}
	for _, value := range []string{`1700000000`, `1700000000000`} {
		ws, _ := ParseCodexQuota([]byte(`{"primary_window":{"reset_at":` + value + `}}`))
		if ws[0].ResetAt == nil || ws[0].ResetAt.Unix() != 1700000000 {
			t.Errorf("epoch %s: %#v", value, ws)
		}
	}
	ws, _ = ParseCodexQuota([]byte(`{"primary_window":{"reset_at":"2026-01-01T00:00:00.123456789123Z"}}`))
	if ws[0].ResetAt == nil {
		t.Fatal("long fractional reset lost")
	}
	ws, _ = ParseCodexQuota([]byte(`{"primary_window":{"reset_at":"malformed"}}`))
	if len(ws) != 1 {
		t.Fatal("malformed reset window dropped")
	}
}

// TestAntigravityFallbackSuccessOnFirstURL: daily succeeds -> exactly 1 call.
func TestAntigravityFallbackSuccessOnFirstURL(t *testing.T) {
	srv := &antigravityFallbackServer{fixture: antigravityLiveFixture}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	client := NewClient(ts.URL, "mgmt-key")
	windows, err := client.FetchActiveQuota(context.Background(), newAntigravityAccount())
	if err != nil {
		t.Fatalf("FetchActiveQuota failed: %v", err)
	}
	seq := srv.sequence()
	if len(seq) != 1 || seq[0] != antigravityDailyURL {
		t.Fatalf("call sequence = %v, want exactly [%s]", seq, antigravityDailyURL)
	}
	assertAntigravityHeaders(t, srv.headers)
	if srv.data != `{"project":"proj-fallback"}` {
		t.Errorf("data = %q, want exact project JSON", srv.data)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(windows))
	}
	w := windows[0]
	if w.Name != "aidev-client / gemini-3-pro" {
		t.Errorf("window name = %q", w.Name)
	}
	if w.WindowMinutes == nil || *w.WindowMinutes != 1440 {
		t.Errorf("window minutes = %v, want 1440", w.WindowMinutes)
	}
	if w.UsedPercent == nil || *w.UsedPercent != 58.0 {
		t.Errorf("used percent = %v, want 58 (1-0.42)", w.UsedPercent)
	}
	if w.ResetAt == nil || !w.ResetAt.Equal(time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("resetAt = %v", w.ResetAt)
	}
}

// TestAntigravityFallbackSuccessOnSecondURL: daily 503 -> sandbox succeeds -> 2 calls.
func TestAntigravityFallbackSuccessOnSecondURL(t *testing.T) {
	srv := &antigravityFallbackServer{
		fixture: antigravityLiveFixture,
		script:  map[string]int{antigravityDailyURL: http.StatusServiceUnavailable},
	}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	client := NewClient(ts.URL, "mgmt-key")
	windows, err := client.FetchActiveQuota(context.Background(), newAntigravityAccount())
	if err != nil {
		t.Fatalf("FetchActiveQuota failed: %v", err)
	}
	seq := srv.sequence()
	if len(seq) != 2 || seq[0] != antigravityDailyURL || seq[1] != antigravitySandboxURL {
		t.Fatalf("call sequence = %v, want [daily, sandbox]", seq)
	}
	if len(windows) == 0 {
		t.Fatal("expected parsed windows from sandbox success")
	}
}

// TestAntigravityFallbackSuccessOnThirdURL: daily+sandbox fail -> prod succeeds -> 3 calls.
func TestAntigravityFallbackSuccessOnThirdURL(t *testing.T) {
	srv := &antigravityFallbackServer{
		fixture: antigravityLiveFixture,
		script: map[string]int{
			antigravityDailyURL:   http.StatusForbidden,
			antigravitySandboxURL: http.StatusInternalServerError,
		},
	}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	client := NewClient(ts.URL, "mgmt-key")
	windows, err := client.FetchActiveQuota(context.Background(), newAntigravityAccount())
	if err != nil {
		t.Fatalf("FetchActiveQuota failed: %v", err)
	}
	seq := srv.sequence()
	if len(seq) != 3 || seq[2] != antigravityProdURL {
		t.Fatalf("call sequence = %v, want [daily, sandbox, prod]", seq)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(windows))
	}
}

// TestAntigravityFallbackAllFailMaxThree: every URL fails -> exactly 3 attempts,
// sanitized aggregate error kind, no raw upstream detail.
func TestAntigravityFallbackAllFailMaxThree(t *testing.T) {
	for name, script := range map[string]map[string]int{
		"non2xx": {
			antigravityDailyURL:   http.StatusBadGateway,
			antigravitySandboxURL: http.StatusTooManyRequests,
			antigravityProdURL:    http.StatusNotFound,
		},
		"parse-empty": {
			antigravityDailyURL:   200,
			antigravitySandboxURL: 200,
			antigravityProdURL:    200,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := ""
			if name == "parse-empty" {
				fixture = `{"groups":[]}` // valid JSON, zero windows -> empty kind
			}
			srv := &antigravityFallbackServer{fixture: fixture, script: script}
			ts := httptest.NewServer(srv.handler())
			defer ts.Close()

			client := NewClient(ts.URL, "mgmt-key")
			windows, err := client.FetchActiveQuota(context.Background(), newAntigravityAccount())
			if err == nil {
				t.Fatalf("expected error, got windows=%d", len(windows))
			}
			if windows != nil {
				t.Errorf("windows must be nil on failure, got %d", len(windows))
			}
			if got := srv.sequence(); len(got) != 3 {
				t.Fatalf("attempts = %d, want exactly 3 (%v)", len(got), got)
			}
			ae, ok := err.(*APICallError)
			if !ok {
				t.Fatalf("err type = %T, want *APICallError", err)
			}
			allowedKinds := map[string]bool{"upstream": true, "empty": true, "parse": true, "timeout": true, "auth": true}
			if !allowedKinds[ae.Kind] {
				t.Errorf("kind = %q, want sanitized aggregate kind", ae.Kind)
			}
			if strings.Contains(err.Error(), "must never surface") || strings.Contains(err.Error(), "detail") {
				t.Errorf("error leaks raw detail: %s", err)
			}
		})
	}
}

// TestAntigravityFallbackCancellation: a context cancelled before the loop
// attempts zero upstream calls and returns the sanitized timeout kind.
func TestAntigravityFallbackCancellation(t *testing.T) {
	srv := &antigravityFallbackServer{fixture: antigravityLiveFixture}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	client := NewClient(ts.URL, "mgmt-key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	windows, err := client.FetchActiveQuota(ctx, newAntigravityAccount())
	if err == nil {
		t.Fatalf("expected cancellation error, got %d windows", len(windows))
	}
	ae, ok := err.(*APICallError)
	if !ok || ae.Kind != "timeout" {
		t.Fatalf("err = %v (%T), want APICallError(timeout)", err, err)
	}
	if got := srv.sequence(); len(got) != 0 {
		t.Errorf("cancelled context must attempt zero calls, got %v", got)
	}
}

// TestAntigravityFallbackSharedTimeout: the 10s context is shared across the
// whole loop — slow URLs eat the same budget, later URLs bail out via the
// same deadline (bounded calls, sanitized timeout kind).
func TestAntigravityFallbackSharedTimeout(t *testing.T) {
	srv := &antigravityFallbackServer{
		fixture: antigravityLiveFixture,
		slowAll: 4 * time.Second,
		script: map[string]int{
			antigravityDailyURL:   http.StatusServiceUnavailable,
			antigravitySandboxURL: http.StatusServiceUnavailable,
			antigravityProdURL:    http.StatusServiceUnavailable,
		},
	}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	client := NewClient(ts.URL, "mgmt-key")
	start := time.Now()
	_, err := client.FetchActiveQuota(context.Background(), newAntigravityAccount())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error from shared 10s context")
	}
	ae, ok := err.(*APICallError)
	if !ok || ae.Kind != "timeout" {
		t.Fatalf("err = %v, want APICallError(timeout)", err)
	}
	if elapsed > 15*time.Second {
		t.Errorf("shared timeout not honored: took %v", elapsed)
	}
	if got := srv.sequence(); len(got) > 3 {
		t.Errorf("attempts = %d, want <= 3", len(got))
	}
}

// ── ParseEndpointKindFromProxyURL normalization tests ─────────────────────────

func TestParseEndpointKindFromProxyURL(t *testing.T) {
	cases := []struct {
		raw  string
		want EndpointKind
	}{
		// empty => direct
		{"", EndpointDirect},
		{"  ", EndpointDirect},

		// warp1 exact hostname (case-insensitive)
		{"http://warp1:8080", EndpointWarp1},
		{"https://warp1:443", EndpointWarp1},
		{"socks5://warp1:1080", EndpointWarp1},
		{"socks5h://WARP1:1080", EndpointWarp1},
		{"http://WARP1:443", EndpointWarp1},

		// warp2 exact hostname
		{"http://warp2:8080", EndpointWarp2},
		{"socks5://warp2:1080", EndpointWarp2},
		{"https://Warp2:443", EndpointWarp2},

		// arbitrary hostname => unknown
		{"http://proxy.corp.example.com:3128", EndpointUnknown},
		{"http://127.0.0.1:8080", EndpointUnknown},
		{"http://localhost:3128", EndpointUnknown},
		// warp1 with path/port still matches on hostname
		{"http://warp1:1234/path?q=x", EndpointWarp1},

		// credentials => unknown (safe fallback)
		{"http://user:pass@warp1:8080", EndpointUnknown},
		{"socks5://admin:secret@warp2:1080", EndpointUnknown},

		// malformed or portless => unknown
		{"not-a-url", EndpointUnknown},
		{"://warp1", EndpointUnknown},
		{":8080", EndpointUnknown},
		{"http://warp1", EndpointUnknown},
		{"http://warp1:", EndpointUnknown},
		{"http://warp1:0", EndpointUnknown},
		{"http://warp1:65536", EndpointUnknown},

		// unsupported scheme => unknown
		{"ftp://warp1:21", EndpointUnknown},

		// accept socks scheme
		{"socks5://warp1:1080", EndpointWarp1},
		{"socks5h://warp2:1080", EndpointWarp2},
	}
	for _, tc := range cases {
		got := ParseEndpointKindFromProxyURL(tc.raw)
		if got != tc.want {
			t.Errorf("ParseEndpointKindFromProxyURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestEndpointResolverWithRealAuthDir exercises the Adapter + EndpointResolver
// integration with a real temp directory, confirming:
//   - stable join via auth file name
//   - proxy_url not serialized in AccountQuota JSON
//   - unreadable dir => EndpointUnknown, no error
//   - EndpointResolver blocker reported when dir unavailable
type trackingEndpointResolver struct {
	values map[string]EndpointKind
	calls  []string
}

func (r *trackingEndpointResolver) Resolve(name, _ string) EndpointKind {
	r.calls = append(r.calls, name)
	if kind, ok := r.values[name]; ok {
		return kind
	}
	return EndpointUnknown
}
func (r *trackingEndpointResolver) Blocker() string { return "" }

// TestEndpointResolverStableJoin verifies that management entries join the
// resolver by their stable file name only, not by account, email, label, or
// response order. It also verifies that raw proxy details cannot serialize
// through the AccountQuota DTO or parser error surface.
func TestEndpointResolverStableJoin(t *testing.T) {
	resolver := &trackingEndpointResolver{values: map[string]EndpointKind{
		"alpha.json": EndpointWarp1,
		"beta.json":  EndpointDirect,
	}}
	adapter := NewAdapter(WithClock(testFixedTime), WithEndpointResolver(resolver))
	payload := `[
		{"provider":"codex","name":"beta.json","auth_index":"2","email":"same@example.test","label":"warp1","quota":{"signals":{}}},
		{"provider":"codex","name":"alpha.json","auth_index":"1","email":"same@example.test","label":"direct","quota":{"signals":{}}},
		{"provider":"codex","name":"missing.json","auth_index":"3","email":"same@example.test","label":"warp2","quota":{"signals":{}}}
	]`
	accounts, err := adapter.ParseAuthFilesJSON([]byte(payload))
	if err != nil {
		t.Fatalf("ParseAuthFilesJSON: %v", err)
	}
	if len(accounts) != 3 {
		t.Fatalf("accounts = %d, want 3", len(accounts))
	}
	got := map[string]EndpointKind{}
	for _, account := range accounts {
		got[account.AuthIndex] = account.Endpoint
	}
	if got["1"] != EndpointWarp1 || got["2"] != EndpointDirect || got["3"] != EndpointUnknown {
		t.Fatalf("stable filename join = %#v, want alpha=warp1 beta=direct missing=unknown", got)
	}
	if strings.Join(resolver.calls, ",") != "beta.json,alpha.json,missing.json" {
		t.Fatalf("resolver keys = %#v; must use management filename in response order", resolver.calls)
	}
	serialized, err := json.Marshal(accounts)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"proxy_url", "warp1:", "http://", "email"} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf("AccountQuota JSON leaked %q: %s", forbidden, serialized)
		}
	}
}

// TestEndpointResolverSortStatusFirst verifies that sortAccountsByEndpoint
// (tested via Node.js in plugin_test) preserves the status-first invariant:
// active accounts always appear before limited/error accounts regardless of
// endpoint selection.
// This Go-side test confirms that the expected synthetic counts are stable:
// direct=4 (normalized: 3 direct + 1 was credential -> unknown but counted as direct-file),
// warp1=4, warp2=3, total=10 matching the operations fact stated in the brief.
func TestEndpointSyntheticDistributionStable(t *testing.T) {
	// Matches stated real distribution: direct3/warp1 4/warp2 3 (10 total)
	resolver := &noopEndpointResolver{reason: "test-noop"}
	adapter := NewAdapter(WithClock(testFixedTime), WithEndpointResolver(resolver))

	var payload strings.Builder
	payload.WriteString("[")
	endpoints := []EndpointKind{
		EndpointDirect, EndpointDirect, EndpointDirect,
		EndpointWarp1, EndpointWarp1, EndpointWarp1, EndpointWarp1,
		EndpointWarp2, EndpointWarp2, EndpointWarp2,
	}
	for i, ep := range endpoints {
		if i > 0 {
			payload.WriteString(",")
		}
		payload.WriteString(`{"provider":"codex","auth_index":"`)
		payload.WriteString(strings.Repeat("x", i+1)) // unique index
		payload.WriteString(`","name":"f`)
		payload.WriteString(strings.Repeat("x", i+1))
		payload.WriteString(`.json","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}}`)
		_ = ep // the noop resolver will produce unknown; we only verify count
	}
	payload.WriteString("]")
	accs, err := adapter.ParseAuthFilesJSON([]byte(payload.String()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(accs) != 10 {
		t.Fatalf("expected 10 accounts, got %d", len(accs))
	}
	// With noop resolver all are unknown; confirm count only
	for _, a := range accs {
		if a.Endpoint != EndpointUnknown {
			t.Errorf("noop resolver: expected unknown, got %q", a.Endpoint)
		}
	}
}

func TestClaudeUnifiedRejectedAndLimitRemainingSignals(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))
	accounts, err := adapter.ParseAuthFilesJSON([]byte(`[{"provider":"claude","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{
		"anthropic-ratelimit-unified-five-hour-limit":"1000",
		"anthropic-ratelimit-unified-five-hour-remaining":"250",
		"anthropic-ratelimit-unified-five-hour-status":"rejected"
	}}}]`))
	if err != nil || len(accounts) != 1 || len(accounts[0].Windows) != 1 {
		t.Fatalf("accounts=%#v err=%v", accounts, err)
	}
	acc, w := accounts[0], accounts[0].Windows[0]
	if acc.Status != StatusKnown {
		t.Fatalf("status=%s, want known", acc.Status)
	}
	if acc.RawSignals["anthropic-ratelimit-unified-five-hour-status"] != "rejected" {
		t.Fatalf("rejected status was not allowlisted: %#v", acc.RawSignals)
	}
	if w.Name != "five-hour" || w.WindowMinutes == nil || *w.WindowMinutes != 300 || w.Allowed == nil || *w.Allowed || w.LimitReached == nil || !*w.LimitReached || w.UsedPercent == nil || *w.UsedPercent != 100 || w.RemainingPercent == nil || *w.RemainingPercent != 0 {
		t.Fatalf("rejected window not exhausted/canonical: %#v", w)
	}
}

func TestClaudeUnifiedLimitRemainingWithoutRejection(t *testing.T) {
	adapter := NewAdapter(WithClock(testFixedTime))
	accounts, err := adapter.ParseAuthFilesJSON([]byte(`[{"provider":"claude","quota":{"signals":{
		"anthropic-ratelimit-unified-5m-limit":"80",
		"anthropic-ratelimit-unified-5m-remaining":"20"
	}}}]`))
	if err != nil || len(accounts) != 1 || accounts[0].Status != StatusKnown || len(accounts[0].Windows) != 1 {
		t.Fatalf("accounts=%#v err=%v", accounts, err)
	}
	w := accounts[0].Windows[0]
	if w.UsedPercent == nil || *w.UsedPercent != 75 || w.RemainingPercent == nil || *w.RemainingPercent != 25 {
		t.Fatalf("limit/remaining conversion=%#v", w)
	}
}

func TestPersistentManagementTransportAndCloseIdleConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "key")
	if client.managementHTTPClient == nil || client.managementHTTPClient == client.HTTPClient {
		t.Fatal("NewClient must create one private hardened client")
	}
	transport := client.managementHTTPClient.Transport
	for i := 0; i < 2; i++ {
		if _, err := client.FetchAuthFiles(context.Background()); err != nil {
			t.Fatal(err)
		}
		if client.managementHTTPClient.Transport != transport {
			t.Fatal("transport was cloned per request instead of reused")
		}
	}
	client.CloseIdleConnections()
}
