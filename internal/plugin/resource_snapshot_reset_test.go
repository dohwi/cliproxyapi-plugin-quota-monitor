package plugin

import (
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestResourceSnapshotDerivesMissingWindowResetAt(t *testing.T) {
	reset(t)
	cachedAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	observedAt := cachedAt.Add(-time.Minute)
	resetAfter := int64(90)
	state.mu.Lock()
	state.cachedAt = cachedAt
	state.last = []quotamonitor.AccountQuota{
		{
			AccountKey: "observed", Provider: "codex", ObservedAt: &observedAt,
			Windows: []quotamonitor.QuotaWindow{{Name: "primary", ResetAfterSec: &resetAfter}},
		},
		{
			AccountKey: "cached", Provider: "codex",
			Windows: []quotamonitor.QuotaWindow{{Name: "secondary", ResetAfterSec: &resetAfter}},
		},
	}
	state.mu.Unlock()

	body := resourceSnapshot()
	accounts := body["accounts"].([]safeAccount)
	if got := accounts[0].Windows[0].ResetAt; got == nil || !got.Equal(observedAt.Add(90*time.Second)) {
		t.Fatalf("account observed_at reset_at = %v, want %v", got, observedAt.Add(90*time.Second))
	}
	if got := accounts[1].Windows[0].ResetAt; got == nil || !got.Equal(cachedAt.Add(90*time.Second)) {
		t.Fatalf("cached_at fallback reset_at = %v, want %v", got, cachedAt.Add(90*time.Second))
	}
}

func TestResourceSnapshotPreservesExplicitWindowResetAt(t *testing.T) {
	reset(t)
	cachedAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	explicit := cachedAt.Add(2 * time.Hour)
	resetAfter := int64(90)
	state.mu.Lock()
	state.cachedAt = cachedAt
	state.last = []quotamonitor.AccountQuota{{
		AccountKey: "explicit", Provider: "codex",
		Windows: []quotamonitor.QuotaWindow{{Name: "primary", ResetAfterSec: &resetAfter, ResetAt: &explicit}},
	}}
	state.mu.Unlock()

	accounts := resourceSnapshot()["accounts"].([]safeAccount)
	if got := accounts[0].Windows[0].ResetAt; got == nil || !got.Equal(explicit) {
		t.Fatalf("explicit reset_at = %v, want %v", got, explicit)
	}
}

func TestResourceSnapshotUsesStableCachedAtForDerivedResetAt(t *testing.T) {
	reset(t)
	cachedAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	resetAfter := int64(90)
	state.mu.Lock()
	state.cachedAt = cachedAt
	state.last = []quotamonitor.AccountQuota{{
		AccountKey: "stale", Provider: "codex",
		Windows: []quotamonitor.QuotaWindow{{Name: "primary", ResetAfterSec: &resetAfter}},
	}}
	state.mu.Unlock()

	first := resourceSnapshot()["accounts"].([]safeAccount)[0].Windows[0].ResetAt
	second := resourceSnapshot()["accounts"].([]safeAccount)[0].Windows[0].ResetAt
	want := cachedAt.Add(90 * time.Second)
	if first == nil || second == nil || !first.Equal(want) || !second.Equal(want) {
		t.Fatalf("derived reset_at values = %v, %v; want stable %v", first, second, want)
	}
}
