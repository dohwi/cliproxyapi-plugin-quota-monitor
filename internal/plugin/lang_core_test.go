package plugin

import (
	"os/exec"
	"strings"
	"testing"
)

// The shared language core (between the @lang-core markers, identical in every dohwi
// CLIProxyAPI plugin) runs standalone under node: tag mapping, preference resolution,
// console sources and storage rules.
func TestLanguageCoreNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := `
const vm = require('vm');
const html = process.argv[1];
const a = html.indexOf('// @lang-core-begin'), b = html.indexOf('// @lang-core-end');
if (a < 0 || b < a) { console.error('FAIL: @lang-core markers missing'); process.exit(1); }
const core = html.slice(a, b);
const fail = [];
const eq = (got, want, m) => { if (got !== want) fail.push(m + ': ' + JSON.stringify(got) + ' != ' + JSON.stringify(want)); };
// env: { store, parentLang (undefined = top window, null = cross-origin parent), nav }
function env(o) {
  const store = Object.assign({}, o.store || {}), writes = [];
  // Node >= 21 has a global navigator; the context gets its own.
  const win = {};
  if (o.parentLang === null) win.parent = { get document() { throw new Error('cross-origin'); } };
  else if (o.parentLang !== undefined) win.parent = { document: { documentElement: { getAttribute: k => k === 'lang' ? o.parentLang : null } } };
  else win.parent = win;
  const ctx = { JSON, String, Object, Array, window: win,
    localStorage: { getItem: k => (k in store ? store[k] : null), setItem: (k, v) => { writes.push(k); store[k] = String(v); }, removeItem: k => { writes.push('-' + k); delete store[k]; } } };
  Object.defineProperty(ctx, 'navigator', { value: { language: o.nav || '', languages: o.nav ? [o.nav] : [] }, configurable: true });
  vm.createContext(ctx);
  vm.runInContext(core, ctx);
  return { ctx, store, writes };
}
// normLang table.
const n = env({}).ctx;
const table = { 'ko': 'ko', 'ko-KR': 'ko', 'zh': 'zh-CN', 'zh-CN': 'zh-CN', 'zh-TW': 'zh-CN', 'zh-Hant': 'zh-CN', 'zh_HK': 'zh-CN', 'en-US': 'en', 'ru': 'en', 'tr': 'en', 'vi': 'en', '': '' };
for (const k in table) eq(n.normLang(k), table[k], 'normLang(' + k + ')');
eq(n.intlLocale('zh-CN'), 'zh-CN', 'intlLocale zh'); eq(n.intlLocale('ko'), 'ko-KR', 'intlLocale ko'); eq(n.intlLocale('en'), 'en-US', 'intlLocale en');
eq(n.LANGS.join(), 'en,ko,zh-CN', 'LANGS');
eq(n.LANG_NAMES['zh-CN'] + n.LANG_NAMES.ko + n.LANG_NAMES.en, '简体中文한국어English', 'native names');
// auto with the console's persisted entry in its three shapes.
const zustand = l => JSON.stringify({ state: { language: l }, version: 0 });
eq(env({ store: { 'cli-proxy-language': zustand('zh-CN') }, nav: 'en-US' }).ctx.resolveLang('auto'), 'zh-CN', 'zustand JSON');
eq(env({ store: { 'cli-proxy-language': JSON.stringify({ language: 'ko' }) }, nav: 'en-US' }).ctx.resolveLang('auto'), 'ko', '{language}');
eq(env({ store: { 'cli-proxy-language': 'zh-TW' }, nav: 'en-US' }).ctx.resolveLang('auto'), 'zh-CN', 'bare string');
eq(env({ store: { 'cli-proxy-language': JSON.stringify('ko') }, nav: 'en-US' }).ctx.resolveLang('auto'), 'ko', 'JSON string');
// Parent <html lang> wins over the stored entry; cross-origin parent falls back to it.
eq(env({ parentLang: 'zh-CN', store: { 'cli-proxy-language': zustand('ko') }, nav: 'en-US' }).ctx.resolveLang('auto'), 'zh-CN', 'parent html lang');
eq(env({ parentLang: null, store: { 'cli-proxy-language': zustand('ko') }, nav: 'en-US' }).ctx.resolveLang('auto'), 'ko', 'cross-origin parent');
// No console → browser → en.
eq(env({ nav: 'zh-Hant-TW' }).ctx.resolveLang('auto'), 'zh-CN', 'browser zh');
eq(env({ nav: 'ko-KR' }).ctx.resolveLang('auto'), 'ko', 'browser ko');
eq(env({}).ctx.resolveLang('auto'), 'en', 'nothing -> en');
// Pinned preferences win over the console.
const pin = env({ parentLang: 'ko', store: { cpa_plugin_language: 'zh-CN' }, nav: 'en-US' });
eq(pin.ctx.resolveLang(pin.ctx.readLangPref()), 'zh-CN', 'pinned zh-CN wins over console ko');
const pinEn = env({ parentLang: 'zh-CN', store: { cpa_plugin_language: 'en' } });
eq(pinEn.ctx.resolveLang(pinEn.ctx.readLangPref()), 'en', 'pinned en wins over console zh-CN');
// Invalid stored value → auto; old keys ignored.
eq(env({ store: { cpa_plugin_language: 'fr' } }).ctx.readLangPref(), 'auto', 'invalid -> auto');
eq(env({ store: { cpa_plugin_language: 'zh' } }).ctx.readLangPref(), 'auto', 'zh (not zh-CN) -> auto');
eq(env({}).ctx.readLangPref(), 'auto', 'default auto');
const old = env({ parentLang: 'ko', store: { cpa_plugin_lang: 'en', cpa_plugin_lang_host: 'ko' } });
eq(old.ctx.readLangPref(), 'auto', 'old keys ignored'); eq(old.ctx.resolveLang(old.ctx.readLangPref()), 'ko', 'old keys ignored (resolved)');
// Writes: only cpa_plugin_language, sanitised; never cli-proxy-language.
const w = env({ store: { 'cli-proxy-language': zustand('ko') } });
['en', 'zh-CN', 'auto', 'xx'].forEach(v => w.ctx.writeLangPref(v));
eq(w.store.cpa_plugin_language, 'auto', 'invalid write stored as auto');
eq(w.writes.join(), 'cpa_plugin_language,cpa_plugin_language,cpa_plugin_language,cpa_plugin_language', 'only the shared key written');
eq(w.store['cli-proxy-language'], zustand('ko'), 'console entry untouched');
if (/cpa_plugin_lang['_]|removeItem/.test(core.replace(/\/\/.*$/gm, ''))) fail.push('core code names an old key or removes entries');
if (fail.length) { console.error(fail.join('\n')); process.exit(1); }
console.log('OK_LANG_CORE');
`
	out, err := nodeDashboardCmdRaw(t, "node", script).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_LANG_CORE") {
		t.Fatalf("language core contract failed: %v\n%s", err, out)
	}
}

// The menu glue: both language buttons (not the refresh icon that shares .lang-toggle)
// open the shared menu; the old toggle code is gone.
func TestLanguageMenuWiring(t *testing.T) {
	page := dashboardPage(t)
	for _, want := range []string{
		"function initLangMenu(buttons, opts)", "function watchLanguage(getLang, onChange)",
		"querySelectorAll('.lang-toggle:not(.refresh-btn)')", "role', 'menuitemradio'", "aria-haspopup', 'menu'",
		`id="languageToggle"`, `id="authLanguageToggle"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("language menu wiring missing %q", want)
		}
	}
	for _, gone := range []string{"lang.toEn", "lang.toKo", "'lang.label'", "followHostLang", "writeLangPrefs", "detectLang", "data-current-lang"} {
		if strings.Contains(page, gone) {
			t.Errorf("old language toggle code %q still present", gone)
		}
	}
}
