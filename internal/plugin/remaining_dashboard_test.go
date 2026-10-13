package plugin

import (
	"strings"
	"testing"
)

func TestRemainingOverviewAndIndividualRefreshNode(t *testing.T) {
	script := `
const vm=require('vm');const html=process.argv[1], script=html.match(/<script>([\s\S]*?)<\/script>/)[1];
const assert=(c,m)=>{if(!c)throw Error(m)};
let clock=1800000000000,id=0;const timers=new Map(),nodes=new Map();
class FakeDate extends Date{static now(){return clock}}
function node(){return {textContent:'',innerHTML:'',style:{},attrs:{},value:'',setAttribute(k,v){this.attrs[k]=v},getAttribute(k){return this.attrs[k]||null},appendChild(){},addEventListener(){},classList:{add(){},remove(){},toggle(c,on){this.last=[c,!!on]}}}}
const buttons=['a&b','sibling'].map(key=>{const n=node();n.attrs['data-refresh-account']=key;return n});
const statuses=buttons.map(b=>{const n=node();n.attrs['data-refresh-status']=b.attrs['data-refresh-account'];return n});
const document={hidden:false,getElementById(id){if(!nodes.has(id))nodes.set(id,node());return nodes.get(id)},createElement:node,createDocumentFragment:node,addEventListener(){},querySelectorAll(selector){return selector==='[data-refresh-account]'?buttons:selector==='[data-refresh-status]'?statuses:[]}};
let calls=[],respond=async()=>({ok:true,status:200,json:async()=>({refreshed:true})});
const ctx={console,document,Date:FakeDate,AbortController,setTimeout(fn,ms){const n=++id;timers.set(n,{fn,ms});return n},clearTimeout(id){timers.delete(id)},fetch(url,opts){calls.push({url,opts});return respond(url,opts)}};
vm.createContext(ctx);vm.runInContext(script,ctx);ctx.loadDashboard=()=>{};
assert(typeof ctx.renderQuotaOverview==='undefined'&&!html.includes('id="quotaOverview"'),'top overview removed');
let resolve;respond=()=>new Promise(r=>resolve=r);
const pending=ctx.refreshAccount('a&b');
assert(calls[0].url.endsWith('&account_key=a%26b'),'target encoded');
assert(buttons[0].disabled&&buttons[1].disabled&&buttons[0].title==='갱신 중…','all controls disabled while busy');
assert(document.getElementById('fullRefreshBtn').disabled,'full disabled during card');
await ctx.refreshAllAccounts();await ctx.refreshAccount('sibling');assert(calls.length===1,'mutual exclusion');
resolve({ok:true,status:200,json:async()=>({refreshed:true})});await pending;
assert(ctx.snapshotEpoch===1&&buttons[0].disabled&&!buttons[1].disabled,'only target cooldown and epoch invalidation');
assert(statuses[0].textContent===''&&buttons[0].classList.last[0]==='is-done'&&buttons[0].classList.last[1]===true,'success shows check icon, no card text');
ctx.updateAccountRefreshButtons();assert(statuses[0].textContent==='','no persistent success text');
await ctx.refreshAccount('a&b');assert(calls.length===1,'per-key cooldown');
respond=async()=>({ok:false,status:409});await ctx.refreshAccount('sibling');assert(statuses[1].textContent.includes('다른 수집'),'busy error actionable');
respond=async()=>({ok:true,status:200,json:async()=>({refreshed:false,error:'upstream-secret'})});await ctx.refreshAccount('sibling');
assert(statuses[1].textContent.includes('이전 데이터')&&!statuses[1].textContent.includes('upstream-secret')&&buttons[1].disabled,'failed probe retained/sanitized/cooldown');
clock+=31000;ctx.updateAccountRefreshButtons();assert(!buttons[0].disabled&&!buttons[1].disabled,'cooldowns expire');
respond=async()=>({ok:false,status:404});await ctx.refreshAccount('sibling');assert(statuses[1].textContent.includes('전체 갱신'),'removed key guidance');
assert(calls.every(c=>c.opts.credentials==='omit'&&c.opts.cache==='no-store'&&!c.opts.headers),'no credential leakage');
assert((html.match(/fetch\(/g)||[]).length===2,'only snapshot and explicit refresh fetch');
console.log('OK_REMAINING');
`
	cmd := nodeDashboardCmd(t, "node", "(async()=>{"+script+"})().catch(e=>{console.error(e);process.exitCode=1})")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_REMAINING") {
		t.Fatalf("remaining dashboard: %v\n%s", err, out)
	}
}
