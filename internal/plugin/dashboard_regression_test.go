package plugin

import (
	"quota-monitor/internal/dashboard"
	"strings"
	"testing"
)

// TestDashboardRegressionTokens verifies required static tokens for CSS, accessibility, and timeout.
func TestDashboardRegressionTokens(t *testing.T) {
	tokens := []string{
		"grid-auto-rows: 1fr;",
		"min-height: 226px",
		"height: 100%",
		".quota-groups { display: flex; flex-direction: column; gap: 5px; margin-top: 0; min-width: 0; }",
		".quota-group { min-width: 0; margin-top: 0; padding: 6px 0 0;",
		".card-header { display: block; min-width: 0; min-height: 38px; }",
		".quota-lock-state { display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 58px;",
		".card-heading { display: flex; flex-direction: column; gap: 6px; min-width: 0; }",
		".card-quota { flex: 1 0 auto; display: flex; flex-direction: column; justify-content: center; min-width: 0; }",
		".credit-summary { display: flex; flex-direction: column; gap: 4px; margin-top: 0;",
		"staleNoticeBox",
		"accountStatusLive",
		"FETCH_TIMEOUT_MS = 16000",
		"startResetCountdownTick",
		"getAccountObservedTime",
		"isDisplayingStale",
		"다시 시도",
	}
	for _, tok := range tokens {
		if !strings.Contains(dashboard.HTML, tok) {
			t.Errorf("dashboard.HTML missing required token %q", tok)
		}
	}

	// Verify aria-live is removed from accountsList container
	if strings.Contains(dashboard.HTML, `id="accountsList" class="cards-grid" role="region" aria-label="계정 목록" aria-live=`) {
		t.Error("accountsList container must not retain aria-live attribute")
	}

	// Verify short status live region exists
	if !strings.Contains(dashboard.HTML, `id="accountStatusLive" class="sr-only" role="status" aria-live="polite" aria-atomic="true"`) {
		t.Error("missing accountStatusLive short status live region")
	}

	// Verify 320px responsive CSS rules
	for _, cssRule := range []string{
		"@media (max-width: 359px)",
		"overflow-wrap: anywhere",
		"min-width: 0",
		".credit-reset-summary { display: flex; flex-direction: column;",
		".credit-api-summary { display: flex; flex-wrap: wrap;",
		".credit-reset-primary { display: flex; flex-wrap: wrap;",
		".credit-reset-expiry { display: inline-flex; flex-wrap: nowrap; align-items: flex-start;",
		".credit-reset-expiry-tree { flex: 0 0 auto; white-space: pre; }",
		".credit-reset-expiry-text { flex: 1 1 auto; min-width: 0; overflow-wrap: anywhere; }",
		".credit-summary-detail { min-width: 0; max-width: 100%; overflow-wrap: anywhere; }",
	} {
		if !strings.Contains(dashboard.HTML, cssRule) {
			t.Errorf("missing 320px responsive CSS rule %q", cssRule)
		}
	}
	if !strings.Contains(dashboard.HTML, "function formatResetExpiryKo(") {
		t.Error("missing formatResetExpiryKo function in dashboard.HTML")
	}
}

// TestRegressionDashboardHTMLFeatures verifies all standard Dashboard HTML features.
func TestRegressionDashboardHTMLFeatures(t *testing.T) {
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
		"padding: 0 14px 16px;",
		"padding: 0 10px 12px;",
		"남은 쿼터",
		"effectiveUsed",
		"fmtPct",
		"scheduleNextPoll",
		"computeAdaptivePollMs",
		"next_poll_hint_ms",
		"setTimeout",
		"POLL_FALLBACK_ACTIVE_MS",
		"current_account",
		"data-current",
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

	for _, forbiddenUI := range []string{
		"운영 상태", "op-panel", "quota-ring-arc", "quota-group-badge", "quota-bar-track", "role=\"meter\"",
		"badge-current", "--current-badge-", "최근 토큰 사용 계정",
		"autoRefreshSelect", "qm_auto_refresh_interval", "setInterval",
		"자동 갱신",
		"usage-audit", "Promise.allSettled", "오늘 기록 없음", "오늘 사용량", "오늘 요청",
	} {
		if strings.Contains(dashboard.HTML, forbiddenUI) {
			t.Errorf("dashboard.HTML retains removed UI element %q", forbiddenUI)
		}
	}

	if got := strings.Count(dashboard.HTML, "fetch("); got != 2 {
		t.Errorf("dashboard.HTML fetch calls = %d, want 2 (snapshot and explicit authenticated refresh)", got)
	}

	if !strings.Contains(dashboard.HTML, "MGMT_BASE + '/data/accounts'") {
		t.Errorf("dashboard.HTML must read the snapshot from the authenticated management route")
	}
	if strings.Contains(dashboard.HTML, "/v0/resource/plugins/quota-monitor/data") {
		t.Errorf("dashboard.HTML must not call the unauthenticated /v0/resource prefix for data")
	}
}

// TestRegressionPastelTokensAndProviderIcons verifies pastel design tokens and SVG icons.
func TestRegressionPastelTokensAndProviderIcons(t *testing.T) {
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
			t.Errorf("dashboard.HTML missing expected token: %q", tok)
		}
	}

	for _, forbid := range []string{
		"<img", "unpkg.com", "cdn.", "cdnjs.cloudflare.com", "linear-gradient(", "radial-gradient(",
	} {
		if strings.Contains(dashboard.HTML, forbid) {
			t.Errorf("dashboard.HTML contains forbidden pattern: %q", forbid)
		}
	}
}

// TestDashboardXSSPreventionNodeContract verifies dynamic rendering safety against XSS attacks.
func TestDashboardXSSPreventionNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

function fakeNode(tag) {
  const node = {
    tagName: tag, _children: [], style: {}, className: '',
    attrs: {}, _text: '', _html: '',
    appendChild(c) {
      if (c && c._isFragment) {
        for (const child of c._children) {
          this._children.push(child);
        }
        c._children = [];
        return c;
      }
      this._children.push(c);
      return c;
    },
    setAttribute(k, v) { this.attrs[k] = v; },
    getAttribute(k) { return this.attrs[k] || null; },
    classList: {
      add(c) { if (!this._classes) this._classes = []; this._classes.push(c); },
      remove(c) { if (this._classes) this._classes = this._classes.filter(x => x !== c); }
    },
  };
  Object.defineProperty(node, 'textContent', {
    get() {
      if (this._children && this._children.length > 0) {
        return this._children.map(c => c.textContent).join('');
      }
      return this._text || '';
    },
    set(v) {
      this._text = String(v);
      this._html = this._text
        .replace(/&/g, '&amp;').replace(/</g, '&lt;')
        .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
      this._children = [];
    },
  });
  Object.defineProperty(node, 'innerHTML', {
    get() {
      if (this._html) return this._html;
      return (this._text || '')
        .replace(/&/g, '&amp;').replace(/</g, '&lt;')
        .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    },
    set(v) { this._html = String(v); this._text = String(v); this._children = []; },
  });
  return node;
}

