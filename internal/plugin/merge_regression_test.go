package plugin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func regressionWindow(name string, mins int, reset int64) quotamonitor.QuotaWindow {
	return quotamonitor.QuotaWindow{Name: name, WindowMinutes: &mins, ResetAfterSec: &reset}
}

// These tests pin the canonical logical-dedup contract: active values win,
// distinct Antigravity groups remain separate, and the full synthetic snapshot
// retains all expected windows.
func TestMergeWindowsLogicalDedupRegression(t *testing.T) {
	t.Run("codex active/passive reset variants", func(t *testing.T) {
		passive := []quotamonitor.QuotaWindow{
			regressionWindow("primary", 300, 3600),
			regressionWindow("secondary", 10080, 7200),
		}
		active := []quotamonitor.QuotaWindow{
			regressionWindow("primary", 300, 3500),
			regressionWindow("secondary", 10080, 7100),
		}
		got := mergeWindows(passive, active)
		if len(got) != 2 {
			t.Fatalf("Codex logical windows: got %d, want 2 (actual current=%d)", len(got), len(got))
		}
	})

	t.Run("AG same-name groups remain distinct while source variants dedup", func(t *testing.T) {
		active := []quotamonitor.QuotaWindow{
			regressionWindow("group-a / 5h", 300, 3600),
			regressionWindow("group-a / weekly", 10080, 7200),
			regressionWindow("group-b / 5h", 300, 3600),
			regressionWindow("group-b / weekly", 10080, 7200),
		}
		passive := []quotamonitor.QuotaWindow{
			regressionWindow("group-a / 5h", 300, 3500),
		}
		got := mergeWindows(passive, active)
		if len(got) != 4 {
			t.Fatalf("AG logical windows: got %d, want 4 (actual current=%d)", len(got), len(got))
		}
	})
}

func TestCodexWeeklyOnlyProbeDropsObsoleteShortWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":25,"reset_after_seconds":600000},"secondary_window":null}}`
		fmt.Fprintf(w, `{"status_code":200,"body":%s}`, jsonString(body))
	}))
	defer server.Close()
	client := quotamonitor.NewClient(server.URL, "k")
	used := 100.0
	account := quotamonitor.AccountQuota{
		AccountKey: "codex", AuthIndex: "1", Provider: "codex", Source: "passive", Status: quotamonitor.StatusKnown,
		Windows: []quotamonitor.QuotaWindow{regressionWindow("primary", 300, 0), regressionWindow("secondary", 10080, 0)},
	}
	account.Windows[0].UsedPercent = &used
	account.Windows[1].UsedPercent = &used
	got := probeActive(client, []quotamonitor.AccountQuota{account}, nil)[0]
	assertWeekly := func(stage string, a quotamonitor.AccountQuota) {
		t.Helper()
		if len(a.Windows) != 1 || a.Windows[0].WindowMinutes == nil || *a.Windows[0].WindowMinutes != 10080 || a.Windows[0].RemainingPercent == nil || *a.Windows[0].RemainingPercent != 75 || a.Windows[0].ResetAfterSec == nil || *a.Windows[0].ResetAfterSec != 600000 {
			t.Fatalf("%s: want only fresh weekly quota, got %+v", stage, a.Windows)
		}
	}
	assertWeekly("active probe", got)
	olderPassive := applyLastGoodWindows([]quotamonitor.AccountQuota{account}, []quotamonitor.AccountQuota{got})[0]
	assertWeekly("older passive data", olderPassive)

	// A newer observation can report a real 5h window again.
	newer := got.LastSuccessAt.Add(time.Minute)
	account.ObservedAt = &newer
	newerPassive := applyLastGoodWindows([]quotamonitor.AccountQuota{account}, []quotamonitor.AccountQuota{got})[0]
	if len(newerPassive.Windows) != 2 {
		t.Fatalf("newer two-window observation lost a quota: %+v", newerPassive.Windows)
	}

	// New passive headers can report use in the same slot without repeating its duration.
	used = 30
	account.Windows = []quotamonitor.QuotaWindow{{Name: "primary", UsedPercent: &used}}
	partial := applyLastGoodWindows([]quotamonitor.AccountQuota{account}, []quotamonitor.AccountQuota{got})[0]
	if len(partial.Windows) != 1 || partial.Windows[0].WindowMinutes == nil || *partial.Windows[0].WindowMinutes != 10080 || partial.Windows[0].UsedPercent == nil || *partial.Windows[0].UsedPercent != 30 {
		t.Fatalf("durationless passive data invented a 5h window: %+v", partial.Windows)
	}
}

