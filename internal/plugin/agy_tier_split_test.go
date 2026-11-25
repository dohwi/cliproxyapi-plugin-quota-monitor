package plugin

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestAntigravityModelTierCollectionAndSanitization(t *testing.T) {
	reset(t)
	var failModels atomic.Bool
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[
				{"name":"ag-ultra.json","provider":"antigravity","auth_index":"1","account":"ultra@example.com"},
				{"name":"ag-pro.json","provider":"antigravity","auth_index":"2","account":"pro@example.com"}
			]}`))
		case "/management/auth-files/models":
			if failModels.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			switch r.URL.Query().Get("name") {
			case "ag-ultra.json":
				_, _ = w.Write([]byte(`{"models":[{"id":"gemini-3.8-flash"},{"id":"gemini-4-argon"},{"id":"gemini-claude-opus-5.5"}]}`))
			case "ag-pro.json":
				_, _ = w.Write([]byte(`{"models":[{"id":"gemini-3.8-flash"},{"id":"gemini-claude-opus-4.6"}]}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		case "/management/api-call":
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"groups\":[{\"displayName\":\"Gemini Models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\",\"bucketId\":\"gemini-5h\",\"window\":\"5h\",\"remainingFraction\":0.95}]},{\"displayName\":\"Claude and GPT models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\",\"bucketId\":\"3p-5h\",\"window\":\"5h\",\"remainingFraction\":0.8}]}]}"}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})

	first := collectAccounts(context.Background(), true)
	if first.err != nil || len(first.accounts) != 2 {
		t.Fatalf("first collect: len=%d err=%v", len(first.accounts), first.err)
	}
	byAcc := map[string]quotamonitor.AccountQuota{}
	for _, a := range first.accounts {
		byAcc[a.Account] = a
	}
	if byAcc["ultra@example.com"].ModelTier != quotamonitor.AntigravityTier4 {
		t.Fatalf("ultra model_tier = %q, want 4", byAcc["ultra@example.com"].ModelTier)
	}
	if byAcc["pro@example.com"].ModelTier != quotamonitor.AntigravityTier38 {
		t.Fatalf("pro model_tier = %q, want 3.8", byAcc["pro@example.com"].ModelTier)
	}

	// Transient models endpoint failure must retain last-good model_tier.
	failModels.Store(true)
	state.mu.Lock()
	state.cachedAt = time.Time{}
	state.lastForceAt = time.Time{}
	for k := range state.probeDue {
		state.probeDue[k] = now().Add(-time.Second)
	}
	state.mu.Unlock()

	second := collectAccounts(context.Background(), true)
	if second.err != nil || len(second.accounts) != 2 {
		t.Fatalf("second collect: len=%d err=%v", len(second.accounts), second.err)
	}
	for _, a := range second.accounts {
		byAcc[a.Account] = a
	}
	if byAcc["ultra@example.com"].ModelTier != quotamonitor.AntigravityTier4 || byAcc["pro@example.com"].ModelTier != quotamonitor.AntigravityTier38 {
		t.Fatalf("model_tier lost on transient error: ultra=%q pro=%q", byAcc["ultra@example.com"].ModelTier, byAcc["pro@example.com"].ModelTier)
	}

	// Allowlist sanitization drops hostile or non-Antigravity model_tier values.
	safe := sanitizeAccounts([]quotamonitor.AccountQuota{
		{AccountKey: "k1", Provider: "antigravity", Account: "a@x.com", ModelTier: "4"},
		{AccountKey: "k2", Provider: "antigravity", Account: "b@x.com", ModelTier: "<script>alert(1)</script>"},
		{AccountKey: "k3", Provider: "codex", Account: "c@x.com", ModelTier: "4"},
	})
	if safe[0].ModelTier != "4" || safe[1].ModelTier != "" || safe[2].ModelTier != "" {
		t.Fatalf("sanitized tiers = [%q, %q, %q], want [4, \"\", \"\"]", safe[0].ModelTier, safe[1].ModelTier, safe[2].ModelTier)
	}
}

