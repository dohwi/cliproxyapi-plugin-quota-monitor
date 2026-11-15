package plugin

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func capturePanics(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var logs []string
	old := panicLog
	panicLog = func(msg string) { mu.Lock(); logs = append(logs, msg); mu.Unlock() }
	t.Cleanup(func() { panicLog = old })
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), logs...) }
}

func TestSafeGoRecoversAndRunsCleanup(t *testing.T) {
	logs := capturePanics(t)
	cleaned := make(chan struct{})
	safeGo("test task", func() {
		defer close(cleaned)
		panic("boom")
	})
	select {
	case <-cleaned:
	case <-time.After(2 * time.Second):
		t.Fatal("deferred cleanup did not run")
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(logs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := logs()
	if len(got) != 1 || !strings.HasPrefix(got[0], "plugin: test task panic: boom\n") || !strings.Contains(got[0], "goroutine") {
		t.Fatalf("panic log = %q", got)
	}
}

// A panic while collecting must fail that collection and release the flight;
// otherwise every later caller would wait on a flight that never completes.
func TestCollectionPanicReleasesFlight(t *testing.T) {
	reset(t)
	logs := capturePanics(t)
	state.mu.Lock()
	state.configured = true
	state.client = nil // runCollection dereferences it and panics
	state.mu.Unlock()

	done := make(chan collectionResult, 1)
	go func() { done <- collectAccounts(context.Background(), false) }()
	var r collectionResult
	select {
	case r = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("collection deadlocked after a panic")
	}
	if r.err == nil || r.category != "internal_error" || !r.stale {
		t.Fatalf("result = %+v, want a failed internal_error collection", r)
	}
	state.mu.Lock()
	flight, inProgress, failures := state.flight, state.inProgress, state.health.Failures
	state.mu.Unlock()
	if flight != nil || inProgress || failures != 1 {
		t.Fatalf("flight=%v inProgress=%v failures=%d, want released", flight, inProgress, failures)
	}
	if got := logs(); len(got) != 1 || !strings.Contains(got[0], "plugin: collection panic:") {
		t.Fatalf("panic log = %q", got)
	}
	// The next collection starts normally instead of joining a dead flight.
	go func() { done <- collectAccounts(context.Background(), false) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("second collection blocked")
	}
}

// A panic in one probe job must not kill its worker: the remaining jobs are
// still drained (the enqueue loop would otherwise block) and wg.Wait returns.
func TestProbeWorkerPanicStillCompletes(t *testing.T) {
	reset(t)
	logs := capturePanics(t)
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":20}}"}`))
	})
	state.mu.Lock()
	client := state.client
	state.mu.Unlock()
	var accounts []quotamonitor.AccountQuota
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		accounts = append(accounts, quotamonitor.AccountQuota{AccountKey: k, AuthIndex: k, Provider: "codex"})
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	onAccount := func(a quotamonitor.AccountQuota) {
		mu.Lock()
		seen[a.AccountKey] = true
		mu.Unlock()
		panic("account callback " + a.AccountKey)
	}
	type out struct {
		accounts []quotamonitor.AccountQuota
	}
	done := make(chan out, 1)
	go func() {
		got, _, _ := probeAccountsProgressive(context.Background(), client, accounts, nil, nil, true, nil, onAccount)
		done <- out{got}
	}()
	select {
	case o := <-done:
		if len(o.accounts) != len(accounts) {
			t.Fatalf("accounts = %d", len(o.accounts))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe workers deadlocked after panics")
	}
	if len(seen) != len(accounts) {
		t.Fatalf("probed %d of %d accounts after panics", len(seen), len(accounts))
	}
	if n := len(logs()); n != len(accounts) {
		t.Fatalf("recovered panics = %d, want %d", n, len(accounts))
	}
}

// A panic in the background collector loop is logged and still closes done,
// so Shutdown/reconfigure (which wait on it) never hang.
func TestBackgroundCollectorPanicClosesDone(t *testing.T) {
	reset(t)
	logs := capturePanics(t)
	setTestHooks(nil, func(uint64, bool) { panic("hook") })
	t.Cleanup(func() { setTestHooks(nil, nil) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go runBackgroundCollector(ctx, 0, done)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("collector done channel not closed after panic")
	}
	if got := logs(); len(got) != 1 || !strings.Contains(got[0], "plugin: background collector panic: hook") {
		t.Fatalf("panic log = %q", got)
	}
}
