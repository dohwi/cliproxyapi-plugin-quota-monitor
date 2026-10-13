package plugin

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestNextAccountProbeDueAroundReset(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	zero, healthy, reached, disabled := 0.0, 53.0, true, true
	soon, past, later := at.Add(3*time.Minute), at.Add(-time.Minute), at.Add(2*time.Hour)
	seconds := int64(180)
	for _, tc := range []struct {
		name    string
		account quotamonitor.AccountQuota
		want    time.Duration
	}{
		{"short reset", quotamonitor.AccountQuota{Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &zero, ResetAt: &soon}}}, 3*time.Minute + probeResetGrace},
		{"weekly reset", quotamonitor.AccountQuota{Windows: []quotamonitor.QuotaWindow{{Name: "secondary", LimitReached: &reached, ResetAt: &soon}}}, 3*time.Minute + probeResetGrace},
		{"expired reset", quotamonitor.AccountQuota{Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &zero, ResetAt: &past}}}, probeIntervalResetPending},
		{"distant reset", quotamonitor.AccountQuota{Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &zero, ResetAt: &later}}}, probeIntervalShortLimited},
		{"no reset", quotamonitor.AccountQuota{Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &zero}}}, probeIntervalShortLimited},
		{"healthy reset ignored", quotamonitor.AccountQuota{Status: quotamonitor.StatusKnown, Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &healthy, ResetAt: &past}}}, probeIntervalHealthyKnown},
		{"disabled reset ignored", quotamonitor.AccountQuota{Operational: &quotamonitor.OperationalStatus{Disabled: &disabled}, Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &zero, ResetAt: &past}}}, probeIntervalDisabled},
		{"auth error backoff", quotamonitor.AccountQuota{ErrorKind: "auth", Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &zero, ResetAt: &past}}}, probeIntervalExhausted},
		{"relative reset anchored", quotamonitor.AccountQuota{LastSuccessAt: &at, Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &zero, ResetAfterSec: &seconds}}}, 3*time.Minute + probeResetGrace},
		{"relative without observation ignored", quotamonitor.AccountQuota{Windows: []quotamonitor.QuotaWindow{{Name: "primary", RemainingPercent: &zero, ResetAfterSec: &seconds}}}, probeIntervalShortLimited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nextAccountProbeDue(tc.account, false, at)
			if want := at.Add(tc.want); !got.Equal(want) {
				t.Fatalf("due = %v, want %v", got, want)
			}
		})
	}
}

func TestResetProbeBypassesFreshCacheWithoutRepeatedProbes(t *testing.T) {
	reset(t)
	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	previousNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = previousNow })
	var probes atomic.Int32
	configureTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"a@x"}]`))
		case "/management/api-call":
			if probes.Add(1) == 4 {
				_, _ = fmt.Fprintf(w, `{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":0,\"reset_at\":%d}}"}`, clock.Add(5*time.Hour).Unix())
				return
			}
			// Upstream still reports the expired exhausted window: it must be
			// retried promptly, but never auto-unlocked or continuously probed.
			_, _ = fmt.Fprintf(w, `{"status_code":200,"body":"{\"primary_window\":{\"used_percent\":100,\"reset_at\":%d}}"}`, clock.Add(-time.Minute).Unix())
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	first := collectAccounts(context.Background(), false)
	if first.err != nil || len(first.accounts) != 1 {
		t.Fatalf("first collection: %+v", first)
	}
	key := first.accounts[0].AccountKey
	if got := state.probeDue[key]; !got.Equal(clock.Add(probeIntervalResetPending)) {
		t.Fatalf("expired reset scheduled for %v, want %v", got, clock.Add(probeIntervalResetPending))
	}
	clock = clock.Add(probeIntervalResetPending)
	second := collectAccounts(context.Background(), false)
	if second.err != nil || probes.Load() != 2 {
		t.Fatalf("fresh cache blocked due reset probe: result=%+v probes=%d", second, probes.Load())
	}
	if len(second.accounts[0].Windows) == 0 || *second.accounts[0].Windows[0].RemainingPercent != 0 {
		t.Fatal("deadline must not automatically restore exhausted quota")
	}
	for i := 0; i < 3; i++ {
		collectAccounts(context.Background(), false)
	}
	if probes.Load() != 2 {
		t.Fatalf("expired reset bypassed retry floor: probes=%d", probes.Load())
	}
	clock = clock.Add(probeIntervalResetPending)
	if result := collectAccounts(context.Background(), false); result.err != nil || probes.Load() != 3 {
		t.Fatalf("reset not retried after bounded backoff: result=%+v probes=%d", result, probes.Load())
	}
	clock = clock.Add(probeIntervalResetPending)
	recovered := collectAccounts(context.Background(), false)
	if recovered.err != nil || probes.Load() != 4 || *recovered.accounts[0].Windows[0].RemainingPercent != 100 {
		t.Fatalf("confirmed reset did not restore quota: result=%+v probes=%d", recovered, probes.Load())
	}
	if got := state.probeDue[key]; !got.Equal(clock.Add(probeIntervalHealthyKnown)) {
		t.Fatalf("healthy account did not return to normal schedule: %v", got)
	}
}
