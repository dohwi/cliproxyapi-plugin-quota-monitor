package plugin

import (
	"os/exec"
	"strings"
	"testing"
)

// The dashboard follows the console language while the shared preference is 'auto' (the
// default): the console's <html lang> (same-origin parent) or its persisted cli-proxy-language
// entry, live. A pinned preference (en/ko/zh-CN) wins until "Follow CLIProxyAPI" is chosen again.
// Nothing is written by following, the console's own entry is never written, and the old
// cpa_plugin_lang / cpa_plugin_lang_host keys are ignored.
func TestDashboardFollowsHostLanguage(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := `
const vm = require('vm');
const html = process.argv[1];
const src = html.match(/<script>([\s\S]*?)<\/script>/)[1];
function el() {
  return { attrs: {}, style: {}, children: [], textContent: '', innerHTML: '',
    setAttribute(k, v) { this.attrs[k] = String(v); }, getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; },
    removeAttribute(k) { delete this.attrs[k]; }, hasAttribute(k) { return k in this.attrs; },
    appendChild(c) { return c; }, addEventListener() {}, classList: { add() {}, remove() {}, toggle() {} } };
}
// opts: { store, parentLang (undefined = no parent frame, null = cross-origin parent), nav }
function load(opts) {
  const store = Object.assign({}, opts.store || {});
  const writes = [];
  const listeners = {};
  let observer = null;
  const parentRoot = el();
  if (typeof opts.parentLang === 'string') parentRoot.attrs.lang = opts.parentLang;
  const win = { addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); }, removeEventListener() {}, matchMedia() { return { matches: false, addEventListener() {} }; } };
  if (opts.parentLang === null) win.parent = { get document() { throw new Error('cross-origin'); } };
  else if (opts.parentLang !== undefined) win.parent = { document: { documentElement: parentRoot } };
  else win.parent = win;
  const doc = { title: '', documentElement: el(), hidden: false, getElementById() { return el(); }, addEventListener() {}, removeEventListener() {},
    querySelectorAll() { return []; }, querySelector() { return null; }, createElement: el, createDocumentFragment: el };
  const ctx = {
    console, Date, Math, JSON, Promise, Object, Array, String, Number, isNaN, isFinite, setTimeout() { return 0; }, clearTimeout() {},
    navigator: { language: opts.nav || 'en-US', languages: [opts.nav || 'en-US'] },
    localStorage: { getItem: k => (k in store ? store[k] : null), setItem: (k, v) => { writes.push(k); store[k] = String(v); }, removeItem: k => { writes.push('-' + k); delete store[k]; } },
    sessionStorage: { getItem() { return null; }, setItem() {}, removeItem() {} },
    MutationObserver: function(fn) { this.observe = (target, o) => { if (o.attributeFilter && o.attributeFilter.join() === 'lang') observer = { fn, target, o }; }; },
    document: doc, window: win, fetch() { return new Promise(() => {}); },
  };
  win.document = doc;
  win.MutationObserver = ctx.MutationObserver; // in a browser window === globalThis
  vm.createContext(ctx);
  vm.runInContext(src, ctx);
  ctx.initLanguageControls();
  return {
    ctx, store, writes, parentRoot,
    get lang() { return ctx.currentLang; },
    get observer() { return observer; },
    storageEvent(key, value) { if (value === undefined) delete store[key]; else store[key] = value; (listeners.storage || []).forEach(f => f({ key, newValue: value })); },
    setParentLang(v) { parentRoot.attrs.lang = v; observer && observer.fn(); },
  };
}
function assert(c, m) { if (!c) { console.error('FAIL: ' + m); process.exit(1); } }
const zustand = l => JSON.stringify({ state: { language: l }, version: 0 });

// Console zh-CN at load → Chinese; then ko live → Korean; zh-TW → Simplified.
let p = load({ parentLang: 'zh-CN', store: {} });
assert(p.lang === 'zh-CN', 'console zh-CN -> zh-CN, got ' + p.lang);
assert(p.ctx.document.documentElement.attrs.lang === 'zh-CN' && p.ctx.document.title === '各账号剩余额度', 'html lang/title in Chinese');
assert(p.observer && p.observer.o.attributeFilter.join() === 'lang', 'observes parent lang attribute only');
p.setParentLang('ko');
assert(p.lang === 'ko', 'console ko live -> ko');
p.setParentLang('zh-TW');
assert(p.lang === 'zh-CN', 'console zh-TW -> zh-CN');
p.setParentLang('ru');
assert(p.lang === 'en', 'console ru -> en');
assert(p.writes.length === 0, 'following the console writes nothing: ' + p.writes);
// Old keys are ignored: everyone starts on auto.
p = load({ parentLang: 'ko', store: { cpa_plugin_lang: 'en', cpa_plugin_lang_host: 'ko' } });
assert(p.lang === 'ko', 'old cpa_plugin_lang ignored, got ' + p.lang);
// A pinned preference wins over the console, also after console changes.
p = load({ parentLang: 'zh-CN', store: { cpa_plugin_language: 'en' } });
assert(p.lang === 'en', 'pinned en wins over console zh-CN');
p.setParentLang('ko');
assert(p.lang === 'en', 'pinned en survives a console change');
// Another plugin page switches the shared preference back to auto.
p.storageEvent('cpa_plugin_language', 'auto');
assert(p.lang === 'ko', 'auto via storage event follows the console again');
p.storageEvent('cpa_plugin_language', 'zh-CN');
assert(p.lang === 'zh-CN', 'pinned zh-CN via storage event');
p.storageEvent('cpa_plugin_language', 'bogus');
assert(p.lang === 'ko', 'invalid stored value -> auto');
// No parent at all: the console's stored entry (zustand JSON, {language}, bare string), else browser.
p = load({ store: { 'cli-proxy-language': zustand('zh-CN') }, nav: 'en-US' });
assert(p.lang === 'zh-CN', 'zustand JSON host entry parsed, got ' + p.lang);
p = load({ parentLang: null, store: { 'cli-proxy-language': JSON.stringify({ language: 'ko' }) } });
assert(p.lang === 'ko', '{language} JSON parsed (cross-origin parent)');
p = load({ store: { 'cli-proxy-language': 'ko' }, nav: 'en-US' });
assert(p.lang === 'ko', 'bare string host entry parsed');
p.storageEvent('cli-proxy-language', zustand('zh-HK'));
assert(p.lang === 'zh-CN', 'storage event for the console key -> follow');
p = load({ store: {}, nav: 'ko-KR' });
assert(p.lang === 'ko', 'no console -> browser language');
// Never written or removed: the console's own entry.
assert(!p.writes.some(k => /cli-proxy-language/.test(k)), 'cli-proxy-language never written');
console.log('OK_HOST_LANG');
`
	out, err := nodeDashboardCmdRaw(t, "node", script).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_HOST_LANG") {
		t.Fatalf("host language contract failed: %v\n%s", err, out)
	}
}

