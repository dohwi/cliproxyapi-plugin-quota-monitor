package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"quota-monitor/internal/dashboard"
	"strings"
	"sync/atomic"
	"testing"
)

func TestV53ResetDetailForEveryEligibleAccountAndFailureIsNonfatal(t *testing.T) {
	reset(t)
	var usageCalls, detailCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"1","account":"first@example.com","quota":{"signals":{}}},{"provider":"codex","auth_index":"2","account":"second@example.com","quota":{"signals":{}}}]`))
		case "/management/api-call":
			var p struct {
				URL string `json:"url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&p)
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(p.URL, "/usage") {
				usageCalls.Add(1)
				_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"rate_limit\":{\"primary_window\":{\"used_percent\":20}},\"rate_limit_reset_credits\":{\"available_count\":2,\"applicable_available_count\":1},\"credits\":{\"has_credits\":true,\"balance\":4}}"}`))
				return
			}
			detailCalls.Add(1)
			// Optional detail failure must preserve successful usage/count data.
			_, _ = w.Write([]byte(`{"status_code":500,"body":"not exposed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	os.Setenv("QUOTA_MONITOR_MGMT_URL", ts.URL)
	os.Setenv("QUOTA_MONITOR_MGMT_KEY", "k")
	configureFromEnv(nil)
	accounts, _, err := getAccounts()
	if err != nil || len(accounts) != 2 {
		t.Fatalf("accounts=%d err=%v", len(accounts), err)
	}
	if usageCalls.Load() != 2 || detailCalls.Load() != 2 {
		t.Fatalf("usage/detail=%d/%d want 2/2", usageCalls.Load(), detailCalls.Load())
	}
	for _, a := range accounts {
		if a.ResetCredits == nil || a.ResetCredits.AvailableCount == nil || *a.ResetCredits.AvailableCount != 2 || a.APICredits == nil {
			t.Fatalf("missing safe extras: %#v", a)
		}
		if a.ResetCredits.NearestExpiry != nil {
			t.Fatalf("failed detail unexpectedly retained expiry: %#v", a.ResetCredits)
		}
	}
	raw, _ := json.Marshal(sanitizeAccounts(accounts))
	for _, banned := range []string{"auth_index", "first@example.com", "second@example.com", "not exposed", "\"id\"", "granted"} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("resource leak %q: %s", banned, raw)
		}
	}
}

func TestV53KoreanCanonicalDisplayAndSelfHostedSuit(t *testing.T) {
	for _, want := range []string{"5시간 제한", "주간 제한", "잔여 ", "후 초기화", "리셋권 ", "API 크레딧", "@font-face", "suit-regular.woff2", "suit-semibold.woff2", "dashboard-control-panel", "font-family: 'SUIT'"} {
		if !strings.Contains(dashboard.HTML, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	for _, forbidden := range []string{"'Primary'", "'Secondary'", "cdn", "fonts.googleapis", "http://", "https://"} {
		if strings.Contains(dashboard.HTML, forbidden) {
			t.Errorf("dashboard has forbidden visible/external label %q", forbidden)
		}
	}
}

func TestCreditSummaryApplicableTooltipAndZeroVsNull(t *testing.T) {
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

// 1. Owned > 0 and applicable available count > 0: shows auxiliary applicability.
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
assert(resBoth.indexOf('└ </span><span class="credit-reset-expiry-text">가장 빠른 만료일: 2026. 10. 4. 오후 3:05:29') !== -1, 'has separate tree-style earliest expiry');
assert(resBoth.indexOf('적용 가능한 리셋권') === -1, 'applicability tooltip is hidden');
assert(resBoth.indexOf('API 크레딧</strong> · 잔액 0') !== -1, 'API zero balance remains explicit');
assert(resBoth.indexOf('class="credit-api-summary"') !== -1, 'API credits remain a separate row');
assert(resBoth.indexOf('aria-label="리셋권 5장 보유. 가장 빠른 만료일: 2026. 10. 4. 오후 3:05:29. API 크레딧 · 잔액 0"') !== -1, 'accessible summary matches display');

// 2. applicable zero and null do not add UI text; only owned count is shown.
const applicableZero = ctx.renderCreditSummary({
  reset_credits: {
    available_count: 2,
    applicable_available_count: 0,
    nearest_expiry: '2026-10-04T15:05:29'
  }
});
assert(applicableZero.indexOf('리셋권 2장 보유') !== -1, 'applicable zero preserves owned count');
assert(applicableZero.indexOf('현재 계정에는 적용 불가') === -1, 'applicable zero text is hidden');
assert(applicableZero.indexOf('<div class="credit-reset-primary"><strong>리셋권 2장 보유</strong></div>') !== -1, 'owned count is the only primary reset text');
assert(applicableZero.indexOf('가장 빠른 만료일: 2026. 10. 4. 오후 3:05:29') !== -1, 'requested earliest-expiry wording is exact');

const applicableNull = ctx.renderCreditSummary({
  reset_credits: { available_count: 3, applicable_available_count: null }
});
assert(applicableNull.indexOf('리셋권 3장 보유') !== -1, 'applicable null preserves owned count');
assert(applicableNull.indexOf('현재 계정 적용 여부 미확인') === -1, 'applicable null text is hidden');

// 3. available zero and null are both hidden, and tested independently.
const availableZero = ctx.renderCreditSummary({
  reset_credits: { available_count: 0, applicable_available_count: 2 }
});
assert(availableZero === '', 'available zero hides reset credits');
assert(availableZero.indexOf('리셋권 없음') === -1, 'available zero is not emphasized');

const availableNull = ctx.renderCreditSummary({
  reset_credits: { available_count: null, applicable_available_count: 2 }
});
assert(availableNull === '', 'available null remains unknown and hidden');

console.log('OK_CREDIT_SUMMARY_TESTS');
`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_CREDIT_SUMMARY_TESTS") {
		t.Fatalf("Credit summary applicable tooltip tests failed: %v\n%s", err, out)
	}
}
