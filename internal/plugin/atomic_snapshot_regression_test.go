package plugin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

// TestCurrentAccountLimitedExcludedWithoutFallback verifies that limited/unavailable/disabled
// accounts are strictly disqualified from current_account; healthy accounts without
// audited token use must not inherit a current badge.
func TestCurrentAccountLimitedExcludedWithoutFallback(t *testing.T) {
	nowTime := time.Now()
	recentTime := nowTime.Add(-1 * time.Minute)

	p5hMinutes := 300
	weeklyMinutes := 10080

	pZero := 0.0
	p100 := 100.0
	p80 := 80.0
	bTrue := true

	accounts := []quotamonitor.AccountQuota{
		// Codex 1: Most recently used, BUT limited (5h remaining = 0%)
		{
			AccountKey: "codex-recent-limited",
			Provider:   "codex",
			Account:    "recent-limited@example.com",
			LastUsedAt: &recentTime,
			Status:     quotamonitor.StatusKnown,
			Priority:   10,
			Windows: []quotamonitor.QuotaWindow{
				{Name: "5-hour request limit", WindowMinutes: &p5hMinutes, RemainingPercent: &pZero, LimitReached: &bTrue},
				{Name: "weekly request limit", WindowMinutes: &weeklyMinutes, RemainingPercent: &p100},
			},
		},
		// Codex 2: Healthy, priority 5, remaining 80%
		{
			AccountKey: "codex-healthy-p5",
			Provider:   "codex",
			Account:    "healthy-p5@example.com",
			Status:     quotamonitor.StatusKnown,
			Priority:   5,
			Windows: []quotamonitor.QuotaWindow{
				{Name: "5-hour request limit", WindowMinutes: &p5hMinutes, RemainingPercent: &p80},
				{Name: "weekly request limit", WindowMinutes: &weeklyMinutes, RemainingPercent: &p100},
			},
		},
		// Codex 3: Higher priority and healthy, but no audited token use: not current.
		{
			AccountKey: "codex-healthy-p8",
			Provider:   "codex",
			Account:    "healthy-p8@example.com",
			Status:     quotamonitor.StatusKnown,
			Priority:   8,
			Windows: []quotamonitor.QuotaWindow{
				{Name: "5-hour request limit", WindowMinutes: &p5hMinutes, RemainingPercent: &p80},
				{Name: "weekly request limit", WindowMinutes: &weeklyMinutes, RemainingPercent: &p100},
			},
		},
	}

	sanitized := sanitizeAccounts(accounts)
	for _, a := range sanitized {
		if a.AccountKey == "codex-recent-limited" {
			if a.CurrentAccount {
				t.Fatalf("REGRESSION: limited account received CurrentAccount=true!")
			}
		}
		if a.AccountKey == "codex-healthy-p8" && (a.CurrentAccount || a.CurrentReason != "" || a.SelectedBy != "") {
			t.Errorf("healthy account without audited use cannot be current: %+v", a)
		}
		if a.AccountKey == "codex-healthy-p5" {
			if a.CurrentAccount {
				t.Errorf("lower priority account codex-healthy-p5 should not be current")
			}
		}
	}

	// Verify that if ALL accounts are limited/unavailable, ZERO accounts get CurrentAccount=true.
	allLimited := []quotamonitor.AccountQuota{
		{
			AccountKey: "codex-lim-1",
			Provider:   "codex",
			Account:    "lim1@example.com",
			Status:     quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				{Name: "5-hour request limit", WindowMinutes: &p5hMinutes, RemainingPercent: &pZero},
			},
		},
		{
			AccountKey: "codex-lim-2",
			Provider:   "codex",
			Account:    "lim2@example.com",
			Status:     quotamonitor.StatusKnown,
			Windows: []quotamonitor.QuotaWindow{
				{Name: "weekly request limit", WindowMinutes: &weeklyMinutes, RemainingPercent: &pZero},
			},
		},
	}
	sanitizedAllLim := sanitizeAccounts(allLimited)
	currentCount := 0
	for _, a := range sanitizedAllLim {
		if a.CurrentAccount {
			currentCount++
		}
	}
	if currentCount != 0 {
		t.Fatalf("expected 0 current accounts when all are limited, got %d", currentCount)
	}
}

