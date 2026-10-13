package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func configureTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("QUOTA_MONITOR_MGMT_URL", server.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "test-management-secret")
	t.Setenv("QUOTA_MONITOR_AUTH_DIR", t.TempDir())
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	configureFromEnv(nil)
	return server
}

func receiveResult(t *testing.T, ch <-chan collectionResult) collectionResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("collection did not finish")
		return collectionResult{}
	}
}

// Done is evaluated only after collectAccounts has joined the current flight.
// This barrier avoids sleeps and guarantees the test exercises a true waiter.
type joinedContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *joinedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

func joinCollection(t *testing.T) <-chan collectionResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	joined := &joinedContext{Context: ctx, joined: make(chan struct{})}
	result := make(chan collectionResult, 1)
	go func() { result <- collectAccounts(joined, false) }()
	select {
	case <-joined.joined:
	case <-ctx.Done():
		t.Fatal("waiter did not join")
	}
	return result
}

func TestResourceSnapshotNeverFetches(t *testing.T) {
	reset(t)
	var calls atomic.Int32
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`[]`)) })
	for _, cached := range []bool{false, true} {
		state.mu.Lock()
		if cached {
			state.last = []quotamonitor.AccountQuota{{AccountKey: "opaque", Provider: "codex", Account: "private@example.com"}}
			state.cachedAt = now().Add(-2 * cacheTTL)
			state.lastSuccessAt = state.cachedAt
		}
		state.mu.Unlock()
		code, body := decodeMgmt(t, handleManagementHandle(request(resourceBase+"/data/accounts")))
		if code != 200 || body["configured"] != true || body["stale"] != true || body["reason"] == "" {
			t.Fatalf("snapshot: %#v", body)
		}
		if cached && (body["cached_at"] == nil || body["last_success_at"] == nil) {
			t.Fatal("cache timestamps missing")
		}
		for _, method := range []string{"POST", "HEAD", "PUT"} {
			raw, _ := json.Marshal(managementRequest{Method: method, Path: resourceBase + "/data/refresh", Query: map[string][]string{"confirm": {"1"}}})
			code, _ := decodeMgmt(t, handleManagementHandle(raw))
			if code != 404 {
				t.Fatalf("refresh %s = %d", method, code)
			}
		}
	}
	if calls.Load() != 0 || state.resolver.ScanCount() != 0 {
		t.Fatalf("resource performed privileged work: %d", calls.Load())
	}
	var registered struct {
		Result struct {
			Resources []resourceRoute `json:"resources"`
			Routes    []mgmtRoute     `json:"routes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(handleManagementRegister(), &registered); err != nil {
		t.Fatal(err)
	}
	for _, r := range registered.Result.Resources {
		if r.Path == "/data/refresh" && (r.Method != "GET" || r.Menu != "") {
			t.Fatal("refresh resource must be hidden and host-compatible")
		}
	}
	for _, r := range registered.Result.Routes {
		if strings.HasSuffix(r.Path, "/refresh") && r.Method != "POST" {
			t.Fatal("refresh is not POST-only")
		}
	}
}

func TestProbeDueNotPostponedByPassiveCollections(t *testing.T) {
	reset(t)
	var probes atomic.Int32
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"a@x"}]`))
		case "/management/api-call":
			probes.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":20}}"}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	first := collectAccounts(context.Background(), false)
	if first.err != nil || len(first.accounts) != 1 {
		t.Fatalf("first: %+v", first)
	}
	key := first.accounts[0].AccountKey
	original := state.schedules[key]
	if original.LastAttempt.IsZero() || original.LastSuccess.IsZero() || original.NextDue.IsZero() {
		t.Fatalf("missing scheduling timestamps: %+v", original)
	}
	for i := 0; i < 3; i++ {
		state.mu.Lock()
		state.cachedAt = now().Add(-2 * cacheTTL)
		state.mu.Unlock()
		r := collectAccounts(context.Background(), false)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if state.schedules[key] != original || state.probeDue[key] != original.NextDue {
			t.Fatal("passive read postponed probe")
		}
	}
	if probes.Load() != 1 {
		t.Fatalf("probes=%d", probes.Load())
	}
	state.mu.Lock()
	state.cachedAt = time.Time{}
	state.probeDue[key] = now().Add(-time.Second)
	state.mu.Unlock()
	if r := collectAccounts(context.Background(), false); r.err != nil {
		t.Fatal(r.err)
	}
	if probes.Load() != 2 {
		t.Fatal("due account not probed")
	}
}

