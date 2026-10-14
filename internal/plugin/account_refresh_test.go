package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func cardRefreshRequest(key string) []byte {
	raw, _ := json.Marshal(managementRequest{Method: "POST", Path: mgmtBase + "/data/refresh", Query: map[string][]string{"confirm": {"1"}, "account_key": {key}}})
	return raw
}

func TestCardRefreshOnlyProbesTargetAndPreservesSnapshot(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fails], func(t *testing.T) {
			reset(t)
			var lists, probes atomic.Int32
			configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/management/auth-files":
					lists.Add(1)
					_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"a@x"},{"provider":"codex","auth_index":"8","account":"b@x"}]`))
				case "/management/api-call":
					var req map[string]any
					_ = json.NewDecoder(r.Body).Decode(&req)
					if req["auth_index"] != "7" && req["auth_index"] != "8" {
						t.Errorf("wrong target %#v", req)
					}
					probes.Add(1)
					if fails {
						_, _ = w.Write([]byte(`{"status_code":500,"body":"upstream-secret"}`))
						return
					}
					_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":20}}"}`))
				default:
					_, _ = w.Write([]byte(`[]`))
				}
			})
			// Build the snapshot without active work so only the card request can probe.
			state.mu.Lock()
			client := state.client
			state.mu.Unlock()
			accounts, err := client.FetchAuthFiles(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			remaining := 67.0
			for i := range accounts {
				accounts[i].Windows = []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &remaining}}
			}
			cached := time.Now().Add(-time.Minute)
			state.mu.Lock()
			state.last = cloneAccounts(accounts)
			state.cachedAt = cached
			state.lastSuccessAt = cached
			state.snapshotComplete = true
			state.order = accountOrder(accounts)
			state.mu.Unlock()
			sibling := cloneAccounts(accounts[1:])[0]
			code, body := decodeMgmt(t, handleManagementHandle(cardRefreshRequest(accounts[0].AccountKey)))
			if code != 200 || body["refreshed"] != !fails || probes.Load() != 1 || lists.Load() != 1 {
				t.Fatalf("code=%d body=%#v probes=%d lists=%d", code, body, probes.Load(), lists.Load())
			}
			state.mu.Lock()
			got := cloneAccounts(state.last)
			cacheAt := state.cachedAt
			state.mu.Unlock()
			if !reflect.DeepEqual(got[1], sibling) || !cacheAt.Equal(cached) {
				t.Fatal("partial refresh changed sibling or global freshness")
			}
			if fails && (!got[0].Stale || *got[0].Windows[0].RemainingPercent != 67) {
				t.Fatalf("lost last-good snapshot %#v", got[0])
			}
			if !fails && (got[0].ObservedAt == nil || *got[0].Windows[0].RemainingPercent != 80) {
				t.Fatalf("target not refreshed %#v", got[0])
			}
			if strings.Contains(string(handleManagementHandle(request(mgmtBase+"/data/accounts"))), "upstream-secret") {
				t.Fatal("upstream secret leaked")
			}
			code, _ = decodeMgmt(t, handleManagementHandle(cardRefreshRequest(accounts[0].AccountKey)))
			if code != 429 || probes.Load() != 1 {
				t.Fatal("per-card cooldown bypassed")
			}
			code, _ = decodeMgmt(t, handleManagementHandle(cardRefreshRequest("not-an-account")))
			if code != 404 {
				t.Fatalf("unknown key=%d", code)
			}
			code, body = decodeMgmt(t, handleManagementHandle(cardRefreshRequest(accounts[1].AccountKey)))
			if code != 200 || body["refreshed"] != !fails || probes.Load() != 2 || lists.Load() != 1 {
				t.Fatalf("sibling must have independent cooldown: code=%d body=%#v probes=%d", code, body, probes.Load())
			}
		})
	}
}

func TestCardRefreshConfirmationAndBusy(t *testing.T) {
	reset(t)
	raw, _ := json.Marshal(managementRequest{Method: "POST", Path: mgmtBase + "/data/refresh", Query: map[string][]string{"account_key": {"a"}}})
	if code, _ := decodeMgmt(t, handleManagementHandle(raw)); code != 400 {
		t.Fatal("unconfirmed accepted")
	}
	if code, _ := decodeMgmt(t, handleManagementHandle(cardRefreshRequest("a"))); code != 503 {
		t.Fatal("unconfigured accepted")
	}
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("must not contact upstream") })
	state.mu.Lock()
	state.last = []quotamonitor.AccountQuota{{AccountKey: "a", AuthIndex: "7", Provider: "codex"}, {AccountKey: "native", Provider: "gemini"}}
	state.flight = &collectionFlight{cancel: func() {}}
	state.mu.Unlock()
	if code, _ := decodeMgmt(t, handleManagementHandle(cardRefreshRequest("a"))); code != 409 {
		t.Fatal("overlapping flight accepted")
	}
	if code, _ := decodeMgmt(t, handleManagementHandle(cardRefreshRequest("native"))); code != 422 {
		t.Fatal("unsupported provider accepted")
	}
	raw, _ = json.Marshal(managementRequest{Method: "POST", Path: mgmtBase + "/data/refresh", Query: map[string][]string{"confirm": {"1"}, "account_key": {"a", "native"}}})
	if code, _ := decodeMgmt(t, handleManagementHandle(raw)); code != 400 {
		t.Fatal("ambiguous target accepted")
	}
}