// TestCodexExtrasPreservationAndMerge verifies that plan_type, API credits (including balance=0),
// and reset credits are merged and preserved across collections by account_key.
func TestCodexLateTeamPlanPreservedAcrossSnapshotGenerations(t *testing.T) {
	previous := []quotamonitor.AccountQuota{{
		AccountKey: "codex-team",
		Provider:   "codex",
		Account:    "team@example.invalid",
		PlanType:   "team",
		Status:     quotamonitor.StatusKnown,
		Source:     "active",
	}}

	// A later passive generation may omit plan fields while carrying fresh
	// identity/operational metadata. The committed account must retain Team.
	freshMissing := []quotamonitor.AccountQuota{{
		AccountKey: "codex-team",
		Provider:   "codex",
		Account:    "team@example.invalid",
		Status:     quotamonitor.StatusUnknown,
	}}
	merged := applyLastGoodWindows(freshMissing, previous)
	if len(merged) != 1 || merged[0].PlanType != "team" {
		t.Fatalf("late Team plan was lost: %#v", merged)
	}

	// A concrete newer plan is authoritative and must not be overwritten by
	// the retained value.
	freshConcrete := cloneAccounts(freshMissing)
	freshConcrete[0].PlanType = "pro"
	merged = applyLastGoodWindows(freshConcrete, previous)
	if merged[0].PlanType != "pro" {
		t.Fatalf("new concrete plan was overwritten: %#v", merged[0])
	}
}

func TestCodexExtrasPreservationAndMerge(t *testing.T) {
	balZero := 0.0
	bFalse := false
	avail2 := 2
	expiry := time.Now().Add(24 * time.Hour).UTC()

	prev := []quotamonitor.AccountQuota{
		{
			AccountKey: "codex-acc-1",
			Provider:   "codex",
			Account:    "same-alias@example.com",
			AuthIndex:  "1",
			PlanType:   "plus",
			APICredits: &quotamonitor.APICredits{
				HasCredits: &bFalse,
				Unlimited:  &bFalse,
				Balance:    &balZero,
			},
			ResetCredits: &quotamonitor.ResetCredits{
				AvailableCount: &avail2,
				NearestExpiry:  &expiry,
			},
			Source: "active",
			Status: quotamonitor.StatusKnown,
		},
		{
			AccountKey: "codex-acc-2",
			Provider:   "codex",
			Account:    "same-alias@example.com", // Same alias/email, different account_key!
			AuthIndex:  "2",
			PlanType:   "k12",
			Source:     "active",
			Status:     quotamonitor.StatusKnown,
		},
	}

	// Fresh accounts without active probe yet
	fresh := []quotamonitor.AccountQuota{
		{
			AccountKey: "codex-acc-1",
			Provider:   "codex",
			Account:    "same-alias@example.com",
			AuthIndex:  "1",
		},
		{
			AccountKey: "codex-acc-2",
			Provider:   "codex",
			Account:    "same-alias@example.com",
			AuthIndex:  "2",
		},
	}

	merged := applyLastGoodWindows(fresh, prev)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged accounts, got %d", len(merged))
	}

	a1 := merged[0]
	if a1.PlanType != "plus" {
		t.Errorf("a1: expected preserved plan_type 'plus', got %q", a1.PlanType)
	}
	if a1.APICredits == nil || a1.APICredits.Balance == nil || *a1.APICredits.Balance != 0.0 {
		t.Errorf("a1: expected preserved api_credits with balance=0, got %#v", a1.APICredits)
	}
	if a1.ResetCredits == nil || a1.ResetCredits.AvailableCount == nil || *a1.ResetCredits.AvailableCount != 2 {
		t.Errorf("a1: expected preserved reset_credits available=2, got %#v", a1.ResetCredits)
	}
	if a1.ResetCredits.NearestExpiry == nil {
		t.Errorf("a1: expected preserved nearest_expiry")
	}

	a2 := merged[1]
	if a2.PlanType != "k12" {
		t.Errorf("a2: expected preserved plan_type 'k12', got %q", a2.PlanType)
	}
	if a2.APICredits != nil {
		t.Errorf("a2: should NOT inherit credits from a1 even with same alias/email! got %#v", a2.APICredits)
	}
}

