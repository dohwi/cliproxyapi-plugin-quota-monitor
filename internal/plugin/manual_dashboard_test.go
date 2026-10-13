package plugin

import (
	"quota-monitor/internal/dashboard"
	"strings"
	"testing"
)

func TestDashboardManualRefreshNodeContract(t *testing.T) {
	for _, token := range []string{
		`id="fullRefreshBtn"`,
		`id="manualRefreshStatus" class="manual-refresh-status" role="status" aria-live="polite"`,
		`.filter-actions { display: flex; flex-wrap: wrap;`,
		`.filter-actions .filter-reset-button { flex: 0 1 auto; width: auto;`,
	} {
		if !strings.Contains(dashboard.HTML, token) {
			t.Errorf("missing manual refresh accessibility/responsiveness token %q", token)
		}
	}
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
function assert(cond, message) { if (!cond) throw new Error(message); }
let clock = 1800000000000, timerID = 0;
const timers = new Map(), nodes = new Map(), documentEvents = {};
class FakeDate extends Date { static now() { return clock; } }
function node() {
  return { value:'', textContent:'', style:{}, attrs:{}, events:{}, disabled:false, open:false,
    setAttribute(k,v) { this.attrs[k] = v; }, getAttribute(k) { return this.attrs[k] || null; },
    addEventListener(k,fn) { this.events[k] = fn; }, focus() {},
    showModal() { this.open = true; }, close() { this.open = false; if (this.events.close) this.events.close(); },
    appendChild() {}, classList:{ add(){}, remove(){} }
  };
}
const doc = { hidden:false, addEventListener(k,fn) { documentEvents[k] = fn; },
  getElementById(id) { if (!nodes.has(id)) nodes.set(id, node()); return nodes.get(id); },
  createElement() { return node(); }, createDocumentFragment() { return node(); }
};
let calls = [], fetchImpl = async () => ({ok:true, status:200, json:async () => ({refreshed:true})});
const ctx = { console, document:doc, Date:FakeDate, AbortController,
  setTimeout(fn,ms) { const id = ++timerID; timers.set(id,{fn,ms}); return id; },
  clearTimeout(id) { timers.delete(id); }, fetch(url,options) { calls.push({url,options}); return fetchImpl(url,options); }
};
vm.createContext(ctx); vm.runInContext(script,ctx);
const el = id => doc.getElementById(id), btn = el('fullRefreshBtn');
let snapshots = 0, filterRenders = 0;
const realLoad = ctx.loadDashboard;
ctx.loadDashboard = () => { snapshots++; };
ctx.initLoadingState = () => {}; ctx.startResetCountdownTick = () => {};
ctx.applyFilters = () => { filterRenders++; };
ctx.selectedProvider = 'Codex'; ctx.selectedEndpoint = 'warp1'; el('statusFilter').value = 'limited';
const cached = [{account_key:'retained',provider:'codex', windows:[]}]; ctx.cachedAccounts = cached;
documentEvents.DOMContentLoaded();
assert(calls.length === 0, 'init must not POST');
documentEvents.visibilitychange();
ctx.scheduleNextPoll(1000); timers.get(ctx.pollTimer).fn();
assert(calls.length === 0, 'visibility and normal polling must not POST');
let resolve;
fetchImpl = () => new Promise(r => { resolve = r; });
el('manualRefreshStatus').textContent = 'previous notice';
const firstRefreshPromise = btn.onclick();
assert(el('manualRefreshStatus').textContent === '', 'busy state uses button only and clears previous notices');
assert(calls.length === 1 && btn.disabled && btn.textContent === '갱신 중…' && btn.attrs['aria-busy'] === 'true', 'busy action and accessibility');
const first = calls[0];
assert(first.url === '/v0/resource/plugins/quota-monitor/data/refresh?confirm=1' && first.options.method === 'GET', 'keyless refresh resource');
assert(!first.options.headers, 'no authorization header');
assert(first.options.credentials === 'omit' && first.options.redirect === 'error' && first.options.cache === 'no-store', 'no cookies, redirects, or caching');
assert(!first.options.body && first.url.endsWith('?confirm=1'), 'no filter/account parameters');
assert(Array.from(timers.values()).some(t => t.ms === 60000), 'POST uses bounded 60s timeout');
await ctx.refreshAllAccounts();
assert(calls.length === 1, 'busy action cannot duplicate POST');
ctx.scheduleNextPoll(900000);
assert(timers.get(ctx.pollTimer).ms === 2000, 'snapshot polling stays enabled and fast during POST');
resolve({ok:true,status:200,json:async()=>({refreshed:true,count:2})});
await firstRefreshPromise;
assert(!ctx.isManualRefreshing && btn.disabled && btn.textContent.includes('30s'), 'success starts 30-second cooldown');
assert(snapshots >= 3 && el('manualRefreshStatus').textContent === '갱신 완료', 'completion reloads snapshot with compact notice');
const successNoticeTimer = ctx.manualRefreshStatusTimer;
assert(timers.get(successNoticeTimer).ms === 3000, 'success disappears after three seconds');
timers.get(successNoticeTimer).fn(); timers.delete(successNoticeTimer);
assert(el('manualRefreshStatus').textContent === '' && ctx.manualRefreshStatusTimer === null, 'success notice clears');
ctx.setManualRefreshStatus('갱신 완료', 3000);
const cancelledTimer = ctx.manualRefreshStatusTimer;
ctx.setManualRefreshStatus('error notice');
assert(!timers.has(cancelledTimer) && ctx.manualRefreshStatusTimer === null && el('manualRefreshStatus').textContent === 'error notice', 'new error cancels success timer and persists');
assert(ctx.cachedAccounts === cached && ctx.selectedProvider === 'Codex' && ctx.selectedEndpoint === 'warp1' && el('statusFilter').value === 'limited', 'refresh does not clear cards or filters');
await ctx.refreshAllAccounts(); assert(calls.length === 1, 'cooldown prevents repeated POST');
clock += 31000; ctx.updateManualRefreshButton(); assert(!btn.disabled, 'absolute cooldown expires despite throttled timers');
const failSecret = 'unsafe-upstream-secret';
fetchImpl = async () => ({ok:false,status:429,json:async()=>({error:failSecret})});
ctx.isFetching = true;
await ctx.refreshAllAccounts();
assert(btn.disabled && !ctx.isManualRefreshing, 'server cooldown enforced in UI');
assert(el('manualRefreshStatus').textContent.includes('30초') && !el('manualRefreshStatus').textContent.includes(failSecret), 'cooldown message generic');
assert(timers.get(ctx.pollTimer).ms === 2000, 'existing snapshot fetch is not blocked, follow-up scheduled');
await ctx.refreshAllAccounts(); assert(calls.length === 2, '429 cooldown prevents another request');
clock += 31000; ctx.updateManualRefreshButton(); ctx.isFetching = false;
for (const failure of [
  async()=>{throw new Error(failSecret)},
  async()=>({ok:false,status:500,json:async()=>({error:failSecret})}),
  async()=>({ok:true,status:200,json:async()=>{throw new Error(failSecret)}}),
  async()=>({ok:true,status:200,json:async()=>({refreshed:false,error:failSecret})}),
  async()=>({ok:false,status:403,json:async()=>({error:failSecret})})
]) {
  fetchImpl = failure;
  await ctx.refreshAllAccounts();
  assert(!ctx.isManualRefreshing && !btn.disabled, 'failure restores retry button');
  assert(!el('manualRefreshStatus').textContent.includes(failSecret) && ctx.cachedAccounts === cached, 'failure sanitized and cached cards retained');
}
fetchImpl = (url,options) => new Promise((r,reject)=> options.signal.addEventListener('abort',()=>reject(new Error(failSecret))));
const timeoutPromise = ctx.refreshAllAccounts();
const timeout = Array.from(timers.values()).find(t => t.ms === 60000); timeout.fn();
await timeoutPromise;
assert(!ctx.isManualRefreshing && !btn.disabled && el('manualRefreshStatus').textContent.includes('아직 수집 중'), 'timeout recovers and explains server may still work');
fetchImpl = async()=>({ok:true,status:200,json:async()=>({refreshed:true,truncated:true,stale:true})});
await ctx.refreshAllAccounts();
assert(el('manualRefreshStatus').textContent.includes('일부') && el('manualRefreshStatus').textContent.includes('이전 데이터'), 'partial/stale is not claimed fully refreshed');
assert(ctx.manualRefreshStatusTimer === null, 'partial/stale warning stays visible');
clock += 31000; ctx.updateManualRefreshButton();
// Exercise the real automatic read path; it remains GET-only.
const postCount = calls.filter(c => c.url.includes('/data/refresh')).length;
fetchImpl = async()=>({ok:true,status:200,json:async()=>({configured:true,snapshot_complete:true,accounts:cached})});
ctx.populateProviderSegmented = () => {}; ctx.renderAccounts = () => {}; ctx.clearError = () => {};
realLoad(false); await new Promise(setImmediate);
const last = calls[calls.length-1];
assert(last.url === '/v0/resource/plugins/quota-monitor/data/accounts' && !last.options.headers && !last.options.method, 'automatic GET never carries manual credential');
assert(calls.filter(c => c.url.includes('/data/refresh')).length === postCount, 'automatic polling never adds force request');
for (const forbidden of ['manualRefreshKey','manualRefreshDialog','manualRefreshPassword','Authorization','Bearer ','type="password"']) assert(!html.includes(forbidden), 'no credential requirement remains');
assert(html.indexOf('id="resetFiltersBtn"') < html.indexOf('id="fullRefreshBtn"'), 'full refresh placed beside reset');
for (const forbidden of ['localStorage','sessionStorage','window.parent','postMessage']) assert(!script.includes(forbidden), 'no credential storage or implicit host bridge');
console.log('OK_MANUAL_REFRESH');
`
	// Top-level await is wrapped to keep compatibility with Node CommonJS -e.
	cmd := nodeDashboardCmd(t, "node", "(async () => {"+nodeScript+"})().catch(err => { console.error(err); process.exitCode = 1; });")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_MANUAL_REFRESH") {
		t.Fatalf("manual refresh contract: %v\n%s", err, out)
	}
}
