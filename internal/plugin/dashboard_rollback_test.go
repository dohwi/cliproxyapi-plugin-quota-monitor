package plugin

import (
	"strings"
	"testing"
)

func TestDashboardRefreshRejectsOldInFlightSnapshot(t *testing.T) {
	script := `
const vm=require('vm'),assert=(x,m)=>{if(!x)throw Error(m)};
const js=process.argv[1].match(/<script>([\s\S]*?)<\/script>/)[1];
const nodes=new Map(), timers=new Map();let tid=0;
const doc={hidden:false,addEventListener(){},getElementById(id){if(!nodes.has(id))nodes.set(id,{style:{},textContent:'',value:'all',setAttribute(){},getAttribute(){return null},classList:{add(){},remove(){}}});return nodes.get(id)}};
let resolver, rejecter, fetched=0;
const ctx={console,document:doc,AbortController,Date,setTimeout(fn,ms){const n=++tid;timers.set(n,{fn,ms});return n},clearTimeout(n){timers.delete(n)},fetch(url){fetched++;if(url.includes('/data/refresh'))return Promise.resolve({ok:true,status:200,json:async()=>({refreshed:true})});return new Promise((resolve,reject)=>{resolver=resolve;rejecter=reject})}};
vm.createContext(ctx);vm.runInContext(js,ctx);ctx.mgmtKey="test-key";ctx.keyVerified=true;
ctx.clearError=()=>{};ctx.populateProviderSegmented=()=>{};ctx.applyFilters=()=>{};ctx.captureRenderState=()=>({});ctx.restoreRenderState=()=>{};
const snapshot=(n,time='2026-10-01T12:00:00Z',gen=3)=>({configured:true,snapshot_complete:true,generation:gen,collected_at:time,health:{collections:n,last_category:'success',failures:0},accounts:[{account_key:'fixture',windows:[{name:'primary',remaining_percent:n}]}]});
assert(ctx.acceptSnapshotVersion(snapshot(20)),'initial');assert(!ctx.acceptSnapshotVersion(snapshot(19)),'older counter must reject');assert(!ctx.acceptSnapshotVersion(snapshot(21,'2026-10-01T11:00:00Z')),'older observation must reject');
assert(ctx.acceptSnapshotVersion(snapshot(1,'2026-10-01T13:00:00Z',1)),'server restart with newer observation accepted');assert(!ctx.acceptSnapshotVersion(snapshot(30,'2026-10-01T12:00:00Z',3)),'older process response cannot rollback newer observation');
assert(ctx.acceptSnapshotVersion({accounts:[]}),'metadata optional');assert(!ctx.acceptSnapshotVersion(snapshot(0,'2026-10-01T12:00:00Z',0)),'missing metadata cannot reset ordering watermark');
ctx.acceptedSnapshotVersion=null;ctx.cachedAccounts=[{account_key:'latest',windows:[]}];
ctx.loadDashboard(false);const old=resolver;
await ctx.refreshAllAccounts();assert(ctx.snapshotEpoch===1&&ctx.manualRefreshSnapshotPending,'manual completion invalidates pending GET');
old({ok:true,status:200,json:async()=>snapshot(1)});await new Promise(setImmediate);
assert(ctx.cachedAccounts[0].account_key==='latest'&&!ctx.isFetching,'old in-flight GET discarded and fetching released');assert(ctx.manualRefreshSnapshotPending,'discarded snapshot cannot consume pending refresh');
ctx.loadDashboard(false);resolver({ok:true,status:200,json:async()=>snapshot(20)});await new Promise(setImmediate);
assert(ctx.cachedAccounts[0].windows[0].remaining_percent===20,'post-refresh snapshot accepted');
ctx.loadDashboard(false);resolver({ok:true,status:200,json:async()=>snapshot(19)});await new Promise(setImmediate);
assert(ctx.cachedAccounts[0].windows[0].remaining_percent===20,'later older snapshot cannot overwrite latest');
ctx.loadDashboard(false);ctx.snapshotEpoch++;rejecter(new Error('late failure'));await new Promise(setImmediate);
assert(!ctx.isDisplayingStale&&ctx.cachedAccounts[0].windows[0].remaining_percent===20,'late failure cannot mark newer data stale');
console.log('REFRESH_ROLLBACK_GUARD_PASS');
`
	cmd := nodeDashboardCmd(t, "node", "(async()=>{"+script+"})().catch(e=>{console.error(e);process.exitCode=1})")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "REFRESH_ROLLBACK_GUARD_PASS") {
		t.Fatalf("snapshot rollback regression: %v\n%s", err, out)
	}
}