// Known server reason/error codes are shown translated; unknown text is shown raw.
func TestDashboardTranslatesServerCodes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := `
const vm = require('vm');
const src = process.argv[1].match(/<script>([\s\S]*?)<\/script>/)[1];
const el = () => ({ attrs: {}, style: {}, classList: { add() {}, remove() {}, toggle() {} }, setAttribute(k, v) { this.attrs[k] = v; }, getAttribute() { return null; }, removeAttribute() {}, addEventListener() {} });
const ctx = { console, JSON, Object, Array, String, Number, Math, Date, Promise, isNaN, isFinite, setTimeout() {}, clearTimeout() {},
  localStorage: { getItem: k => k === 'cpa_plugin_language' ? 'ko' : null, setItem() {} },
  document: { documentElement: el(), getElementById: el, addEventListener() {}, querySelectorAll() { return []; }, createElement: el },
  window: { addEventListener() {} }, fetch() { return new Promise(() => {}); } };
vm.createContext(ctx); vm.runInContext(src, ctx);
const fail = [];
const eq = (a, b, m) => { if (a !== b) fail.push(m + ': ' + JSON.stringify(a) + ' != ' + JSON.stringify(b)); };
eq(ctx.serverText('auth_files_failed', 'quota lookup failed'), '인증 파일 목록을 가져오지 못했습니다', 'code wins');
eq(ctx.serverText('', 'timeout'), '요청 시간 초과', 'bare category as message');
eq(ctx.serverText('management_key_missing', 'Management key not available yet: ...'), ctx.t('err.management_key_missing'), 'reason_code');
eq(ctx.serverText('', 'some new upstream text'), 'some new upstream text', 'unknown message falls back to raw text');
eq(ctx.serverText('no_such_code', 'Raw'), 'Raw', 'unknown code falls back to the message');
eq(ctx.serverText('constructor', ''), 'constructor', 'prototype names are not keys');
const reason = ctx.reasonText({ reason: 'auth_files_failed' });
eq(reason(), '인증 파일 목록을 가져오지 못했습니다', 'reason resolved lazily (ko)');
ctx.setLanguage('en');
eq(reason(), 'Could not list the auth files', 'reason re-resolved after a language switch');
ctx.setLanguage('zh-CN');
eq(reason(), '无法获取认证文件列表', 'reason re-resolved in Chinese');
ctx.setLanguage('en');
eq(ctx.fetchErrorText({ name: 'AbortError' }), 'Request timed out', 'abort');
eq(ctx.fetchErrorText({ status: 502 }), 'API request failed (502)', 'http status');
eq(ctx.fetchErrorText(new TypeError('Failed to fetch')), 'Cannot reach the server', 'network error');
if (fail.length) { console.error(fail.join('\n')); process.exit(1); }
console.log('OK_CODES');
`
	out, err := nodeDashboardCmdRaw(t, "node", script).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_CODES") {
		t.Fatalf("server code translation failed: %v\n%s", err, out)
	}
}

// Every reason code the server can emit has a dashboard translation.
func TestReasonCodesHaveTranslations(t *testing.T) {
	codes := []string{
		reasonCode(missingKeyReason), reasonCode(pluginStoppedReason), reasonCode("management URL must use a loopback host"),
		"not_configured", "auth_files_failed", "auth_directory_unavailable", "usage_unavailable", "canceled", "timeout",
		"configuration_changed", "account_refresh_failed", "refresh_failed", "internal_error",
	}
	for _, c := range codes {
		if c == "" || !strings.Contains(dashboardPage(t), "'err."+c+"':") {
			t.Errorf("reason code %q has no err.%s translation", c, c)
		}
	}
	if reasonCode("cache_expired") != "cache_expired" || reasonCode("") != "" || reasonCode("Free text") != "" {
		t.Error("reasonCode passthrough/empty cases changed")
	}
}