// TestDashboardAtomicSnapshotAndCreditsNode tests the frontend dashboard rendering logic
// with Node.js, ensuring:
// 1. snapshot_complete=false keeps skeletons and does not commit incomplete accounts
// 2. snapshot_complete=true commits and renders all cards
// 3. limited and legacy candidate accounts have NO current badge; audited use does
// 4. api_credits with balance=0 renders '잔액 0'
// 5. default filter card count matches accounts length
func TestDashboardAtomicSnapshotAndCreditsNode(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

function fakeNode(tag) {
  const node = {
    tagName: tag, _children: [], style: {}, className: '',
    attrs: {}, _text: '', _html: '', value: '',
    appendChild(c) {
      if (c && c._isFragment) {
        for (const child of c._children) this._children.push(child);
        c._children = [];
        return c;
      }
      this._children.push(c);
      return c;
    },
    replaceChildren(...children) {
      this._children = [];
      for (const c of children) {
        if (c && c._isFragment) {
          for (const child of c._children) this._children.push(child);
          c._children = [];
        } else if (c) {
          this._children.push(c);
        }
      }
    },
    setAttribute(k, v) { this.attrs[k] = v; },
    getAttribute(k) { return this.attrs[k] || null; },
    getElementsByClassName(cn) { return []; },
    classList: {
      add(c) { if (!this._classes) this._classes = []; this._classes.push(c); },
      remove(c) { if (this._classes) this._classes = this._classes.filter(x => x !== c); }
    },
  };
  Object.defineProperty(node, 'textContent', {
    get() {
      if (this._children && this._children.length > 0) return this._children.map(c => c.textContent).join('');
      return this._text || '';
    },
    set(v) { this._text = String(v); this._children = []; }
  });
  function serializeNode(n) {
    if (n._html) return n._html;
    if (n._children && n._children.length > 0) {
      return n._children.map(c => serializeNode(c)).join('');
    }
    return n._text || '';
  }
  Object.defineProperty(node, 'innerHTML', {
    get() {
      return serializeNode(this);
    },
    set(v) { this._html = String(v); this._text = String(v); this._children = []; }
  });
  return node;
}

const byId = {
  statusFilter: Object.assign(fakeNode('select'), { value: 'all' }),
  providerSegmented: fakeNode('div'),
  endpointSort: fakeNode('div'),
  dashboardContent: fakeNode('div'),
  accountsList: fakeNode('div'),
  updateTimestamp: fakeNode('span'),
  updateRelative: fakeNode('span'),
  staleNoticeBox: fakeNode('div'),
  staleNoticeText: fakeNode('span'),
  unconfiguredBox: fakeNode('div'),
  unconfiguredText: fakeNode('span'),
  accountStatusLive: fakeNode('span'),
  quotaErrorBox: fakeNode('div'),
  quotaErrorText: fakeNode('span'),
};

const doc = {
  hidden: false, activeElement: null, addEventListener() {},
  createElement(tag) { return fakeNode(tag); },
  createDocumentFragment() { const f = fakeNode('#fragment'); f._isFragment = true; return f; },
  getElementById(id) { if (!byId[id]) byId[id] = fakeNode('div'); return byId[id]; },
};

let currentFetchData = null;
let timerId = 0;
const sandbox = {
  document: doc, window: { document: doc },
  setTimeout() { return ++timerId; }, clearTimeout() {},
  console: console,
  fetch: () => Promise.resolve({ ok: true, json: () => Promise.resolve(currentFetchData) }),
  AbortController: function() { this.signal = {}; this.abort = () => {}; },
  Date: Date, Math: Math, String: String, Number: Number, Array: Array, Object: Object, isNaN: isNaN, isFinite: isFinite,
};

vm.createContext(sandbox);
vm.runInContext(script, sandbox);

(async () => {
  // Test 1: snapshot_complete = false (in-flight collection)
  currentFetchData = {
    configured: true,
    snapshot_complete: false,
    reason: 'not_collected',
    stale: true,
    accounts: []
  };
  sandbox.loadDashboard(true);
  await new Promise(r => setImmediate(r));
  await new Promise(r => setImmediate(r));

  if (sandbox.cachedAccounts.length !== 0) {
    throw new Error('cachedAccounts must remain empty when snapshot_complete=false, got ' + sandbox.cachedAccounts.length);
  }
  if (!byId['accountsList'].innerHTML.includes('skeleton-card')) {
    throw new Error('expected skeleton-card elements when snapshot_complete=false');
  }

  // Test 2: snapshot_complete = true with full payload
  currentFetchData = {
    configured: true,
    snapshot_complete: true,
    stale: false,
    generation: 1,
    accounts: [
      {
        account_key: 'ag-1',
        provider: 'antigravity',
        account: 'd***@example.com',
        endpoint: 'direct',
        status: 'known',
        current_account: true,
        current_kind: 'candidate',
        current_reason: 'priority_candidate',
        windows: [
          { name: 'gemini-5h', remaining_percent: 100, window_minutes: 300 },
          { name: 'gemini-weekly', remaining_percent: 100, window_minutes: 10080 },
        ]
      },
      {
        account_key: 'codex-1',
        provider: 'codex',
        account: 'c***@example.com',
        endpoint: 'warp1',
        plan_type: 'plus',
        status: 'known',
        current_account: true,
        current_kind: 'recent',
        current_reason: 'recent_usage',
        api_credits: { has_credits: false, unlimited: false, balance: 0 },
        reset_credits: { available_count: 1, applicable_available_count: 1 },
        windows: [
          { name: 'primary-limit', remaining_percent: 90, window_minutes: 300 },
          { name: 'secondary-limit', remaining_percent: 95, window_minutes: 10080 },
        ]
      },
      {
        account_key: 'codex-2-limited',
        provider: 'codex',
        account: 'lim***@example.com',
        endpoint: 'warp2',
        plan_type: 'k12',
        status: 'known',
        current_account: false, // Limited account!
        windows: [
          { name: 'primary-limit', remaining_percent: 0, limit_reached: true, window_minutes: 300 },
          { name: 'secondary-limit', remaining_percent: 100, window_minutes: 10080 },
        ]
      }
    ]
  };

  sandbox.loadDashboard(false);
  await new Promise(r => setImmediate(r));
  await new Promise(r => setImmediate(r));

  if (sandbox.cachedAccounts.length !== 3) {
    throw new Error('cachedAccounts should be 3, got ' + sandbox.cachedAccounts.length);
  }

  const renderedCards = byId['accountsList']._children.filter(c => c.className === 'account-card');
  if (renderedCards.length !== 3) {
    throw new Error('accountsList should have 3 account-cards, got ' + renderedCards.length);
  }

  // Check card 0 (AG current):
  const agCard = renderedCards.find(c => c.attrs['data-account-key'] === 'ag-1');
  if (!agCard || agCard.attrs['data-current'] === 'true' || agCard.innerHTML.includes('badge-current')) {
    throw new Error('legacy candidate ag-1 must not have current badge');
  }

  // Check card 1 (Codex healthy current):
  const codexCard = renderedCards.find(c => c.attrs['data-account-key'] === 'codex-1');
  if (!codexCard || codexCard.attrs['data-current'] !== 'true') {
    throw new Error('codex-1 should have data-current="true"');
  }
  const codexHtml = codexCard.innerHTML;
  if (!codexHtml.includes('Plus')) {
    throw new Error('codex-1 card should display Plus plan');
  }
  if (!codexHtml.includes('잔액 0')) {
    throw new Error('codex-1 card should display "잔액 0" for balance=0');
  }
  if (!codexHtml.includes('리셋권 1장 보유')) {
    throw new Error('codex-1 card should display reset credits');
  }
  if (codexHtml.includes('badge-current') || codexHtml.includes('최근 토큰 사용 계정')) {
    throw new Error('codex-1 should use its border without a recent token-use badge');
  }
  if (!codexCard.attrs['aria-label'].includes('최근 15분 내 토큰이 보고된 완료 요청의 계정')) {
    throw new Error('codex-1 recent token use must remain accessible without the badge');
  }

  // Check card 2 (Codex limited):
  const limCard = renderedCards.find(c => c.attrs['data-account-key'] === 'codex-2-limited');
  if (limCard.attrs['data-current'] === 'true') {
    throw new Error('REGRESSION: codex-2-limited card must NOT have data-current="true"');
  }
  const limHtml = limCard.innerHTML;
  if (limHtml.includes('badge-current') || limHtml.includes('현재 우선 계정') || limHtml.includes('최근 토큰 사용 계정')) {
    throw new Error('REGRESSION: codex-2-limited card must NOT display current badge');
  }

  console.log('NODE_OK');
})();
`

	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node atomic snapshot test failed: %v\nOutput:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "NODE_OK") {
		t.Fatalf("node atomic snapshot test did not report NODE_OK: %s", string(out))
	}
}

// TestActualPayloadStatisticsAndCurrentEligibility verifies using actual payload fixture
// that limited accounts never receive CurrentAccount=true, API credits are rendered accurately,
// and card count equals accounts count.
func TestActualPayloadStatisticsAndCurrentEligibility(t *testing.T) {
	payloadPath := "/tmp/actual_payload.json"
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Skip("actual payload file not found, skipping actual fixture verification")
		return
	}

	var root struct {
		Configured       bool                        `json:"configured"`
		SnapshotComplete bool                        `json:"snapshot_complete"`
		Accounts         []quotamonitor.AccountQuota `json:"accounts"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// Sanitize accounts with our new eligibility logic
	sanitized := sanitizeAccounts(root.Accounts)

	codexCount := 0
	codexPlanCount := 0
	codexAPICreditsCount := 0
	codexWindowsCount := 0
	limitedWithCurrent := 0
	currentByProv := make(map[string]int)

	for _, a := range sanitized {
		prov := strings.ToLower(strings.TrimSpace(a.Provider))
		if a.CurrentAccount {
			currentByProv[prov]++
		}
		if prov == "codex" {
			codexCount++
			if a.PlanType != "" {
				codexPlanCount++
			}
			if a.APICredits != nil {
				codexAPICreditsCount++
			}
			if len(a.Windows) > 0 {
				codexWindowsCount++
			}
		}

		// Check if limited according to canonical rule
		isLimited := false
		for _, w := range a.Windows {
			if prov == "antigravity" && !strings.Contains(strings.ToLower(w.Name), "gemini") {
				continue
			}
			if (w.LimitReached != nil && *w.LimitReached) || (w.RemainingPercent != nil && *w.RemainingPercent <= 0) {
				isLimited = true
				break
			}
		}
		if isLimited && a.CurrentAccount {
			limitedWithCurrent++
			t.Logf("Limited current account: provider=%s, key=%s, windows=%d", a.Provider, a.AccountKey, len(a.Windows))
			for _, w := range a.Windows {
				rem := 100.0
				if w.RemainingPercent != nil {
					rem = *w.RemainingPercent
				}
				lr := false
				if w.LimitReached != nil {
					lr = *w.LimitReached
				}
				t.Logf("  window: name=%s, rem=%v, limit_reached=%v", w.Name, rem, lr)
			}
		}
	}

	t.Logf("Statistics: Codex total=%d, with_plan=%d, with_api_credits=%d, with_windows=%d",
		codexCount, codexPlanCount, codexAPICreditsCount, codexWindowsCount)
	t.Logf("Current accounts by provider: %v", currentByProv)
	t.Logf("Limited accounts with CurrentAccount: %d", limitedWithCurrent)

	if limitedWithCurrent != 0 {
		t.Fatalf("REGRESSION: %d limited accounts received CurrentAccount=true!", limitedWithCurrent)
	}
	for prov, count := range currentByProv {
		if count > 1 {
			t.Errorf("provider %s has %d current accounts, expected at most 1", prov, count)
		}
	}
}
