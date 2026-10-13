package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestProgressiveSnapshotWorkerOrderAndFixedSelection(t *testing.T) {
	reset(t)
	gates := map[string]chan struct{}{"1": make(chan struct{}), "2": make(chan struct{})}
	started := make(chan string, 2)
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			_, _ = w.Write([]byte(`[
				{"provider":"codex","auth_index":"1","account":"first@example.invalid"},
				{"provider":"codex","auth_index":"2","account":"second@example.invalid"}
			]`))
		case "/management/api-call":
			var payload struct {
				AuthIndex string `json:"auth_index"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			started <- payload.AuthIndex
			select {
			case <-gates[payload.AuthIndex]:
			case <-r.Context().Done():
				return
			}
			used := 10
			if payload.AuthIndex == "2" {
				used = 80
			}
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":` + jsonNumber(used) + `}}"}`))
		default:
			_, _ = w.Write([]byte(`{"records":[]}`))
		}
	})

	oldRemaining := 50.0
	previous := []quotamonitor.AccountQuota{
		{AccountKey: "old-1", AuthIndex: "1", Provider: "codex", Account: "first@example.invalid", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &oldRemaining}}},
		{AccountKey: "old-2", AuthIndex: "2", Provider: "codex", Account: "second@example.invalid", Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &oldRemaining}}},
	}
	// Account keys must match the passive parser. Obtain them without probing.
	parsed, err := state.client.Adapter.ParseAuthFilesJSON([]byte(`[
		{"provider":"codex","auth_index":"1","account":"first@example.invalid"},
		{"provider":"codex","auth_index":"2","account":"second@example.invalid"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	for i := range previous {
		previous[i].AccountKey = parsed[i].AccountKey
	}
	state.mu.Lock()
	state.cachedAt = now().Add(-2 * cacheTTL)
	state.lastSuccessAt = state.cachedAt
	state.snapshotComplete = true
	state.order = accountOrder(previous)
	recent := now().Add(-time.Minute)
	previous[0].LastUsedAt = &recent
	state.last = cloneAccounts(previous)
	state.current = currentAccountSelection(previous)
	state.mu.Unlock()

	done := make(chan collectionResult, 1)
	go func() { done <- collectAccounts(context.Background(), false) }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("worker did not start")
		}
	}

	body := resourceSnapshot()
	if body["collection_in_progress"] != true || body["snapshot_complete"] != true {
		t.Fatalf("in-progress metadata: %#v", body)
	}
	progress := body["progress"].(map[string]any)
	if progress["completed"] != 0 || progress["total"] != 2 || body["run_id"] == "" {
		t.Fatalf("initial progress: %#v", body)
	}

	close(gates["2"])
	waitProgress(t, 1)
	mid := resourceSnapshot()
	accounts := mid["accounts"].([]safeAccount)
	if accounts[0].AccountKey != previous[0].AccountKey || accounts[1].AccountKey != previous[1].AccountKey {
		t.Fatalf("order changed mid-batch: %#v", accounts)
	}
	if !accounts[0].CurrentAccount || accounts[1].CurrentAccount {
		t.Fatalf("current changed mid-batch: %#v", accounts)
	}
	if accounts[1].Windows[0].RemainingPercent == nil || *accounts[1].Windows[0].RemainingPercent != 20 {
		t.Fatalf("second worker result not visible: %#v", accounts[1])
	}
	if accounts[0].Windows[0].RemainingPercent == nil || *accounts[0].Windows[0].RemainingPercent != 50 {
		t.Fatalf("unfinished account changed: %#v", accounts[0])
	}

	close(gates["1"])
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("collection did not finish")
	}
	final := resourceSnapshot()
	if final["collection_in_progress"] != false || final["snapshot_complete"] != true {
		t.Fatalf("final metadata: %#v", final)
	}
}

func waitProgress(t *testing.T, completed int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		body := resourceSnapshot()
		if p, ok := body["progress"].(map[string]any); ok && p["completed"] == completed {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("progress never reached %d: %#v", completed, body)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestProgressiveMergeRetainsNilFieldsAndLocalFailure(t *testing.T) {
	balance, available := 0.0, 2
	reset := time.Now().Add(time.Hour).UTC()
	remaining := 40.0
	previous := []quotamonitor.AccountQuota{{
		AccountKey: "a", Provider: "codex", PlanType: "team", Status: quotamonitor.StatusKnown, Source: "active",
		Windows:    []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &remaining, ResetAt: &reset}},
		APICredits: &quotamonitor.APICredits{Balance: &balance}, ResetCredits: &quotamonitor.ResetCredits{AvailableCount: &available, NearestExpiry: &reset},
	}}
	used := 25.0
	fresh := []quotamonitor.AccountQuota{{AccountKey: "a", Provider: "codex", Windows: []quotamonitor.QuotaWindow{{Name: "primary", UsedPercent: &used}}}}
	merged := applyLastGoodWindows(fresh, previous)
	got := merged[0]
	if got.PlanType != "team" || got.APICredits == nil || got.APICredits.Balance == nil || *got.APICredits.Balance != 0 || got.ResetCredits == nil || got.ResetCredits.NearestExpiry == nil {
		t.Fatalf("extras erased: %#v", got)
	}
	if len(got.Windows) != 1 || got.Windows[0].ResetAt == nil || got.Windows[0].RemainingPercent == nil || *got.Windows[0].RemainingPercent != 40 || got.Windows[0].UsedPercent == nil || *got.Windows[0].UsedPercent != 25 {
		t.Fatalf("window field merge failed: %#v", got.Windows)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "private", http.StatusUnauthorized) }))
	defer server.Close()
	account := previous[0]
	account.AuthIndex = "1"
	out, attempts, _ := probeAccounts(context.Background(), quotamonitor.NewClient(server.URL, "k"), []quotamonitor.AccountQuota{account}, previous, nil, true)
	if len(attempts) != 1 || !out[0].Stale || out[0].Status != quotamonitor.StatusStale || out[0].PlanType != "team" || len(out[0].Windows) != 1 {
		t.Fatalf("local failure lost last-good: %#v %#v", out, attempts)
	}
}

func TestSuccessfulCollectionKeepsAccountLocalStaleOutOfTopLevel(t *testing.T) {
	reset(t)
	const authFiles = `[
		{"provider":"codex","auth_index":"1","account":"stale@example.invalid"},
		{"provider":"codex","auth_index":"2","account":"fresh@example.invalid"}
	]`
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			_, _ = w.Write([]byte(authFiles))
		case "/management/api-call":
			var payload struct {
				AuthIndex string `json:"auth_index"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.AuthIndex == "1" {
				_, _ = w.Write([]byte(`{"status_code":401,"body":"{}"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":20}}"}`))
		default:
			_, _ = w.Write([]byte(`{"records":[]}`))
		}
	})

	parsed, err := state.client.Adapter.ParseAuthFilesJSON([]byte(authFiles))
	if err != nil || len(parsed) != 2 {
		t.Fatalf("parse previous identity: accounts=%#v err=%v", parsed, err)
	}
	oldRemaining := 65.0
	lastSuccess := time.Now().Add(-time.Minute).UTC()
	previous := parsed[0]
	previous.Status = quotamonitor.StatusKnown
	previous.Source = "active"
	previous.LastSuccessAt = &lastSuccess
	previous.Windows = []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &oldRemaining}}
	state.mu.Lock()
	state.last = []quotamonitor.AccountQuota{previous}
	state.cachedAt = now().Add(-2 * cacheTTL)
	state.lastSuccessAt = state.cachedAt
	state.snapshotComplete = true
	state.mu.Unlock()

	result := collectAccounts(context.Background(), true)
	if result.err != nil || result.stale || result.category != "success" {
		t.Fatalf("successful collection became globally stale: %+v", result)
	}
	if len(result.accounts) != 2 {
		t.Fatalf("accounts=%d, want 2", len(result.accounts))
	}
	byIndex := make(map[string]quotamonitor.AccountQuota, len(result.accounts))
	for _, account := range result.accounts {
		byIndex[account.AuthIndex] = account
	}
	if failed := byIndex["1"]; failed.Status != quotamonitor.StatusStale || !failed.Stale || failed.ErrorKind != "auth" || len(failed.Windows) != 1 || failed.Windows[0].RemainingPercent == nil || *failed.Windows[0].RemainingPercent != oldRemaining {
		t.Fatalf("account-local auth failure DTO=%#v", failed)
	}
	if fresh := byIndex["2"]; fresh.Status != quotamonitor.StatusKnown || fresh.Stale || len(fresh.Windows) == 0 {
		t.Fatalf("successful sibling DTO=%#v", fresh)
	}
	body := resourceSnapshot()
	if body["stale"] != false || body["reason"] != "" {
		t.Fatalf("committed resource snapshot reports collection stale: %#v", body)
	}
}

