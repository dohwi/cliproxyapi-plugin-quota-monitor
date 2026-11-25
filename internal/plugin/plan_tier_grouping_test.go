package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	quotamonitor "quota-monitor/internal/quota"
)

func TestProbeAndSanitizePlansForAllProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{{"id": "gemini-4-hydrogen"}},
			})
		case "/management/api-call":
			var req struct {
				AuthIndex string `json:"auth_index"`
				URL       string `json:"url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			switch {
			case strings.Contains(req.URL, "loadCodeAssist"):
				if req.AuthIndex == "ag-free" {
					_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{StatusCode: 200, Body: `{"currentTier":{"id":"free-tier"}}`})
				} else {
					_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{StatusCode: 200, Body: `{"paidTier":{"id":"g1-ultra-tier"}}`})
				}
			case strings.Contains(req.URL, "retrieveUserQuotaSummary"):
				_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{
					StatusCode: 200,
					Body: `{"groups":[` +
						`{"displayName":"Gemini Models","buckets":[{"displayName":"Five Hour Limit Remaining","remainingFraction":0.9}]},` +
						`{"displayName":"Claude and GPT models","buckets":[{"displayName":"Five Hour Limit Remaining","remainingFraction":0.8}]}` +
						`]}`,
				})
			case strings.Contains(req.URL, "/api/oauth/usage"):
				_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{
					StatusCode: 200,
					Body:       `{"five_hour":{"utilization":20,"resets_at":"2026-11-25T01:00:00Z"}}`,
				})
			case strings.Contains(req.URL, "/api/oauth/profile"):
				_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{
					StatusCode: 200,
					Body:       `{"organization":{"organization_type":"claude_max","rate_limit_tier":"default_claude_max_20x"}}`,
				})
			case strings.Contains(req.URL, "wham/usage"):
				_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{
					StatusCode: 200,
					Body:       `{"plan_type":"team","rate_limit":{"primary_window":{"used_percent":10,"reset_at":"2026-11-25T01:00:00Z"}}}`,
				})
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := quotamonitor.NewClient(server.URL, "mgmt-key")
	input := []quotamonitor.AccountQuota{
		{Provider: "antigravity", AccountKey: "ag-u", AuthIndex: "ag-ultra", Account: "u@x"},
		{Provider: "antigravity", AccountKey: "ag-f", AuthIndex: "ag-free", Account: "f@x"},
		{Provider: "claude", AccountKey: "cl-1", AuthIndex: "c1", Account: "c@x"},
		{Provider: "codex", AccountKey: "cx-1", AuthIndex: "x1", Account: "x@x"},
	}

	got, _, truncated := probeAccounts(context.Background(), client, input, nil, nil, true)
	if truncated || len(got) != 4 {
		t.Fatalf("probeAccounts got=%d truncated=%v", len(got), truncated)
	}
	safe := sanitizeAccounts(got)
	wantPlans := map[string]string{
		"ag-u": "ultra",
		"ag-f": "free",
		"cl-1": "ultra",
		"cx-1": "team",
	}
	for _, a := range safe {
		if a.PlanType != wantPlans[a.AccountKey] {
			t.Errorf("account %s plan_type=%q, want %q", a.AccountKey, a.PlanType, wantPlans[a.AccountKey])
		}
	}

	// Hostile / non-allowlisted plan values must be dropped by sanitizeAccounts.
	hostile := sanitizeAccounts([]quotamonitor.AccountQuota{
		{Provider: "claude", AccountKey: "h1", PlanType: "<script>alert(1)</script>"},
		{Provider: "antigravity", AccountKey: "h2", PlanType: "evil_plan"},
	})
	for _, a := range hostile {
		if a.PlanType != "" {
			t.Errorf("hostile plan leaked for %s: %q", a.AccountKey, a.PlanType)
		}
	}
}

func TestDashboardTierAndEndpointGroupingNodeContract(t *testing.T) {
	script := `
const assert = require('assert');
const vm = require('vm');
const html = process.argv[1];
const m = html.match(/<script>([\s\S]*?)<\/script>/);
assert(m, 'dashboard inline script not found');

function makeEl(tag) {
  return {
    tagName: (tag || 'div').toUpperCase(),
    className: '', id: '', textContent: '', value: '', title: '',
    attrs: {}, style: {}, children: [], options: [], hidden: false,
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k] || null; },
    appendChild(c) {
      if (c && c.tagName === 'FRAG') { this.children.push(...c.children); return c; }
      this.children.push(c);
      if (this.tagName === 'SELECT') this.options.push(c);
      return c;
    },
    addEventListener() {},
    closest() { return null; },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    set innerHTML(v) { this._html = String(v); if (!v) { this.children = []; this.options = []; } },
    get innerHTML() { return this._html || ''; },
  };
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