func TestCodexWeeklyOnlyMergeKeepsRealOrUnconfirmedShortWindows(t *testing.T) {
	base := []quotamonitor.QuotaWindow{regressionWindow("primary", 300, 600), regressionWindow("secondary", 10080, 600000)}
	for _, tc := range []struct {
		name      string
		fresh     []quotamonitor.QuotaWindow
		confirmed bool
		want      int
	}{
		{"passive partial weekly", []quotamonitor.QuotaWindow{regressionWindow("secondary", 10080, 120)}, false, 2},
		{"passive slot repurposed", []quotamonitor.QuotaWindow{regressionWindow("primary", 10080, 120)}, false, 1},
		{"active weekly in secondary", []quotamonitor.QuotaWindow{regressionWindow("secondary", 10080, 120)}, true, 1},
		{"legacy active partial", []quotamonitor.QuotaWindow{{Name: "secondary"}}, true, 2},
		{"active short and weekly", base, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeCodexWindowFields(base, tc.fresh, tc.confirmed)
			if len(got) != tc.want {
				t.Fatalf("want %d windows, got %+v", tc.want, got)
			}
		})
	}
}

func TestProbeCacheFullSyntheticLogicalDedupRegression(t *testing.T) {
	const agBody = `{"groups":[{"displayName":"group-a","buckets":[{"displayName":"5h","bucketId":"a-5","window":"five_hour","remainingFraction":0.8},{"displayName":"weekly","bucketId":"a-w","window":"weekly","remainingFraction":0.8}]},{"displayName":"group-b","buckets":[{"displayName":"5h","bucketId":"b-5","window":"five_hour","remainingFraction":0.8},{"displayName":"weekly","bucketId":"b-w","window":"weekly","remainingFraction":0.8}]}]}`
	const codexBody = `{"primary_window":{"used_percent":20,"reset_after_seconds":3500},"secondary_window":{"used_percent":30,"reset_after_seconds":7100}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		body := agBody
		if p.URL == "https://chatgpt.com/backend-api/wham/usage" {
			body = codexBody
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status_code":200,"header":{},"body":%s}`, jsonString(body))
	}))
	defer server.Close()

	client := quotamonitor.NewClient(server.URL, "k", quotamonitor.WithClientAdapter(quotamonitor.NewAdapter(quotamonitor.WithClock(func() time.Time { return time.Now() }))))
	accounts := make([]quotamonitor.AccountQuota, 0, 10)
	for i := 0; i < 6; i++ {
		mins := 300
		reset := int64(3600)
		accounts = append(accounts, quotamonitor.AccountQuota{Provider: "antigravity", AuthIndex: fmt.Sprint(i + 1), AccountKey: fmt.Sprintf("ag-%d", i+1), Account: fmt.Sprintf("ag-%d", i+1), Windows: []quotamonitor.QuotaWindow{{Name: "group-a / 5h", WindowMinutes: &mins, ResetAfterSec: &reset}}})
	}
	for i := 0; i < 4; i++ {
		accounts = append(accounts, quotamonitor.AccountQuota{Provider: "codex", AuthIndex: fmt.Sprint(i + 7), AccountKey: fmt.Sprintf("codex-%d", i+1), Account: fmt.Sprintf("codex-%d", i+1)})
	}
	got := probeActive(client, accounts, nil)
	count := 0
	for _, a := range got {
		count += len(a.Windows)
	}
	if count != 32 {
		extra := make([]string, 0)
		for _, a := range got {
			if a.Provider == "antigravity" && len(a.Windows) > 4 {
				extra = append(extra, a.Account)
			}
		}
		t.Fatalf("full probe/cache synthetic snapshot: got %d windows, want 32; extra=%d accounts=%v", count, count-32, extra)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
