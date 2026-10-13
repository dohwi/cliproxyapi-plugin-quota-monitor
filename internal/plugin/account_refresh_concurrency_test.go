package plugin

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func seedCardRefresh(t *testing.T) []quotamonitor.AccountQuota {
	t.Helper()
	state.mu.Lock()
	client := state.client
	state.mu.Unlock()
	accounts, err := client.FetchAuthFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.last = cloneAccounts(accounts)
	state.cachedAt = now()
	state.snapshotComplete = true
	state.mu.Unlock()
	return accounts
}

func TestCardRefreshFullWaiterStillCollectsAllAccounts(t *testing.T) {
	reset(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var lists, probes atomic.Int32
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			lists.Add(1)
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"a@x"},{"provider":"codex","auth_index":"8","account":"b@x"}]`))
		case "/management/api-call":
			if probes.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":20}}"}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	accounts := seedCardRefresh(t)
	done := make(chan struct{})
	go func() { refreshSingleAccount(accounts[0].AccountKey); close(done) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not start")
	}
	ctx := &joinedContext{Context: context.Background(), joined: make(chan struct{})}
	full := make(chan collectionResult, 1)
	go func() { full <- collectAccounts(ctx, true) }()
	select {
	case <-ctx.joined:
	case <-time.After(3 * time.Second):
		t.Fatal("waiter did not join")
	}
	once.Do(func() { close(release) })
	result := receiveResult(t, full)
	<-done
	if result.err != nil || len(result.accounts) != 2 || lists.Load() != 2 || probes.Load() != 3 {
		t.Fatalf("full refresh inherited partial: result=%#v lists=%d probes=%d", result, lists.Load(), probes.Load())
	}
}

func TestCardRefreshReconfigureRejectsOldPatch(t *testing.T) {
	reset(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"a@x"}]`))
		case "/management/api-call":
			close(entered)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	accounts := seedCardRefresh(t)
	done := make(chan map[string]any, 1)
	go func() { _, body := refreshSingleAccount(accounts[0].AccountKey); done <- body }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not start")
	}
	configureFromEnv(nil)
	var body map[string]any
	select {
	case body = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("reconfigure did not cancel card probe")
	}
	if body["refreshed"] != false || body["reason"] != "configuration_changed" {
		t.Fatalf("old result committed %#v", body)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.inProgress || state.flight != nil {
		t.Fatal("old flight survived reconfigure")
	}
	for _, a := range state.last {
		if a.ObservedAt != nil {
			t.Fatal("old probe patched new generation")
		}
	}
}