func TestAntigravityCurrentAccountSplitByModelTier(t *testing.T) {
	reset(t)
	nowTime := now()
	t1 := nowTime.Add(-2 * time.Minute)
	t2 := nowTime.Add(-5 * time.Minute)
	t3 := nowTime.Add(-1 * time.Minute)
	rem := 80.0
	win := []quotamonitor.QuotaWindow{{Name: "Gemini Models / Five Hour Limit Remaining", RemainingPercent: &rem}}

	accounts := []quotamonitor.AccountQuota{
		{AccountKey: "ag4", Provider: "antigravity", ModelTier: "4", Account: "u@x.com", Status: quotamonitor.StatusKnown, Windows: win, LastUsedAt: &t2},
		{AccountKey: "ag38-old", Provider: "antigravity", ModelTier: "3.8", Account: "p1@x.com", Status: quotamonitor.StatusKnown, Windows: win, LastUsedAt: &t1},
		{AccountKey: "ag38-new", Provider: "antigravity", ModelTier: "3.8", Account: "p2@x.com", Status: quotamonitor.StatusKnown, Windows: win, LastUsedAt: &t3},
	}

	safe := sanitizeAccounts(accounts)
	current := map[string]bool{}
	for _, s := range safe {
		current[s.AccountKey] = s.CurrentAccount
	}
	if !current["ag4"] {
		t.Fatal("AGY (4) account should be marked current independently of AGY (3.8)")
	}
	if !current["ag38-new"] || current["ag38-old"] {
		t.Fatalf("AGY (3.8) current selection wrong: new=%v old=%v", current["ag38-new"], current["ag38-old"])
	}
}

func TestAntigravityModelTierDemoteGuard(t *testing.T) {
	reset(t)
	var omitGemini4 atomic.Bool
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[{"name":"ag-ultra.json","provider":"antigravity","auth_index":"1","account":"ultra@example.com"}]}`))
		case "/management/auth-files/models":
			if omitGemini4.Load() {
				_, _ = w.Write([]byte(`{"models":[{"id":"gemini-3.8-flash"},{"id":"gemini-claude-opus-4.6"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"models":[{"id":"gemini-3.8-flash"},{"id":"gemini-4-argon"}]}`))
		case "/management/api-call":
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"groups\":[{\"displayName\":\"Gemini Models\",\"buckets\":[{\"displayName\":\"Five Hour Limit Remaining\",\"bucketId\":\"gemini-5h\",\"window\":\"5h\",\"remainingFraction\":0.95}]}]}"}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})

	forceCollect := func() string {
		state.mu.Lock()
		state.cachedAt = time.Time{}
		state.lastForceAt = time.Time{}
		for k := range state.probeDue {
			state.probeDue[k] = now().Add(-time.Second)
		}
		state.mu.Unlock()
		res := collectAccounts(context.Background(), true)
		if res.err != nil || len(res.accounts) != 1 {
			t.Fatalf("collect failed: len=%d err=%v", len(res.accounts), res.err)
		}
		return res.accounts[0].ModelTier
	}

	if got := forceCollect(); got != quotamonitor.AntigravityTier4 {
		t.Fatalf("initial tier = %q, want 4", got)
	}

	// Two transient "3.8" catalogs must not demote a confirmed "4" account.
	omitGemini4.Store(true)
	for i := 1; i < antigravityTier4DemoteThreshold; i++ {
		if got := forceCollect(); got != quotamonitor.AntigravityTier4 {
			t.Fatalf("transient miss #%d demoted early: got %q, want 4", i, got)
		}
	}

	// A recovered "4" catalog resets the demotion counter.
	omitGemini4.Store(false)
	if got := forceCollect(); got != quotamonitor.AntigravityTier4 {
		t.Fatalf("recovered tier = %q, want 4", got)
	}

	// Three consecutive "3.8" catalogs finally demote the account.
	omitGemini4.Store(true)
	for i := 1; i < antigravityTier4DemoteThreshold; i++ {
		if got := forceCollect(); got != quotamonitor.AntigravityTier4 {
			t.Fatalf("post-reset miss #%d demoted early: got %q, want 4", i, got)
		}
	}
	if got := forceCollect(); got != quotamonitor.AntigravityTier38 {
		t.Fatalf("miss #%d should demote to 3.8, got %q", antigravityTier4DemoteThreshold, got)
	}
}

func TestDashboardAntigravityTierSplitNodeContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := `
const vm = require('vm');
const assert = require('assert');
const html = process.argv[1];
const m = html.match(/<script>([\s\S]*?)<\/script>/);
if (!m) throw new Error('script tag not found');

function makeEl(tag) {
  const el = {
    tagName: (tag || 'div').toUpperCase(),
    children: [],
    attrs: {},
    style: {},
    className: '',
    value: 'all',
    innerHTML: '',
    textContent: '',
    title: '',
    options: [],
    selectedIndex: 0,
    appendChild(c) {
      if (c && c.tagName === 'FRAG') {
        for (const child of c.children) this.children.push(child);
        return c;
      }
      this.children.push(c);
      if (this.tagName === 'SELECT') this.options.push(c);
      return c;
    },
    removeChild(c) { const i = this.children.indexOf(c); if (i >= 0) this.children.splice(i, 1); if (this.tagName === 'SELECT') this.options = this.children.slice(); },
    get firstChild() { return this.children[0] || null; },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k]; },
    addEventListener() {},
    querySelectorAll() { return []; },
    querySelector() { return null; },
    classList: { add() {}, remove() {}, contains() { return false; } },
  };
  return el;
}

