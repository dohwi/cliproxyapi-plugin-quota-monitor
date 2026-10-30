package plugin

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"quota-monitor/internal/dashboard"
)

// The dashboard persists the language preference (localStorage, LANG_KEY) and, for the
// current tab only, the management key (sessionStorage, KEY_STORAGE). The key must never
// reach localStorage, which outlives the tab and is shared across tabs.
func TestDashboardStorageContract(t *testing.T) {
	html := dashboard.HTML
	if !strings.Contains(html, "var LANG_KEY = 'cpa_plugin_lang';") {
		t.Fatal("language preference key is missing or renamed")
	}
	if !strings.Contains(html, "var KEY_STORAGE = 'cpa_quota_monitor_mgmt_key';") {
		t.Fatal("session key storage name is missing or renamed")
	}
	total := strings.Count(html, "localStorage")
	allowed := regexp.MustCompile(`localStorage\.(?:getItem\(LANG_KEY\)|setItem\(LANG_KEY, lang\))`).FindAllString(html, -1)
	if total != len(allowed) || total != 2 {
		t.Fatalf("localStorage references = %d (allowed LANG_KEY accessors = %d, want exactly 2)", total, len(allowed))
	}
	sessionTotal := strings.Count(html, "sessionStorage")
	sessionAllowed := regexp.MustCompile(`sessionStorage\.(?:getItem\(KEY_STORAGE\)|setItem\(KEY_STORAGE, key\)|removeItem\(KEY_STORAGE\))`).FindAllString(html, -1)
	if sessionTotal != len(sessionAllowed) || sessionTotal != 3 {
		t.Fatalf("sessionStorage references = %d (allowed KEY_STORAGE accessors = %d, want exactly 3)", sessionTotal, len(sessionAllowed))
	}
	// The key travels only in the Authorization header: never concatenated into a URL.
	if n := len(regexp.MustCompile(`\+\s*mgmtKey`).FindAllString(html, -1)); n != 2 {
		t.Errorf("mgmtKey concatenations = %d, want exactly 2 (the two Authorization headers)", n)
	}
}

// Every translation key must exist in both languages with identical
// placeholders, and every t()/tn()/data-i18n key used by the page must exist.
func TestDashboardI18nCatalogsAreComplete(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := `
const html = process.argv[1];
const m = html.match(/<script>([\s\S]*?)<\/script>/);
const src = m[1];
const start = src.indexOf('var I18N = {');
const end = src.indexOf('var currentLang');
const I18N = new Function(src.slice(start, end) + '; return I18N;')();
const fail = [];
const ko = Object.keys(I18N.ko), en = Object.keys(I18N.en);
const base = k => k.replace(/\.one$/, '');
for (const k of ko) if (!(k in I18N.en)) fail.push('missing in en: ' + k);
for (const k of en) if (!(k in I18N.ko) && !(k.endsWith('.one') && base(k) in I18N.ko)) fail.push('missing in ko: ' + k);
const ph = s => (s.match(/\{\w+\}/g) || []).sort().join();
for (const k of ko) if (k in I18N.en && ph(I18N.ko[k]) !== ph(I18N.en[k])) fail.push('placeholder mismatch: ' + k);
const used = new Set();
for (const r of src.matchAll(/\bt[n]?\('([\w.]+)'/g)) used.add(r[1]);
for (const r of html.matchAll(/data-i18n(?:-aria)?="([\w.]+)"/g)) used.add(r[1]);
const dynamic = /^(fleet|grp)\./;
for (const k of used) if (!(k in I18N.ko) && !dynamic.test(k)) fail.push('unknown key used: ' + k);
for (const k of ['grp.antigravity','grp.claude','grp.limits','grp.unknown','fleet.antigravity_gemini','fleet.antigravity_claude']) if (!(k in I18N.ko && k in I18N.en)) fail.push('dynamic key missing: ' + k);
if (fail.length) { console.error(fail.join('\n')); process.exit(1); }
console.log('OK_I18N');
`
	cmd := nodeDashboardCmd(t, "node", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("i18n catalog check failed: %v\n%s", err, out)
	}
}

