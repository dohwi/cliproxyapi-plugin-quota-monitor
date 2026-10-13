package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestBackgroundCollectorImmediateFillAndResourceRepetitionNoProbes verifies:
//  1. Configured starts an immediate complete background collection.
//  2. Repeated calls to resource /data/accounts are pure snapshot reads and
//     never trigger upstream auth-files scans or provider probes (probe delta = 0).
func TestBackgroundCollectorImmediateFillAndResourceRepetitionNoProbes(t *testing.T) {
	reset(t)
	authDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(authDir, "codex-test.json"), []byte(`{"proxy_url":"direct","id":"bg-id","type":"codex"}`), 0600); err != nil {
		t.Fatal(err)
	}

	var authFiles, apiCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			authFiles.Add(1)
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"1","account":"bg@example.invalid","name":"codex-test.json","id":"bg-id","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}}]`))
		case "/management/api-call":
			apiCalls.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"header":{},"body":"{\"rate_limit\":{\"primary_window\":{\"used_percent\":10}}}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("QUOTA_MONITOR_MGMT_URL", server.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "fixture-management-key")
	t.Setenv("QUOTA_MONITOR_AUTH_DIR", authDir)
	// Ensure background collector is enabled
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "0")
	t.Setenv("QUOTA_MONITOR_BACKGROUND_COLLECTOR", "true")

	hookDone := make(chan struct{}, 1)
	setTestHooks(nil, func(gen uint64, success bool) {
		select {
		case hookDone <- struct{}{}:
		default:
		}
	})
	t.Cleanup(func() {
		Shutdown()
		setTestHooks(nil, nil)
	})

	configureFromEnv(nil)

	// Await the initial complete collection executed immediately by the background collector
	select {
	case <-hookDone:
	case <-time.After(5 * time.Second):
		t.Fatal("background collector did not complete initial collection within deadline")
	}

	initialAuthFiles := authFiles.Load()
	initialApiCalls := apiCalls.Load()
	if initialAuthFiles != 1 || initialApiCalls != 1 {
		t.Fatalf("expected initial fill 1 authFile, 1 apiCall; got authFiles=%d apiCalls=%d", initialAuthFiles, initialApiCalls)
	}

	// Repeated passive resource queries
	for i := 0; i < 20; i++ {
		req, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: resourceBase + "/data/accounts"})
		code, res := decodeMgmt(t, handleManagementHandle(req))
		if code != http.StatusOK {
			t.Fatalf("resource call %d failed with code %d", i, code)
		}
		if res["snapshot_complete"] != true {
			t.Fatalf("resource call %d: expected snapshot_complete=true, got %#v", i, res["snapshot_complete"])
		}
		if res["stale"] != false {
			t.Fatalf("resource call %d: expected stale=false, got %#v", i, res["stale"])
		}
	}

	// Verify that 20 resource requests triggered exactly ZERO additional probes or auth file reads
	afterAuthFiles := authFiles.Load()
	afterApiCalls := apiCalls.Load()
	if afterAuthFiles != initialAuthFiles || afterApiCalls != initialApiCalls {
		t.Fatalf("resource calls performed privileged work: authFiles before=%d after=%d, apiCalls before=%d after=%d",
			initialAuthFiles, afterAuthFiles, initialApiCalls, afterApiCalls)
	}
}