const byId = {
  providerFilter: makeEl('select'),
  endpointFilter: makeEl('select'),
  statusFilter: makeEl('select'),
  fleetAvailability: makeEl('ul'),
  accountsList: makeEl('div'),
  accountsEmpty: makeEl('div'),
};

const ctx = {
  console, Date, Math, Number, String, Array, Object, Boolean, RegExp, JSON, Error,
  isFinite, isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
  setTimeout: () => 0, clearTimeout: () => {}, setInterval: () => 0, clearInterval: () => {},
  navigator: { language: 'ko-KR' },
  location: { href: 'http://127.0.0.1/', pathname: '/', search: '' },
  localStorage: { getItem: () => null, setItem: () => {} },
  sessionStorage: { getItem: () => null, setItem: () => {} },
  document: {
    documentElement: { lang: 'ko', setAttribute() {}, getAttribute() { return ''; } },
    getElementById: id => byId[id] || null,
    createElement: tag => makeEl(tag),
    createDocumentFragment: () => makeEl('frag'),
    querySelectorAll: () => [],
    querySelector: () => null,
    addEventListener() {},
  },
  window: { addEventListener() {}, matchMedia: () => ({ matches: false, addEventListener() {} }) },
};
ctx.window.document = ctx.document;
vm.createContext(ctx);
vm.runInContext(m[1], ctx);

// 1. Fleet availability collapses AGY (4) and AGY (3.8) into separate chips.
ctx.renderFleetAvailability([
  { key: 'claude', label: 'Claude', remaining_percent: 60, accounts: 3 },
  { key: 'codex', label: 'GPT', remaining_percent: 80, accounts: 1 },
  { key: 'antigravity_4_gemini', label: 'AGY (4) · Gemini', remaining_percent: 99.3, accounts: 1 },
  { key: 'antigravity_4_claude', label: 'AGY (4) · Claude/GPT', remaining_percent: 100, accounts: 1 },
  { key: 'antigravity_3_8_gemini', label: 'AGY (3.8) · Gemini', remaining_percent: 98.9, accounts: 3 },
  { key: 'antigravity_3_8_claude', label: 'AGY (3.8) · Claude/GPT', remaining_percent: 0, accounts: 3 },
]);
const fleetHTML = byId.fleetAvailability.innerHTML;
assert(fleetHTML.includes('>AGY (4)<'), 'missing AGY (4) fleet chip: ' + fleetHTML);
assert(fleetHTML.includes('>AGY (3.8)<'), 'missing AGY (3.8) fleet chip: ' + fleetHTML);
assert(fleetHTML.includes('99.3%') && fleetHTML.includes('100%'), 'missing AGY (4) percentages');
assert(fleetHTML.includes('98.9%') && fleetHTML.includes('0%'), 'missing AGY (3.8) percentages');

