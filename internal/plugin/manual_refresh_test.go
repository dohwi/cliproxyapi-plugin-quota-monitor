package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagementFullRefreshIgnoresProbeDueAndRetainsCooldown(t *testing.T) {
	reset(t)
	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	previousNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = previousNow })
	var probes, lists atomic.Int32
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			lists.Add(1)
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"a@x"},{"provider":"codex","auth_index":"8","account":"b@x"},{"provider":"codex","auth_index":"9","account":"disabled@x","disabled":true}]`))
		case "/management/api-call":
			probes.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":20}}"}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	initial := collectAccounts(context.Background(), false)
	if initial.err != nil || len(initial.accounts) != 3 || probes.Load() != 2 {
		t.Fatalf("initial: %+v probes=%d", initial, probes.Load())
	}
	// Management force refresh has no filter parameters: both eligible accounts
	// must be rechecked even though neither is due; disabled remains skipped.
	raw, _ := json.Marshal(managementRequest{Method: http.MethodPost, Path: mgmtBase + "/refresh"})
	for i := 0; i < 2; i++ {
		code, body := decodeMgmt(t, handleManagementHandle(raw))
		if code != http.StatusOK || body["refreshed"] != true || body["count"] != float64(3) {
			t.Fatalf("refresh %d: code=%d body=%#v", i, code, body)
		}
		if probes.Load() != 4 || lists.Load() != 2 {
			t.Fatalf("refresh %d bypassed eligibility/cooldown or due override: probes=%d lists=%d", i, probes.Load(), lists.Load())
		}
		if strings.Contains(string(handleManagementHandle(raw)), "test-management-secret") {
			t.Fatal("refresh exposed management secret")
		}
	}
	clock = clock.Add(forceCooldown + time.Second)
	code, body := decodeMgmt(t, handleManagementHandle(raw))
	if code != http.StatusOK || body["refreshed"] != true || probes.Load() != 6 || lists.Load() != 3 {
		t.Fatalf("refresh after cooldown: code=%d body=%#v probes=%d lists=%d", code, body, probes.Load(), lists.Load())
	}
	for _, req := range []managementRequest{
		{Method: http.MethodGet, Path: mgmtBase + "/refresh"},
		{Method: http.MethodPost, Path: resourceBase + "/data/refresh"},
	} {
		b, _ := json.Marshal(req)
		if code, _ := decodeMgmt(t, handleManagementHandle(b)); code != http.StatusNotFound {
			t.Fatalf("unsafe refresh route accepted: %+v code=%d", req, code)
		}
	}
}