function makeDoc() {
  const byId = {};
  return {
    hidden: false,
    activeElement: null,
    addEventListener() {},
    createElement(tag) { return fakeNode(tag); },
    createDocumentFragment() {
      const frag = fakeNode('#fragment');
      frag._isFragment = true;
      return frag;
    },
    getElementById(id) {
      if (!byId[id]) byId[id] = fakeNode('div');
      return byId[id];
    },
  };
}

const doc = makeDoc();
const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite, Date,
  document: doc,
  localStorage: { getItem() { return null; }, setItem() {} },
  setTimeout(fn, ms) { return 1; }, clearTimeout() {},
};
vm.createContext(ctx);
vm.runInContext(script, ctx);

function assert(cond, msg) { if (!cond) throw new Error('FAIL: ' + msg); }

// Test esc() helper escapes dangerous HTML characters
assert(ctx.esc('<script>alert(1)</script>') === '&lt;script&gt;alert(1)&lt;/script&gt;', 'esc escapes script tags');
assert(ctx.esc('"><img src=x onerror=alert(1)>') === '&quot;&gt;&lt;img src=x onerror=alert(1)&gt;', 'esc escapes img tags');

// Test renderAccounts with malicious payload
const maliciousAccounts = [{
  account_key: 'xss-key"><script>alert(2)</script>',
  account: '<img src=x onerror=alert(1)>',
  alias: '<script>evil()</script>',
  provider: 'codex<script>',
  plan_type: 'pro"><svg onload=alert(3)>',
  endpoint: 'direct',
  status: 'known',
  operational: { status: 'active' },
  windows: [{ name: '<b onmouseover=evil()>limit</b>', remaining_percent: 50 }]
}];

ctx.renderAccounts(maliciousAccounts);
const listEl = doc.getElementById('accountsList');
assert(listEl._children.length > 0, 'cards rendered');
const card = listEl._children[0];

// Ensure account_key is safely set via setAttribute
assert(card.attrs['data-account-key'] === 'xss-key"><script>alert(2)</script>', 'attribute safe');

console.log('OK_XSS_PREVENTION');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_XSS_PREVENTION") {
		t.Fatalf("XSS prevention node tests failed: %v\n%s", err, out)
	}
}

// TestDashboardLongTextAndLargeVolumeNodeContract verifies rendering stability with long strings and 500 accounts.
func TestDashboardLongTextAndLargeVolumeNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

function fakeNode(tag) {
  const node = {
    tagName: tag, _children: [], style: {}, className: '',
    attrs: {}, _text: '', _html: '',
    appendChild(c) {
      if (c && c._isFragment) {
        for (const child of c._children) this._children.push(child);
        c._children = [];
        return c;
      }
      this._children.push(c);
      return c;
    },
    setAttribute(k, v) { this.attrs[k] = v; },
    getAttribute(k) { return this.attrs[k] || null; },
    classList: { add(c) {}, remove(c) {} },
  };
  Object.defineProperty(node, 'textContent', {
    get() {
      if (this._children && this._children.length > 0) return this._children.map(c => c.textContent).join('');
      return this._text || '';
    },
    set(v) { this._text = String(v); this._html = String(v); this._children = []; },
  });
  Object.defineProperty(node, 'innerHTML', {
    get() { return this._html || this._text || ''; },
    set(v) { this._html = String(v); this._text = String(v); this._children = []; },
  });
  return node;
}

function makeDoc() {
  const byId = {};
  return {
    hidden: false,
    activeElement: null,
    addEventListener() {},
    createElement(tag) { return fakeNode(tag); },
    createDocumentFragment() {
      const frag = fakeNode('#fragment');
      frag._isFragment = true;
      return frag;
    },
    getElementById(id) {
      if (!byId[id]) byId[id] = fakeNode('div');
      return byId[id];
    },
  };
}

const doc = makeDoc();
const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite, Date,
  document: doc,
  localStorage: { getItem() { return null; }, setItem() {} },
  setTimeout(fn, ms) { return 1; }, clearTimeout() {},
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(cond, msg) { if (!cond) throw new Error('FAIL: ' + msg); }

// 1. Long text robustness (2000+ chars)
const longStr = 'a'.repeat(2500) + '@example.com';
const longAccounts = [{
  account_key: 'k-long',
  account: longStr,
  alias: 'long-alias-' + 'x'.repeat(1500),
  provider: 'antigravity',
  endpoint: 'direct',
  status: 'known',
  operational: { status: 'active' },
  windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 50 }]
}];

ctx.renderAccounts(longAccounts);
const listEl = doc.getElementById('accountsList');
assert(listEl._children.length === 1, 'long text card rendered successfully');

// 2. Large volume (500 accounts)
const largeAccounts = [];
for (let i = 0; i < 500; i++) {
  largeAccounts.push({
    account_key: 'acc-' + i,
    account: 'user' + i + '@domain.test',
    alias: 'alias' + i,
    provider: (i % 2 === 0) ? 'antigravity' : 'codex',
    endpoint: (i % 3 === 0) ? 'direct' : (i % 3 === 1 ? 'warp1' : 'warp2'),
    status: (i % 5 === 0) ? 'stale' : 'known',
    operational: { status: 'active' },
    windows: [{ name: 'Five Hour', remaining_percent: (i % 100) }]
  });
}

ctx.cachedAccounts = largeAccounts;
assert(typeof ctx.renderKPIs === 'undefined', 'removed account summary must not be rendered');

ctx.renderAccounts(largeAccounts);
assert(listEl._children.length === 500, '500 accounts rendered without failure');

// 3. Live region announced correct count
const liveEl = doc.getElementById('accountStatusLive');
assert(liveEl.textContent === '계정 500개 표시 중', 'live region announced count');

console.log('OK_LONG_AND_LARGE_VOLUME');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_LONG_AND_LARGE_VOLUME") {
		t.Fatalf("Long text and large volume node tests failed: %v\n%s", err, out)
	}
}

// TestDashboardConfiguredFalseAndStaleNoticeNodeContract verifies configured=false / data.error handling and stale preservation.
func TestDashboardConfiguredFalseAndStaleNoticeNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

function fakeNode(tag) {
  const node = {
    tagName: tag, _children: [], style: {}, className: '',
    attrs: {}, _text: '', _html: '',
    appendChild(c) {
      if (c && c._isFragment) {
        for (const child of c._children) this._children.push(child);
        c._children = [];
        return c;
      }
      this._children.push(c);
      return c;
    },
    setAttribute(k, v) { this.attrs[k] = v; },
    getAttribute(k) { return this.attrs[k] || null; },
    classList: { add(c) {}, remove(c) {} },
  };
  Object.defineProperty(node, 'textContent', {
    get() {
      if (this._children && this._children.length > 0) return this._children.map(c => c.textContent).join('');
      return this._text || '';
    },
    set(v) { this._text = String(v); this._html = String(v); this._children = []; },
  });
  Object.defineProperty(node, 'innerHTML', {
    get() { return this._html || this._text || ''; },
    set(v) { this._html = String(v); this._text = String(v); this._children = []; },
  });
  return node;
}

