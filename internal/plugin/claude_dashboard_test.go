package plugin

import (
	"testing"
)

func TestClaudeDashboardContracts(t *testing.T) {
	script := `
const vm = require('vm');
const html = process.argv[1];
function node() {
 return {style:{}, attrs:{}, children:[], className:'', _text:'', _html:'',
  classList:{add(){},remove(){}}, setAttribute(k,v){this.attrs[k]=v;},
  appendChild(c){this.children.push(c);return c;}, querySelector(){return null;}, querySelectorAll(){return [];},
  get textContent(){return this._text;},set textContent(v){this._text=String(v);},
  get innerHTML(){return this._html || this._text.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');},
  set innerHTML(v){this._html=String(v);this.children=[];}
 };
}
const ids = {};
const ctx = { console, Math, Number, Array, Object, String, Date, isNaN, isFinite,
 document:{addEventListener(){},getElementById(id){return ids[id] || (ids[id]=node());},createElement:node},
 localStorage:{getItem(){return null;},setItem(){}},setTimeout(){return 0;},clearTimeout(){},setInterval(){return 0;},clearInterval(){} };
vm.createContext(ctx);vm.runInContext(html.match(/<script>([\s\S]*?)<\/script>/)[1],ctx);
function assert(ok,msg){if(!ok)throw new Error(msg);}
assert(ctx.getProviderKey(' CLAUDE ')==='claude','official provider key');
assert(ctx.formatProviderLabel('Claude')==='claude','official provider label matches lowercase provider style');
for (const provider of ['codex','claude','antigravity','unknown']) {
 const credits=ctx.renderCreditSummary({provider,reset_credits:{available_count:2,nearest_expiry:'2026-11-01T00:00:00Z'}});
 assert(credits.includes('리셋권 2장 보유') && credits.includes('가장 빠른 만료일'),'reset rights render for every provider '+provider);
 assert(ctx.renderCreditSummary({provider})==='','missing reset rights are not invented '+provider);
 assert(ctx.renderCreditSummary({provider,reset_credits:{available_count:0}})==='','zero rights retain compact display '+provider);
}
const official=ctx.providerIcon('claude',14), ag=ctx.agFamilyIcon('claude');
assert(official.includes('<title>Claude</title>') && official.includes('width="14"'),'official icon title and size');
assert(ag.includes('<title>Claude</title>'),'existing AG Claude icon title unchanged');
assert(official!==ctx.providerIcon('codex') && official!==ctx.providerIcon('unknown'),'distinct icon');
assert(['antigravity','codex','claude','unknown'].map(ctx.providerRank).join(',')==='0,1,2,3','provider order');
const names=['five-hour','weekly','weekly-opus','weekly-sonnet','weekly-oauth-apps','weekly-cowork','weekly-sessions'];
const windows=names.map((name,i)=>({name,window_minutes:i?10080:300,remaining_percent:80-i,reset_after_seconds:3600}));
const grouped=ctx.groupQuotaWindows(windows,'claude');
assert(grouped.length===1 && grouped[0].windows.length===7,'all weekly windows retained');
assert(grouped[0].windows.map(w=>w.kind).join(',')===names.join(','),'weekly kinds preserved');
const aliases=ctx.groupQuotaWindows([{name:'five_hour',window_minutes:300},{name:'seven_day',window_minutes:10080},{name:'seven-day-sonnet',window_minutes:10080}],'claude');
assert(aliases[0].windows.map(w=>w.kind).join(',')==='five-hour,weekly,weekly-sonnet','passive aliases labeled correctly');
const quota=ctx.renderQuotaGroups(windows,'claude',{key:'active'});
['Opus','Sonnet','OAuth 앱','Cowork','Sessions'].forEach(label=>assert(quota.includes(label+' · 주간 제한'),'weekly label '+label));
assert(!quota.includes('data-kind="unknown"'),'no unknown model windows');
const repeated=ctx.groupQuotaWindows(windows.concat(windows[2]),'claude');
assert(repeated[0].windows.length===8,'no deduplication of official windows');
const upstream=[{name:'Claude and GPT models / Five Hour Limit Remaining',remaining_percent:40},{name:'Claude and GPT models / Weekly Limit Remaining',remaining_percent:60}];
const agHTML=ctx.renderAGFamilySection('claude',upstream);
assert(agHTML.includes('title="Claude"') && agHTML.includes('aria-label="Claude 쿼터"'),'existing AG tooltip and aria unchanged');
const fallback=ctx.renderQuotaPercentages(ctx.groupQuotaWindows(upstream,'antigravity')[0],'antigravity',{key:'active'});
assert(fallback.includes('Claude · 5시간 제한'),'existing AG fallback percentages unchanged');
const withOverage={provider:'claude',status:'known',operational:{status:'active'},windows:[{name:'5h',window_minutes:300,remaining_percent:95},{name:'7d',window_minutes:10080,remaining_percent:99},{name:'overage',remaining_percent:0}]};
assert(ctx.displayedQuotaWindows(withOverage).length===2,'disabled billing overage is not a subscription window');
assert(ctx.getAvailabilityState(withOverage).key==='active','disabled overage must not imply exhaustion');
function flatten(n){return n._html+' '+n.className+' '+JSON.stringify(n.attrs)+' '+n.children.map(flatten).join(' ');}
ctx.renderAccounts([withOverage]);
const overageCard=flatten(ids.accountsList);
assert(!overageCard.includes('data-window-name="overage"'),'billing overage not rendered on official card');
const recentClaude={...withOverage,account:'recent official',account_key:'claude-recent',current_account:true,current_kind:'recent',current_reason:'recent_usage',selected_by:'recent_usage'};
ctx.cachedAccounts=[recentClaude];ctx.renderAccounts([recentClaude]);
const recentCard=ids.accountsList.children.find(n=>n.attrs['data-account-key']==='claude-recent');
assert(recentCard && recentCard.attrs['data-current']==='true','recent Claude receives the same blue-border attribute as other providers');
assert(recentCard.attrs['data-availability']==='active','billing overage does not change recent Claude availability');
assert(recentCard.attrs['aria-label'].includes('최근 15분 내 토큰이 보고된 완료 요청'),'Claude border meaning remains accessible');
ctx.renderAccounts([{...recentClaude,current_kind:'candidate',current_reason:'priority'}]);
assert(ids.accountsList.children[0].attrs['data-current']===undefined,'non-audited Claude candidates cannot acquire a border');
for(const index of [0,1,2]){
 const lockedWindows=windows.map(w=>({...w}));lockedWindows[index].remaining_percent=0;
 const account={provider:'claude',account:'official',status:'known',operational:{status:'active'},windows:lockedWindows};
 const availability=ctx.getAvailabilityState(account);
 assert(availability.key===(index===0?'limited':'unavailable'),'Claude lock availability '+index);
 const lockHTML=ctx.renderQuotaLock(account,availability);
 assert(lockHTML.includes('quota-lock-state'),'Claude lock component '+index);
 if(index===2)assert(lockHTML.includes('Opus · 주간 제한'),'model-specific lock title');
 ctx.cachedAccounts=[account];ctx.renderAccounts([account]);
 const card=Object.values(ids).flatMap(n=>n.children).find(n=>n.attrs['data-provider']==='claude');
 assert(card && card.attrs['data-locked']==='true','Claude locked card '+index);
 const rendered=flatten(card);
 assert(rendered.includes('badge-provider-claude') && rendered.includes('quota-lock-state'),'Claude badge and lock rendered');
 ['weekly-opus','weekly-sonnet','weekly-oauth-apps','weekly-cowork','weekly-sessions'].forEach(name=>assert(rendered.includes('data-window-name="'+name+'"'),'locked weekly retained '+name));
}
console.log('OK');
`
	cmd := nodeDashboardCmd(t, "node", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Claude dashboard node contracts failed: %v\n%s", err, out)
	}
}