// TestBackgroundCollectorFakeClockTickTriggersCollection verifies:
// Background tick manually delivered through fake clock triggers complete collection,
// increments health collections, and updates cache without racing.
func TestBackgroundCollectorFakeClockTickTriggersCollection(t *testing.T) {
	reset(t)
	authDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(authDir, "codex-test.json"), []byte(`{"proxy_url":"direct","id":"bg-tick-id","type":"codex"}`), 0600); err != nil {
		t.Fatal(err)
	}

	var authFiles atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/management/auth-files" {
			authFiles.Add(1)
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"1","account":"tick@example.invalid","name":"codex-test.json","id":"bg-tick-id"}]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	t.Setenv("QUOTA_MONITOR_MGMT_URL", server.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "fixture-management-key")
	t.Setenv("QUOTA_MONITOR_AUTH_DIR", authDir)
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "0")

	// Set up tick channel for fake clock control
	tickCh := make(chan struct{}, 10)
	collected := make(chan uint64, 10)
	setTestHooks(tickCh, func(gen uint64, success bool) {
		collected <- gen
	})
	t.Cleanup(func() {
		Shutdown()
		setTestHooks(nil, nil)
	})

	configureFromEnv(nil)

	// 1. Initial collection
	select {
	case <-collected:
	case <-time.After(3 * time.Second):
		t.Fatal("initial collection timed out")
	}

	// 2. Deliver tick #1 via fake clock
	// Let the collector wait in its timer select loop, then advance clock
	time.Sleep(50 * time.Millisecond)
	// Must force tick through cacheTTL or force collection to record collections
	// To exercise background tick running a collection:
	// Set cachedAt backwards to simulate time elapsed beyond cacheTTL
	state.mu.Lock()
	state.cachedAt = time.Now().Add(-2 * cacheTTL)
	state.mu.Unlock()

	tickCh <- struct{}{}
	select {
	case <-collected:
	case <-time.After(3 * time.Second):
		t.Fatal("collection after tick #1 timed out")
	}

	if authFiles.Load() < 2 {
		t.Fatalf("expected at least 2 authFiles collections, got %d", authFiles.Load())
	}
}

// TestBackgroundCollectorReconfigureAndShutdownCleanStop verifies:
// Rapid reconfigure and shutdown stops collector tickers/goroutines cleanly without leaks or race conditions.
func TestBackgroundCollectorReconfigureAndShutdownCleanStop(t *testing.T) {
	reset(t)
	authDir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	t.Setenv("QUOTA_MONITOR_MGMT_URL", server.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "fixture-key")
	t.Setenv("QUOTA_MONITOR_AUTH_DIR", authDir)
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "0")

	t.Cleanup(func() {
		Shutdown()
	})

	for i := 0; i < 10; i++ {
		configureFromEnv(nil)
		time.Sleep(5 * time.Millisecond)
	}

	Shutdown()

	state.mu.Lock()
	active := state.collectorCancel != nil
	configured := state.configured
	state.mu.Unlock()

	if active || configured {
		t.Fatalf("after shutdown: collectorActive=%v configured=%v", active, configured)
	}
}

