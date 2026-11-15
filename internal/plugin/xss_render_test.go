package plugin

import (
	"os/exec"
	"strings"
	"testing"
)

// Dynamic account fields (label, email, name, provider, plan, endpoint, window
// names) and server error/reason text must never become markup. Every string
// handed to innerHTML is parsed for elements: an injected <img onerror> must
// appear only as escaped text.
func TestDashboardRendersHostileAccountFieldsAsText(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := `
const vm = require('vm');
const html = process.argv[1];
const src = html.match(/<script>([\s\S]*?)<\/script>/)[1];
const X = '<img src=x onerror=alert(1)>';
const markup = [];
function node(tag) {
  const n = {
    tagName: tag, id: '', children: [], style: {}, attrs: {}, className: '', _text: '', _html: '',
    appendChild(c) { if (c && c._frag) { this.children.push(...c.children); c.children = []; } else this.children.push(c); return c; },
    replaceChildren(...c) { this.children = []; c.forEach(x => this.appendChild(x)); },
    setAttribute(k, v) { this.attrs[k] = String(v); }, getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; },
    removeAttribute(k) { delete this.attrs[k]; }, hasAttribute(k) { return k in this.attrs; },
    addEventListener() {}, querySelector() { return null; }, querySelectorAll() { return []; },
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    get firstChild() { return null; },
  };
  Object.defineProperty(n, 'textContent', { get() { return this._text; }, set(v) { this._text = String(v); this.children = []; } });
  Object.defineProperty(n, 'title', { get() { return this.attrs.title || ''; }, set(v) { this.attrs.title = String(v); } });
  Object.defineProperty(n, 'innerHTML', {
    get() { return String(this._text).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); },
    set(v) { markup.push(String(v)); this._html = String(v); this.children = []; },
  });
  return n;
}
const byId = {};
const doc = {
  hidden: false, activeElement: null, documentElement: node('html'), title: '',
  getElementById(id) { return byId[id] || (byId[id] = Object.assign(node('div'), { id })); },
  createElement: node, createDocumentFragment() { const f = node('#f'); f._frag = true; return f; },
  querySelectorAll() { return []; }, querySelector() { return null; }, addEventListener() {},
};
let respond;
const ctx = {
  console, Math, Number, Array, Object, String, JSON, Date, Promise, isNaN, isFinite, encodeURIComponent,
  document: doc, navigator: { language: 'en-US', languages: ['en-US'] },
  localStorage: { getItem() { return null; }, setItem() {} }, sessionStorage: { getItem() { return null; }, setItem() {}, removeItem() {} },
  window: { addEventListener() {}, matchMedia() { return { matches: false, addEventListener() {} }; } },
  setTimeout() { return 0; }, clearTimeout() {}, setInterval() { return 0; }, clearInterval() {},
  AbortController: function() { this.signal = {}; this.abort = () => {}; },
  fetch() { return new Promise(r => { respond = r; }); },
};
ctx.window.document = doc;
vm.createContext(ctx);
vm.runInContext(src, ctx);
const tick = () => new Promise(r => setImmediate(r));
const acc = (provider, extra) => Object.assign({
  account_key: 'k-' + provider + X, provider, account: X + '@example.invalid', alias: X, email: X, name: X, label: X,
  plan_type: X, endpoint: X, status: 'stale', stale: true, error: X, error_kind: X, reason: X,
  observed_at: new Date().toISOString(), operational: { status: X, reason: X },
  windows: [{ name: X, remaining_percent: 40, reset_at: new Date(Date.now() + 3600e3).toISOString() }],
  reset_credits: { available_count: 2, nearest_expiry: X }, api_credits: { balance: X },
}, extra || {});

(async () => {
  ctx.mgmtKey = 'k'; ctx.keyVerified = true;
  const accounts = [acc('codex'), acc('claude'), acc('antigravity'), acc(X)];
  ctx.renderAccounts(accounts);
  ctx.renderFleetAvailability([{ key: X, label: X, remaining_percent: 50, accounts: 1 }, { key: 'antigravity_gemini', label: X, remaining_percent: 10, accounts: 1 }]);
  // A failed snapshot with hostile reason text, then a payload error, then an unconfigured reason.
  ctx.cachedAccounts = accounts;
  ctx.loadDashboard(false);
  respond({ ok: true, status: 200, json: async () => ({ configured: true, snapshot_complete: true, reason: X, accounts, health: { last_category: X, failures: 1 } }) });
  await tick(); await tick();
  ctx.loadDashboard(false);
  respond({ ok: true, status: 200, json: async () => ({ configured: true, error: X, code: X }) });
  await tick(); await tick();
  ctx.cachedAccounts = [];
  ctx.loadDashboard(false);
  respond({ ok: true, status: 200, json: async () => ({ configured: true, error: X }) });
  await tick(); await tick(); await tick();
  const err = doc.getElementById('errorBox').children[0];
  const errText = err ? err.textContent : '(none)';
  ctx.loadDashboard(false);
  respond({ ok: true, status: 200, json: async () => ({ configured: false, reason: X }) });
  await tick(); await tick();

  // Minimal HTML tokenizer: element names and attribute names, honouring quoted values.
  function parse(m) {
    const els = [], attrs = [];
    const tag = /<([a-zA-Z][\w-]*)((?:\s+[^\s=>\/]+(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+))?)*)\s*\/?>/g;
    const attr = /([^\s=>\/]+)(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+))?/g;
    let t;
    while ((t = tag.exec(m))) { els.push(t[1].toLowerCase()); let a; while ((a = attr.exec(t[2]))) attrs.push(a[1].toLowerCase()); }
    return { els, attrs };
  }
  const fail = [];
  if (!markup.length) fail.push('no markup was rendered');
  for (const m of markup) {
    const { els, attrs } = parse(m);
    if (els.includes('img') || els.includes('script')) fail.push('element injected: ' + m.slice(0, 160));
    if (attrs.some(a => a.startsWith('on'))) fail.push('event handler attribute injected: ' + m.slice(0, 160));
  }
  // Text sinks must carry the payload verbatim (shown, not dropped or parsed).
  const stale = doc.getElementById('staleNoticeText').textContent, unconf = doc.getElementById('unconfiguredText').textContent;
  if (!stale.includes(X)) fail.push('stale notice should show the raw reason as text: ' + stale);
  if (!unconf.includes(X)) fail.push('unconfigured notice should show the raw reason as text: ' + unconf);
  if (!errText.includes(X)) fail.push('error box should show the raw error as text: ' + errText);
  if (!markup.some(m => m.includes('&lt;img src=x onerror=alert(1)&gt;'))) fail.push('payload never reached markup escaped');
  if (fail.length) { console.error(fail.join('\n')); process.exit(1); }
  console.log('OK_XSS_RENDER');
})().catch(e => { console.error(e); process.exit(1); });
`
	out, err := nodeDashboardCmdRaw(t, "node", script).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_XSS_RENDER") {
		t.Fatalf("hostile account fields rendered as markup: %v\n%s", err, out)
	}
}