// 1. All 3 providers sort by provider -> tier/plan -> endpoint -> status -> account_key.
const accounts = [
  { account_key: 'ag-free-2', provider: 'antigravity', plan_type: 'free', model_tier: '4', endpoint: 'warp2', status: 'known', account: 'af2@x', windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 90 }] },
  { account_key: 'ag-free-1', provider: 'antigravity', plan_type: 'free', model_tier: '4', endpoint: 'direct', status: 'known', account: 'af1@x', windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 90 }] },
  { account_key: 'ag-ultra-2', provider: 'antigravity', plan_type: 'ultra', model_tier: '4', endpoint: 'warp2', status: 'known', account: 'au2@x', windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 90 }] },
  { account_key: 'ag-ultra-1', provider: 'antigravity', plan_type: 'ultra', model_tier: '4', endpoint: 'direct', status: 'known', account: 'au1@x', windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 90 }] },
  { account_key: 'ag-pro-1', provider: 'antigravity', plan_type: 'pro', model_tier: '4', endpoint: 'warp1', status: 'known', account: 'ap1@x', windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', remaining_percent: 90 }] },

  { account_key: 'cx-plus-w1', provider: 'codex', plan_type: 'plus', endpoint: 'warp1', status: 'known', account: 'xp1@x', windows: [{ name: 'primary', remaining_percent: 80 }] },
  { account_key: 'cx-pro100-dir', provider: 'codex', plan_type: 'pro_100', endpoint: 'direct', status: 'known', account: 'xpl@x', windows: [{ name: 'primary', remaining_percent: 80 }] },
  { account_key: 'cx-team-w2', provider: 'codex', plan_type: 'team', endpoint: 'warp2', status: 'known', account: 'xt2@x', windows: [{ name: 'primary', remaining_percent: 80 }] },
  { account_key: 'cx-team-dir', provider: 'codex', plan_type: 'team', endpoint: 'direct', status: 'known', account: 'xt1@x', windows: [{ name: 'primary', remaining_percent: 80 }] },
  { account_key: 'cx-pro-w2', provider: 'codex', plan_type: 'pro', endpoint: 'warp2', status: 'known', account: 'xpro@x', windows: [{ name: 'primary', remaining_percent: 80 }] },
  { account_key: 'cx-free-dir', provider: 'codex', plan_type: 'free', endpoint: 'direct', status: 'known', account: 'xf@x', windows: [{ name: 'primary', remaining_percent: 80 }] },

  { account_key: 'cl-pro-dir', provider: 'claude', plan_type: 'pro', endpoint: 'direct', status: 'known', account: 'cp@x', windows: [{ name: 'five-hour', remaining_percent: 70 }] },
  { account_key: 'cl-ultra-w2', provider: 'claude', plan_type: 'ultra', endpoint: 'warp2', status: 'known', account: 'cm20b@x', windows: [{ name: 'five-hour', remaining_percent: 70 }] },
  { account_key: 'cl-ultra-dir', provider: 'claude', plan_type: 'ultra', endpoint: 'direct', status: 'known', account: 'cm20a@x', windows: [{ name: 'five-hour', remaining_percent: 70 }] },
  { account_key: 'cl-max-w1', provider: 'claude', plan_type: 'max', endpoint: 'warp1', status: 'known', account: 'cm5@x', windows: [{ name: 'five-hour', remaining_percent: 70 }] },
];

const sorted = ctx.sortAccountsByEndpoint(accounts).map(a => a.account_key);
assert.strictEqual(JSON.stringify(sorted), JSON.stringify([
  'ag-ultra-1', 'ag-ultra-2', 'ag-pro-1', 'ag-free-1', 'ag-free-2',
  'cx-pro-w2', 'cx-pro100-dir', 'cx-team-dir', 'cx-team-w2', 'cx-plus-w1', 'cx-free-dir',
  'cl-ultra-dir', 'cl-ultra-w2', 'cl-max-w1', 'cl-pro-dir',
]), 'unexpected tier+EP sort order: ' + JSON.stringify(sorted));

// 2. Card rendering shows plan badge for all 3 providers and sets data-plan-type.
ctx.cachedAccounts = accounts;
ctx.renderAccounts(ctx.sortAccountsByEndpoint(accounts));
assert.strictEqual(byId.accountsList.children.length, accounts.length);