func TestDisabledAccountsNeverProbeEvenForced(t *testing.T) {
	reset(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`[]`)) }))
	defer server.Close()
	disabled, count := true, 2
	accounts := []quotamonitor.AccountQuota{{AccountKey: "disabled", AuthIndex: "7", Provider: "codex", Operational: &quotamonitor.OperationalStatus{Disabled: &disabled}, ResetCredits: &quotamonitor.ResetCredits{AvailableCount: &count}}}
	for _, force := range []bool{false, true} {
		got, attempts, truncated := probeAccounts(context.Background(), quotamonitor.NewClient(server.URL, "k"), accounts, nil, nil, force)
		if len(got) != 1 || len(attempts) != 0 || truncated {
			t.Fatalf("disabled probe result: %+v %+v", got, attempts)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("disabled usage/reset detail calls=%d", calls.Load())
	}
}

func TestCollectionDeadlineCancelsQueuedWork(t *testing.T) {
	reset(t)
	var active, maxActive, calls atomic.Int32
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			entries := make([]map[string]any, 30)
			for i := range entries {
				entries[i] = map[string]any{"provider": "codex", "auth_index": fmt.Sprint(i + 1), "account": fmt.Sprint(i)}
			}
			_ = json.NewEncoder(w).Encode(entries)
		case "/management/api-call":
			calls.Add(1)
			n := active.Add(1)
			defer active.Add(-1)
			for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
			}
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := collectAccounts(ctx, false)
	if time.Since(started) > time.Second || result.err == nil || !result.stale || !result.truncated || result.category != "timeout" {
		t.Fatalf("deadline result: %+v", result)
	}
	if calls.Load() > collectionWorkers || maxActive.Load() > collectionWorkers {
		t.Fatalf("unbounded workers: calls=%d concurrency=%d", calls.Load(), maxActive.Load())
	}
	if len(state.schedules) != int(calls.Load()) || len(state.schedules) == 0 {
		t.Fatalf("queued jobs advanced due: %d schedules, %d calls", len(state.schedules), calls.Load())
	}
	if state.health.Timeouts != 1 || state.health.LastCategory != "timeout" || !state.health.Truncated || state.health.LastLatencyMS <= 0 {
		t.Fatalf("health: %+v", state.health)
	}
}

func TestCollectionGenerationRejectsOldCommitAndWaiter(t *testing.T) {
	reset(t)
	started := make(chan struct{})
	var once sync.Once
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		_, _ = w.Write([]byte(`[{"provider":"codex","account":"old"}]`))
	})
	leader := make(chan collectionResult, 1)
	oldGeneration := state.generation
	go func() { leader <- collectAccounts(context.Background(), false) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old collection not started")
	}
	waiter := joinCollection(t)
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/management/auth-files" {
			_, _ = w.Write([]byte(`[{"provider":"codex","account":"new"}]`))
		} else {
			_, _ = w.Write([]byte(`[]`))
		}
	})
	fresh := collectAccounts(context.Background(), false)
	a, b := receiveResult(t, leader), receiveResult(t, waiter)
	if a.category != "configuration_changed" || a.err == nil || !a.stale || a.generation != oldGeneration || !reflect.DeepEqual(a, b) {
		t.Fatalf("generation results differ: %+v %+v", a, b)
	}
	if fresh.err != nil || fresh.generation == oldGeneration || len(state.last) != 1 || state.last[0].Account != "new" {
		t.Fatalf("old commit overwrote config: %+v %+v", fresh, state.last)
	}
	if state.health.Collections != 1 || state.lastForceAt != (time.Time{}) {
		t.Fatalf("old collection polluted health/cooldown: %+v", state.health)
	}
}

