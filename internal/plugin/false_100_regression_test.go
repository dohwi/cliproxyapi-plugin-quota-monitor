package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quota-monitor/internal/availability"
	quotamonitor "quota-monitor/internal/quota"
)

func TestPassiveRecoveryClearsStaleExhaustionFlags(t *testing.T) {
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(30 * time.Minute)
	used100, rem0 := 100.0, 0.0
	yes, no := true, false
	over := 12.0
	previous := []quotamonitor.AccountQuota{{
		AccountKey:    "acc-1",
		Provider:      "codex",
		Status:        quotamonitor.StatusKnown,
		Source:        "active",
		LastSuccessAt: &t1,
		ObservedAt:    &t1,
		Windows: []quotamonitor.QuotaWindow{{
			Name:             "primary",
			UsedPercent:      &used100,
			RemainingPercent: &rem0,
			LimitReached:     &yes,
			Allowed:          &no,
			OverLimitPercent: &over,
		}},
	}}

	// Newer passive observation after reset reports 15% used without explicit flags.
	used15, rem85 := 15.0, 85.0
	fresh := []quotamonitor.AccountQuota{{
		AccountKey: "acc-1",
		Provider:   "codex",
		Status:     quotamonitor.StatusKnown,
		Source:     "passive",
		ObservedAt: &t2,
		Windows: []quotamonitor.QuotaWindow{{
			Name:             "primary",
			UsedPercent:      &used15,
			RemainingPercent: &rem85,
		}},
	}}
	merged := applyLastGoodWindows(fresh, previous)
	if len(merged) != 1 || len(merged[0].Windows) != 1 {
		t.Fatalf("unexpected merged accounts: %#v", merged)
	}
	w := merged[0].Windows[0]
	if w.UsedPercent == nil || *w.UsedPercent != 15 || w.RemainingPercent == nil || *w.RemainingPercent != 85 {
		t.Fatalf("percentages wrong after recovery: %#v", w)
	}
	if w.LimitReached == nil || *w.LimitReached {
		t.Fatalf("stale LimitReached=true survived passive recovery: %#v", w)
	}
	if w.Allowed == nil || !*w.Allowed {
		t.Fatalf("stale Allowed=false survived passive recovery: %#v", w)
	}
	if w.OverLimitPercent == nil || *w.OverLimitPercent != 0 {
		t.Fatalf("stale OverLimitPercent survived passive recovery: %#v", w)
	}

	// Reset-only update without percentages must NOT clear LimitReached=true.
	resetAt := t2.Add(time.Hour)
	resetOnly := []quotamonitor.AccountQuota{{
		AccountKey: "acc-1",
		Provider:   "codex",
		Status:     quotamonitor.StatusKnown,
		Source:     "passive",
		ObservedAt: &t2,
		Windows: []quotamonitor.QuotaWindow{{
			Name:    "primary",
			ResetAt: &resetAt,
		}},
	}}
	mergedReset := applyLastGoodWindows(resetOnly, previous)
	rw := mergedReset[0].Windows[0]
	if rw.LimitReached == nil || !*rw.LimitReached || rw.Allowed == nil || *rw.Allowed {
		t.Fatalf("reset-only update must retain exhaustion flags: %#v", rw)
	}
}

func TestValuelessActiveProbeWindowDoesNotPromoteToKnown(t *testing.T) {
	// Codex upstream returns a primary_window with only limit_window_seconds (no percentage/flags).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"rate_limit\":{\"primary_window\":{\"limit_window_seconds\":18000}}}"}`))
	}))
	defer srv.Close()

	client := quotamonitor.NewClient(srv.URL, "k")
	tPrev := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rem75, used25 := 75.0, 25.0
	previous := []quotamonitor.AccountQuota{{
		AccountKey:    "has-prev",
		AuthIndex:     "1",
		Provider:      "codex",
		Status:        quotamonitor.StatusKnown,
		Source:        "active",
		LastSuccessAt: &tPrev,
		ObservedAt:    &tPrev,
		Windows:       []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &rem75, UsedPercent: &used25}},
	}}
	fresh := []quotamonitor.AccountQuota{
		{AccountKey: "has-prev", AuthIndex: "1", Provider: "codex"},
		{AccountKey: "no-prev", AuthIndex: "2", Provider: "codex"},
	}
	out, _, _ := probeAccounts(context.Background(), client, fresh, previous, nil, true)
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Status != quotamonitor.StatusStale || !out[0].Stale || out[0].Error == "" {
		t.Fatalf("account with last-good should be stale on valueless probe, got %#v", out[0])
	}
	if len(out[0].Windows) != 1 || out[0].Windows[0].RemainingPercent == nil || *out[0].Windows[0].RemainingPercent != 75 {
		t.Fatalf("last-good 75%% window lost on valueless probe: %#v", out[0].Windows)
	}
	if out[1].Status != quotamonitor.StatusError || out[1].Error == "" {
		t.Fatalf("account without last-good should be error on valueless probe, got %#v", out[1])
	}
}