func TestProgressiveMergeCreditsFieldLevelPartialAndExplicitZeroFalse(t *testing.T) {
	trueValue, falseValue := true, false
	oldBalance, zeroBalance := 42.5, 0.0
	oldAvailable, zeroAvailable := 7, 0
	oldApplicable, zeroApplicable := 5, 0
	oldExpiry := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)

	previous := []quotamonitor.AccountQuota{{
		AccountKey: "a",
		Provider:   "codex",
		APICredits: &quotamonitor.APICredits{
			HasCredits: &trueValue,
			Unlimited:  &trueValue,
			Balance:    &oldBalance,
		},
		ResetCredits: &quotamonitor.ResetCredits{
			AvailableCount:           &oldAvailable,
			ApplicableAvailableCount: &oldApplicable,
			NearestExpiry:            &oldExpiry,
		},
	}}

	partial := []quotamonitor.AccountQuota{{
		AccountKey: "a",
		Provider:   "codex",
		APICredits: &quotamonitor.APICredits{Balance: &zeroBalance},
		ResetCredits: &quotamonitor.ResetCredits{
			ApplicableAvailableCount: &zeroApplicable,
		},
	}}
	got := applyLastGoodWindows(partial, previous)[0]
	if got.APICredits == nil || got.APICredits.HasCredits == nil || !*got.APICredits.HasCredits || got.APICredits.Unlimited == nil || !*got.APICredits.Unlimited || got.APICredits.Balance == nil || *got.APICredits.Balance != 0 {
		t.Fatalf("partial api_credits merge=%#v", got.APICredits)
	}
	if got.ResetCredits == nil || got.ResetCredits.AvailableCount == nil || *got.ResetCredits.AvailableCount != oldAvailable || got.ResetCredits.ApplicableAvailableCount == nil || *got.ResetCredits.ApplicableAvailableCount != 0 || got.ResetCredits.NearestExpiry == nil || !got.ResetCredits.NearestExpiry.Equal(oldExpiry) {
		t.Fatalf("partial reset_credits merge=%#v", got.ResetCredits)
	}

	explicit := []quotamonitor.AccountQuota{{
		AccountKey: "a",
		Provider:   "codex",
		APICredits: &quotamonitor.APICredits{
			HasCredits: &falseValue,
			Unlimited:  &falseValue,
			Balance:    &zeroBalance,
		},
		ResetCredits: &quotamonitor.ResetCredits{
			AvailableCount:           &zeroAvailable,
			ApplicableAvailableCount: &zeroApplicable,
		},
	}}
	got = applyLastGoodWindows(explicit, previous)[0]
	if got.APICredits == nil || got.APICredits.HasCredits == nil || *got.APICredits.HasCredits || got.APICredits.Unlimited == nil || *got.APICredits.Unlimited || got.APICredits.Balance == nil || *got.APICredits.Balance != 0 {
		t.Fatalf("explicit zero/false api_credits merge=%#v", got.APICredits)
	}
	if got.ResetCredits == nil || got.ResetCredits.AvailableCount == nil || *got.ResetCredits.AvailableCount != 0 || got.ResetCredits.ApplicableAvailableCount == nil || *got.ResetCredits.ApplicableAvailableCount != 0 || got.ResetCredits.NearestExpiry == nil || !got.ResetCredits.NearestExpiry.Equal(oldExpiry) {
		t.Fatalf("explicit zero reset_credits merge=%#v", got.ResetCredits)
	}
}