func TestSingleflightWaitersShareFailureAndRetainedSnapshot(t *testing.T) {
	reset(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		http.Error(w, "secret upstream detail", http.StatusInternalServerError)
	})
	observed := now().Add(-time.Hour)
	state.last = []quotamonitor.AccountQuota{{AccountKey: "retained", Provider: "codex", ObservedAt: &observed, LastUsedAt: &observed}}
	state.cachedAt = now().Add(-2 * cacheTTL)
	leader := make(chan collectionResult, 1)
	go func() { leader <- collectAccounts(context.Background(), false) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("fetch not started")
	}
	waiter := joinCollection(t)
	// A passive resource returns immediately while a privileged flight is blocked.
	if code, _ := decodeMgmt(t, handleManagementHandle(request(resourceBase+"/data/accounts"))); code != 200 {
		t.Fatal(code)
	}
	close(release)
	a, b := receiveResult(t, leader), receiveResult(t, waiter)
	if !reflect.DeepEqual(a, b) || a.err == nil || !a.stale || len(a.accounts) != 1 || !a.accounts[0].Stale || a.accounts[0].LastUsedAt == nil {
		t.Fatalf("inconsistent failure: %+v %+v", a, b)
	}
	*a.accounts[0].LastUsedAt = time.Time{}
	if b.accounts[0].LastUsedAt.IsZero() || state.last[0].LastUsedAt.IsZero() {
		t.Fatal("waiter/cache aliases result")
	}
	if strings.Contains(a.err.Error(), "secret") {
		t.Fatal("unsafe error")
	}
}

func TestSingleflightWaiterCancellationDoesNotCancelLeader(t *testing.T) {
	reset(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		_, _ = w.Write([]byte(`[]`))
	})
	leader := make(chan collectionResult, 1)
	go func() { leader <- collectAccounts(context.Background(), false) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("leader not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r := collectAccounts(ctx, false)
	if r.category != "canceled" || r.generation != state.generation {
		t.Fatalf("waiter cancellation: %+v", r)
	}
	close(release)
	if r := receiveResult(t, leader); r.err != nil {
		t.Fatal(r.err)
	}
}

func TestSkippedProbePreservesCompleteStateAndDeepCopies(t *testing.T) {
	reset(t)
	timestamp := now()
	remaining := 42.0
	balance := 12.0
	retry := int64(120)
	count := 2
	disabled := false
	old := quotamonitor.AccountQuota{AccountKey: "a", AuthIndex: "7", Provider: "codex", Status: quotamonitor.StatusStale, Stale: true, Error: "quota lookup failed", ErrorKind: "auth", Source: "active", LastSuccessAt: &timestamp, LastUsedAt: &timestamp, ObservedAt: &timestamp, RetryAfter: &retry, ActiveLimit: "secondary", Credits: map[string]string{"balance": "12"}, RawSignals: map[string]string{"safe": "value"}, Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &remaining, ResetAt: &timestamp}}, APICredits: &quotamonitor.APICredits{Balance: &balance}, ResetCredits: &quotamonitor.ResetCredits{AvailableCount: &count, NearestExpiry: &timestamp}, Operational: &quotamonitor.OperationalStatus{Disabled: &disabled}}
	fresh := quotamonitor.AccountQuota{AccountKey: "a", AuthIndex: "7", Provider: "codex", Status: quotamonitor.StatusKnown, Operational: &quotamonitor.OperationalStatus{Status: "fresh"}, Windows: []quotamonitor.QuotaWindow{{Name: "primary"}}}
	accounts, attempts, _ := probeAccounts(context.Background(), nil, []quotamonitor.AccountQuota{fresh}, []quotamonitor.AccountQuota{old}, map[string]time.Time{"a": now().Add(time.Hour)}, false)
	got := accounts[0]
	if len(attempts) != 0 || got.Status != old.Status || !got.Stale || got.Error != old.Error || got.ErrorKind != old.ErrorKind || got.Source != old.Source || !reflect.DeepEqual(got.APICredits, old.APICredits) || !reflect.DeepEqual(got.ResetCredits, old.ResetCredits) || got.LastUsedAt == nil || *got.RetryAfter != retry || got.Operational.Status != "fresh" || *got.Windows[0].RemainingPercent != remaining {
		t.Fatalf("state lost: %+v", got)
	}
	copied := cloneAccounts([]quotamonitor.AccountQuota{old})[0]
	*copied.LastUsedAt = time.Time{}
	*copied.Windows[0].RemainingPercent = 0
	*copied.ResetCredits.AvailableCount = 0
	*copied.APICredits.Balance = 0
	*copied.Operational.Disabled = true
	copied.Credits["balance"] = "0"
	copied.RawSignals["safe"] = "changed"
	if old.LastUsedAt.IsZero() || *old.Windows[0].RemainingPercent != 42 || *old.ResetCredits.AvailableCount != 2 || *old.APICredits.Balance != 12 || *old.Operational.Disabled || old.Credits["balance"] != "12" || old.RawSignals["safe"] != "value" {
		t.Fatal("clone shares mutable state")
	}
}