func TestAntigravityHealthyGeminiClearsUnavailableDespiteClaudeExhaustion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"groups\":[{\"displayName\":\"Gemini Models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\",\"remainingFraction\":0.8}]},{\"displayName\":\"Claude and GPT models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\",\"remainingFraction\":0.0}]}]}"}`))
	}))
	defer srv.Close()

	yes := true
	accounts := []quotamonitor.AccountQuota{{
		AccountKey:  "ag-1",
		AuthIndex:   "1",
		Provider:    "antigravity",
		Operational: &quotamonitor.OperationalStatus{Status: "unavailable", Unavailable: &yes},
	}}
	out, _, _ := probeAccounts(context.Background(), quotamonitor.NewClient(srv.URL, "k"), accounts, nil, nil, true)
	if len(out) != 1 || out[0].Operational == nil || out[0].Operational.Unavailable == nil || *out[0].Operational.Unavailable {
		t.Fatalf("expected Operational.Unavailable cleared for healthy Gemini, got %#v", out)
	}
	entries := availability.Compute(out)
	var gem *availability.Entry
	for i := range entries {
		if entries[i].Key == "antigravity_gemini" {
			gem = &entries[i]
		}
	}
	if gem == nil || gem.RemainingPercent != 80 {
		t.Fatalf("expected antigravity_gemini at 80%%, got %#v", entries)
	}
}

func TestDashboardFalse100AndErrorCoercionNodeContract(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];

function makeEl(tag, id) {
  return {
    tagName: (tag || 'div').toUpperCase(),
    id: id || '',
    className: '',
    textContent: '',
    title: '',
    value: 'all',
    style: { setProperty() {} },
    attrs: {},
    children: [],
    dataset: {},
    classList: { add() {}, remove() {}, toggle() {} },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k] ?? null; },
    removeAttribute(k) { delete this.attrs[k]; },
    appendChild(c) { this.children.push(c); return c; },
    removeChild(c) { this.children = this.children.filter(x => x !== c); },
    replaceChildren(...cs) { this.children = cs.flatMap(c => c && c.tagName === 'FRAGMENT' ? c.children : [c]); },
    get firstChild() { return this.children[0] || null; },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    addEventListener() {},
    getBoundingClientRect() { return { width: 1200 }; },
    _html: '',
    set innerHTML(v) { this._html = String(v); },
    get innerHTML() { return this._html; }
  };
}
const byId = {
  accountsList: makeEl('div', 'accountsList'),
  fleetAvailability: makeEl('ul', 'fleetAvailability'),
  statusFilter: makeEl('select', 'statusFilter'),
  providerFilter: makeEl('select', 'providerFilter'),
  endpointFilter: makeEl('select', 'endpointFilter'),
  dashboardContent: makeEl('div', 'dashboardContent'),
  staleNoticeBox: makeEl('div', 'staleNoticeBox'),
  staleNoticeText: makeEl('span', 'staleNoticeText'),
  errorBox: makeEl('div', 'errorBox'),
  unconfiguredBox: makeEl('div', 'unconfiguredBox'),
  accountStatusLive: makeEl('div', 'accountStatusLive'),
};
byId.statusFilter.value = 'all';
byId.providerFilter.value = 'all';
byId.endpointFilter.value = 'all';

let fetchImpl = null;
const ctx = {
  console, Math, Number, Array, Object, String, Boolean, isNaN, isFinite, Date, Promise,
  URL, TextEncoder, TextDecoder, Uint8Array,
  encodeURIComponent,
  location: { origin: 'http://127.0.0.1:8317', host: '127.0.0.1:8317' },
  navigator: { language: 'ko-KR', languages: ['ko-KR'] },
  sessionStorage: { getItem() { return 'valid-key'; }, setItem() {}, removeItem() {} },
  localStorage: { getItem() { return null; }, setItem() {} },
  setTimeout() { return 1; }, clearTimeout() {}, setInterval() { return 1; }, clearInterval() {},
  AbortController: function() { this.signal = {}; this.abort = () => {}; },
  fetch: (...args) => fetchImpl(...args),
  document: {
    hidden: false,
    activeElement: null,
    documentElement: makeEl('html'),
    body: makeEl('body'),
    addEventListener() {},
    getElementById(id) { return byId[id] || null; },
    createElement(tag) { return makeEl(tag); },
    createDocumentFragment() { return makeEl('fragment'); },
    querySelector() { return null; },
    querySelectorAll() { return []; },
  },
  window: { addEventListener() {}, scrollTo() {} },
};
vm.createContext(ctx);
vm.runInContext(script, ctx);
function assert(ok, msg) { if (!ok) throw new Error('FAIL: ' + msg); }

// 1. Empty string, whitespace, boolean, Infinity must never coerce to 0% or 100%.
assert(ctx.effectiveUsed({ remaining_percent: '' }) === null, 'empty remaining_percent must be null, not 100');
assert(ctx.effectiveUsed({ remaining_percent: '   ' }) === null, 'whitespace remaining_percent must be null');
assert(ctx.effectiveUsed({ remaining_percent: false }) === null, 'boolean false remaining_percent must be null');
assert(ctx.effectiveUsed({ used_percent: '' }) === null, 'empty used_percent must be null, not 0');
assert(ctx.effectiveUsed({ used_percent: false }) === null, 'boolean false used_percent must be null');
assert(ctx.effectiveUsed({ used_percent: Infinity }) === null, 'Infinity used_percent must be null');
assert(ctx.effectiveUsed({ remaining_percent: 0 }) === 100, 'explicit 0 remaining_percent => 100 used');
assert(ctx.effectiveUsed({ used_percent: 0 }) === 0, 'explicit 0 used_percent => 0 used');

assert(ctx.quotaNumber({ remaining_percent: '' }) === null, 'quotaNumber empty string must be null');
assert(ctx.quotaNumber({ remaining_percent: false }) === null, 'quotaNumber boolean false must be null');
assert(ctx.quotaNumber({ remaining_percent: 0 }) === 0, 'quotaNumber explicit 0 => 0');
assert(ctx.quotaNumber({ remaining_percent: 40, allowed: false }) === 0, 'quotaNumber respects allowed=false');
assert(ctx.quotaNumber({ remaining_percent: 40, limit_reached: true }) === 0, 'quotaNumber respects limit_reached=true');

assert(ctx.fmtPct('') === '—', 'fmtPct empty string => dash');
assert(ctx.fmtPct(false) === '—', 'fmtPct false => dash');
assert(ctx.fmtPct(Infinity) === '—', 'fmtPct Infinity => dash');
assert(ctx.fmtPct(0) === '0%', 'fmtPct 0 => 0%');

assert(ctx.validFleetPercent('') === null, 'validFleetPercent empty string => null');
assert(ctx.validFleetPercent(false) === null, 'validFleetPercent false => null');
assert(ctx.validFleetPercent(true) === null, 'validFleetPercent true => null');
assert(ctx.validFleetPercent(0) === 0, 'validFleetPercent 0 => 0');

// 2. Claude disabled overage billing window must not make healthy account limited.
const claudeWithOverage = {
  provider: 'claude',
  status: 'known',
  windows: [
    { name: 'five-hour', window_minutes: 300, remaining_percent: 82, used_percent: 18 },
    { name: 'overage', used_percent: 100, remaining_percent: 0, allowed: false, limit_reached: true }
  ]
};
assert(ctx.getCanonicalStatus(claudeWithOverage).key === 'active', 'Claude overage must not mark healthy account limited');

// 3. Error accounts get error availability (never pending for Antigravity, never blank dashes for valueless windows).
assert(ctx.getAvailabilityState({ provider: 'antigravity', status: 'error', windows: [] }).key === 'error', 'AG error account => error availability, not pending');
assert(ctx.getAvailabilityState({ provider: 'codex', status: 'error', windows: [] }).key === 'error', 'Codex error account => error availability');

ctx.renderAccounts([{
  account_key: 'err-placeholder',
  provider: 'codex',
  account: 'err@example.invalid',
  status: 'error',
  windows: [{ name: 'primary', window_minutes: 300 }]
}]);
const card = byId.accountsList.children[0];
assert(card && card.getAttribute('data-availability') === 'error', 'rendered error card data-availability=error');
const quotaBody = card.children.find(c => c.className === 'card-quota');
assert(quotaBody && quotaBody.children[0] && quotaBody.children[0].className.includes('is-error'), 'valueless error window renders is-error fetchFailed panel');

// 4. Incomplete startup snapshot must NOT render fleetAvailability prematurely.
ctx.mgmtKey = 'valid-key';
ctx.keyVerified = true;
ctx.cachedAccounts = [];
byId.fleetAvailability.innerHTML = '';
fetchImpl = async () => ({
  ok: true, status: 200,
  json: async () => ({
    configured: true,
    snapshot_complete: false,
    availability: [{ key: 'codex', label: 'GPT', remaining_percent: 0, accounts: 1 }],
    accounts: []
  })
});
(async () => {
  ctx.loadDashboard(true);
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));
  assert(byId.fleetAvailability.innerHTML === '', 'incomplete startup snapshot must not flash fleetAvailability: ' + byId.fleetAvailability.innerHTML);
  console.log('OK_FALSE_100_NODE');
})().catch(e => { console.error(e); process.exit(1); });
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_FALSE_100_NODE") {
		t.Fatalf("false-100 node contract failed: %v\n%s", err, out)
	}
}