function makeDoc() {
  const byId = {};
  return {
    hidden: false,
    activeElement: null,
    addEventListener() {},
    createElement(tag) { return fakeNode(tag); },
    createDocumentFragment() {
      const frag = fakeNode('#fragment');
      frag._isFragment = true;
      return frag;
    },
    getElementById(id) {
      if (!byId[id]) byId[id] = fakeNode('div');
      return byId[id];
    },
  };
}

const doc = makeDoc();
const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite, Date,
  document: doc,
  localStorage: { getItem() { return null; }, setItem() {} },
  setTimeout(fn, ms) { return 1; }, clearTimeout() {},
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(cond, msg) { if (!cond) throw new Error('FAIL: ' + msg); }

// Account freshness extracts real last_success/observed_at timestamps
const testAccounts = [
  { account_key: '1', last_success: '2026-03-30T10:00:00Z', windows: [] },
  { account_key: '2', observed_at: '2026-03-30T12:30:00Z', windows: [{ observed_at: '2026-03-30T14:15:00Z' }] }
];
assert(ctx.getAccountObservedTime(testAccounts[0]).toISOString() === '2026-03-30T10:00:00.000Z', 'account last_success is preserved');
assert(ctx.getAccountObservedTime(testAccounts[1]).toISOString() === '2026-03-30T14:15:00.000Z', 'account observed time prefers newest window observation');
const freshHTML = ctx.renderAccountFreshness(testAccounts[1]);
assert(freshHTML.indexOf('card-freshness') !== -1, 'card freshness row rendered');
assert(freshHTML.indexOf('마지막 확인') !== -1, 'card freshness has account-specific label');
assert(freshHTML.indexOf('계정 상태 확인:') !== -1, 'card freshness exposes exact timestamp tooltip');
const unknownHTML = ctx.renderAccountFreshness({ account_key: 'unknown' });
assert(unknownHTML.indexOf('아직 확인되지 않음') !== -1, 'missing observation is explicit');
const staleHTML = ctx.renderAccountFreshness(Object.assign({}, testAccounts[1], { stale: true }));
assert(staleHTML.includes('is-failed') && staleHTML.includes('이전 데이터'), 'account stale freshness remains visible');
const failedHTML = ctx.renderAccountFreshness(Object.assign({}, testAccounts[1], { error: 'quota lookup failed' }));
assert(failedHTML.includes('is-failed') && failedHTML.includes('이전 데이터'), 'account error freshness remains visible');
const beforeSync = [{ account_key: 'sync', last_success: '2026-03-30T14:00:00Z', windows: [] }];
const afterSync = [{ account_key: 'sync', last_success: '2026-03-30T14:01:00Z', windows: [] }];
const syncDiff = ctx.diffQuotaSnapshots(ctx.buildQuotaSnapshot(beforeSync), afterSync);
assert(syncDiff['sync#0'].sync === 'flash-sync', 'new account observation triggers card sync flash');

// Test showError produces a retry button with '다시 시도'
ctx.showError('네트워크 실패');
const errBox = doc.getElementById('errorBox');
assert(errBox.style.display === 'block', 'error box displayed');
assert(errBox.textContent.indexOf('네트워크 실패') !== -1, 'error message in error box: ' + errBox.textContent);
assert(errBox.textContent.indexOf('다시 시도') !== -1, 'retry button with 다시 시도 in error box');

console.log('OK_CONFIGURED_AND_STALE');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_CONFIGURED_AND_STALE") {
		t.Fatalf("Configured=false / stale node tests failed: %v\n%s", err, out)
	}
}

// TestDashboardAdaptiveResetPollingNodeContract verifies snapshot polling around exhausted quota resets.
func TestDashboardAdaptiveResetPollingNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
let mockNow = 1774872000000;
class MockDate extends Date {
  constructor(...args) { super(...(args.length ? args : [mockNow])); }
  static now() { return mockNow; }
}
const ctx = {
  console, Date: MockDate,
  document: { hidden: false, addEventListener() {} },
  localStorage: { getItem() { return null; }, setItem() {} },
  setTimeout() { return 1; }, clearTimeout() {},
  fetch() { throw new Error('poll computation must not fetch or probe'); },
};
vm.createContext(ctx);
vm.runInContext(scriptMatch[1], ctx);
function assertPoll(hint, accounts, expected, msg) {
  const actual = ctx.computeAdaptivePollMs(hint, accounts);
  if (actual !== expected) throw new Error(msg + ': got ' + actual + ', want ' + expected);
}
function at(seconds) { return new Date(mockNow + seconds * 1000).toISOString(); }
function account(windows, extra = {}) {
  return Object.assign({ provider: 'codex', status: 'known', windows }, extra);
}
function exhausted(seconds) { return { name: 'primary', used_percent: 100, reset_at: at(seconds) }; }
const healthy = account([{ name: 'primary', used_percent: 20, reset_at: at(1) }]);
const current = account(healthy.windows, {
  current_account: true, current_kind: 'recent', current_reason: 'recent_usage',
});

// Normal cadence must remain intact when no exhausted deadline needs attention.
assertPoll(null, [], 900000, 'empty accounts retain slow fallback');
assertPoll(null, [account([])], 300000, 'pending accounts retain stale fallback');
assertPoll(null, [healthy], 90000, 'healthy future reset does not accelerate polling');
assertPoll(null, [current], 30000, 'recent current account retains active fallback');
assertPoll(720000, [healthy], 720000, 'healthy accounts retain server hint');
assertPoll(1200000, [healthy], 900000, 'normal server hint retains fifteen-minute cap');
assertPoll(null, [account([{ name: 'primary', used_percent: 100 }])], 900000, 'limited without reset retains slow fallback');

// A slow hint or fallback must not hide a future exhausted window reset.
assertPoll(900000, [account([exhausted(120)])], 125000, 'future reset caps server hint at deadline plus five seconds');
assertPoll(null, [account([exhausted(120)])], 125000, 'future reset caps limited fallback');
assertPoll(900000, [account([exhausted(0.1)])], 5100, 'subsecond future reset still gets five-second grace');
assertPoll(900000, [account([{ remaining_percent: 0, reset_after_seconds: 0.25 }])], 5250, 'relative reset respects five-second floor');
assertPoll(900000, [account([{ limit_reached: true, reset_at: at(10) }])], 15000, 'explicit exhaustion is recognized');
assertPoll(900000, [account([{ allowed: false, reset_at: at(10) }])], 15000, 'disallowed window is exhausted');
assertPoll(900000, [account([{ remaining_percent: 0, reset_at: at(10) }])], 15000, 'zero remaining window is exhausted');
assertPoll(900000, [account([{ used_percent: 100, reset_after_seconds: 120 }])], 125000, 'relative reset fallback is supported');
assertPoll(900000, [account([{ used_percent: 100, reset_at: at(120), reset_after_seconds: 3600 }])], 125000, 'absolute reset takes precedence');
assertPoll(10000, [account([exhausted(120)])], 10000, 'reset cap never lengthens shorter hint');
assertPoll(900000, [account([exhausted(3600)])], 900000, 'distant reset preserves normal cap');
assertPoll(null, [account([exhausted(10)], {
  current_account: true, current_kind: 'recent', current_reason: 'recent_usage',
})], 15000, 'current exhausted account still observes reset deadline');
assertPoll(900000, [account([exhausted(120), exhausted(10)])], 15000, 'earliest exhausted deadline wins');
assertPoll(900000, [account([exhausted(120)]), account([exhausted(10)])], 15000, 'earliest account deadline wins');