// TestDashboardNodeContractCacheExpiredNoFalseWarning verifies:
// 1. Under cache_expired + snapshot_complete=true + last_category=success:
//   - staleNoticeBox is hidden (display: 'none') - NO FALSE WARNING BANNER!
//   - account observation timestamps remain available for card freshness
//
// 2. Under real failures (health.last_category != 'success', failures > 0, or explicit error):
//   - staleNoticeBox is displayed (display: 'flex') with error notice.
func TestDashboardNodeContractCacheExpiredNoFalseWarning(t *testing.T) {
	nodeScript := `
const vm = require('vm');
const html = process.argv[1];
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) throw new Error('script tag not found');
const script = scriptMatch[1];

function makeMockElement() {
  return {
    style: {},
    setAttribute() {},
    getAttribute() { return ''; },
    appendChild() {},
    removeChild() {},
    replaceChildren() {},
    addEventListener() {},
    querySelector() { return makeMockElement(); },
    querySelectorAll() { return []; },
    getElementsByClassName() { return []; },
    set textContent(v) { this._v = String(v); },
    get textContent() { return this._v || ''; },
    get innerHTML() { return this._v || ''; },
    set innerHTML(v) { this._v = String(v); },
    value: 'all'
  };
}

let staleBoxDisplay = 'none';
let staleBoxText = '';

const domElements = {
  staleNoticeBox: {
    style: {
      get display() { return staleBoxDisplay; },
      set display(v) { staleBoxDisplay = v; }
    }
  },
  staleNoticeText: {
    get textContent() { return staleBoxText; },
    set textContent(v) { staleBoxText = String(v); }
  },
  unconfiguredBox: makeMockElement(),
  unconfiguredText: makeMockElement(),
  dashboardContent: makeMockElement(),
  providerFilter: makeMockElement(),
  statusFilter: makeMockElement(),
  endpointFilter: makeMockElement(),
  accountsList: makeMockElement(),
  accountStatusLive: makeMockElement(),
  errorBox: makeMockElement()
};

let fetchResolve = null;
const ctx = {
  console, Math, Number, Array, Object, String, isNaN, isFinite, Date,
  document: {
    hidden: false,
    addEventListener() {},
    getElementById(id) {
      if (id === 'updateTimestamp' || id === 'updateRelative') throw new Error('obsolete header timestamp lookup');
      return domElements[id] || makeMockElement();
    },
    createElement() { return makeMockElement(); },
    querySelector() { return makeMockElement(); },
    querySelectorAll() { return []; },
    getElementsByClassName() { return []; }
  },
  localStorage: { getItem() { return '0'; }, setItem() {} },
  setTimeout(fn, ms) { return 1; },
  clearTimeout() {},
  fetch(url, opts) {
    return new Promise((resolve) => {
      fetchResolve = resolve;
    });
  }
};

vm.createContext(ctx);
vm.runInContext(script, ctx);

function assert(ok, msg) {
  if (!ok) throw new Error('FAIL: ' + msg);
}

// -------------------------------------------------------------
// Scenario 1: cache_expired + snapshot_complete + last_category=success
// -------------------------------------------------------------
const expiredPayload = {
  configured: true,
  snapshot_complete: true,
  stale: true,
  reason: "cache_expired",
  accounts: [
    {
      provider: "codex",
      account: "user@example.com",
      status: "known",
      observed_at: "2026-03-30T14:15:00Z",
      operational: { status: "active" },
      windows: [{ remaining_percent: 85 }]
    }
  ],
  health: {
    collections: 1,
    successes: 1,
    failures: 0,
    last_category: "success"
  },
  next_poll_hint_ms: 10000
};

// Emulate initial load returning expired cache
ctx.loadDashboard(false);
fetchResolve({
  ok: true,
  json: () => Promise.resolve(expiredPayload)
});

setTimeout(() => {
  // Check that NO false warning banner is shown!
  assert(staleBoxDisplay === 'none', 'Scenario 1: staleNoticeBox must be hidden for cache_expired with last_category=success, but was: ' + staleBoxDisplay);
  assert(ctx.cachedAccounts[0].observed_at === expiredPayload.accounts[0].observed_at, 'Scenario 1: account observation timestamp is retained');
  assert(ctx.renderAccountFreshness(ctx.cachedAccounts[0]).includes('마지막 확인'), 'Scenario 1: account freshness remains available');

  // -------------------------------------------------------------
  // Scenario 2: Real failure (health.last_category != 'success' or failures increased)
  // -------------------------------------------------------------
  const failedPayload = {
    configured: true,
    snapshot_complete: true,
    stale: true,
    reason: "auth_files_failed",
    accounts: [
      {
        provider: "codex",
        account: "user@example.com",
        status: "stale",
        operational: { status: "active" },
        windows: [{ remaining_percent: 85 }]
      }
    ],
    health: {
      collections: 2,
      successes: 1,
      failures: 1,
      last_category: "auth_files_failed"
    },
    next_poll_hint_ms: 10000
  };

  ctx.loadDashboard(false);
  fetchResolve({
    ok: true,
    json: () => Promise.resolve(failedPayload)
  });

  setTimeout(() => {
    // Check that real failure DOES display the warning banner!
    assert(staleBoxDisplay === 'flex', 'Scenario 2: staleNoticeBox must be flex on real failure, got: ' + staleBoxDisplay);
    assert(staleBoxText.indexOf('데이터 갱신 실패') !== -1, 'Scenario 2: staleBoxText must mention 데이터 갱신 실패, got: ' + staleBoxText);
    console.log('OK_CACHE_EXPIRED_AND_FAILURE_SEPARATION');
  }, 10);
}, 10);
`

	cmd := nodeDashboardCmd(t, "node", nodeScript)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_CACHE_EXPIRED_AND_FAILURE_SEPARATION") {
		t.Fatalf("Dashboard contract cache_expired separation failed: %v\n%s", err, out)
	}
}