func TestCollectionAccountAndJobCaps(t *testing.T) {
	reset(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":20}}"}`))
	}))
	defer server.Close()
	accounts := make([]quotamonitor.AccountQuota, maxCollectionAccounts+10)
	for i := range accounts {
		accounts[i] = quotamonitor.AccountQuota{AccountKey: fmt.Sprint(i), AuthIndex: fmt.Sprint(i), Provider: "codex"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, attempts, truncated := probeAccounts(ctx, quotamonitor.NewClient(server.URL, "k"), accounts, nil, nil, true)
	if len(got) != maxCollectionAccounts || len(attempts) != maxProbeJobs || calls.Load() != maxProbeJobs || !truncated {
		t.Fatalf("caps: accounts=%d attempts=%d calls=%d truncated=%v", len(got), len(attempts), calls.Load(), truncated)
	}
}

func TestFailedForcedRefreshCooldownKeepsFailure(t *testing.T) {
	reset(t)
	var calls atomic.Int32
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "private detail", http.StatusInternalServerError)
	})
	first := collectAccounts(context.Background(), true)
	second := collectAccounts(context.Background(), true)
	if first.err == nil || !first.stale || !reflect.DeepEqual(first, second) || calls.Load() != 1 {
		t.Fatalf("cooldown lost result: %+v %+v calls=%d", first, second, calls.Load())
	}
}

func TestFailedProbeAdvancesAttemptButPreservesSuccess(t *testing.T) {
	reset(t)
	succeeded := now().Add(-time.Hour)
	attempted := now()
	account := quotamonitor.AccountQuota{AccountKey: "a", AuthIndex: "7", Provider: "codex", ErrorKind: "auth"}
	state.schedules = map[string]probeSchedule{"a": {LastAttempt: succeeded, LastSuccess: succeeded, NextDue: succeeded}}
	state.probeDue["a"] = succeeded
	recordProbeAttempts(state, []quotamonitor.AccountQuota{account}, map[string]probeSchedule{"a": {LastAttempt: attempted}})
	got := state.schedules["a"]
	if got.LastAttempt != attempted || got.LastSuccess != succeeded || got.NextDue != attempted.Add(probeIntervalExhausted) || state.health.ProbeFailures != 1 {
		t.Fatalf("failed attempt schedule: %+v health=%+v", got, state.health)
	}
	disabled := true
	account.Operational = &quotamonitor.OperationalStatus{Disabled: &disabled}
	recordProbeAttempts(state, []quotamonitor.AccountQuota{account}, nil)
	if len(state.schedules) != 0 || len(state.probeDue) != 0 {
		t.Fatal("disabled schedule retained")
	}
}