// Current/past reset timestamps should not leave the snapshot waiting fifteen minutes.
assertPoll(900000, [account([exhausted(0)])], 30000, 'reset reached now polls within thirty seconds');
assertPoll(null, [account([exhausted(-120)])], 30000, 'expired exhausted reset caps fallback');
assertPoll(900000, [account([exhausted(-3600)])], 30000, 'long-expired reset caps server hint');
assertPoll(900000, [account([{ used_percent: 100, reset_after_seconds: 0 }])], 30000, 'zero relative reset caps polling');
assertPoll(900000, [account([{ used_percent: 100, reset_after_seconds: -5 }])], 30000, 'negative relative reset caps polling');
assertPoll(10000, [account([exhausted(-1)])], 10000, 'past reset preserves faster server cadence');
const ticking = account([exhausted(10)]);
assertPoll(900000, [ticking], 15000, 'reset starts in future');
mockNow += 10000;
assertPoll(900000, [ticking], 30000, 'reset crossing deadline changes cap');

// Disabled accounts, healthy windows, reset credits and invalid data are not reset signals.
assertPoll(null, [account([exhausted(-1)], { operational: { disabled: true } })], 900000, 'disabled accounts retain slow fallback');
assertPoll(900000, [account([exhausted(1)], { operational: { disabled: true } })], 900000, 'disabled future reset ignores server cap');
assertPoll(900000, [account([{ used_percent: 99, reset_at: at(-1) }])], 900000, 'healthy past reset is ignored');
assertPoll(900000, [account([{ used_percent: 0, reset_at: at(1) }])], 900000, 'healthy future reset is ignored');
assertPoll(900000, [account([{ used_percent: 100 }], {
  reset_credits: { available_count: 1, nearest_expiry: at(1) },
})], 900000, 'reset credit expiry does not accelerate polling');
for (const invalid of [
  { reset_at: 'not-a-date' },
  { reset_at: 'not-a-date', reset_after_seconds: 1 },
  { reset_after_seconds: 'not-a-number' },
  { reset_after_seconds: Infinity },
  { reset_after_seconds: '' },
  { reset_after_seconds: null },
  {},
]) {
  assertPoll(900000, [account([Object.assign({ used_percent: 100 }, invalid)])], 900000, 'invalid or missing reset is ignored: ' + JSON.stringify(invalid));
}
console.log('OK_ADAPTIVE_RESET_POLLING');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_ADAPTIVE_RESET_POLLING") {
		t.Fatalf("Adaptive reset polling node tests failed: %v\n%s", err, out)
	}
}

// TestDashboardResetAtPriorityAndTickNodeContract verifies reset_at priority over reset_after_seconds.
func TestDashboardResetAtPriorityAndTickNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

function fakeNode(tag) {
  const node = {
    tagName: tag, _children: [], style: {}, className: '',
    attrs: {}, _text: '', _html: '',
    appendChild(c) { this._children.push(c); return c; },
    setAttribute(k, v) { this.attrs[k] = v; },
    getAttribute(k) { return this.attrs[k] || null; },
    classList: { add(c) {}, remove(c) {} },
  };
  Object.defineProperty(node, 'textContent', {
    get() { return this._text; },
    set(v) { this._text = String(v); this._htmlSet = false; },
  });
  return node;
}

const doc = {
  hidden: false,
  addEventListener() {},
  createElement(tag) { return fakeNode(tag); },
  getElementById(id) { return fakeNode('div'); },
};

// Freeze Date.now() to 2026-03-30T12:00:00Z (1774872000000), then advance 30s.
let mockNow = 1774872000000;
class MockDate extends Date {
  constructor(...args) {
    if (args.length === 0) super(mockNow);
    else super(...args);
  }
  static now() { return mockNow; }
}

const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite,
  Date: MockDate,
  document: doc,
  localStorage: { getItem() { return null; }, setItem() {} },
  setTimeout(fn, ms) { return 1; }, clearTimeout() {},
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(cond, msg) { if (!cond) throw new Error('FAIL: ' + msg); }

// Window with reset_at in 10 minutes (600s), but static reset_after_seconds = 3600 (1 hour stale)
const win = {
  reset_at: new Date(mockNow + 600 * 1000).toISOString(),
  reset_after_seconds: 3600
};

// resetSeconds must prefer reset_at (600s = 10분) over reset_after_seconds (3600s = 1시간)
const sec = ctx.resetSeconds(win);
assert(sec === 600, 'reset_at takes precedence over reset_after_seconds: got ' + sec + ' want 600');

const relReset = ctx.formatRelativeReset(win);
assert(relReset.indexOf('10분') !== -1, 'formatRelativeReset reflects reset_at: ' + relReset);

// Window with only reset_after_seconds falls back correctly
const winFallback = {
  reset_after_seconds: 1800
};
const secFb = ctx.resetSeconds(winFallback);
assert(secFb === 1800, 'fallback to reset_after_seconds when reset_at absent: got ' + secFb);

// Regression: the 30-second dashboard tick crosses reset_at to exactly zero.
const boundaryWin = { reset_at: new Date(mockNow + 30000).toISOString() };
assert(ctx.formatRelativeReset(boundaryWin) === '30초 후 초기화', 'boundary starts at 30 seconds');
mockNow += 30000;
assert(ctx.resetSeconds(boundaryWin) === 0, '30-second tick reaches zero boundary');
assert(ctx.formatRelativeReset(boundaryWin) === '초기화 시각 도달 · 갱신 대기', 'zero boundary explains that a refresh is pending');
assert(ctx.formatRelativeReset(boundaryWin).indexOf('0초 후 초기화') === -1, 'zero boundary never renders zero-second countdown');
mockNow += 30000;
assert(ctx.formatRelativeReset(boundaryWin) === '초기화 시각 도달 · 갱신 대기', 'past reset remains explicitly pending refresh');
assert(ctx.formatRelativeReset({ reset_after_seconds: 0 }) === '초기화 시각 도달 · 갱신 대기', 'zero relative reset explicitly waits for refresh');
assert(ctx.formatRelativeReset({ reset_after_seconds: -1 }) === '초기화 시각 도달 · 갱신 대기', 'negative relative reset explicitly waits for refresh');
assert(ctx.formatRelativeReset({}) === '초기화 미확인', 'missing reset data remains unconfirmed');

console.log('OK_RESET_AT_PRIORITY');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_RESET_AT_PRIORITY") {
		t.Fatalf("Reset_at priority node tests failed: %v\n%s", err, out)
	}
}

// TestDashboardProviderRevertOnDisappearanceNodeContract verifies selectedProvider reverts to 'all' if provider disappears.
func TestDashboardProviderRevertOnDisappearanceNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

