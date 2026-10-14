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
vm.createContext(ctx); vm.runInContext(script,ctx); ctx.mgmtKey="test-key"; ctx.keyVerified=true;
const el = id => doc.getElementById(id), btn = el('fullRefreshBtn');
let snapshots = 0, filterRenders = 0;
const realLoad = ctx.loadDashboard;
ctx.loadDashboard = () => { snapshots++; };
ctx.initLoadingState = () => {}; ctx.startResetCountdownTick = () => {};
ctx.applyFilters = () => { filterRenders++; };
ctx.selectedProvider = 'Codex'; ctx.selectedEndpoint = 'warp1'; el('statusFilter').value = 'limited';
const cached = [{account_key:'retained',provider:'codex', windows:[]}]; ctx.cachedAccounts = cached;
documentEvents.DOMContentLoaded(); ctx.mgmtKey = 'test-key'; ctx.keyVerified = true;
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
assert(first.url === '/v0/management/plugins/quota-monitor/data/refresh?confirm=1' && first.options.method === 'POST', 'authenticated management refresh');
assert(first.options.headers && first.options.headers.Authorization === 'Bearer test-key', 'Bearer header carries the management key');
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
  async()=>({ok:false,status:502,json:async()=>({error:failSecret})})
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
assert(last.url === '/v0/management/plugins/quota-monitor/data/accounts' && !last.options.method && last.options.headers.Authorization === 'Bearer test-key', 'automatic GET reads the authenticated snapshot');
assert(calls.filter(c => c.url.includes('/data/refresh')).length === postCount, 'automatic polling never adds force request');
for (const forbidden of ['manualRefreshKey','manualRefreshDialog','manualRefreshPassword']) assert(!html.includes(forbidden), 'no per-refresh credential dialog remains');
assert(html.indexOf('id="resetFiltersBtn"') < html.indexOf('id="fullRefreshBtn"'), 'full refresh placed beside reset');
for (const forbidden of ['window.parent','postMessage','localStorage.setItem(KEY']) assert(!script.includes(forbidden), 'no implicit host bridge or persistent key storage');
assert((script.match(/localStorage/g)||[]).length === (script.match(/localStorage\.(?:getItem\(LANG_KEY\)|setItem\(LANG_KEY, lang\))/g)||[]).length && script.includes("var LANG_KEY = 'cpa_plugin_lang';"), 'localStorage is limited to the language preference key');
console.log('OK_MANUAL_REFRESH');
`
	// Top-level await is wrapped to keep compatibility with Node CommonJS -e.
	cmd := nodeDashboardCmd(t, "node", "(async () => {"+nodeScript+"})().catch(err => { console.error(err); process.exitCode = 1; });")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_MANUAL_REFRESH") {
		t.Fatalf("manual refresh contract: %v\n%s", err, out)
	}
}

// A 401/403 from the host means the management key is wrong or revoked: the dashboard
// must drop it, stop polling and ask again instead of retrying (the host bans an IP after
// five rejected attempts), and must never treat it as a generic refresh failure.
func TestDashboardAuthRejectionDropsKeyAndStopsPolling(t *testing.T) {
	script := `
const vm=require('vm'),assert=(x,m)=>{if(!x)throw Error(m)};
const js=process.argv[1].match(/<script>([\s\S]*?)<\/script>/)[1];
const nodes=new Map(), timers=new Map(), store={};let tid=0, calls=0;
const mk=()=>({style:{},textContent:'',value:'',setAttribute(){},getAttribute(){return null},classList:{add(){},remove(){}},focus(){},appendChild(){},addEventListener(){}});
const doc={hidden:false,addEventListener(){},getElementById(id){if(!nodes.has(id))nodes.set(id,mk());return nodes.get(id)},createElement:mk,createDocumentFragment:mk};
const ctx={console,document:doc,AbortController,Date,
  sessionStorage:{getItem:k=>k in store?store[k]:null,setItem:(k,v)=>{store[k]=v},removeItem:k=>{delete store[k]}},
  setTimeout(fn,ms){const n=++tid;timers.set(n,{fn,ms});return n},clearTimeout(n){timers.delete(n)},
  fetch(){calls++;return Promise.resolve({ok:false,status:401,json:async()=>({error:'invalid management key'})})}};
vm.createContext(ctx);vm.runInContext(js,ctx);
ctx.mgmtKey='stale-key';ctx.keyVerified=true;store.cpa_quota_monitor_mgmt_key='stale-key';
ctx.loadDashboard(true);await new Promise(setImmediate);
assert(calls===1,'exactly one rejected request');
assert(ctx.mgmtKey===''&&!store.cpa_quota_monitor_mgmt_key,'rejected key dropped from memory and storage');
assert(doc.getElementById('authGate').style.display==='block'&&doc.getElementById('dashboardContent').style.display==='none','auth gate shown');
assert(timers.size===0,'no poll scheduled after a rejection');
ctx.loadDashboard(false);ctx.loadDashboard(true);await new Promise(setImmediate);
assert(calls===1,'without a key no further request is sent');
ctx.mgmtKey='another';ctx.keyVerified=false;
ctx.fetch=()=>{calls++;return Promise.resolve({ok:false,status:403,json:async()=>({})})};
ctx.loadDashboard(true);await new Promise(setImmediate);
assert(calls===2&&ctx.mgmtKey===''&&!store.cpa_quota_monitor_mgmt_key,'403 handled like 401, key never stored');
console.log('AUTH_REJECT_PASS');
`
	cmd := nodeDashboardCmd(t, "node", "(async()=>{"+script+"})().catch(e=>{console.error(e);process.exitCode=1})")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "AUTH_REJECT_PASS") {
		t.Fatalf("auth rejection contract: %v\n%s", err, out)
	}
}
