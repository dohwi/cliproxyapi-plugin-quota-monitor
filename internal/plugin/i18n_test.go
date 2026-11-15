package plugin

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"quota-monitor/internal/dashboard"
)

// The dashboard persists the language preference (localStorage, LANG_PREF_KEY through the shared
// core's lsGet/lsSet) and the theme mode (THEME_KEY), and, for the current tab only, the management
// key (sessionStorage, KEY_STORAGE). "System" theme follows the host (cli-proxy-theme). It may read,
// never write, the host's own entries (management key, theme, language). The management key must
// never be written to localStorage. The old "last action wins" keys are gone.
func TestDashboardStorageContract(t *testing.T) {
	html := dashboard.HTML
	if !strings.Contains(html, "var LANG_PREF_KEY = 'cpa_plugin_language';") {
		t.Fatal("language preference key is missing or renamed")
	}
	if !strings.Contains(html, "var KEY_STORAGE = 'cpa_quota_monitor_mgmt_key';") {
		t.Fatal("session key storage name is missing or renamed")
	}
	if !strings.Contains(html, "var THEME_KEY = 'cpa_plugin_theme';") || !strings.Contains(html, "var HOST_THEME_KEY = 'cli-proxy-theme';") {
		t.Fatal("theme preference key or host theme key is missing or renamed")
	}
	// Old keys are neither read nor written (only named in the core's comment).
	for _, old := range []string{"'cpa_plugin_lang'", "'cpa_plugin_lang_host'", "LANG_HOST_KEY", "var LANG_KEY"} {
		if strings.Contains(html, old) {
			t.Fatalf("old language key %s is still referenced", old)
		}
	}
	// localStorage is touched only by the core's lsGet/lsSet, the host-store reader and the theme
	// accessors: 5 references in total.
	total := strings.Count(html, "localStorage")
	allowed := regexp.MustCompile(`localStorage\.(?:getItem\((?:k|THEME_KEY|name)\)|setItem\((?:k, v|THEME_KEY, mode)\))`).FindAllString(html, -1)
	if total != len(allowed) || total != 5 {
		t.Fatalf("localStorage references = %d (allowed accessors = %d, want exactly 5)", total, len(allowed))
	}
	// lsSet writes only the language preference; lsGet reads only it and the console's entry.
	for _, m := range regexp.MustCompile(`\blsSet\(([A-Za-z_]+)`).FindAllStringSubmatch(html, -1) {
		if m[1] != "LANG_PREF_KEY" && m[1] != "k" {
			t.Fatalf("lsSet writes %s; only LANG_PREF_KEY is allowed", m[1])
		}
	}
	for _, m := range regexp.MustCompile(`\blsGet\(([A-Za-z_]+)`).FindAllStringSubmatch(html, -1) {
		if m[1] != "LANG_PREF_KEY" && m[1] != "HOST_LANG_KEY" && m[1] != "k" {
			t.Fatalf("lsGet reads %s; only LANG_PREF_KEY and HOST_LANG_KEY are allowed", m[1])
		}
	}
	// The console's own language entry is read, never written or removed.
	if regexp.MustCompile(`(?:setItem|removeItem|lsSet)\(HOST_LANG_KEY`).MatchString(html) || !strings.Contains(html, "var HOST_LANG_KEY = 'cli-proxy-language';") {
		t.Fatal("host language key must exist and stay read-only")
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

// Every translation key must exist in en, ko and zh-CN with identical placeholders, and
// every t()/tn()/data-i18n key used by the page must exist in all three.
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
const ko = Object.keys(I18N.ko), en = Object.keys(I18N.en), zh = Object.keys(I18N['zh-CN'] || {});
if (Object.keys(I18N).join() !== 'ko,en,zh-CN') fail.push('languages: ' + Object.keys(I18N).join());
const base = k => k.replace(/\.one$/, '');
for (const k of ko) if (!(k in I18N.en)) fail.push('missing in en: ' + k);
for (const k of en) if (!(k in I18N.ko) && !(k.endsWith('.one') && base(k) in I18N.ko)) fail.push('missing in ko: ' + k);
// zh-CN has every en key, .one variants included (same text: Chinese has no plural).
for (const k of en) if (!(k in I18N['zh-CN'])) fail.push('missing in zh-CN: ' + k);
for (const k of zh) if (!(k in I18N.en)) fail.push('zh-CN only: ' + k);
const ph = s => (s.match(/\{\w+\}/g) || []).sort().join();
for (const k of ko) if (k in I18N.en && ph(I18N.ko[k]) !== ph(I18N.en[k])) fail.push('placeholder mismatch: ' + k);
for (const k of zh) if (k in I18N.en && ph(I18N['zh-CN'][k]) !== ph(I18N.en[k])) fail.push('zh-CN placeholder mismatch: ' + k);
for (const k of zh) if (/[가-힣]/.test(I18N['zh-CN'][k])) fail.push('Hangul in zh-CN: ' + k);
for (const k of ['lang.toEn', 'lang.toKo', 'lang.label']) if (k in I18N.en) fail.push('old toggle key left: ' + k);
if (I18N.en['lang.menu'] !== 'Language' || I18N.ko['lang.menu'] !== '언어' || I18N['zh-CN']['lang.menu'] !== '语言') fail.push('lang.menu labels');
if (I18N.en['lang.auto'] !== 'Follow CLIProxyAPI' || I18N.ko['lang.auto'] !== 'CLIProxyAPI 설정 따르기' || I18N['zh-CN']['lang.auto'] !== '跟随 CLIProxyAPI') fail.push('lang.auto labels');
const used = new Set();
for (const r of src.matchAll(/\bt[n]?\('([\w.]+)'/g)) used.add(r[1]);
for (const r of html.matchAll(/data-i18n(?:-aria)?="([\w.]+)"/g)) used.add(r[1]);
const dynamic = /^(fleet|grp)\./;
for (const k of used) if (!(k in I18N.ko && k in I18N.en && k in I18N['zh-CN']) && !dynamic.test(k)) fail.push('unknown key used: ' + k);
for (const k of ['grp.antigravity','grp.claude','grp.limits','grp.unknown','fleet.antigravity_gemini','fleet.antigravity_claude']) if (!(k in I18N.ko && k in I18N.en && k in I18N['zh-CN'])) fail.push('dynamic key missing: ' + k);
if (fail.length) { console.error(fail.join('\n')); process.exit(1); }
console.log('OK_I18N');
`
	cmd := nodeDashboardCmd(t, "node", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("i18n catalog check failed: %v\n%s", err, out)
	}
}

// Switching language re-renders the static markup and dynamic text (durations, dates, numbers)
// in en, ko and zh-CN; a pinned preference wins over the browser language.
func TestDashboardLanguageSwitchNodeContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := `
const vm = require('vm');
const html = process.argv[1];
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
function makeCtx(stored, navLangs) {
  const store = {}; if (stored) store.cpa_plugin_language = stored;
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
r = makeCtx(null, ['zh-TW']);
assert(r.ctx.currentLang === 'zh-CN', 'browser language zh-TW selects zh-CN');
r = makeCtx(null, ['fr-FR']);
assert(r.ctx.currentLang === 'en', 'unsupported browser language falls back to en');
r = makeCtx('ko', ['en-US']);
assert(r.ctx.currentLang === 'ko', 'pinned preference wins over browser language');
r = makeCtx('zh-CN', ['ko-KR']);
assert(r.ctx.currentLang === 'zh-CN', 'pinned zh-CN wins');
r = makeCtx('xx', ['ko-KR']);
assert(r.ctx.currentLang === 'ko', 'invalid stored value is treated as auto');
r = makeCtx('auto', ['ko-KR']);
assert(r.ctx.currentLang === 'ko', 'auto without a console follows the browser');

r = makeCtx('ko', null);
const c = r.ctx;
assert(c.humanizeSeconds(3700) === '1시간 1분', 'ko duration: ' + c.humanizeSeconds(3700));
assert(c.formatResetCountdownSeconds(90) === '1분 30초 후 초기화', 'ko countdown');
assert(c.formatResetExpiry('2026-10-04T15:05:29') === '2026. 10. 4. 오후 3:05:29', 'ko expiry');
c.setLanguage('en');
assert(c.currentLang === 'en', 'setLanguage shows en');
assert(r.store.cpa_plugin_language === 'ko', 'setLanguage never writes the preference (the menu does)');
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
// Simplified Chinese: natural durations without spaces, 刚刚, Chinese dates.
c.setLanguage('zh-CN');
assert(r.root.attrs.lang === 'zh-CN' && c.document.title === '各账号剩余额度', 'zh-CN lang/title');
assert(c.humanizeSeconds(10740) === '2小时59分钟', 'zh duration: ' + c.humanizeSeconds(10740));
assert(c.formatResetCountdownSeconds(10740) === '2小时59分钟后重置', 'zh countdown: ' + c.formatResetCountdownSeconds(10740));
assert(c.formatResetCountdownSeconds(90061) === '1天1小时后重置', 'zh days: ' + c.formatResetCountdownSeconds(90061));
assert(c.formatResetCountdownSeconds(90) === '1分钟30秒后重置', 'zh minutes: ' + c.formatResetCountdownSeconds(90));
assert(c.relativeTime(new Date(Date.now() - 1000)) === '刚刚', 'zh just now');
assert(c.relativeTime(new Date(Date.now() - 125000)) === '2分钟前', 'zh minutes ago');
assert(c.t('fresh.last', { t: c.t('rel.now') }) === '上次检测：刚刚', 'zh last checked');
assert(c.t('refresh.allCooldown', { s: 12 }) === '刷新全部（12秒）', 'zh cooldown seconds');
assert(c.formatResetExpiry('2026-10-04T15:05:29') === '2026年10月4日 15:05:29', 'zh expiry: ' + c.formatResetExpiry('2026-10-04T15:05:29'));
assert(c.tn('live.shown', 1) === '正在显示1个账号' && c.tn('fleet.count', 1) === '1个账号', 'zh no plural');
assert(c.quotaKindLabel('weekly-opus') === 'Opus \u00b7 周限额' && c.quotaKindLabel('five-hour') === '5 小时限额', 'zh glossary quota labels');
assert(c.serverText('auth_files_failed', 'x') === '无法获取认证文件列表', 'zh server code');
c.setLanguage('ko');
assert(c.quotaKindLabel('weekly-opus') === 'Opus · 주간 제한', 'quota kind label back to ko');
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