function fakeNode(tag) {
  const node = {
    tagName: tag, _children: [], style: {}, className: '',
    attrs: {}, _text: '', _html: '', _htmlSet: false,
    appendChild(c) { this._children.push(c); return c; },
    setAttribute(k, v) { this.attrs[k] = v; },
    getElementsByClassName(c) { return []; },
  };
  Object.defineProperty(node, 'textContent', {
    get() { return this._text; },
    set(v) { this._text = String(v); this._htmlSet = false; },
  });
  Object.defineProperty(node, 'innerHTML', {
    get() { return this._text; },
    set(v) { this._html = String(v); this._htmlSet = true; },
  });
  return node;
}

const byId = {};
const doc = {
  hidden: false,
  addEventListener() {},
  createElement(tag) { return fakeNode(tag); },
  getElementById(id) {
    if (!byId[id]) byId[id] = fakeNode('div');
    return byId[id];
  },
};

const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite, Date,
  document: doc,
  localStorage: { getItem() { return null; }, setItem() {} },
  setTimeout(fn, ms) { return 1; }, clearTimeout() {},
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(cond, msg) { if (!cond) throw new Error('FAIL: ' + msg); }

// Initially, provider 'codex' is selected
ctx.selectedProvider = 'codex';

// New account list has only 'antigravity' (codex disappeared)
const accounts = [
  { provider: 'antigravity', account: 'ag@test.com' }
];

ctx.populateProviderSegmented(accounts);

// selectedProvider must have reverted to 'all'
assert(ctx.selectedProvider === 'all', 'selectedProvider reverted to all when codex disappeared: got ' + ctx.selectedProvider);

console.log('OK_PROVIDER_REVERT');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_PROVIDER_REVERT") {
		t.Fatalf("Provider revert node tests failed: %v\n%s", err, out)
	}
}

// TestDashboardUnknownPendingStaleSeparationNodeContract verifies canonical status separation.
func TestDashboardUnknownPendingStaleSeparationNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite, Date,
  document: { addEventListener() {}, getElementById() { return { value: '0' }; }, createElement() { return {}; } },
  localStorage: { getItem() { return null; }, setItem() {} },
  setTimeout(fn, ms) { return 1; }, clearTimeout() {},
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(cond, msg) { if (!cond) throw new Error('FAIL: ' + msg); }

// 1. Pending: known status with empty windows
const pendingAcc = { status: 'known', windows: [] };
assert(ctx.getCanonicalStatus(pendingAcc).key === 'pending', 'pending canonical key');
assert(ctx.getAvailabilityState(pendingAcc).key === 'pending', 'pending availability key');

// 2. Stale: stale = true
const staleAcc = { status: 'known', stale: true, windows: [{ remaining_percent: 80 }] };
assert(ctx.getCanonicalStatus(staleAcc).key === 'stale', 'stale canonical key');
assert(ctx.getAvailabilityState(staleAcc).key === 'stale', 'stale availability key');
assert(ctx.getAvailabilityState(staleAcc).label === '정보 지연', 'stale label is 정보 지연');

// 3. Unknown: no status or empty operational
const unknownAcc = { status: 'unknown', windows: [] };
assert(ctx.getCanonicalStatus(unknownAcc).key === 'unknown', 'unknown canonical key');

// Ensure all 3 keys are strictly distinct
assert(ctx.getCanonicalStatus(pendingAcc).key !== ctx.getCanonicalStatus(staleAcc).key, 'pending !== stale');
assert(ctx.getCanonicalStatus(staleAcc).key !== ctx.getCanonicalStatus(unknownAcc).key, 'stale !== unknown');
assert(ctx.getCanonicalStatus(pendingAcc).key !== ctx.getCanonicalStatus(unknownAcc).key, 'pending !== unknown');

console.log('OK_STATUS_SEPARATION');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_STATUS_SEPARATION") {
		t.Fatalf("Status separation node tests failed: %v\n%s", err, out)
	}
}

