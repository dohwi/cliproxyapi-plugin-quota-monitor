package plugin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"quota-monitor/internal/dashboard"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func reset(t *testing.T) {
	t.Helper()
	Shutdown()
	state = &stateData{reason: "QUOTA_MONITOR_MGMT_URL and QUOTA_MONITOR_MGMT_KEY are required", probeDue: make(map[string]time.Time)}
	t.Cleanup(func() {
		Shutdown()
		os.Unsetenv("QUOTA_MONITOR_MGMT_URL")
		os.Unsetenv("QUOTA_MONITOR_MGMT_KEY")
	})
}
func decodeMgmt(t *testing.T, raw []byte) (int, map[string]any) {
	t.Helper()
	var e struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	var r managementResponse
	if err := json.Unmarshal(e.Result, &r); err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	if err := json.Unmarshal(r.Body, &b); err != nil {
		t.Fatal(err)
	}
	return r.StatusCode, b
}
func request(path string) []byte {
	b, _ := json.Marshal(managementRequest{Method: "GET", Path: path})
	return b
}

func TestUnconfigured(t *testing.T) {
	reset(t)
	configureFromEnv(nil)
	_, b := decodeMgmt(t, handleManagementHandle(request(mgmtBase+"/accounts")))
	if b["configured"] != false || len(b["accounts"].([]any)) != 0 || b["reason"] == "" {
		t.Fatalf("unexpected body: %#v", b)
	}
}

func TestMockFetchCacheAndNoSecret(t *testing.T) {
	reset(t)
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer top-secret" {
			t.Errorf("missing delegated key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"provider":"codex","alias":"acct","token":"do-not-return","quota":{"observed_at":"2025-01-01T00:00:00Z","signals":{"x-codex-plan-type":"pro","authorization":"do-not-return"}}}]`))
	}))
	defer ts.Close()
	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "top-secret")
	configureFromEnv(nil)
	for i := 0; i < 2; i++ {
		_, b := decodeMgmt(t, handleManagementHandle(request(mgmtBase+"/accounts")))
		raw, _ := json.Marshal(b)
		s := string(raw)
		if strings.Contains(s, "top-secret") || strings.Contains(s, "do-not-return") || strings.Contains(s, ts.URL) {
			t.Fatalf("secret/url leaked: %s", s)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d want 2 (auth-files + usage records), then cached", calls.Load())
	}
}

func TestCoalescing(t *testing.T) {
	reset(t)
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer ts.Close()
	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	configureFromEnv(nil)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, _ = getAccounts() }()
	}
	wg.Wait()
	if calls.Load() != 2 {
		t.Fatalf("calls=%d want 2 coalesced upstream calls", calls.Load())
	}
}

func TestErrorReturnsStaleLastGoodPreservingObservedAt(t *testing.T) {
	reset(t)
	var fail atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "secret upstream detail", 500)
			return
		}
		_, _ = w.Write([]byte(`[{"provider":"codex","alias":"acct","quota":{"observed_at":"2025-01-01T00:00:00Z","signals":{"x-codex-plan-type":"pro"}}}]`))
	}))
	defer ts.Close()
	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	configureFromEnv(nil)
	good, _, err := getAccounts()
	if err != nil || len(good) != 1 || good[0].ObservedAt == nil {
		t.Fatalf("good=%#v err=%v", good, err)
	}
	observed := *good[0].ObservedAt
	state.mu.Lock()
	state.cachedAt = time.Time{}
	state.mu.Unlock()
	fail.Store(true)
	stale, isStale, err := getAccounts()
	if err == nil || !isStale || len(stale) != 1 {
		t.Fatalf("stale=%#v flag=%v err=%v", stale, isStale, err)
	}
	if stale[0].Status != "stale" || stale[0].ObservedAt == nil || !stale[0].ObservedAt.Equal(observed) {
		t.Fatalf("last-good not preserved: %#v", stale[0])
	}
	raw, _ := json.Marshal(stale)
	if strings.Contains(string(raw), "secret upstream detail") {
		t.Fatal("upstream error leaked")
	}
}

// TestAccountsAPIPreservesTenAccountsWithDuplicateLabels pins the B5 contract:
// the plugin state must preserve ALL 10 entries (no dedup by account label or
// account_key), each with a distinct auth_index-derived account_key, and the
// /accounts API must return the full array. Filter defaults in the dashboard
// (provider=all, status=all) then render all 10 cards.
func TestAccountsAPIPreservesTenAccountsWithDuplicateLabels(t *testing.T) {
	reset(t)
	var b strings.Builder
	b.WriteString(`[`)
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteString(`,`)
		}
		// All 10 share the same account label to force display suffixing;
		// only auth_index distinguishes them.
		_, _ = fmt.Fprintf(&b, `{"provider":"codex","auth_index":"%d","account":"dup@example.com","email":"dup@example.com","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}}`, i+1)
	}
	b.WriteString(`]`)
	payload := b.String()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer ts.Close()

	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	configureFromEnv(nil)

	status, body := decodeMgmt(t, handleManagementHandle(request(mgmtBase+"/accounts")))
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	arr, ok := body["accounts"].([]any)
	if !ok {
		t.Fatalf("accounts missing: %#v", body["accounts"])
	}
	if len(arr) != 10 {
		t.Fatalf("expected all 10 accounts preserved, got %d", len(arr))
	}
	seenKeys := make(map[string]int)
	for i, raw := range arr {
		acc, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("account %d: not an object", i)
		}
		key, _ := acc["account_key"].(string)
		if key == "" {
			t.Fatalf("account %d: empty account_key", i)
		}
		seenKeys[key]++
	}
	for key, n := range seenKeys {
		if n > 1 {
			t.Errorf("account_key collision across 10 distinct auth_index entries: %s seen %d times", key, n)
		}
	}
}

func TestURLValidation(t *testing.T) {
	for _, u := range []string{"http://example.com:1", "http://user@127.0.0.1:1", "http://127.0.0.1:1?q=x"} {
		if validateLoopbackURL(u) == nil {
			t.Errorf("accepted %q", u)
		}
	}
	for _, u := range []string{"http://127.0.0.1:1", "http://localhost:1", "http://[::1]:1"} {
		if err := validateLoopbackURL(u); err != nil {
			t.Errorf("rejected %q: %v", u, err)
		}
	}
}

func TestSchemaVersionAndRawManagementJSON(t *testing.T) {
	reset(t)
	raw, _ := HandleMethod("plugin.register", []byte(`{"schema_version":6}`))
	var e struct {
		Result registrationResponse `json:"result"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if e.Result.SchemaVersion != pluginSchemaVersion {
		t.Fatalf("schema_version=%d want %d", e.Result.SchemaVersion, pluginSchemaVersion)
	}
	if e.Result.Metadata.Name != pluginName || !e.Result.Capabilities["management_api"] {
		t.Fatalf("unexpected registration: %#v", e.Result)
	}

	raw = mgmtResponse(200, map[string]any{"prompt": "inspect <input> & report"})
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	var resp managementResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	body := string(resp.Body)
	if !strings.Contains(body, "<input> &") {
		t.Fatalf("management body lost raw JSON text after RPC Body decode: %s", body)
	}
	if strings.Contains(body, `\u003c`) || strings.Contains(body, `\u0026`) {
		t.Fatalf("management body was HTML-escaped: %s", body)
	}
}

func TestRoutesAndMetadata(t *testing.T) {
	reset(t)
	raw, _ := HandleMethod("plugin.register", []byte(`{"schema_version":6}`))
	s := string(raw)
	for _, want := range []string{"quota-monitor", "0.1.0", "homelab/dohwi", "https://github.com/router-for-me/CLIProxyAPI", "management_api", `"schema_version":6`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q: %s", want, s)
		}
	}
	raw, _ = HandleMethod("management.register", nil)
	s = string(raw)
	if !strings.Contains(s, mgmtBase+"/status") || !strings.Contains(s, mgmtBase+"/accounts") {
		t.Fatal(s)
	}
	// Resource with Menu label must be declared so the management console
	// renders it in the left sidebar via GET /v0/management/plugins.
	var menuEnv struct {
		Result struct {
			Resources []resourceRoute `json:"resources"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &menuEnv); err != nil {
		t.Fatal(err)
	}
	if len(menuEnv.Result.Resources) != 6 {
		t.Fatalf("expected dashboard plus five hidden resources, got %d: %s", len(menuEnv.Result.Resources), s)
	}
	res := menuEnv.Result.Resources[0]
	if res.Path != "/dashboard" || res.Menu != "계정 쿼터" || res.Description != "OAuth 계정별 남은 쿼터 및 상태 모니터" {
		t.Fatalf("unexpected dashboard resource: %#v", res)
	}
	for _, want := range []string{"/data/accounts", "/assets/manrope-variable.woff2", "/assets/suit-regular.woff2", "/assets/suit-semibold.woff2"} {
		var found *resourceRoute
		for i := range menuEnv.Result.Resources {
			if menuEnv.Result.Resources[i].Path == want {
				found = &menuEnv.Result.Resources[i]
				break
			}
		}
		if found == nil || found.Menu != "" || found.Method != "GET" {
			t.Fatalf("hidden resource %s invalid: %#v", want, found)
		}
	}
}

