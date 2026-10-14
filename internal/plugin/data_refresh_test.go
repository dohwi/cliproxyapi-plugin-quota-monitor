package plugin

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDataRefreshGlobalCooldownIsManagementPOST(t *testing.T) {
	for _, upstreamFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[upstreamFails], func(t *testing.T) {
			reset(t)
			clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
			previousNow := now
			now = func() time.Time { return clock }
			t.Cleanup(func() { now = previousNow })
			var lists, probes atomic.Int32
			configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/management/auth-files":
					lists.Add(1)
					if upstreamFails {
						http.Error(w, "do-not-expose-upstream-key", 500)
						return
					}
					_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"a@x"}]`))
				case "/management/api-call":
					probes.Add(1)
					_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":20}}"}`))
				default:
					_, _ = w.Write([]byte(`[]`))
				}
			})
			raw, _ := json.Marshal(managementRequest{Method: "POST", Path: mgmtBase + "/data/refresh", Query: map[string][]string{"confirm": {"1"}}})
			var wg sync.WaitGroup
			codes := make(chan int, 12)
			for i := 0; i < cap(codes); i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var e struct {
						Result managementResponse `json:"result"`
					}
					if err := json.Unmarshal(handleManagementHandle(raw), &e); err != nil {
						codes <- 0
						return
					}
					if e.Result.Headers["Cache-Control"][0] != "no-store" {
						codes <- 0
						return
					}
					codes <- e.Result.StatusCode
				}()
			}
			wg.Wait()
			close(codes)
			accepted, limited := 0, 0
			for code := range codes {
				switch code {
				case 200:
					accepted++
				case 429:
					limited++
				default:
					t.Fatalf("unexpected response %d", code)
				}
			}
			if accepted != 1 || limited != 11 || lists.Load() != 1 {
				t.Fatalf("global reservation failed: accepted=%d limited=%d lists=%d", accepted, limited, lists.Load())
			}
			missing, _ := json.Marshal(managementRequest{Method: "POST", Path: mgmtBase + "/data/refresh"})
			if code, _ := decodeMgmt(t, handleManagementHandle(missing)); code != 400 {
				t.Fatalf("unconfirmed refresh accepted: %d", code)
			}
			clock = clock.Add(forceCooldown + time.Second)
			code, body := decodeMgmt(t, handleManagementHandle(raw))
			if code != 200 || lists.Load() != 2 || body["refreshed"] != !upstreamFails {
				t.Fatalf("retry after cooldown: code=%d lists=%d body=%#v", code, lists.Load(), body)
			}
			if !upstreamFails && probes.Load() != 2 {
				t.Fatalf("force refresh must probe: %d", probes.Load())
			}
			for _, method := range []string{"GET", "HEAD", "PUT", "DELETE"} {
				b, _ := json.Marshal(managementRequest{Method: method, Path: mgmtBase + "/data/refresh"})
				if code, _ := decodeMgmt(t, handleManagementHandle(b)); code != 404 {
					t.Fatalf("refresh accepts %s", method)
				}
			}
		})
	}
}