// TestRegressionAvailabilityAndEndpointSortContract verifies endpoint sort and availability together.
func TestRegressionAvailabilityAndEndpointSortContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite, Date,
  document: { addEventListener() {}, getElementById() { return { value: '0' }; }, createElement() { return { set textContent(v){this._v=String(v);}, get innerHTML(){return (this._v||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');} }; } },
  localStorage: { getItem() { return '0'; }, setItem() {} },
  setTimeout(fn, ms) { return 1; }, clearTimeout() {},
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(ok, msg) { if (!ok) throw new Error('FAIL: ' + msg); }

// Availability contract assertions
assert(ctx.getAvailabilityState({provider:'codex',status:'known',operational:{status:'active'},windows:[{remaining_percent:67}]}).key==='active','active');
assert(ctx.getAvailabilityState({provider:'codex',status:'known',operational:{status:'active'},windows:[{name:'primary',remaining_percent:0},{name:'secondary',remaining_percent:70}]}).key==='limited','5-hour exhaustion is limited');
const weeklyLocked={provider:'codex',status:'known',operational:{status:'active'},windows:[{name:'primary',remaining_percent:67},{name:'secondary',remaining_percent:0,reset_after_seconds:3600}]};
assert(ctx.getAvailabilityState(weeklyLocked).key==='unavailable','weekly exhaustion is unavailable state');
assert(ctx.getAvailabilityState(weeklyLocked).label==='주간 제한','weekly exhaustion label is explicit');
assert(ctx.renderQuotaLock(weeklyLocked,ctx.getAvailabilityState(weeklyLocked)).indexOf('주간 제한')!==-1,'weekly lock panel');
assert(ctx.renderQuotaLock(weeklyLocked,ctx.getAvailabilityState(weeklyLocked)).indexOf('1시간')!==-1,'weekly reset centered');
assert(ctx.getAvailabilityState({provider:'codex',status:'known',operational:{status:'active'},windows:[{name:'primary',remaining_percent:15},{name:'secondary',remaining_percent:70}]}).key==='warning','low remaining is warning');

// Antigravity contract
const ag={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:70},{name:'Claude and GPT models / Weekly Limit Remaining',remaining_percent:0}]};
assert(ctx.getAvailabilityState(ag).key==='active','AG card background follows healthy Gemini despite Claude weekly lock');
const agClaudeError={provider:'antigravity',status:'known',operational:{unavailable:true,status:'error'},windows:[{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:70},{name:'Claude and GPT models / Weekly Limit Remaining',remaining_percent:0}]};
assert(ctx.getAvailabilityState(agClaudeError).key==='active','healthy Gemini + Claude 0 + op.unavailable=true => active');
const agGeminiExhausted5h={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Gemini Models / Five Hour Limit Remaining',remaining_percent:0},{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:70}]};
assert(ctx.getAvailabilityState(agGeminiExhausted5h).key==='limited','Gemini 5h exhaustion => limited');
const agGeminiExhaustedWeekly={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Gemini Models / Weekly Limit Remaining',remaining_percent:0}]};
assert(ctx.getAvailabilityState(agGeminiExhaustedWeekly).key==='unavailable','Gemini weekly exhaustion => unavailable');
const agMissingGemini={provider:'antigravity',status:'known',operational:{status:'active'},windows:[{name:'Claude and GPT models / Weekly Limit Remaining',remaining_percent:80}]};
assert(ctx.getAvailabilityState(agMissingGemini).key==='pending','missing Gemini => pending');

// Probed Codex active vs operational error contract
const codexProbedWithOpError={provider:'codex',status:'known',operational:{unavailable:false,status:'error'},windows:[{name:'primary',remaining_percent:100},{name:'secondary',remaining_percent:100}]};
assert(ctx.getAvailabilityState(codexProbedWithOpError).key==='active','probed 100% codex with op.status=error must be active');
assert(ctx.getCanonicalStatus(codexProbedWithOpError).key==='active','canonical status for probed 100% codex with op.status=error must be active');
const codexResetWithOpUnavailable={provider:'codex',status:'known',operational:{unavailable:true,status:'error'},windows:[{name:'primary',remaining_percent:100},{name:'secondary',remaining_percent:100}]};
assert(ctx.getAvailabilityState(codexResetWithOpUnavailable).key==='active','reset codex with 100% quota must be active');
assert(ctx.getCanonicalStatus(codexResetWithOpUnavailable).key==='active','canonical status for reset codex with 100% quota must be active');

console.log('OK_REGRESSION_CONTRACTS');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_REGRESSION_CONTRACTS") {
		t.Fatalf("Regression contracts node tests failed: %v\n%s", err, out)
	}
}

// TestRegressionCreditSummaryContract verifies credit summary rendering.
func TestRegressionCreditSummaryContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite, Date,
  document: { addEventListener() {}, getElementById() { return { value: '0' }; },
    createElement() { return { set textContent(v){this._v=String(v);}, get innerHTML(){return (this._v||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');} }; }
  },
  localStorage: { getItem() { return null; }, setItem() {} },
  setTimeout(fn, ms) { return 1; }, clearTimeout() {},
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(ok, msg) { if (!ok) throw new Error('FAIL: ' + msg); }

const accWithBoth = {
  reset_credits: {
    available_count: 5,
    applicable_available_count: 2,
    nearest_expiry: '2026-10-04T15:05:29'
  },
  api_credits: { has_credits: false, unlimited: false, balance: 0 }
};
const resBoth = ctx.renderCreditSummary(accWithBoth);
assert(resBoth.indexOf('리셋권 5장 보유') !== -1, 'has owned count');
assert(resBoth.indexOf('현재 적용 가능') === -1, 'applicable count is hidden');
assert(resBoth.indexOf('<div class="credit-reset-primary"><strong>리셋권 5장 보유</strong></div>') !== -1, 'owned text remains the only primary reset text');
assert(resBoth.indexOf('<div class="credit-reset-expiry"><span class="credit-reset-expiry-tree" aria-hidden="true">└ </span><span class="credit-reset-expiry-text">가장 빠른 만료일: 2026. 10. 4. 오후 3:05:29</span></div>') !== -1, 'expiry is a separate tree-style helper row');
assert(resBoth.indexOf('가장 가까운 만료') === -1, 'removed legacy nearest-expiry wording');
assert(resBoth.indexOf('적용 가능한 리셋권') === -1, 'applicability tooltip is hidden');
assert(resBoth.indexOf('class="credit-reset-summary') !== -1, 'reset summary is one semantic group');
assert(resBoth.indexOf('reset-credit-summary') !== -1, 'reset-credit-summary token present');
assert(typeof ctx.formatResetExpiryKo === 'function', 'formatResetExpiryKo is a function');
assert(ctx.formatResetExpiryKo('2026-10-04T15:05:29') === '2026. 10. 4. 오후 3:05:29', 'formatResetExpiryKo formats date');
assert(resBoth.indexOf('class="credit-api-summary"') !== -1, 'API credits use a separate row');
assert(resBoth.indexOf('API 크레딧</strong> · 잔액 0') !== -1, 'explicit API zero balance');
assert(resBoth.indexOf('aria-label="리셋권 5장 보유. 가장 빠른 만료일: 2026. 10. 4. 오후 3:05:29. API 크레딧 · 잔액 0"') !== -1, 'accessible text matches the visible row meanings');

const applicableZero = ctx.renderCreditSummary({
  reset_credits: {
    available_count: 2,
    applicable_available_count: 0,
    nearest_expiry: '2026-10-04T15:05:29'
  }
});
assert(applicableZero.indexOf('리셋권 2장 보유') !== -1, 'owned count survives applicable zero');
assert(applicableZero.indexOf('현재 계정에는 적용 불가') === -1, 'applicable zero is hidden');
assert(applicableZero.indexOf('<div class="credit-reset-primary"><strong>리셋권 2장 보유</strong></div>') !== -1, 'owned count is the only primary reset text');
assert(applicableZero.indexOf('└ </span><span class="credit-reset-expiry-text">가장 빠른 만료일: 2026. 10. 4. 오후 3:05:29') !== -1, 'tree helper uses requested expiry wording');

const applicableNull = ctx.renderCreditSummary({
  reset_credits: { available_count: 3, applicable_available_count: null }
});
assert(applicableNull.indexOf('리셋권 3장 보유') !== -1, 'owned count survives applicable null');
assert(applicableNull.indexOf('현재 계정 적용 여부 미확인') === -1, 'applicable null is hidden');

const availableZero = ctx.renderCreditSummary({
  reset_credits: { available_count: 0, applicable_available_count: 2 }
});
assert(availableZero === '', 'available zero hides the reset summary');
assert(availableZero.indexOf('리셋권 없음') === -1, 'available zero is not emphasized');

const availableNull = ctx.renderCreditSummary({
  reset_credits: { available_count: null, applicable_available_count: 2 }
});
assert(availableNull === '', 'available null is independently treated as unknown and hidden');

const invalidExpiry = ctx.renderCreditSummary({
  reset_credits: { available_count: 1, nearest_expiry: 'not-a-date' }
});
assert(invalidExpiry.indexOf('가장 빠른 만료일') === -1, 'invalid expiry is hidden');

console.log('OK_CREDIT_SUMMARY_REGRESSION');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_CREDIT_SUMMARY_REGRESSION") {
		t.Fatalf("Credit summary regression node tests failed: %v\n%s", err, out)
	}
}

// TestRegressionAGFamilyIconsAndPadlockContract verifies SVG icons and locked states.
func TestRegressionAGFamilyIconsAndPadlockContract(t *testing.T) {
	for _, want := range []string{
		`esc(sectionAria)`,
		`sectionAria = t('q.familyAria'`,
		`aria-hidden="true"`,
		`agFamilyIcon`,
		`M11.04 19.32Q12 21.51`,
		`M18.7657 62.4437L37.1822 52.1167`,
		`ag-family-icon`,
		`ag-lock-padlock`,
		`<title>'+esc(t('ag.name'))+'</title>`,
		`<title>Claude</title>`,
	} {
		if !strings.Contains(dashboard.HTML, want) {
			t.Errorf("dashboard.HTML missing AG icon/accessibility element: %q", want)
		}
	}
}

// TestResetCountdownAccentColorClasses verifies Go-level static tokens for primary/weekly countdown accents.
func TestResetCountdownAccentColorClasses(t *testing.T) {
	requiredTokens := []string{
		"--reset-primary: #c2410c;",
		"--reset-weekly: #4338ca;",
		".quota-reset-primary",
		".quota-reset-weekly",
		"quota-reset-primary",
		"quota-reset-weekly",
		"<small class=\"quota-reset-weekly\">",
		"<span class=\"quota-reset-weekly\">",
		"formatResetCountdownSeconds",
	}
	for _, tok := range requiredTokens {
		if !strings.Contains(dashboard.HTML, tok) {
			t.Errorf("dashboard.HTML missing reset accent token: %q", tok)
		}
	}

	// Verify distinct class names
	primaryClass := "quota-reset-primary"
	weeklyClass := "quota-reset-weekly"
	if primaryClass == weeklyClass {
		t.Fatal("primary reset class and weekly reset class must be distinct")
	}
}

// TestResetCountdownAccentNodeContract verifies Node-level contract for primary vs weekly countdown classes.
func TestResetCountdownAccentNodeContract(t *testing.T) {
	nodeScript := `
const fs = require('fs');
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('no script tag in dashboard html');
const script = scriptMatch[1];

const ctx = {
  document: {
    hidden: false,
    addEventListener() {},
    getElementById(id) { return null; },
    createElement(tag) {
      return {
        className: '',
        style: {},
        setAttribute() {},
        appendChild() {},
        innerHTML: '',
        textContent: '',
      };
    }
  },
  window: { location: { search: '' } },
  console: { log() {}, warn() {}, error() {} },
  setTimeout(fn, ms) { return 1; },
  clearTimeout() {},
  Date: Date,
  Math: Math,
};
vm.createContext(ctx);
vm.runInContext(script, ctx);

function assert(ok, msg) { if (!ok) throw new Error('FAIL: ' + msg); }

const primaryClass = 'quota-reset-primary';
const weeklyClass = 'quota-reset-weekly';
assert(primaryClass !== weeklyClass, 'primary reset class and weekly reset class must be distinct');

// 1. Codex 5h (primary) lock
const codexShortAcc = {
  provider: 'codex',
  windows: [
    { name: 'primary', kind: 'primary', used_percent: 100, reset_after_seconds: 7200 },
    { name: 'secondary', kind: 'secondary', used_percent: 40, reset_after_seconds: 86400 * 3 }
  ]
};
const codexShortAvail = ctx.getAvailabilityState(codexShortAcc);
assert(codexShortAvail.key === 'limited', 'codex short availability should be limited');
const codexShortHTML = ctx.renderQuotaLock(codexShortAcc, codexShortAvail);
assert(codexShortHTML.includes('quota-lock-time ' + primaryClass), 'codex short lock must have primary reset class on quota-lock-time');
assert(!codexShortHTML.includes('quota-lock-time ' + weeklyClass), 'codex short lock must NOT have weekly reset class on main quota-lock-time');
assert(codexShortHTML.includes('5시간 제한'), 'codex short lock must retain 5시간 제한 label');
assert(codexShortHTML.includes('aria-label="5시간 제한 잠금'), 'codex short lock must retain accessibility label');
assert(codexShortHTML.includes('small class="' + weeklyClass + '"'), 'codex short lock weekly extra must have weekly reset class');
assert(codexShortHTML.includes('주간 잔여 <strong>60%</strong><span aria-hidden="true"> · </span><small class="' + weeklyClass + '">3일 후 초기화</small>'), 'codex weekly context is a single independent line');
assert((codexShortHTML.match(/>2시간 후 초기화</g) || []).length === 1, 'codex primary visible countdown appears exactly once');
assert((codexShortHTML.match(/>3일 후 초기화</g) || []).length === 1, 'codex weekly visible countdown appears exactly once');

// 2. Codex weekly (secondary) lock - even when remaining time is 20 hours
const codexWeeklyAcc = {
  provider: 'codex',
  windows: [
    { name: 'primary', kind: 'primary', used_percent: 20, reset_after_seconds: 3600 },
    { name: 'secondary', kind: 'secondary', used_percent: 100, reset_after_seconds: 20 * 3600 } // 20 hours remaining in weekly
  ]
};
const codexWeeklyAvail = ctx.getAvailabilityState(codexWeeklyAcc);
assert(codexWeeklyAvail.key === 'unavailable', 'codex weekly availability should be unavailable');
const codexWeeklyHTML = ctx.renderQuotaLock(codexWeeklyAcc, codexWeeklyAvail);
assert(codexWeeklyHTML.includes('quota-lock-time ' + weeklyClass), 'codex weekly lock must have weekly reset class even when 20h remaining');
assert(!codexWeeklyHTML.includes('quota-lock-time ' + primaryClass), 'codex weekly lock must NOT have primary reset class');
assert(codexWeeklyHTML.includes('주간 제한'), 'codex weekly lock must retain 주간 제한 label');
assert(codexWeeklyHTML.includes('aria-label="주간 제한 잠금'), 'codex weekly lock must retain accessibility label');

// 3. Antigravity 5h (short) family lock
const agShortWindows = [
  { name: 'gemini-flash-five-hour', kind: 'five-hour', window_minutes: 300, used_percent: 100, reset_after_seconds: 3600 },
  { name: 'gemini-flash-weekly', kind: 'weekly', window_minutes: 10080, used_percent: 20, reset_after_seconds: 86400 * 4 }
];
const agShortSection = ctx.renderAGFamilySection('gemini', agShortWindows);
assert(agShortSection.includes('ag-family-lock-time ' + primaryClass), 'AG short lock must have primary reset class on ag-family-lock-time');
assert(!agShortSection.includes('ag-family-lock-time ' + weeklyClass), 'AG short lock must NOT have weekly reset class on main time');
assert(agShortSection.includes('5시간 제한'), 'AG short lock must retain 5시간 제한 label');
assert(agShortSection.includes('span class="' + weeklyClass + '"'), 'AG short lock context must have weekly reset class');
assert(agShortSection.includes('주간 잔여 <strong>80%</strong><span aria-hidden="true"> · </span><span class="' + weeklyClass + '">4일 후 초기화</span>'), 'AG weekly context is a single independent line');
assert((agShortSection.match(/>1시간 후 초기화</g) || []).length === 1, 'AG primary visible countdown appears exactly once');
assert((agShortSection.match(/>4일 후 초기화</g) || []).length === 1, 'AG weekly visible countdown appears exactly once');

// 4. Antigravity weekly (long) family lock - even when remaining time is 20 hours
const agWeeklyWindows = [
  { name: 'claude-five-hour', kind: 'five-hour', window_minutes: 300, used_percent: 10, reset_after_seconds: 1800 },
  { name: 'claude-weekly', kind: 'weekly', window_minutes: 10080, used_percent: 100, reset_after_seconds: 20 * 3600 } // 20 hours remaining in weekly
];
const agWeeklySection = ctx.renderAGFamilySection('claude', agWeeklyWindows);
assert(agWeeklySection.includes('ag-family-lock-time ' + weeklyClass), 'AG weekly lock must have weekly reset class even when 20h remaining');
assert(!agWeeklySection.includes('ag-family-lock-time ' + primaryClass), 'AG weekly lock must NOT have primary reset class');
assert(agWeeklySection.includes('주간 제한'), 'AG weekly lock must retain 주간 제한 label');

// 5. Explicit 20h unit string vs window kind verification
assert(codexWeeklyHTML.includes('20시간') && codexWeeklyHTML.includes(weeklyClass), 'codex weekly lock with 20h string must have weekly reset class');
assert(agWeeklySection.includes('20시간') && agWeeklySection.includes(weeklyClass), 'AG weekly lock with 20h string must have weekly reset class');

// 6. Reset countdown tick simulation (class persistence after 30s countdown updates)
codexShortAcc.windows[0].reset_after_seconds -= 30;
const tickedShortHTML = ctx.renderQuotaLock(codexShortAcc, codexShortAvail);
assert(tickedShortHTML.includes('quota-lock-time ' + primaryClass), 'ticked codex short lock must maintain primary reset class');

codexWeeklyAcc.windows[1].reset_after_seconds -= 30;
const tickedWeeklyHTML = ctx.renderQuotaLock(codexWeeklyAcc, codexWeeklyAvail);
assert(tickedWeeklyHTML.includes('quota-lock-time ' + weeklyClass), 'ticked codex weekly lock must maintain weekly reset class');

agShortWindows[0].reset_after_seconds -= 30;
const tickedAGShort = ctx.renderAGFamilySection('gemini', agShortWindows);
assert(tickedAGShort.includes('ag-family-lock-time ' + primaryClass), 'ticked AG short lock must maintain primary reset class');

agWeeklyWindows[1].reset_after_seconds -= 30;
const tickedAGWeekly = ctx.renderAGFamilySection('claude', agWeeklyWindows);
assert(tickedAGWeekly.includes('ag-family-lock-time ' + weeklyClass), 'ticked AG weekly lock must maintain weekly reset class');

// 7. Expired/zero countdowns never render "0초 후 초기화".
const expiredCodex = {
  provider: 'codex',
  windows: [
    { name: 'primary', used_percent: 100, reset_after_seconds: 0 },
    { name: 'secondary', used_percent: 20, reset_after_seconds: 5 * 86400 + 16 * 3600 }
  ]
};
const expiredCodexHTML = ctx.renderQuotaLock(expiredCodex, ctx.getAvailabilityState(expiredCodex));
assert(expiredCodexHTML.includes('quota-lock-time ' + primaryClass + '">초기화 시각 도달 · 갱신 대기</div>'), 'expired codex primary explicitly waits for refresh');
assert(!expiredCodexHTML.includes('0초 후 초기화'), 'expired codex omits zero-second countdown');
assert(expiredCodexHTML.includes('주간 잔여 <strong>80%</strong>'), 'expired codex retains separate weekly remaining context');
assert((expiredCodexHTML.match(/>5일 16시간 후 초기화</g) || []).length === 1, 'weekly visible reset appears once and is not joined to primary reset');

const expiredAG = [
  { name: 'gemini-five-hour', window_minutes: 300, used_percent: 100, reset_after_seconds: -1 },
  { name: 'gemini-weekly', window_minutes: 10080, used_percent: 18.7, reset_after_seconds: 5 * 86400 + 16 * 3600 }
];
const expiredAGHTML = ctx.renderAGFamilySection('gemini', expiredAG);
assert(expiredAGHTML.includes('ag-family-lock-time ' + primaryClass + '">초기화 시각 도달 · 갱신 대기</div>'), 'expired AG primary explicitly waits for refresh');
assert(!expiredAGHTML.includes('0초 후 초기화'), 'expired AG omits zero-second countdown');
assert(expiredAGHTML.includes('주간 잔여 <strong>81.3%</strong>'), 'AG weekly remaining remains separate');
assert((expiredAGHTML.match(/>5일 16시간 후 초기화</g) || []).length === 1, 'AG weekly visible reset appears exactly once');

console.log('OK_RESET_COUNTDOWN_ACCENT_REGRESSION');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_RESET_COUNTDOWN_ACCENT_REGRESSION") {
		t.Fatalf("Reset countdown accent node contract tests failed: %v\n%s", err, out)
	}
}

func TestDashboardCompactFilterAndNoVisualShadows(t *testing.T) {
	lower := strings.ToLower(dashboard.HTML)
	for _, forbidden := range []string{"box-shadow", "text-shadow", "drop-shadow", "filter:drop-shadow", "filter: drop-shadow", "accountsearch", "debounceapplyfilters", "searchdebouncetimer"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("dashboard.HTML contains forbidden dashboard token %q", forbidden)
		}
	}
	for _, required := range []string{
		"--filter-control-height: 38px;", "--filter-control-radius: 8px;", "--filter-control-border: #c7ced8;",
		"--filter-control-focus: #2f6fb0;", "--filter-label-color: #667085;", "--filter-gap: 10px;",
		`id="providerFilter" class="compact-select"`, `id="endpointFilter" class="compact-select"`, `id="statusFilter" class="compact-select"`,
		`id="resetFiltersBtn"`, "appearance: none;", "outline: 2px solid var(--filter-control-focus);", "@media (max-width: 359px)",
	} {
		if !strings.Contains(dashboard.HTML, required) {
			t.Errorf("dashboard.HTML missing compact filter contract %q", required)
		}
	}
}

func TestCreditExpiryMarkerNoOrphanContract(t *testing.T) {
	for _, required := range []string{
		`.credit-reset-expiry { display: inline-flex; flex-wrap: nowrap; align-items: flex-start; gap: 0;`,
		`.credit-reset-expiry-tree { flex: 0 0 auto; white-space: pre; }`,
		`.credit-reset-expiry-text { flex: 1 1 auto; min-width: 0; overflow-wrap: anywhere; }`,
		`aria-hidden="true">└ </span><span class="credit-reset-expiry-text">`,
	} {
		if !strings.Contains(dashboard.HTML, required) {
			t.Errorf("dashboard.HTML missing expiry marker nowrap contract %q", required)
		}
	}
	if strings.Contains(dashboard.HTML, "╰") {
		t.Error("dashboard.HTML retains the old expiry marker")
	}
}

func TestDashboardSnapshotChangeCueContract(t *testing.T) {
	for _, tok := range []string{
		"function buildQuotaSnapshot", "function diffQuotaSnapshots", "pendingQuotaChanges",
		"Only server snapshots enter this comparison",
		".value-flash", "flash-sync", "flash-good", "flash-danger",
		"@keyframes valueFlash", ".value-flash { animation: none; }",
		"function strongestQuotaTone", "function applyCardPulse", ".account-card.card-pulse",
		"observed_at:observed?observed.toISOString()", "fields.sync='flash-sync'",
		"card._cardPulseTimer", "},1400);", "danger':4",
	} {
		if !strings.Contains(dashboard.HTML, tok) {
			t.Errorf("dashboard.HTML missing snapshot cue contract %q", tok)
		}
	}
}