func TestProgressiveResourceConcurrentReadsRace(t *testing.T) {
	reset(t)
	state.mu.Lock()
	state.configured = true
	state.snapshotComplete = true
	state.last = []quotamonitor.AccountQuota{{AccountKey: "a", Provider: "codex", Status: quotamonitor.StatusKnown}}
	state.order = []string{"a"}
	state.current = map[string]currentChoice{}
	state.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = resourceSnapshot()
			}
		}()
	}
	wg.Wait()
}

func TestDashboardInProgressPollAndFocusScrollNode(t *testing.T) {
	nodeScript := `
const vm=require('vm'); const html=process.argv[1]; const script=html.match(/<script>([\s\S]*?)<\/script>/)[1];
function node(id){return {id:id||'',style:{},attrs:{},children:[],parentNode:null,value:'',selectionStart:1,selectionEnd:2,appendChild(c){c.parentNode=this;this.children.push(c);return c},replaceChildren(...cs){this.children=[];cs.forEach(c=>{if(c&&c._isFragment)c.children.forEach(x=>this.appendChild(x));else if(c)this.appendChild(c)})},setAttribute(k,v){this.attrs[k]=v},getAttribute(k){return this.attrs[k]||null},getElementsByClassName(){return []},focus(){doc.activeElement=this},classList:{add(){},remove(){}}};}
const by={}; const doc={hidden:false,activeElement:null,documentElement:{scrollTop:0},body:{scrollTop:0},addEventListener(){},createElement(){return node()},createDocumentFragment(){const n=node();n._isFragment=true;return n},getElementById(id){return by[id]||(by[id]=node(id))},querySelector(sel){const m=sel.match(/data-account-key="([^"]+)/); if(!m)return null; return (by.accountsList.children||[]).find(x=>x.attrs['data-account-key']===m[1])||null}};
let delays=[]; let y=321; const win={document:doc,pageYOffset:y,scrollTo(x,v){y=v;this.pageYOffset=v}};
const ctx={document:doc,window:win,console,Date,Math,String,Number,Array,Object,isNaN,isFinite,setTimeout(fn,ms){delays.push(ms);return delays.length},clearTimeout(){},AbortController:function(){this.signal={};this.abort=()=>{}},fetch(){return Promise.resolve({ok:true,json:()=>Promise.resolve({configured:true,snapshot_complete:true,collection_in_progress:true,accounts:[{account_key:'a',provider:'codex',status:'known',windows:[{name:'primary',remaining_percent:80}]}]})})}};
vm.createContext(ctx);vm.runInContext(script,ctx); const search=doc.getElementById('accountSearch');search.value='x';doc.activeElement=search;ctx.cachedAccounts=[{account_key:'a',provider:'codex',status:'known',windows:[]}];ctx.loadDashboard(false);
setImmediate(()=>setImmediate(()=>{if(!delays.includes(2000))throw Error('missing 2s poll '+delays);if(doc.activeElement!==search)throw Error('focus lost');if(y!==321)throw Error('scroll lost '+y);console.log('OK_PROGRESSIVE_FRONTEND')}));`
	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_PROGRESSIVE_FRONTEND") {
		t.Fatalf("node progressive test: %v\n%s", err, out)
	}
}