// Switching language re-renders both the static markup and dynamic cards, and
// the choice survives a reload through the stored preference.
func TestDashboardLanguageSwitchNodeContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := `
const vm = require('vm');
const html = process.argv[1];
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
function makeCtx(stored, navLangs) {
  const store = {}; if (stored) store.cpa_plugin_lang = stored;
  const els = {};
  const el = id => els[id] || (els[id] = {
    id, style: {}, attrs: {}, children: [], textContent: '', innerHTML: '', className: '', _i18n: undefined,
    setAttribute(k, v) { this.attrs[k] = v; }, getAttribute(k) { return this.attrs[k] === undefined ? null : this.attrs[k]; },
    appendChild(c) { this.children.push(c); return c; },
    addEventListener() {}, removeAttribute(k) { delete this.attrs[k]; },
    classList: { add() {}, remove() {}, toggle() {} },
  });
  const root = el('__root');
  const ctx = {
    console, setTimeout() { return 0; }, clearTimeout() {}, Date, Math, JSON, Promise, Object, Array, String, Number, isNaN, isFinite, parseInt,
    navigator: navLangs ? { languages: navLangs, language: navLangs[0] } : undefined,
    localStorage: { getItem: k => (k in store ? store[k] : null), setItem: (k, v) => { store[k] = v; } },
    document: {
      title: '', documentElement: root, hidden: false,
      getElementById: el, addEventListener() {},
      querySelectorAll() { return []; }, querySelector() { return null; },
      createElement(tag) { return el('__n' + Math.random()); },
      createDocumentFragment() { return el('__f' + Math.random()); },
    },
    window: { addEventListener() {}, matchMedia() { return { matches: false, addEventListener() {} }; } },
    fetch() { return new Promise(() => {}); },
  };
  ctx.window.document = ctx.document;
  vm.createContext(ctx);
  vm.runInContext(script, ctx);
  return { ctx, store, root };
}
function assert(c, m) { if (!c) throw new Error(m); }

let r = makeCtx(null, null);
assert(r.ctx.currentLang === 'en', 'defaults to en without navigator');
r = makeCtx(null, ['en-US', 'ko']);
assert(r.ctx.currentLang === 'en', 'browser language en-US selects en');
r = makeCtx(null, ['ko-KR']);
assert(r.ctx.currentLang === 'ko', 'browser language ko-KR selects ko');
r = makeCtx(null, ['fr-FR']);
assert(r.ctx.currentLang === 'en', 'unsupported browser language falls back to en');
r = makeCtx('ko', ['en-US']);
assert(r.ctx.currentLang === 'ko', 'stored preference wins over browser language');
r = makeCtx('xx', ['ko-KR']);
assert(r.ctx.currentLang === 'ko', 'invalid stored value is ignored');

r = makeCtx('ko', null);
const c = r.ctx;
assert(c.humanizeSeconds(3700) === '1시간 1분', 'ko duration: ' + c.humanizeSeconds(3700));
assert(c.formatResetCountdownSeconds(90) === '1분 30초 후 초기화', 'ko countdown');
assert(c.formatResetExpiry('2026-10-04T15:05:29') === '2026. 10. 4. 오후 3:05:29', 'ko expiry');
c.setLanguage('en');
assert(c.currentLang === 'en' && r.store.cpa_plugin_lang === 'en', 'setLanguage persists choice');
assert(r.root.attrs.lang === 'en', 'html lang follows language');
assert(c.document.title === 'Remaining quota by account', 'document title translated');
assert(c.humanizeSeconds(3700) === '1h 1m', 'en duration: ' + c.humanizeSeconds(3700));
assert(c.formatResetCountdownSeconds(90) === 'Resets in 1m 30s', 'en countdown: ' + c.formatResetCountdownSeconds(90));
assert(c.formatResetCountdownSeconds(0) === 'Reset time reached \u00b7 awaiting update', 'en reached');
assert(c.formatResetExpiry('2026-10-04T15:05:29') === 'Oct 4, 2026, 3:05:29 PM', 'en expiry: ' + c.formatResetExpiry('2026-10-04T15:05:29'));
assert(c.tn('live.shown', 1) === 'Showing 1 account' && c.tn('live.shown', 3) === 'Showing 3 accounts', 'en plural');
assert(c.fleetLabel({ key: 'antigravity_gemini', label: '안티그래비티 · Gemini' }) === 'Antigravity \u00b7 Gemini', 'fleet label localized by key');
assert(c.fleetLabel({ key: 'claude', label: 'Claude' }) === 'Claude', 'unknown fleet key falls back to server label');
assert(c.formatNum(1234567) === '1,234,567', 'en number format');
assert(c.getCanonicalStatus({ status: 'known', windows: [{ name: 'primary', used_percent: 10 }] }).label === 'Active', 'canonical label en');
assert(c.quotaKindLabel('weekly-opus') === 'Opus \u00b7 Weekly limit', 'quota kind label en');
c.setLanguage('ko');
assert(c.quotaKindLabel('weekly-opus') === 'Opus · 주간 제한', 'quota kind label back to ko');
assert(r.store.cpa_plugin_lang === 'ko', 'switching back persists');
c.setLanguage('fr');
assert(c.currentLang === 'ko', 'unsupported language ignored');
console.log('OK_I18N_SWITCH');
`
	cmd := nodeDashboardCmdRaw(t, "node", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("language switch contract failed: %v\n%s", err, out)
	}
}