// 2. Provider filter lists AGY and AGY (Free) as distinct top-level units.
const accs = [
  { account_key: 'k-cl', provider: 'claude', account: 'c@x', status: 'known', endpoint: 'direct', windows: [{ name: 'five-hour', remaining_percent: 50 }] },
  { account_key: 'k-38a', provider: 'antigravity', model_tier: '3.8', account: 'p1@x', status: 'known', endpoint: 'direct', windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 98 }] },
  { account_key: 'k-38b', provider: 'antigravity', model_tier: '3.8', account: 'p2@x', status: 'known', endpoint: 'direct', windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 99 }] },
  { account_key: 'k-4', provider: 'antigravity', model_tier: '4', account: 'u@x', status: 'known', endpoint: 'direct', windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 100 }] },
  { account_key: 'k-cx', provider: 'codex', account: 'g@x', status: 'known', endpoint: 'direct', windows: [{ name: 'primary', remaining_percent: 80 }] },
];
ctx.populateProviderSegmented(accs);
const optVals = byId.providerFilter.options.map(o => o.value);
assert.strictEqual(JSON.stringify(optVals), JSON.stringify(['all', 'AGY', 'AGY (Free)', 'codex', 'claude']), 'unexpected filter options: ' + JSON.stringify(optVals));
assert.strictEqual(byId.providerFilter.options[1].textContent, 'AGY (1)');
assert.strictEqual(byId.providerFilter.options[2].textContent, 'AGY (Free) (2)');

// 3. Sorting puts AGY (4) before AGY (3.8) before codex before claude.
const sorted = ctx.sortAccountsByEndpoint(accs).map(a => a.account_key);
assert.strictEqual(JSON.stringify(sorted), JSON.stringify(['k-4', 'k-38a', 'k-38b', 'k-cx', 'k-cl']), 'unexpected sort order: ' + JSON.stringify(sorted));

// 4. Filtering by AGY and AGY (Free) isolates each group.
let renderedKeys = [];
const origRender = ctx.renderAccounts;
ctx.cachedAccounts = accs;
ctx.renderAccounts = list => { renderedKeys = Array.from(list, a => a.account_key); };
ctx.selectedProvider = 'AGY';
ctx.applyFilters();
assert.strictEqual(JSON.stringify(renderedKeys), JSON.stringify(['k-4']), 'AGY filter failed: ' + JSON.stringify(renderedKeys));
ctx.selectedProvider = 'AGY (Free)';
ctx.applyFilters();
assert.strictEqual(JSON.stringify(renderedKeys), JSON.stringify(['k-38a', 'k-38b']), 'AGY (Free) filter failed: ' + JSON.stringify(renderedKeys));

// 5. Card rendering uses AGY (4) / AGY (3.8) badges and ignores hostile model_tier.
ctx.renderAccounts = origRender;
const hostile = { account_key: 'k-x', provider: 'antigravity', model_tier: '<img onerror=alert(1)>', account: 'h@x', status: 'known', endpoint: 'direct', windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 50 }] };
assert.strictEqual(ctx.accountProviderUnit(hostile), 'antigravity', 'hostile model_tier must be ignored');
ctx.renderAccounts([accs[3], accs[1], hostile]);
assert.strictEqual(byId.accountsList.children.length, 3);
const card4 = byId.accountsList.children[0];
const card38 = byId.accountsList.children[1];
const cardHostile = byId.accountsList.children[2];
assert.strictEqual(card4.getAttribute('data-model-tier'), '4');
assert.strictEqual(card38.getAttribute('data-model-tier'), '3.8');
assert.strictEqual(cardHostile.getAttribute('data-model-tier'), undefined);

console.log('OK_AGY_TIER_SPLIT');
`
	cmd := nodeDashboardCmd(t, "node", script)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_AGY_TIER_SPLIT") {
		t.Fatalf("dashboard AGY tier split contract failed: %v\n%s", err, out)
	}
}