function findProvHTML(card) {
  return card.children[0].children[0].children[0].children[0].children[0].innerHTML;
}
function findProvClass(card) {
  return card.children[0].children[0].children[0].children[0].children[0].className;
}

const agUltraCard = byId.accountsList.children[0];
assert.strictEqual(agUltraCard.getAttribute('data-plan-type'), 'ultra');
assert(findProvHTML(agUltraCard).includes('Antigravity') && findProvHTML(agUltraCard).includes('AI Ultra'), 'AG ultra badge: ' + findProvHTML(agUltraCard));
assert(!findProvClass(agUltraCard).includes('badge-provider-antigravity-38'), 'AG ultra must not use secondary color');

const agFreeCard = byId.accountsList.children[3];
assert.strictEqual(agFreeCard.getAttribute('data-plan-type'), 'free');
assert(findProvHTML(agFreeCard).includes('Antigravity') && findProvHTML(agFreeCard).includes('Free'), 'AG free badge: ' + findProvHTML(agFreeCard));
assert(findProvClass(agFreeCard).includes('badge-provider-antigravity-38'), 'AG free should use secondary color');

const codexPro100Card = byId.accountsList.children[6];
assert.strictEqual(codexPro100Card.getAttribute('data-plan-type'), 'pro_100');
assert(findProvHTML(codexPro100Card).includes('Codex') && findProvHTML(codexPro100Card).includes('Pro 100'), 'Codex Pro 100 badge: ' + findProvHTML(codexPro100Card));

const claudeUltraCard = byId.accountsList.children[11];
assert.strictEqual(claudeUltraCard.getAttribute('data-plan-type'), 'ultra');
assert(findProvHTML(claudeUltraCard).includes('Claude') && findProvHTML(claudeUltraCard).includes('Ultra'), 'Claude Ultra badge: ' + findProvHTML(claudeUltraCard));

// 3. Fleet availability promotes official single-tier labels (AGY · AI Ultra, AGY · AI Pro Trial) and never shows arbitrary Lite.
ctx.renderFleetAvailability([
  { key: 'claude', label: 'Claude', remaining_percent: 9.1, accounts: 2, tiers: [{ tier: 'ultra', label: 'Ultra', remaining_percent: 0, accounts: 1 }, { tier: 'pro', label: 'Pro', remaining_percent: 100, accounts: 1 }] },
  { key: 'antigravity_gemini', label: 'AGY · Gemini', remaining_percent: 99, accounts: 1, tiers: [{ tier: 'ultra', label: 'AI Ultra', remaining_percent: 99, accounts: 1 }] },
  { key: 'antigravity_claude', label: 'AGY · Claude/GPT', remaining_percent: 98, accounts: 1, tiers: [{ tier: 'ultra', label: 'AI Ultra', remaining_percent: 98, accounts: 1 }] },
  { key: 'antigravity_lite_gemini', label: 'AGY (AI Pro Trial) · Gemini', remaining_percent: 90, accounts: 3, tiers: [{ tier: 'pro', label: 'AI Pro Trial', remaining_percent: 90, accounts: 3 }] },
  { key: 'antigravity_lite_claude', label: 'AGY (AI Pro Trial) · Claude/GPT', remaining_percent: 88, accounts: 3, tiers: [{ tier: 'pro', label: 'AI Pro Trial', remaining_percent: 88, accounts: 3 }] },
]);
const fleetHTML = byId.fleetAvailability.innerHTML;
assert(fleetHTML.includes('>AGY · AI Ultra<') && fleetHTML.includes('>AGY · AI Pro Trial<'), 'expected official AGY tier chips: ' + fleetHTML);
assert(!fleetHTML.includes('Lite'), 'must not display arbitrary Lite label: ' + fleetHTML);
assert(fleetHTML.includes('Ultra 0%') && fleetHTML.includes('Pro 100%'), 'expected tier breakdown in Claude tooltip: ' + fleetHTML);
assert(fleetHTML.includes('AI Ultra 99%') && fleetHTML.includes('AI Pro Trial 90%'), 'expected tier breakdown in AGY tooltips: ' + fleetHTML);

console.log('OK_TIER_EP_GROUPING');
`
	cmd := nodeDashboardCmd(t, "node", script)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_TIER_EP_GROUPING") {
		t.Fatalf("node tier/EP grouping contract failed: %v\n%s", err, out)
	}
}