// Resource requests are dispatched to management.handle with the full browser
// URL path /v0/resource/plugins/<pluginID>/dashboard and must serve the HTML.
func TestResourceDashboardServedViaHostResourcePath(t *testing.T) {
	reset(t)
	for _, path := range []string{resourceBase + "/dashboard", "/dashboard"} {
		raw := handleManagementHandle(request(path))
		var e struct {
			Result managementResponse `json:"result"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("%s: unmarshal envelope: %v", path, err)
		}
		if e.Result.StatusCode != 200 {
			t.Fatalf("%s: status=%d", path, e.Result.StatusCode)
		}
		if ct := e.Result.Headers["Content-Type"]; len(ct) == 0 || !strings.HasPrefix(ct[0], "text/html") {
			t.Errorf("%s: Content-Type = %v, want text/html", path, ct)
		}
		if !strings.Contains(string(e.Result.Body), "<html") {
			t.Errorf("%s: body is not the dashboard HTML", path)
		}
	}
}

func TestDashboardHTMLFeatures(t *testing.T) {
	// Dashboard must contain the required elements:
	// - document title remains, while redundant visible H1 is removed
	// - account cards and status filters (without a redundant KPI summary)
	// - provider / canonical status filter
	// - adaptive snapshot polling and a separate keyless manual refresh
	// - canonical status logic / compact grid tokens
	// - compact numeric quota percentages
	// - single accounts fetch only
	for _, required := range []string{
		"<title>계정별 남은 쿼터</title>",
		`<h1 data-i18n="page.heading">계정 쿼터</h1>`,
		"전체",
		"Active",
		"providerFilter",
		"statusFilter",
		"quota-value-number",
		"renderQuotaPercentages",
		"document.hidden",
		"initLoadingState",
		"aria-busy",
		"data-account-key",
		"b.ai",
		"esc(str)",
		"getCanonicalStatus(acc)",
		"repeat(auto-fill, minmax(min(100%, 245px), 1fr))",
		"percentageState",
		"남은",
		// Official UI refinements (remaining-centric cards).
		"padding: 0 14px 16px;", // desktop top/body spacing
		"padding: 0 10px 12px;", // mobile top/body spacing
		"남은 쿼터",                 // remaining is the hero focus
		"effectiveUsed",         // limit_reached/allowed force remaining=0
		"fmtPct",                // percentages max 1 decimal
		// Adaptive polling elements (req 3)
		"scheduleNextPoll",
		"computeAdaptivePollMs",
		"next_poll_hint_ms",
		"setTimeout",
		"POLL_FALLBACK_ACTIVE_MS",
		// Current-account marker (req 3)
		"current_account",
		"data-current",
		// dohwi.com typography: Manrope Latin + SUIT Korean
		"@font-face",
		"manrope-variable.woff2",
		"suit-regular.woff2",
		"suit-semibold.woff2",
		"font-family: 'SUIT'",
	} {
		if !strings.Contains(dashboard.HTML, required) {
			t.Errorf("dashboard.HTML missing required element: %q", required)
		}
	}
	// Legacy automatic-refresh controls remain removed; explicit card refresh is supported.
	for _, forbiddenUI := range []string{
		"운영 상태", "op-panel", "quota-ring-arc", "quota-group-badge", "quota-bar-track", "role=\"meter\"",
		"badge-current", "--current-badge-", "최근 토큰 사용 계정",
		"autoRefreshSelect",
		"qm_auto_refresh_interval",
		"setInterval",
		"자동 갱신",
	} {
		if strings.Contains(dashboard.HTML, forbiddenUI) {
			t.Errorf("dashboard.HTML retains removed UI element %q", forbiddenUI)
		}
	}

	// usage-audit integration must be fully removed
	for _, forbidden := range []string{
		"usage-audit",
		"Promise.allSettled",
		"오늘 기록 없음",
		"오늘 사용량",
		"오늘 요청",
		"오늘 입력 토큰",
		"오늘 출력 토큰",
		"cachedSummary",
		"joinUsageData",
		"todayRange",
		"unmatchedSection",
		"summarySection",
	} {
		if strings.Contains(dashboard.HTML, forbidden) {
			t.Errorf("dashboard.HTML must not contain %q", forbidden)
		}
	}

	// Automatic polling remains snapshot-only; an explicit action may POST to
	// the bounded refresh resource. No browser credentials are used.
	if got := strings.Count(dashboard.HTML, "fetch("); got != 2 {
		t.Errorf("dashboard.HTML fetch calls = %d, want 2 (snapshot and explicit refresh)", got)
	}

	for _, forbidden := range []string{"resolveManagementKey()", "enc::v1::", "cli-proxy-api-webui::secure-storage", "sessionStorage", "/management.html", "saveKey()", "logout()", "authActions", "X-MANAGEMENT-KEY", "Authorization", "Bearer ", "manualRefreshKey", "manualRefreshDialog", `type="password"`, "/v0/management/plugins/quota-monitor/"} {
		if strings.Contains(dashboard.HTML, forbidden) {
			t.Errorf("dashboard.HTML contains forbidden auth/management element: %q", forbidden)
		}
	}
	if !strings.Contains(dashboard.HTML, "/v0/resource/plugins/quota-monitor/data/accounts") {
		t.Errorf("dashboard.HTML missing resource URL: /v0/resource/plugins/quota-monitor/data/accounts")
	}
}

// TestDashboardPastelTokensAndProviderIcons verifies the atomic frontend
// requirements:
//   - Inline SVG provider icons (antigravity orbit/star violet, codex brackets/nodes teal, unknown neutral)
//   - Provider labels remain alongside aria-hidden SVG icons
//   - No external CDN/network assets (<img, http://, https:// inside assets/icons)
//   - Pastel token definitions with accessible dark text (WCAG AA >= 4.5:1, e.g. #065f46)
//   - No saturated gradients on cards or bars
//   - providerIcon helper returns safe hardcoded SVGs with zero server interpolation
func TestDashboardPastelTokensAndProviderIcons(t *testing.T) {
	// 1. Static HTML / CSS token verifications
	requiredTokens := []string{
		"--bg-body: #f7f8fa;",
		"--prov-ag-bg: #f2efff;",
		"--prov-ag-border: #ddd6fe;",
		"--prov-ag-text: #5b4bb7;",
		"--prov-codex-bg: #eaf8f6;",
		"--prov-codex-border: #bfe8e2;",
		"--prov-codex-text: #147a72;",
		"--status-active-bg: #f5faf7;",
		"--status-active-text: #065f46;",
		"--pct-high: #047857;",
		"aria-hidden=\"true\"",
		"providerIcon",
		"badge-provider",
	}
	for _, tok := range requiredTokens {
		if !strings.Contains(dashboard.HTML, tok) {
			t.Errorf("dashboard.HTML missing expected token or attribute: %q", tok)
		}
	}

	// 2. Forbidden external resources, CDNs, img tags, or gradients in cards/bars
	for _, forbid := range []string{
		"<img",
		"unpkg.com",
		"cdn.",
		"cdnjs.cloudflare.com",
		"linear-gradient(",
		"radial-gradient(",
	} {
		if strings.Contains(dashboard.HTML, forbid) {
			t.Errorf("dashboard.HTML contains forbidden pattern: %q", forbid)
		}
	}

	// 3. Node script to test providerIcon helper and renderAccounts DOM generation
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const scriptContent = scriptMatch[1];

function fakeNode(tag) {
  const node = {
    tagName: tag, _children: [], style: {}, className: '',
    attrs: {}, _text: '', _html: '', _htmlSet: false,
    appendChild(c) { this._children.push(c); return c; },
    setAttribute(k, v) { this.attrs[k] = v; },
    classList: { add(c) { if (!this._classes) this._classes = []; this._classes.push(c); }, remove() {} },
  };
  Object.defineProperty(node, 'textContent', {
    get() { return this._text; },
    set(v) { this._text = String(v); this._htmlSet = false; },
  });
  Object.defineProperty(node, 'innerHTML', {
    get() {
      if (this._htmlSet) return this._html;
      return this._text
        .replace(/&/g, '&amp;').replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');
    },
    set(v) { this._html = String(v); this._htmlSet = true; },
  });
  return node;
}

function makeDoc() {
  const byId = {};
  return {
    hidden: false,
    addEventListener() {},
    createElement(tag) { return fakeNode(tag); },
    getElementById(id) {
      if (!byId[id]) byId[id] = fakeNode('div');
      return byId[id];
    },
  };
}

function makeCtx() {
  const ctx = {
    document: makeDoc(),
    localStorage: { getItem() { return null; }, setItem() {} },
    sessionStorage: { getItem() { return null; }, removeItem() {} },
    location: { host: 'localhost', protocol: 'http:' },
    navigator: { userAgent: 'node' },
    console, Date, Math, Number, JSON, Array, Object, isNaN, parseInt, String,
    setTimeout, clearTimeout, setInterval, clearInterval,
  };
  vm.createContext(ctx);
  vm.runInContext(scriptContent, ctx);
  return ctx;
}

function assert(cond, msg) { if (!cond) throw new Error(msg); }

const ctx = makeCtx();

// Verify providerIcon helper
const agSvg = ctx.providerIcon('antigravity');
assert(agSvg.indexOf('<svg') !== -1, 'antigravity must return SVG');
assert(agSvg.indexOf('aria-hidden="true"') !== -1, 'antigravity SVG must be aria-hidden');
assert(agSvg.indexOf('viewBox="0 0 24 24"') !== -1, 'antigravity must retain official 24px viewBox');
assert(agSvg.indexOf('M21.751 22.607') !== -1, 'antigravity must retain official path');
assert(!/[<](?:script)|\bhref=|https?:/i.test(agSvg), 'antigravity SVG must have no active/external content');

const codexSvg = ctx.providerIcon('codex');
assert(codexSvg.indexOf('<svg') !== -1, 'codex must return SVG');
assert(codexSvg.indexOf('aria-hidden="true"') !== -1, 'codex SVG must be aria-hidden');
assert(codexSvg.indexOf('M9.205 8.658') !== -1, 'codex must retain official OpenAI swirl path');
assert(!/[<](?:script)|\bhref=|https?:/i.test(codexSvg), 'codex SVG must have no active/external content');

// Unknown / default safe icon without XSS injection
const evilSvg = ctx.providerIcon('<script>alert(1)</script>');
assert(evilSvg.indexOf('<script>') === -1, 'unknown provider must never interpolate input into SVG');
assert(evilSvg.indexOf('<svg') !== -1, 'fallback unknown SVG returned');

// Verify rendering of accounts with provider badge + label text
ctx.cachedAccounts = [];
const container = ctx.document.getElementById('accountsList');
const testAccounts = [
  { provider: 'antigravity', account: 'ag@test.com', status: 'known', windows: [{ used_percent: 25 }] },
  { provider: 'codex', account: 'codex@test.com', status: 'known', plan_type: 'plus', windows: [{ used_percent: 50 }] },
  { provider: 'unknown-vendor', account: 'vendor@test.com', status: 'known', windows: [{ used_percent: 85 }] }
];
ctx.renderAccounts(testAccounts);
const cards = container._children.filter(c => c.className.indexOf('account-card') !== -1);
assert(cards.length === 3, 'expected 3 cards rendered');

// Verify provider badge contains both SVG icon and text label
cards.forEach((c, idx) => {
  assert(c._children[0].className === 'card-heading', 'identity and freshness grouped above quota');
  assert(c._children[1].className === 'card-quota', 'quota has its own flexible central region');
  const identity = c._children[0]._children[0]._children[0];
  const meta = identity._children.find(x => x.className.indexOf('card-meta-left') !== -1);
  const badge = meta && meta._children.find(x => x.className.indexOf('badge-provider') !== -1);
  assert(badge && badge.className.indexOf('badge-provider') !== -1, 'badge-provider exists');
  assert(badge._html.indexOf('<svg') !== -1, 'badge has inline SVG icon');
  assert(badge._html.indexOf('aria-hidden="true"') !== -1, 'badge icon is aria-hidden');
  const expectedText = testAccounts[idx].provider;
  assert(badge._html.indexOf(expectedText) !== -1, 'badge keeps provider text label');
  // Separate colored badge-plan must not exist
  const separatePlanBadge = meta._children.find(x => x.className.indexOf('badge-plan') !== -1);
  assert(!separatePlanBadge, 'no separate badge-plan element');
});

// Codex plan combination check
const codexBadge = cards[1]._children[0]._children[0]._children[0]._children.find(x => x.className.indexOf('card-meta-left') !== -1)._children.find(x => x.className.indexOf('badge-provider') !== -1);
assert(codexBadge._html.indexOf('prov-name') !== -1, 'prov-name container present');
assert(codexBadge._html.indexOf('prov-sep') !== -1, 'subtle separator dot present');
assert(codexBadge._html.indexOf('Plus') !== -1, 'plan name combined into provider label');

// 4. Availability semantics remain meaningful without redundant visible badges
const activeAccount = { provider: 'codex', status: 'known', operational: { status: 'active' }, windows: [{ remaining_percent: 80 }] };
const warningAccount = { provider: 'codex', status: 'known', operational: { status: 'active' }, windows: [{ remaining_percent: 15 }] };
const limitedAccount = { provider: 'codex', status: 'known', operational: { status: 'active' }, windows: [{ name: 'primary', remaining_percent: 0 }, { name: 'secondary', remaining_percent: 70 }] };
const disabledAccount = { provider: 'codex', status: 'known', operational: { disabled: true, status: 'active' } };

assert(ctx.getAvailabilityState(activeAccount).key === 'active', 'active key');
assert(ctx.getAvailabilityState(warningAccount).key === 'warning', 'warning key');
assert(ctx.getAvailabilityState(warningAccount).label === '소진 임박', 'warning label');
assert(ctx.getAvailabilityState(limitedAccount).key === 'limited', 'limited key');
assert(ctx.getAvailabilityState(disabledAccount).key === 'disabled', 'disabled key');

assert(html.indexOf('availabilityEl') === -1, 'redundant availability badge is not rendered');
assert(html.includes("card.setAttribute('aria-label', String(acc.account || acc.alias || t('card.account')) + ' · ' + availability.label +"), 'state remains accessible without relying on color');

console.log('OK_ICON_TESTS');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node icon tests failed: %v\nOutput:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "OK_ICON_TESTS") {
		t.Fatalf("node icon tests did not report OK: %s", string(out))
	}
}

func TestDashboardNumericPercentageWidgetNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm'); const html = process.argv[1];
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
const ctx = { console, Math, Number, Array, Object, String, isNaN, isFinite, Date, document: { addEventListener() {}, getElementById() { return { value: '0' }; }, createElement() { return { set textContent(v) { this._v = String(v); }, get innerHTML() { return (this._v||'').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); } }; } }, localStorage: { getItem() { return '0'; }, setItem() {} }, setInterval() { return 0; }, clearInterval() {} };
vm.createContext(ctx); vm.runInContext(script, ctx);
function assert(ok, msg) { if (!ok) throw new Error(msg); }
[0, 25, 100].forEach((pct) => { const x = ctx.percentageState({remaining_percent:pct}); assert(x.remaining === pct, 'remaining at '+pct); });
const unknownState = ctx.percentageState({remaining_percent:null});
assert(unknownState.remaining === null, 'unknown remains honest');
const agWindows = [
 {name:'Gemini Models / Five Hour Limit Remaining', remaining_percent:25},
 {name:'Gemini Models / Weekly Limit Remaining', remaining_percent:75},
 {name:'Claude and GPT models / Five Hour Limit Remaining', remaining_percent:40},
 {name:'Claude and GPT models / Weekly Limit Remaining', remaining_percent:80}
];
const agGroups = ctx.groupQuotaWindows(agWindows, 'antigravity');
assert(agGroups.length === 1, 'Antigravity uses one unified group');
assert(agGroups[0].label === 'Antigravity 쿼터', 'unified Antigravity group label');
assert(agGroups[0].windows.length === 4, 'all four AG windows retained');
const agHTML = ctx.renderQuotaGroups(agWindows, 'antigravity', {key:'active'});
assert(agHTML.indexOf('quota-group-title') === -1, 'redundant AG heading element hidden');
assert(agHTML.indexOf('ag-family-stack') !== -1, 'AG family stack visible');
assert(agHTML.indexOf('title="안티그래비티"') !== -1 && agHTML.indexOf('title="Claude"') !== -1, 'Gemini and Claude sections visible with title tooltip');
assert((agHTML.match(/5시간 제한/g)||[]).length === 2 && (agHTML.match(/주간 제한/g)||[]).length === 2, 'each family has 5h and weekly limits');
assert(agHTML.indexOf('title="안티그래비티"') < agHTML.indexOf('title="Claude"'), 'Gemini section above Claude section');
assert(agHTML.indexOf('80%') !== -1 && agHTML.indexOf('40%') !== -1 && agHTML.indexOf('75%') !== -1 && agHTML.indexOf('25%') !== -1, 'all AG percentages visible together');
assert(agHTML.indexOf('ag-limit-value') !== -1, 'AG numeric value class exists');
const cx = ctx.groupQuotaWindows([{name:'primary', remaining_percent:90},{name:'secondary', remaining_percent:10}], 'codex')[0];
const cxHTML = ctx.renderQuotaPercentages(cx, 'codex', {key:'limited'});
assert(cxHTML.indexOf('data-kind="primary"') < cxHTML.indexOf('data-kind="secondary"'), 'Codex 5-hour left, weekly right');
assert(cxHTML.indexOf('quota-group-title') === -1, 'redundant Codex heading element hidden');
assert(cxHTML.indexOf('10%') !== -1 && cxHTML.indexOf('90%') !== -1, 'Codex percentages visible');
const unknown = ctx.renderQuotaGroups([{name:'unmapped', remaining_percent:null}], 'vendor', {key:'unknown'});
assert(unknown.indexOf('>—</span>') !== -1 && unknown.indexOf('미확인') !== -1, 'unknown numeric fallback');
assert(unknown.indexOf('NaN') === -1, 'no NaN');
assert(html.indexOf('quota-bar-track') === -1 && html.indexOf('quota-bar-fill') === -1, 'gauge bars removed');
assert(html.indexOf('quota-ring-arc') === -1, 'ring renderer removed');
assert(html.indexOf('quota-group-badge') === -1, 'redundant group status badge removed');
assert(html.indexOf('운영 상태') === -1 && html.indexOf('op-panel') === -1, 'operational details removed');
console.log('OK_PERCENT_TESTS');`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_PERCENT_TESTS") {
		t.Fatalf("node numeric percentage tests failed: %v\n%s", err, out)
	}
	for _, token := range []string{"--dashboard-top-offset: 24px;", "--dashboard-top-offset: 16px;", "prefers-reduced-motion", "quota-value-number", "5시간 제한", "주간 제한"} {
		if !strings.Contains(dashboard.HTML, token) {
			t.Errorf("missing numeric/accessibility token %q", token)
		}
	}
}

func TestAccountsAPIExposesSafeAccountKeyAndAuthIndex(t *testing.T) {
	reset(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"id": "raw-id-1",
				"auth_index": "101",
				"name": "dup@test.com.json",
				"provider": "codex",
				"email": "dup@test.com",
				"account": "dup@test.com",
				"path": "/home/user/.cli-proxy-api/dup1.json",
				"token": "raw-token-123",
				"id_token": "raw-idtoken-456",
				"chatgpt_account_id": "cg-acct-789",
				"quota": {
					"observed_at": "2026-03-30T12:00:00Z",
					"signals": {
						"x-codex-plan-type": "pro",
						"x-codex-primary-used-percent": "20.0"
					}
				}
			},
			{
				"id": "raw-id-2",
				"auth_index": "102",
				"name": "dup@test.com.json",
				"provider": "codex",
				"email": "dup@test.com",
				"account": "dup@test.com",
				"path": "/home/user/.cli-proxy-api/dup2.json",
				"token": "raw-token-abc",
				"id_token": "raw-idtoken-def",
				"chatgpt_account_id": "cg-acct-xyz",
				"quota": {
					"signals": {}
				}
			}
		]`))
	}))
	defer ts.Close()

	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "test-key")
	configureFromEnv(nil)

	status, b := decodeMgmt(t, handleManagementHandle(request(mgmtBase+"/accounts")))
	if status != 200 {
		t.Fatalf("expected status 200, got %d", status)
	}

	rawJSON, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	s := string(rawJSON)

	// Management API retains auth_index internally; resource API is sanitized.
	if !strings.Contains(s, `"auth_index":"101"`) || !strings.Contains(s, `"auth_index":"102"`) {
		t.Errorf("expected auth_index in management accounts API output: %s", s)
	}
	if !strings.Contains(s, `"account_key"`) {
		t.Errorf("expected account_key in accounts API output: %s", s)
	}

	// Ensure no private credentials / raw fields leak
	for _, secret := range []string{
		"raw-id-1",
		"raw-id-2",
		"raw-token-123",
		"raw-token-abc",
		"raw-idtoken-456",
		"raw-idtoken-def",
		"cg-acct-789",
		"cg-acct-xyz",
		"/home/user/.cli-proxy-api",
		"dup@test.com.json",
	} {
		if strings.Contains(s, secret) {
			t.Errorf("SECURITY LEAK in /accounts API: output contains %q: %s", secret, s)
		}
	}

	accountsArr, ok := b["accounts"].([]any)
	if !ok || len(accountsArr) != 2 {
		t.Fatalf("expected 2 accounts in array, got: %v", b["accounts"])
	}

	acc1 := accountsArr[0].(map[string]any)
	acc2 := accountsArr[1].(map[string]any)
	if acc1["account_key"] == acc2["account_key"] {
		t.Errorf("account_key collision for distinct auth_index: %v", acc1["account_key"])
	}
}

// TestProbeTTLAndForceCooldown pins the active-probe rate-limit contract:
//   - the first GET probes active endpoints once;
//   - auto GETs within the per-account due time refetch auth-files but never call provider
//     endpoints for accounts not yet due, restoring last-good windows instead;
//   - a manual POST refresh forces a probe, but refreshes within forceCooldown
//     are served from the retained snapshot without any refetch.
func TestProbeTTLAndForceCooldown(t *testing.T) {
	reset(t)
	var authFiles, apiCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			authFiles.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"a@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}}]`))
		case "/management/api-call":
			apiCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer k" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status_code":200,"header":{},"body":"{\"rate_limit\":{\"primary_window\":{\"used_percent\":42,\"reset_at\":\"2026-03-30T13:00:00Z\"}}}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	configureFromEnv(nil)

	// 1. First GET probes active endpoints.
	accs, _, err := getAccounts()
	if err != nil || len(accs) != 1 || len(accs[0].Windows) != 1 || accs[0].Windows[0].UsedPercent == nil {
		t.Fatalf("first GET: accs=%#v err=%v", accs, err)
	}
	if apiCalls.Load() != 1 || authFiles.Load() != 1 {
		t.Fatalf("first GET calls: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}

	// 2. Auto GET after cache expiry, but account is not yet due (probeDue is in
	//    the future after first successful probe): refetch auth-files,
	//    no provider call, windows restored from last-good.
	state.mu.Lock()
	state.cachedAt = now().Add(-2 * cacheTTL)
	// Ensure the account is NOT yet due (simulate future due time).
	if len(accs) > 0 && accs[0].AccountKey != "" {
		state.probeDue[accs[0].AccountKey] = now().Add(probeIntervalHealthyKnown)
	}
	state.mu.Unlock()
	accs, _, err = getAccounts()
	if err != nil || len(accs) != 1 || len(accs[0].Windows) != 1 || accs[0].Windows[0].UsedPercent == nil {
		t.Fatalf("second GET: accs=%#v err=%v", accs, err)
	}
	if apiCalls.Load() != 1 || authFiles.Load() != 2 {
		t.Fatalf("second GET calls: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}

	// 3. Manual POST refresh forces an active probe.
	n, err := refreshAccounts()
	if err != nil || n != 1 {
		t.Fatalf("refresh: n=%d err=%v", n, err)
	}
	if apiCalls.Load() != 2 || authFiles.Load() != 3 {
		t.Fatalf("forced refresh calls: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}

	// 4. Manual refresh within forceCooldown: no refetch at all.
	authFiles.Store(0)
	apiCalls.Store(0)
	n, err = refreshAccounts()
	if err != nil || n != 1 {
		t.Fatalf("cooldown refresh: n=%d err=%v", n, err)
	}
	if apiCalls.Load() != 0 || authFiles.Load() != 0 {
		t.Fatalf("cooldown refresh must not refetch: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}

	// 5. After the cooldown expires, refresh forces a probe again.
	state.mu.Lock()
	state.lastForceAt = now().Add(-forceCooldown - time.Second)
	state.cachedAt = now().Add(-2 * cacheTTL)
	state.mu.Unlock()
	authFiles.Store(0)
	apiCalls.Store(0)
	n, err = refreshAccounts()
	if err != nil || n != 1 {
		t.Fatalf("post-cooldown refresh: n=%d err=%v", n, err)
	}
	if apiCalls.Load() != 1 || authFiles.Load() != 1 {
		t.Fatalf("post-cooldown calls: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}
}

// TestAccountsSurviveAllActiveProbeFailures is a regression test for the
// reported bug: when ALL /management/api-call wrapper calls fail with an
// inner 401 (outer 200), the /accounts API must still return ALL 10
// auth-files entries (6 antigravity + 4 codex) — not 0.
//
// Invariants pinned:
//   - err == nil for the whole GET (auth-files fetch succeeded)
//   - len(accounts) == 10, distinct account_keys preserved
//   - each account keeps a meaningful status (unknown / stale / error family)
//     with an error_kind reflecting the auth failure
//   - operational status fields from auth-files are preserved
//   - the upstream wrapper body/detail never leaks into accounts or errors
func TestAccountsSurviveAllActiveProbeFailures(t *testing.T) {
	reset(t)
	var authFiles, apiCalls atomic.Int32
	var b strings.Builder
	b.WriteString(`[`)
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteString(`,`)
		}
		if i < 6 {
			_, _ = fmt.Fprintf(&b, `{"provider":"antigravity","auth_index":"%d","account":"ag%d@example.com","status":"active","success":5,"failed":1,"last_refresh":"2026-03-30T11:00:00Z"}`, i+1, i+1)
		} else {
			_, _ = fmt.Fprintf(&b, `{"provider":"codex","auth_index":"%d","account":"cx%d@example.com","status":"active","success":9,"failed":2,"last_refresh":"2026-03-30T11:30:00Z"}`, i+1, i+1)
		}
	}
	b.WriteString(`]`)
	payload := b.String()
	const leakedDetailMarker = "upstream rejected: token expired 2026-04-01 DO-NOT-LEAK"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			authFiles.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(payload))
		case "/management/api-call":
			apiCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer k" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// Outer 200, inner (wrapper) status_code 401 with a generic body.
			// Antigravity accounts legitimately attempt all 3 fallback URLs
			// (6x3=18) because every upstream answer is non-2xx; Codex is 1
			// per account (4). Total expected provider calls: 22.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status_code":401,"header":{},"body":"` + leakedDetailMarker + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	configureFromEnv(nil)

	// --- Order A: plain GET first (empty cache before it) ---
	accs, stale, err := getAccounts()
	if err != nil {
		t.Fatalf("GET returned err=%v (want nil: auth-files fetch succeeded)", err)
	}
	if stale {
		t.Fatalf("GET flagged stale=true, want false")
	}
	if len(accs) != 10 {
		t.Fatalf("GET: len(accounts)=%d, want 10 (auth-files had 10 entries)", len(accs))
	}
	assertTenAccountsInvariant(t, accs, leakedDetailMarker)
	if got := apiCalls.Load(); got != 22 {
		t.Fatalf("GET: api-call calls=%d, want 22 (6 antigravity x3 fallback + 4 codex x1)", got)
	}

	// --- Order B: manual refresh FIRST on an empty cache, then GET ---
	reset(t)
	authFiles.Store(0)
	apiCalls.Store(0)
	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	configureFromEnv(nil)
	n, err := refreshAccounts()
	if err != nil {
		t.Fatalf("refresh on empty cache: err=%v count=%d", err, n)
	}
	if n != 10 {
		t.Fatalf("refresh on empty cache: count=%d, want 10", n)
	}
	assertTenAccountsInvariant(t, state.last, leakedDetailMarker)
	// Follow-up GET within cache TTL must serve the same 10 from cache.
	status, body := decodeMgmt(t, handleManagementHandle(request(mgmtBase+"/accounts")))
	if status != 200 {
		t.Fatalf("GET /accounts after refresh: status=%d", status)
	}
	arr, ok := body["accounts"].([]any)
	if !ok || len(arr) != 10 {
		t.Fatalf("GET /accounts after refresh: len=%d (%#v), want 10", len(arr), body["accounts"])
	}
	if got, wantAuth := apiCalls.Load(), int32(22); got != wantAuth || authFiles.Load() != 1 {
		t.Fatalf("order B calls: auth-files=%d api-call=%d, want 1/%d (GET served from cache)", authFiles.Load(), got, wantAuth)
	}
}

func assertTenAccountsInvariant(t *testing.T, accs []quotamonitor.AccountQuota, leakedDetail string) {
	t.Helper()
	raw, _ := json.Marshal(accs)
	s := string(raw)
	if strings.Contains(s, leakedDetail) || strings.Contains(s, "DO-NOT-LEAK") {
		t.Errorf("SECURITY: upstream wrapper body/detail leaked into accounts: %s", s)
	}
	seenKeys := map[string]bool{}
	for i, a := range accs {
		if a.AccountKey == "" || seenKeys[a.AccountKey] {
			t.Errorf("account %d: missing or duplicate account_key %q", i, a.AccountKey)
		}
		seenKeys[a.AccountKey] = true
		if a.AuthIndex == "" {
			t.Errorf("account %d: empty auth_index", i)
		}
		switch a.Status {
		case quotamonitor.StatusUnknown, quotamonitor.StatusStale, quotamonitor.StatusError:
			// acceptable degraded statuses
		default:
			t.Errorf("account %d (%s/%s): status=%q, want unknown/stale/error", i, a.Provider, a.AuthIndex, a.Status)
		}
		if a.ErrorKind == "" {
			t.Errorf("account %d (%s/%s): error_kind empty, want auth/active marker", i, a.Provider, a.AuthIndex)
		}
		if a.Operational == nil {
			t.Errorf("account %d (%s/%s): operational status dropped", i, a.Provider, a.AuthIndex)
		} else {
			op := a.Operational
			if op.Status != "active" || op.Success == nil || *op.Success == 0 || op.Failed == nil || *op.Failed == 0 || op.LastRefresh == nil {
				t.Errorf("account %d (%s/%s): operational not preserved: %#v", i, a.Provider, a.AuthIndex, op)
			}
		}
		if strings.Contains(strings.ToLower(a.Error), "token expired") {
			t.Errorf("account %d: upstream error detail leaked into account error: %q", i, a.Error)
		}
	}
}

func TestResourceDashboardRequiresNoAuthKey(t *testing.T) {
	reset(t)
	html := string(dashboard.HTML)
	// Viewing and automatic polling require no key. Manual refresh is a separate
	// user-authenticated action; credentials must never be recovered from storage.
	for _, forbidden := range []string{"resolveManagementKey", "sessionStorage", "window.parent", "postMessage"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("resource dashboard contains forbidden auth surface %q", forbidden)
		}
	}
	if !strings.Contains(html, "/v0/resource/plugins/quota-monitor/data/accounts") {
		t.Fatalf("resource dashboard missing /v0/resource/plugins/quota-monitor/data/accounts")
	}
}

func TestResourceAccountsDTOHasOpaqueKeyAndNoSensitiveFields(t *testing.T) {
	reset(t)
	accounts := []quotamonitor.AccountQuota{
		{AccountKey: "opaque-1", Provider: "codex", Account: "person@example.com", Status: quotamonitor.StatusKnown, PlanType: "Plus"},
		{AccountKey: "opaque-2", Provider: "codex", Account: "other@example.com", Status: quotamonitor.StatusKnown, PlanType: "k12"},
		{AccountKey: "opaque-3", Provider: "codex", Account: "bad@example.com", Status: quotamonitor.StatusKnown, PlanType: "secret_plan_token_123"},
		{AccountKey: "opaque-4", Provider: "codex", Account: "team@example.com", Status: quotamonitor.StatusKnown, PlanType: "ChatGPT Business"},
	}
	got := sanitizeAccounts(accounts)
	if len(got) != 4 || got[0].AccountKey != "opaque-1" || got[0].Account != "p***n@example.com" {
		t.Fatalf("unexpected sanitized account: %#v", got)
	}
	if got[0].PlanType != "plus" {
		t.Errorf("expected plan_type plus, got %q", got[0].PlanType)
	}
	if got[1].PlanType != "k12" {
		t.Errorf("expected plan_type k12, got %q", got[1].PlanType)
	}
	if got[2].PlanType != "" {
		t.Errorf("expected non-allowlisted plan_type to be dropped, got %q", got[2].PlanType)
	}
	if got[3].PlanType != "team" {
		t.Errorf("expected ChatGPT Business to normalize to team, got %q", got[3].PlanType)
	}
	b, err := json.Marshal(got[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"plan_type":"plus"`) {
		t.Errorf("expected JSON to contain plan_type, got %s", b)
	}
	// recent_request_count is safe (plain integer, no timestamp); auth_index is not allowed.
	for _, forbidden := range []string{"auth_index", "auth_id", "session", "api_key", "token", "success", "failed", "message", "project", "account_id"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("resource DTO contains forbidden field %q: %s", forbidden, b)
		}
	}
}

// ── V5 Endpoint & Sort Tests ─────────────────────────────────────────────────

// TestEndpointDTOSafeEnum verifies that the endpoint field in safeAccount is
// strictly one of the allowlisted enum values (direct|warp1|warp2|unknown),
// derived from proxy_url in raw auth files via authDirEndpointResolver,
// and that no URL/host/IP/token is ever exposed in the DTO.
func TestEndpointDTOSafeEnum(t *testing.T) {
	reset(t)

	// Create a temporary auth directory with synthetic auth JSON files.
	authDir := t.TempDir()
	files := []struct {
		name     string
		content  string
		wantEnum string
	}{
		// acc1: empty proxy_url => direct
		{"acc1.json", `{"type":"codex","proxy_url":""}`, "direct"},
		// acc2: warp1 hostname => warp1
		{"acc2.json", `{"type":"codex","proxy_url":"http://warp1:8080"}`, "warp1"},
		// acc3: warp2 hostname => warp2
		{"acc3.json", `{"type":"codex","proxy_url":"socks5://warp2:1080"}`, "warp2"},
		// acc4: no proxy_url field => direct (missing == empty)
		{"acc4.json", `{"type":"codex"}`, "direct"},
		// acc5: WARP1 uppercase => warp1 (case-normalised)
		{"acc5.json", `{"type":"codex","proxy_url":"https://WARP1:443"}`, "warp1"},
		// acc6: credentials in URL => unknown (safe fallback)
		{"acc6.json", `{"type":"codex","proxy_url":"http://user:pass@warp1:8080"}`, "unknown"},
		// acc7: arbitrary hostname => unknown
		{"acc7.json", `{"type":"codex","proxy_url":"http://proxy.corp.example.com:3128"}`, "unknown"},
		// acc8: malformed URL => unknown
		{"acc8.json", `{"type":"codex","proxy_url":"not-a-url"}`, "unknown"},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(authDir, f.name), []byte(f.content), 0o600); err != nil {
			t.Fatalf("write %s: %v", f.name, err)
		}
	}

	// Management server returns entries whose "name" matches the auth file names
	// (stable join key). label field is intentionally mismatched to confirm
	// label is NOT used for endpoint derivation.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"provider":"codex","auth_index":"1","name":"acc1.json","label":"warp2","account":"a@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}},
			{"provider":"codex","auth_index":"2","name":"acc2.json","label":"direct","account":"b@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}},
			{"provider":"codex","auth_index":"3","name":"acc3.json","label":"","account":"c@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}},
			{"provider":"codex","auth_index":"4","name":"acc4.json","label":"","account":"d@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}},
			{"provider":"codex","auth_index":"5","name":"acc5.json","label":"","account":"e@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}},
			{"provider":"codex","auth_index":"6","name":"acc6.json","label":"","account":"f@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}},
			{"provider":"codex","auth_index":"7","name":"acc7.json","label":"","account":"g@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}},
			{"provider":"codex","auth_index":"8","name":"acc8.json","label":"","account":"h@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}}
		]`))
	}))
	defer ts.Close()
	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	os.Setenv("QUOTA_MONITOR_AUTH_DIR", authDir)
	t.Cleanup(func() { os.Unsetenv("QUOTA_MONITOR_AUTH_DIR") })
	configureFromEnv(nil)

	accs, _, err := getAccounts()
	if err != nil {
		t.Fatalf("getAccounts: %v", err)
	}
	if len(accs) != 8 {
		t.Fatalf("expected 8 accounts, got %d", len(accs))
	}

	safe := sanitizeAccounts(accs)
	b, _ := json.Marshal(safe)
	s := string(b)

	// Enum values only — no URL/host/IP/token must appear in DTO
	for _, forbidden := range []string{"http://", "https://", "socks5://", "warp1:", "warp2:", "proxy.corp", "user:pass", "token", "secret", "proxy_url"} {
		if strings.Contains(strings.ToLower(s), strings.ToLower(forbidden)) {
			t.Errorf("endpoint DTO leaks sensitive value %q: %s", forbidden, s)
		}
	}

	// Build name->account map for deterministic checks
	nameToAcc := make(map[string]string) // auth_index -> endpoint
	for _, a := range accs {
		nameToAcc[a.AuthIndex] = string(a.Endpoint)
	}
	expected := map[string]string{
		"1": "direct",  // empty proxy_url
		"2": "warp1",   // http://warp1:8080
		"3": "warp2",   // socks5://warp2:1080
		"4": "direct",  // no proxy_url field
		"5": "warp1",   // WARP1 uppercase
		"6": "unknown", // credentials in URL
		"7": "unknown", // arbitrary host
		"8": "unknown", // malformed
	}
	for idx, want := range expected {
		got := nameToAcc[idx]
		if got != want {
			t.Errorf("auth_index=%s: endpoint=%q, want %q", idx, got, want)
		}
	}

	// Confirm label is NOT the source (acc1 has label=warp2 but proxy_url=empty => direct)
	if nameToAcc["1"] != "direct" {
		t.Errorf("label override: auth_index=1 got %q but label was warp2 and proxy_url was empty; proxy_url must win", nameToAcc["1"])
	}

	// JSON must only contain allowlisted enum strings for the endpoint field
	allowed := map[string]bool{"direct": true, "warp1": true, "warp2": true, "unknown": true}
	for i, sa := range safe {
		if !allowed[string(sa.Endpoint)] {
			t.Errorf("safe[%d].Endpoint=%q is not in allowlist", i, sa.Endpoint)
		}
	}
}

// TestEndpointDTOPresentOnResource verifies the resource /data/accounts endpoint
// field is present and enum-only (no sensitive data), derived from proxy_url.
func TestEndpointDTOPresentOnResource(t *testing.T) {
	reset(t)

	authDir := t.TempDir()
	// acc-direct.json: empty proxy_url => direct
	if err := os.WriteFile(filepath.Join(authDir, "acc-direct.json"), []byte(`{"type":"codex","proxy_url":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// acc-unknown.json: unknown host => unknown
	if err := os.WriteFile(filepath.Join(authDir, "acc-unknown.json"), []byte(`{"type":"codex","proxy_url":"http://someproxy:3128"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"provider":"codex","auth_index":"1","name":"acc-direct.json","account":"a@x","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}},
			{"provider":"codex","auth_index":"2","name":"acc-unknown.json","account":"b@x","quota":{"signals":{}}}
		]`))
	}))
	defer ts.Close()
	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	os.Setenv("QUOTA_MONITOR_AUTH_DIR", authDir)
	t.Cleanup(func() { os.Unsetenv("QUOTA_MONITOR_AUTH_DIR") })
	configureFromEnv(nil)

	// Privileged collection must be initiated through management, never a resource.
	if _, _, err := getAccounts(); err != nil {
		t.Fatal(err)
	}

	raw := handleManagementHandle(func() []byte {
		b, _ := json.Marshal(managementRequest{Method: "GET", Path: resourceBase + "/data/accounts"})
		return b
	}())
	var e struct {
		Result managementResponse `json:"result"`
	}
	if json.Unmarshal(raw, &e) != nil {
		t.Fatal("unmarshal envelope failed")
	}
	var body map[string]any
	if json.Unmarshal(e.Result.Body, &body) != nil {
		t.Fatal("unmarshal body failed")
	}
	arr, _ := body["accounts"].([]any)
	if len(arr) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(arr))
	}
	// Find by auth_index via management accounts endpoint for stable ordering
	endpointByIdx := map[string]string{}
	for _, raw := range arr {
		m, _ := raw.(map[string]any)
		// resource DTO doesn't expose auth_index; use endpoint value directly
		_ = m
	}
	// Use accounts API (management) which retains auth_index
	rawMgmt := handleManagementHandle(func() []byte {
		b, _ := json.Marshal(managementRequest{Method: "GET", Path: mgmtBase + "/accounts"})
		return b
	}())
	var em struct {
		Result managementResponse `json:"result"`
	}
	_ = json.Unmarshal(rawMgmt, &em)
	var bm map[string]any
	_ = json.Unmarshal(em.Result.Body, &bm)
	for _, raw := range bm["accounts"].([]any) {
		m, _ := raw.(map[string]any)
		idx, _ := m["auth_index"].(string)
		ep, _ := m["endpoint"].(string)
		endpointByIdx[idx] = ep
	}
	if endpointByIdx["1"] != "direct" {
		t.Errorf("acc1 (direct) endpoint=%v, want direct", endpointByIdx["1"])
	}
	if endpointByIdx["2"] != "unknown" {
		t.Errorf("acc2 (unknown host) endpoint=%v, want unknown", endpointByIdx["2"])
	}
	// Sensitive fields must be absent from resource DTO
	b, _ := json.Marshal(arr)
	for _, bad := range []string{"auth_index", "api_key", "token", "secret", "password", "proxy_url", "socks", "http://"} {
		if strings.Contains(strings.ToLower(string(b)), bad) {
			t.Errorf("resource DTO leaks %q", bad)
		}
	}
}

func TestCollectRefreshesResolverOncePerFetch(t *testing.T) {
	reset(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.json"), []byte(`{"proxy_url":"http://warp1:8080"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"provider":"unsupported","name":"one.json","auth_index":"1"}]`))
	}))
	defer ts.Close()
	t.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "test-key")
	t.Setenv("QUOTA_MONITOR_AUTH_DIR", dir)
	configureFromEnv(nil)

	if _, _, err := getAccounts(); err != nil {
		t.Fatalf("first getAccounts: %v", err)
	}
	state.mu.Lock()
	resolver := state.resolver
	state.cachedAt = time.Time{} // force a second collect rather than cache reuse
	state.mu.Unlock()
	if resolver == nil || resolver.ScanCount() != 1 {
		t.Fatalf("first fetch scans = %d, want 1", resolver.ScanCount())
	}
	if _, _, err := getAccounts(); err != nil {
		t.Fatalf("second getAccounts: %v", err)
	}
	if got := resolver.ScanCount(); got != 2 {
		t.Fatalf("two fetches scan count = %d, want 2", got)
	}
}

func TestDashboardEndpointSortUI(t *testing.T) {
	for _, want := range []string{
		"endpointFilter",
		"Endpoint 필터",
		"Direct",
		"WARP 1",
		"WARP 2",
		"filterAccountsByEndpoint",
		"sortAccountsByEndpoint",
		"statusRank",
		"endpointRank",
		"account_key",
	} {
		if !strings.Contains(dashboard.HTML, want) {
			t.Errorf("dashboard.HTML missing endpoint sort element: %q", want)
		}
	}
}

// TestDashboardEndpointSortNodeContract verifies via Node.js that:
// - "전체" groups accounts by provider first (Antigravity, then Codex)
// - Endpoints and statuses sort within each provider group
// - Selecting Direct/WARP1/WARP2 filters out every other endpoint
// - Ties are broken deterministically by account_key
func TestDashboardEndpointSortNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
const ctx = {
  console, Math, Number, Array, Object, String, isNaN, Date,
  document: { addEventListener() {}, getElementById() { return { value: '0' }; }, createElement() { return { set textContent(v){}, get innerHTML(){return '';} }; } },
  localStorage: { getItem() { return '0'; }, setItem() {} },
  setInterval() { return 0; }, clearInterval() {}
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(ok, msg) { if (!ok) throw new Error('FAIL: ' + msg); }

// Accounts with mixed statuses and endpoints
const accounts = [
  { account_key:'k1', provider:'codex',       status:'error',   endpoint:'direct', operational:{}, windows:[], stale:false },
  { account_key:'k2', provider:'antigravity', status:'known',   endpoint:'direct', operational:{}, windows:[{used_percent:20}], stale:false },
  { account_key:'k3', provider:'codex',       status:'known',   endpoint:'warp1',  operational:{}, windows:[{used_percent:30}], stale:false },
  { account_key:'k4', provider:'antigravity', status:'error',   endpoint:'warp1',  operational:{}, windows:[], stale:false },
  { account_key:'k5', provider:'codex',       status:'unknown', endpoint:'warp2',  operational:{}, windows:[], stale:false },
  { account_key:'k6', provider:'antigravity', status:'known',   endpoint:'warp2',  operational:{}, windows:[{used_percent:50}], stale:false },
  { account_key:'k7', provider:'codex',       status:'stale',   endpoint:'unknown',operational:{}, windows:[{used_percent:10}], stale:true },
  { account_key:'k8', provider:'antigravity', status:'known',   endpoint:'unknown', operational:{}, windows:[{used_percent:5}], stale:false },
];

function statusRank(acc) { return ctx.statusRank ? ctx.statusRank(acc) : ctx.getCanonicalStatus(acc).key === 'active' ? 0 : 99; }

// Canonical "all" order groups providers contiguously, then endpoints.
const allSorted = ctx.sortAccountsByEndpoint ? ctx.sortAccountsByEndpoint(ctx.filterAccountsByEndpoint(accounts, 'all')) : accounts.slice();
assert(allSorted.map(a=>a.provider).join(',') === 'antigravity,antigravity,antigravity,antigravity,codex,codex,codex,codex', 'all order must group Antigravity then Codex');
assert(allSorted.slice(0,4).map(a=>a.endpoint).join(',') === 'direct,warp1,warp2,unknown', 'Antigravity endpoints sort within group');
assert(allSorted.slice(4).map(a=>a.endpoint).join(',') === 'direct,warp1,warp2,unknown', 'Codex endpoints sort within group');

// WARP1 selection is a real filter: only WARP1 accounts remain.
const filtered = ctx.filterAccountsByEndpoint ? ctx.filterAccountsByEndpoint(accounts, 'warp1') : [];
const sorted = ctx.sortAccountsByEndpoint ? ctx.sortAccountsByEndpoint(filtered) : filtered;
assert(sorted.length === 2, 'WARP1 filter must retain exactly 2 accounts');
assert(sorted.every(a => a.endpoint === 'warp1'), 'WARP1 filter must hide every other endpoint');

// Provider grouping still applies within the selected endpoint.
assert(sorted[0].provider === 'antigravity', 'Antigravity WARP1 first');
assert(sorted[1].provider === 'codex', 'Codex WARP1 second');

// Direct and WARP2 filters obey the same contract.
assert(ctx.filterAccountsByEndpoint(accounts, 'direct').every(a => a.endpoint === 'direct'), 'Direct filter is exclusive');
assert(ctx.filterAccountsByEndpoint(accounts, 'warp2').every(a => a.endpoint === 'warp2'), 'WARP2 filter is exclusive');

// Sort is stable/deterministic.
const sorted2 = ctx.sortAccountsByEndpoint(ctx.filterAccountsByEndpoint(accounts, 'warp1'));
assert(sorted.map(a=>a.account_key).join(',') === sorted2.map(a=>a.account_key).join(','), 'sort must be stable/deterministic');

console.log('OK_ENDPOINT_SORT_TESTS');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_ENDPOINT_SORT_TESTS") {
		t.Fatalf("node endpoint sort tests failed: %v\n%s", err, out)
	}
}

func TestDashboardCardHeaderHasEndpointButNoTopStatusChip(t *testing.T) {
	for _, want := range []string{"badge-endpoint", "badge-endpoint-direct", "endpointLabel(endpointKey)", "WARP 1", "WARP 2", "data-endpoint", ".quota-group:first-child", "getAvailabilityState", "data-availability", "--card-state-bg", "availability.label", "grid-auto-rows: 1fr;", "data-provider", "height: 100%", "min-height: 226px", "grid-template-columns: minmax(0, 1fr) 64px"} {
		if !strings.Contains(dashboard.HTML, want) {
			t.Errorf("dashboard.HTML missing endpoint or availability marker %q", want)
		}
	}
	for _, forbidden := range []string{"availabilityEl", ".availability-state", "chipEl", "header.appendChild(chipEl)", "className = 'chip '+canon.tone", "운영 상태", "op-panel", ".account-card[data-endpoint=\"direct\"]", ".account-card[data-endpoint=\"warp1\"]", ".account-card[data-endpoint=\"warp2\"]", ".account-card[data-provider=\"antigravity\"] {", ".account-card:hover"} {
		if strings.Contains(dashboard.HTML, forbidden) {
			t.Errorf("dashboard.HTML still renders removed status UI %q", forbidden)
		}
	}
}

func TestDashboardAvailabilityStateNodeContract(t *testing.T) {
	nodeScript := `
const vm=require('vm'); const html=process.argv[1]; const script=html.match(/<script>([\s\S]*?)<\/script>/)[1];
const ctx={console,Math,Number,Array,Object,String,isNaN,isFinite,Date,document:{addEventListener(){},getElementById(){return {value:'0'}},createElement(){return {set textContent(v){this.v=String(v)},get innerHTML(){return this.v||''}}}},localStorage:{getItem(){return'0'},setItem(){}},setInterval(){return 0},clearInterval(){}};
vm.createContext(ctx);vm.runInContext(script,ctx);function assert(x,m){if(!x)throw new Error(m)}
assert(ctx.getAvailabilityState({provider:'codex',status:'known',operational:{status:'active'},windows:[{remaining_percent:67}]}).key==='active','active');
assert(ctx.getAvailabilityState({provider:'codex',status:'known',operational:{status:'active'},windows:[{name:'primary',remaining_percent:0},{name:'secondary',remaining_percent:70}]}).key==='limited','5-hour exhaustion is limited');
const weeklyLocked={provider:'codex',status:'known',operational:{status:'active'},windows:[{name:'primary',remaining_percent:67},{name:'secondary',remaining_percent:0,reset_after_seconds:3600}]};
assert(ctx.getAvailabilityState(weeklyLocked).key==='unavailable','weekly exhaustion is unavailable state');
assert(ctx.getAvailabilityState(weeklyLocked).label==='주간 제한','weekly exhaustion label is explicit');
assert(ctx.renderQuotaLock(weeklyLocked,ctx.getAvailabilityState(weeklyLocked)).indexOf('주간 제한')!==-1,'weekly lock panel');
assert(ctx.renderQuotaLock(weeklyLocked,ctx.getAvailabilityState(weeklyLocked)).indexOf('1시간')!==-1,'weekly reset centered');
assert(ctx.getAvailabilityState({provider:'codex',status:'known',operational:{status:'active'},windows:[{name:'primary',remaining_percent:15},{name:'secondary',remaining_percent:70}]}).key==='warning','low remaining is warning');
const ag={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:70},{name:'Claude and GPT models / Weekly Limit Remaining',remaining_percent:0}]};
assert(ctx.getAvailabilityState(ag).key==='active','AG card background follows healthy Gemini despite Claude weekly lock');
const agClaudeError={provider:'antigravity',status:'known',operational:{unavailable:true,status:'error'},windows:[{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:70},{name:'Claude and GPT models / Weekly Limit Remaining',remaining_percent:0}]};
assert(ctx.getAvailabilityState(agClaudeError).key==='active','healthy Gemini + Claude 0 + op.unavailable=true/status=error => Gemini background active');
const agGeminiExhausted5h={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Gemini Models / Five Hour Limit Remaining',remaining_percent:0},{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:70}]};
assert(ctx.getAvailabilityState(agGeminiExhausted5h).key==='limited','Gemini 5h exhaustion => limited');
const agGeminiExhaustedWeekly={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:0}]};
assert(ctx.getAvailabilityState(agGeminiExhaustedWeekly).key==='unavailable','Gemini weekly exhaustion => unavailable');
const agMissingGemini={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Claude and GPT models / Weekly Limit Remaining',remaining_percent:80}]};
assert(ctx.getAvailabilityState(agMissingGemini).key==='pending','missing Gemini => pending not fallback Claude');
const agDisabled={provider:'antigravity',status:'known',operational:{disabled:true,status:'active'},windows:[{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:70}]};
assert(ctx.getAvailabilityState(agDisabled).key==='disabled','Antigravity disabled explicit retains disabled');
const agGeminiWeekly={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:0},{name:'Claude and GPT models / Weekly Limit Remaining',remaining_percent:80}]};
assert(ctx.getAvailabilityState(agGeminiWeekly).key==='unavailable','Gemini weekly exhaustion controls AG card background');
const agShort={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Gemini Models / Five Hour Limit Remaining',remaining_percent:0,reset_after_seconds:1800},{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:50,reset_after_seconds:86400}]};
assert(ctx.getAvailabilityState(agShort).key==='limited','Antigravity 5-hour exhaustion limited');
const shortLock=ctx.renderQuotaLock(agShort,ctx.getAvailabilityState(agShort));
assert(shortLock.indexOf('5시간 제한')!==-1,'5h lock panel');
assert(shortLock.indexOf('주간 잔여')!==-1 && shortLock.indexOf('50%')!==-1,'weekly remaining shown below 5h lock');
assert(shortLock.indexOf('1일')!==-1,'weekly reset shown below 5h lock');
assert(ctx.getAvailabilityState({operational:{disabled:true,status:'active'}}).key==='disabled','disabled precedence');
assert(ctx.getAvailabilityState({operational:{unavailable:true,status:'error'}}).key==='unavailable','unavailable precedence');
assert(ctx.getAvailabilityState({provider:'codex',status:'known',operational:{unavailable:false,status:'error'},windows:[{name:'primary',remaining_percent:100},{name:'secondary',remaining_percent:100}]}).key==='active','healthy probed codex with op.status=error => active');
assert(ctx.getCanonicalStatus({provider:'codex',status:'known',operational:{unavailable:false,status:'error'},windows:[{name:'primary',remaining_percent:100},{name:'secondary',remaining_percent:100}]}).key==='active','canonical status for healthy probed codex with op.status=error => active');
assert(ctx.getAvailabilityState({provider:'codex',status:'known',operational:{unavailable:true,status:'error'},windows:[{name:'primary',remaining_percent:100},{name:'secondary',remaining_percent:100}]}).key==='active','reset codex with op.unavailable=true => active');
assert(ctx.getCanonicalStatus({provider:'codex',status:'known',operational:{unavailable:true,status:'error'},windows:[{name:'primary',remaining_percent:100},{name:'secondary',remaining_percent:100}]}).key==='active','canonical status for reset codex with op.unavailable=true => active');
assert(ctx.getAvailabilityState({provider:'codex',operational:{status:'active'},windows:[]}).key==='pending','missing windows pending, never active');
assert(html.includes('/data/refresh?confirm=1'),'explicit keyless refresh supported');
assert(ctx.getAvailabilityState({operational:{}}).key==='pending','unknown quota is pending');
console.log('OK_AVAILABILITY');`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_AVAILABILITY") {
		t.Fatalf("availability state tests failed: %v\n%s", err, out)
	}
}

// ── New Tests: Per-Account Due Scheduling, Tier Classification, Current Marking ──

// TestClassifyAccountTier verifies the probe tier classification logic.
func TestClassifyAccountTier(t *testing.T) {
	win := func(name string, remaining float64, minutes int) quotamonitor.QuotaWindow {
		rem := remaining
		w := quotamonitor.QuotaWindow{Name: name, RemainingPercent: &rem}
		if minutes > 0 {
			w.WindowMinutes = &minutes
		}
		return w
	}
	boolPtr := func(b bool) *bool { return &b }
	int64Ptr := func(i int64) *int64 { return &i }

	tests := []struct {
		name      string
		acc       quotamonitor.AccountQuota
		isCurrent bool
		wantTier  probeTier
	}{
		{
			name: "disabled",
			acc: quotamonitor.AccountQuota{
				Provider:    "codex",
				Status:      quotamonitor.StatusKnown,
				Operational: &quotamonitor.OperationalStatus{Disabled: boolPtr(true)},
			},
			wantTier: tierDisabled,
		},
		{
			name: "auth error",
			acc: quotamonitor.AccountQuota{
				Provider:  "codex",
				Status:    quotamonitor.StatusError,
				ErrorKind: "auth",
			},
			wantTier: tierExhausted,
		},
		{
			name: "unavailable",
			acc: quotamonitor.AccountQuota{
				Provider:    "codex",
				Status:      quotamonitor.StatusKnown,
				Operational: &quotamonitor.OperationalStatus{Unavailable: boolPtr(true)},
			},
			wantTier: tierExhausted,
		},
		{
			name: "weekly exhausted",
			acc: quotamonitor.AccountQuota{
				Provider: "codex",
				Status:   quotamonitor.StatusKnown,
				Windows:  []quotamonitor.QuotaWindow{win("secondary", 0, 10080)},
			},
			wantTier: tierExhausted,
		},
		{
			name: "retry_after set",
			acc: quotamonitor.AccountQuota{
				Provider:   "codex",
				Status:     quotamonitor.StatusKnown,
				RetryAfter: int64Ptr(300),
				Windows:    []quotamonitor.QuotaWindow{win("primary", 50, 300), win("secondary", 50, 10080)},
			},
			wantTier: tierShortLimited,
		},
		{
			name: "short-window exhausted",
			acc: quotamonitor.AccountQuota{
				Provider: "codex",
				Status:   quotamonitor.StatusKnown,
				Windows:  []quotamonitor.QuotaWindow{win("primary", 0, 300), win("secondary", 50, 10080)},
			},
			wantTier: tierShortLimited,
		},
		{
			name:      "current account",
			isCurrent: true,
			acc: quotamonitor.AccountQuota{
				Provider: "codex",
				Status:   quotamonitor.StatusKnown,
				Windows:  []quotamonitor.QuotaWindow{win("primary", 70, 300)},
			},
			wantTier: tierCurrent,
		},
		{
			name: "healthy known",
			acc: quotamonitor.AccountQuota{
				Provider: "codex",
				Status:   quotamonitor.StatusKnown,
				Windows:  []quotamonitor.QuotaWindow{win("primary", 70, 300)},
			},
			wantTier: tierHealthy,
		},
		{
			name: "unknown status no windows",
			acc: quotamonitor.AccountQuota{
				Provider: "codex",
				Status:   quotamonitor.StatusUnknown,
			},
			wantTier: tierUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyAccountTier(tc.acc, tc.isCurrent)
			if got != tc.wantTier {
				t.Errorf("classifyAccountTier(%q) = %d, want %d", tc.name, got, tc.wantTier)
			}
		})
	}
}

// TestProbeIntervalForTier verifies that each tier maps to its expected interval.
func TestProbeIntervalForTier(t *testing.T) {
	cases := []struct {
		tier    probeTier
		wantMin time.Duration
		wantMax time.Duration
	}{
		{tierCurrent, probeIntervalCurrent - time.Second, probeIntervalCurrent + time.Second},
		{tierHealthy, probeIntervalHealthyKnown - time.Second, probeIntervalHealthyKnown + time.Second},
		{tierUnknown, probeIntervalUnknown - time.Second, probeIntervalUnknown + time.Second},
		{tierShortLimited, probeIntervalShortLimited - time.Second, probeIntervalShortLimited + time.Second},
		{tierExhausted, probeIntervalExhausted - time.Second, probeIntervalExhausted + time.Second},
		{tierDisabled, probeIntervalDisabled - time.Second, probeIntervalDisabled + time.Second},
	}
	for _, tc := range cases {
		got := probeIntervalForTier(tc.tier)
		if got < tc.wantMin || got > tc.wantMax {
			t.Errorf("probeIntervalForTier(%d) = %v, want ~%v", tc.tier, got, tc.wantMin+time.Second)
		}
	}
}

// TestCurrentAccountMarking verifies that currentAccountKeys identifies the
// most-recently-used account per provider within currentAccountWindow, and
// that sanitizeAccounts sets current_account=true on at most one per provider.
func TestCurrentAccountMarking(t *testing.T) {
	reset(t)
	recent := now().Add(-5 * time.Minute)
	older := now().Add(-20 * time.Minute) // outside window

	accounts := []quotamonitor.AccountQuota{
		{
			AccountKey: "k1",
			Provider:   "codex",
			Account:    "a@x.com",
			Status:     quotamonitor.StatusKnown,
			LastUsedAt: &recent,
			Operational: &quotamonitor.OperationalStatus{
				RecentRequestLatest: &recent,
				RecentRequestCount:  5,
			},
		},
		{
			AccountKey: "k2",
			Provider:   "codex",
			Account:    "b@x.com",
			Status:     quotamonitor.StatusKnown,
			LastUsedAt: &older,
			Operational: &quotamonitor.OperationalStatus{
				RecentRequestLatest: &older,
				RecentRequestCount:  2,
			},
		},
		{
			AccountKey: "k3",
			Provider:   "antigravity",
			Account:    "c@x.com",
			Status:     quotamonitor.StatusKnown,
			LastUsedAt: &recent,
			Operational: &quotamonitor.OperationalStatus{
				RecentRequestLatest: &recent,
				RecentRequestCount:  3,
			},
		},
	}

	safe := sanitizeAccounts(accounts)

	// Verify current_account marking.
	var currentCount int
	var currentByProv = map[string]int{}
	for _, s := range safe {
		if s.CurrentAccount {
			currentCount++
			currentByProv[strings.ToLower(s.Provider)]++
		}
	}
	if currentCount != 2 {
		t.Errorf("expected 2 current accounts (one per provider), got %d", currentCount)
	}
	for prov, count := range currentByProv {
		if count > 1 {
			t.Errorf("provider %q has %d current accounts, want at most 1", prov, count)
		}
	}

	// k2 is older than window and lower priority; must not be current.
	for _, s := range safe {
		if s.AccountKey == "k2" && s.CurrentAccount {
			t.Error("k2 (outside window) must not be marked current")
		}
	}

	// k1 (more recent codex) must be current; k2 must not.
	var k1Current, k2Current bool
	for _, s := range safe {
		if s.AccountKey == "k1" {
			k1Current = s.CurrentAccount
		}
		if s.AccountKey == "k2" {
			k2Current = s.CurrentAccount
		}
	}
	if !k1Current {
		t.Error("k1 (most recent codex within window) must be current")
	}
	if k2Current {
		t.Error("k2 (older than window) must not be current")
	}

	// Reliable recent selections are labelled recent.
	for _, s := range safe {
		if s.CurrentAccount && s.CurrentKind != "recent" {
			t.Errorf("recent current account kind=%q, want recent", s.CurrentKind)
		}
	}

	// No timestamps exposed in the DTO.
	b, _ := json.Marshal(safe)
	// recent_request_count IS allowed; raw timestamps are not.
	if strings.Contains(string(b), "recent_request_latest") {
		t.Error("DTO must not expose raw recent_request_latest timestamp")
	}
}

// Without an audit, priority alone cannot establish current use.
func TestCurrentAccountNoPriorityFallback(t *testing.T) {
	reset(t)
	yes := true
	accounts := []quotamonitor.AccountQuota{
		{AccountKey: "blocked", Provider: "codex", Priority: 100, Account: "b@x", Operational: &quotamonitor.OperationalStatus{Unavailable: &yes}},
		{AccountKey: "ready", Provider: "codex", Priority: 30, Account: "r@x", Operational: &quotamonitor.OperationalStatus{Status: "active"}},
	}
	if keys := currentAccountKeys(accounts); len(keys) != 0 {
		t.Fatalf("no audit must yield no selection: %v", keys)
	}
	for _, a := range sanitizeAccounts(accounts) {
		if a.CurrentAccount || a.CurrentKind != "" || a.CurrentReason != "" || a.SelectedBy != "" {
			t.Errorf("no audit must yield no current metadata: %+v", a)
		}
	}
}

// TestAntigravityCurrentAccountWhenClaudeWeeklyExhausted verifies that an
// Antigravity account with healthy Gemini quota remains eligible despite exhausted Claude quota.
func TestAntigravityCurrentAccountWhenClaudeWeeklyExhausted(t *testing.T) {
	reset(t)
	zero := 0.0
	seventy := 70.0
	minsWeekly := 10080
	mins5h := 300
	limitReached := true

	recent := now().Add(-time.Minute)
	accounts := []quotamonitor.AccountQuota{
		{
			LastUsedAt: &recent,
			AccountKey: "ag-candidate",
			Provider:   "antigravity",
			Priority:   50,
			Account:    "user@example.invalid",
			Status:     quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				{Name: "Gemini Models / Five Hour Limit Remaining", RemainingPercent: &seventy, WindowMinutes: &mins5h},
				{Name: "Gemini Models / Weekly Limit Remaining", RemainingPercent: &seventy, WindowMinutes: &minsWeekly},
				{Name: "Claude and GPT models / Five Hour Limit Remaining", RemainingPercent: &zero, WindowMinutes: &mins5h},
				{Name: "Claude and GPT models / Weekly Limit Remaining", RemainingPercent: &zero, WindowMinutes: &minsWeekly, LimitReached: &limitReached},
			},
		},
	}

	keys := currentAccountKeys(accounts)
	if keys["antigravity"] != "ag-candidate" {
		t.Fatalf("expected audited antigravity account 'ag-candidate', got %q", keys["antigravity"])
	}

	safe := sanitizeAccounts(accounts)
	if len(safe) != 1 {
		t.Fatalf("expected 1 safe account, got %d", len(safe))
	}
	if !safe[0].CurrentAccount {
		t.Errorf("expected CurrentAccount=true for healthy Gemini with Claude exhausted, got false")
	}
	if safe[0].CurrentKind != "recent" {
		t.Errorf("expected CurrentKind='recent', got %q", safe[0].CurrentKind)
	}

	// If Gemini weekly quota is exhausted, even audited use cannot mark it current.
	accounts[0].Windows[1].RemainingPercent = &zero
	accounts[0].Windows[1].LimitReached = &limitReached
	keysGeminiExhausted := currentAccountKeys(accounts)
	if _, ok := keysGeminiExhausted["antigravity"]; ok {
		t.Errorf("antigravity account with exhausted Gemini weekly quota must not be current, got %q", keysGeminiExhausted["antigravity"])
	}
}

func TestNextPollHintMS(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	// With a current account, hint should be probeIntervalCurrent.
	recent := now().Add(-3 * time.Minute)
	accs := []quotamonitor.AccountQuota{
		{
			AccountKey:  "k1",
			Provider:    "codex",
			Status:      quotamonitor.StatusKnown,
			Operational: &quotamonitor.OperationalStatus{RecentRequestLatest: &recent},
		},
	}
	hint := nextPollHintMS(accs, map[string]string{"codex": "k1"})
	if hint != probeIntervalCurrent.Milliseconds() {
		t.Errorf("nextPollHintMS with current account = %d ms, want %d ms", hint, probeIntervalCurrent.Milliseconds())
	}

	// With a disabled account, hint should be probeIntervalDisabled.
	disabledAccs := []quotamonitor.AccountQuota{
		{
			AccountKey:  "k2",
			Provider:    "codex",
			Status:      quotamonitor.StatusKnown,
			Operational: &quotamonitor.OperationalStatus{Disabled: boolPtr(true)},
		},
	}
	hint2 := nextPollHintMS(disabledAccs, nil)
	if hint2 != probeIntervalDisabled.Milliseconds() {
		t.Errorf("nextPollHintMS with disabled account = %d ms, want %d ms", hint2, probeIntervalDisabled.Milliseconds())
	}

	// Empty accounts: should return the unknown-tier fallback.
	hint3 := nextPollHintMS(nil, nil)
	if hint3 != probeIntervalUnknown.Milliseconds() {
		t.Errorf("nextPollHintMS with no accounts = %d ms, want %d ms", hint3, probeIntervalUnknown.Milliseconds())
	}
}

// TestNextPollHintInResourceResponse verifies that /data/accounts includes next_poll_hint_ms.
func TestNextPollHintInResourceResponse(t *testing.T) {
	reset(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"1","account":"a@x.com","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}}]`))
	}))
	defer ts.Close()
	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	configureFromEnv(nil)

	raw := handleManagementHandle(func() []byte {
		b, _ := json.Marshal(managementRequest{Method: "GET", Path: resourceBase + "/data/accounts"})
		return b
	}())
	var e struct {
		Result managementResponse `json:"result"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(e.Result.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	hintRaw, ok := body["next_poll_hint_ms"]
	if !ok {
		t.Fatal("response missing next_poll_hint_ms")
	}
	hint, ok := hintRaw.(float64)
	if !ok || hint <= 0 {
		t.Errorf("next_poll_hint_ms = %v, want positive number", hintRaw)
	}
}

// TestUpdateAccountProbeDueBoundedCleanup verifies that stale entries are
// removed from probeDue when the map exceeds maxProbeMapEntries.
func TestUpdateAccountProbeDueBoundedCleanup(t *testing.T) {
	reset(t)
	st := &stateData{probeDue: make(map[string]time.Time)}
	// Pre-fill with stale keys (not in any current account list).
	for i := 0; i < maxProbeMapEntries+10; i++ {
		st.probeDue[fmt.Sprintf("stale-key-%d", i)] = now().Add(time.Hour)
	}
	// Provide a small set of actual accounts.
	accs := []quotamonitor.AccountQuota{
		{AccountKey: "live-1", Provider: "codex", Status: quotamonitor.StatusKnown},
	}
	updateAccountProbeDue(st, accs)
	// After cleanup, only live keys should remain.
	for k := range st.probeDue {
		if strings.HasPrefix(k, "stale-key-") {
			t.Errorf("stale key %q not cleaned up from probeDue", k)
			break
		}
	}
}

// TestDashboardAdaptivePollingWithExplicitRefresh verifies adaptive snapshot
// polling, a separate manual refresh button, no stored credentials or interval
// selector, and the current-account marker.
func TestDashboardAdaptivePollingWithExplicitRefresh(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
function assert(ok, msg) { if (!ok) throw new Error('FAIL: ' + msg); }

// The script must use setTimeout, not setInterval for main polling.
assert(html.indexOf('scheduleNextPoll') !== -1, 'scheduleNextPoll present');
assert(html.indexOf('computeAdaptivePollMs') !== -1, 'computeAdaptivePollMs present');
assert(html.indexOf('next_poll_hint_ms') !== -1, 'next_poll_hint_ms in script');
assert(html.indexOf('setTimeout') !== -1, 'setTimeout used');
// No setInterval for polling (ok in Node test stubs, but not in dashboard).
assert(html.indexOf('setInterval') === -1, 'setInterval forbidden in dashboard');
// Explicit keyless refresh is separate from automatic snapshot polling.
assert(html.indexOf('fullRefreshBtn') !== -1, 'full refresh button present');
assert(html.indexOf('refreshAllAccounts') !== -1, 'explicit refresh action present');
// No auto-refresh interval select.
assert(html.indexOf('autoRefreshSelect') === -1, 'autoRefreshSelect must be absent');
// localStorage is limited to the language preference (see TestDashboardStorageLimitedToLanguagePreference).
assert((html.match(/localStorage/g)||[]).length === (html.match(/localStorage\.(?:getItem\(LANG_KEY\)|setItem\(LANG_KEY, lang\))/g)||[]).length, 'localStorage only for LANG_KEY');
// Keep the compact toolbar action label.
assert(html.indexOf('전체 갱신') !== -1, 'full refresh action label present');
// Current account uses the border instead of a redundant visible badge.
assert(html.indexOf('badge-current') === -1, 'current account badge removed');
assert(html.indexOf('최근 토큰 사용 계정') === -1, 'current account badge label removed');
assert(html.indexOf('data-current') !== -1, 'data-current attribute present');
// SUIT font @font-face present.
assert(html.indexOf('@font-face') !== -1, '@font-face declaration present');
assert(html.indexOf("font-family: 'Manrope'") !== -1, 'Manrope font-family declared');
assert(html.indexOf("font-family: 'Manrope', 'SUIT'") !== -1, 'dohwi.com font stack declared');
assert(html.indexOf('suit-regular.woff2') !== -1, 'suit-regular.woff2 reference present');
// Visibility change handler present.
assert(html.indexOf('visibilitychange') !== -1, 'visibilitychange handler present');
// Resource polling remains snapshot-only; refresh is management-authenticated.
assert(html.indexOf('/data/refresh?confirm=1') !== -1, 'explicit refresh action present');
console.log('OK_ADAPTIVE_POLLING');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_ADAPTIVE_POLLING") {
		t.Fatalf("adaptive polling tests failed: %v\n%s", err, out)
	}
}

// TestDashboardSUITFontAndBodyFamily verifies that @font-face is declared for
// SUIT and that body font-family starts with SUIT.
func TestDashboardSUITFontAndBodyFamily(t *testing.T) {
	required := []string{
		"@font-face",
		"font-family: 'Manrope'",
		"font-family: 'SUIT'",
		"manrope-variable.woff2",
		"suit-regular.woff2",
		"suit-semibold.woff2",
		"font-weight: 400",
		"font-weight: 600",
		"font-display: swap",
		"'Manrope', 'SUIT'",
	}
	for _, r := range required {
		if !strings.Contains(dashboard.HTML, r) {
			t.Errorf("dashboard.HTML missing SUIT font contract %q", r)
		}
	}
	// No external font CDN.
	for _, bad := range []string{"fonts.googleapis", "cdnjs", "unpkg.com", "fonts.gstatic"} {
		if strings.Contains(dashboard.HTML, bad) {
			t.Errorf("dashboard.HTML references external font CDN %q", bad)
		}
	}
}

// TestCurrentAccountCardBorderCSS verifies that current_account cards receive
// a stronger blue border without a badge or changes to the status surface/content inset.
func TestCurrentAccountCardBorderCSS(t *testing.T) {
	required := []string{
		"--current-border: #2563eb;",
		` .account-card[data-current="true"] { border: 2px solid var(--current-border); padding: 9px 10px; }`,
		"border: 1px solid var(--account-border);",
		` .account-card { min-height: 226px; height: 100%; padding: 9px 10px; }`,
		` .account-card[data-current="true"] { padding: 8px 9px; }`,
		`data-current="true"`,
	}
	for _, r := range required {
		if !strings.Contains(dashboard.HTML, r) {
			t.Errorf("dashboard.HTML missing current-account styling %q", r)
		}
	}
	// No .account-card:hover transform effect.
	if strings.Contains(dashboard.HTML, ".account-card:hover") {
		t.Error("account cards must not have a :hover transform/effect")
	}
}

// TestMergedAntigravityCard verifies that Gemini and Claude are rendered in
// one card without the former family toggle.
func TestMergedAntigravityCard(t *testing.T) {
	for _, forbidden := range []string{"antigravityView", "Antigravity 기준:", "data-ag-view", "initAntigravityView", "selectedAntigravityView"} {
		if strings.Contains(dashboard.HTML, forbidden) {
			t.Errorf("dashboard.HTML retains removed Antigravity toggle %q", forbidden)
		}
	}
	for _, required := range []string{"안티그래비티 · ", "Claude · ", "grid-auto-rows: 1fr;", "height: 100%", "credit-summary"} {
		if !strings.Contains(dashboard.HTML, required) {
			t.Errorf("dashboard.HTML missing merged-card marker %q", required)
		}
	}
}

// TestAGFamilyIconsAndAccessibility verifies the Gemini/Claude family icons:
//
//   - Each family section carries an aria-label with the full Korean family name
//     so screen readers announce "Gemini 쿼터" / "Claude 쿼터" regardless of
//     whether they can interpret the SVG.
//   - The SVG icons are aria-hidden (decorative duplicate of visible label) and
//     carry a <title> child matching the family name for any renderer that does
//     expose SVG titles.
//   - Icon shapes are structurally distinct and use verifiable official brand assets:
//     Gemini uses the official 4-pointed Google Gemini sparkle path (from Google web app / simple-icons),
//     Claude uses the official Claude Spark path (from Anthropic official press kit SVG).
//   - No external asset references (no http/https URLs, no <img> tags inside
//     the icons).
//   - The text "Gemini" and "Claude" remain as literal DOM text nodes so the
//     existing >안티그래비티< / >Claude< indexOf contract is preserved.
//   - The same icon and aria-label appear in the locked-section title.
func TestAGFamilyIconsAndAccessibility(t *testing.T) {
	// Static assertions on the raw dashboard.HTML string.
	for _, want := range []string{
		// aria-label is built at runtime from JS sectionAria variable;
		// check that the JS source pattern exists in dashboard.HTML.
		`esc(sectionAria)`,
		`sectionAria = t('q.familyAria'`,
		// SVG icons are aria-hidden and carry SVG <title>
		`aria-hidden="true"`,
		// agFamilyIcon helper exists
		`agFamilyIcon`,
		// Official Gemini sparkle path
		`M11.04 19.32Q12 21.51`,
		// Official Claude Spark path
		`M18.7657 62.4437L37.1822 52.1167`,
		// Icon CSS class
		`ag-family-icon`,
		// Lock padlock class (separate from family icon)
		`ag-lock-padlock`,
		// SVG title elements for both families
		`<title>'+esc(t('ag.name'))+'</title>`,
		`<title>Claude</title>`,
	} {
		if !strings.Contains(dashboard.HTML, want) {
			t.Errorf("dashboard.HTML missing AG icon/accessibility element: %q", want)
		}
	}

	// Node script: run the actual JS helpers and renderAGFamilySection to verify output.
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite, Date,
  document: { addEventListener() {}, getElementById() { return { value: '0' }; },
    createElement() { return { set textContent(v){this._v=String(v);}, get innerHTML(){return this._v||'';} }; }
  },
  localStorage: { getItem() { return null; }, setItem() {} },
  setInterval() { return 0; }, clearInterval() {},
  setTimeout() { return 0; }, clearTimeout() {},
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(ok, msg) { if (!ok) throw new Error(msg); }

// ── agFamilyIcon helper ───────────────────────────────────────────────────────
const geminiSvg = ctx.agFamilyIcon('gemini');
assert(geminiSvg.indexOf('<svg') !== -1, 'gemini: must return SVG');
assert(geminiSvg.indexOf('ag-family-icon') !== -1, 'gemini: ag-family-icon class');
assert(geminiSvg.indexOf('aria-hidden="true"') !== -1, 'gemini: aria-hidden');
assert(geminiSvg.indexOf('<title>안티그래비티</title>') !== -1, 'gemini: title element');
assert(geminiSvg.indexOf('focusable="false"') !== -1, 'gemini: non-focusable');
assert(geminiSvg.indexOf('<path') !== -1, 'gemini: uses path for diamond shape');
assert(geminiSvg.indexOf('M11.04 19.32') !== -1, 'gemini: official sparkle path');
assert(!/https?:\/\//i.test(geminiSvg), 'gemini: no external URL');
assert(geminiSvg.indexOf('<img') === -1, 'gemini: no img tag');

const claudeSvg = ctx.agFamilyIcon('claude');
assert(claudeSvg.indexOf('<svg') !== -1, 'claude: must return SVG');
assert(claudeSvg.indexOf('ag-family-icon') !== -1, 'claude: ag-family-icon class');
assert(claudeSvg.indexOf('aria-hidden="true"') !== -1, 'claude: aria-hidden');
assert(claudeSvg.indexOf('<title>Claude</title>') !== -1, 'claude: title element');
assert(claudeSvg.indexOf('focusable="false"') !== -1, 'claude: non-focusable');
assert(claudeSvg.indexOf('<path') !== -1, 'claude: uses path');
assert(claudeSvg.indexOf('M18.7657 62.4437') !== -1, 'claude: official spark path');
assert(!/https?:\/\//i.test(claudeSvg), 'claude: no external URL');
assert(claudeSvg.indexOf('<img') === -1, 'claude: no img tag');

// ── Shape distinctness ────────────────────────────────────────────────────────
assert(geminiSvg !== claudeSvg, 'icons must differ structurally');
assert(claudeSvg.indexOf('M11.04 19.32') === -1, 'claude: must not contain gemini path');

// ── renderAGFamilySection normal path ────────────────────────────────────────
const agWindows = [
  {name:'Gemini Models / Five Hour Limit Remaining', remaining_percent:55},
  {name:'Gemini Models / Weekly Limit Remaining',    remaining_percent:80},
  {name:'Claude and GPT models / Five Hour Limit Remaining', remaining_percent:30},
  {name:'Claude and GPT models / Weekly Limit Remaining',    remaining_percent:70},
];
const geminiSection = ctx.renderAGFamilySection('gemini', agWindows);
assert(geminiSection.indexOf('aria-label="안티그래비티 쿼터"') !== -1, 'gemini section: aria-label');
assert(geminiSection.indexOf('title="안티그래비티"') !== -1,           'gemini section: title tooltip');
assert(geminiSection.indexOf(geminiSvg)  !== -1,                 'gemini section: icon SVG embedded');
assert(geminiSection.indexOf('<title>안티그래비티</title>') !== -1,    'gemini section: SVG title');

const claudeSection = ctx.renderAGFamilySection('claude', agWindows);
assert(claudeSection.indexOf('aria-label="Claude 쿼터"') !== -1, 'claude section: aria-label');
assert(claudeSection.indexOf('title="Claude"') !== -1,           'claude section: title tooltip');
assert(claudeSection.indexOf(claudeSvg)  !== -1,                 'claude section: icon SVG embedded');
assert(claudeSection.indexOf('<title>Claude</title>') !== -1,    'claude section: SVG title');

// Ordering in the combined render.
const fullAG = ctx.renderQuotaGroups(agWindows, 'antigravity', {key:'active'});
assert(fullAG.indexOf('title="안티그래비티"') < fullAG.indexOf('title="Claude"'),
  'Gemini section must appear before Claude section');
assert(fullAG.indexOf('aria-label="안티그래비티 쿼터"') < fullAG.indexOf('aria-label="Claude 쿼터"'),
  'Gemini aria-label before Claude aria-label in output');

// ── renderAGFamilySection locked path ────────────────────────────────────────
const lockedWindows = [
  {name:'Gemini Models / Five Hour Limit Remaining', remaining_percent:0, reset_after_seconds:3600},
  {name:'Gemini Models / Weekly Limit Remaining',    remaining_percent:60},
];
const lockedSection = ctx.renderAGFamilySection('gemini', lockedWindows);
assert(lockedSection.indexOf('is-locked') !== -1,          'locked: is-locked class');
assert(lockedSection.indexOf('aria-label="안티그래비티 쿼터"') !== -1, 'locked: aria-label preserved');
assert(lockedSection.indexOf('>안티그래비티<') !== -1,           'locked: Gemini text node in lock title');
assert(lockedSection.indexOf('ag-lock-padlock') !== -1,    'locked: padlock uses ag-lock-padlock class');
assert(lockedSection.indexOf('ag-family-icon') !== -1,     'locked: family icon in lock title');
assert(/<img/i.test(lockedSection) === false,              'locked: no img tag');
assert(/https?:\/\//i.test(lockedSection) === false,       'locked: no external URL');

console.log('OK_AG_ICON_TESTS');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_AG_ICON_TESTS") {
		t.Fatalf("AG icon/accessibility node tests failed: %v\n%s", err, out)
	}
}
