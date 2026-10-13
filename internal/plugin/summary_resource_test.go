package plugin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestResourceOverviewReadOnlySafeAndUnknown(t *testing.T) {
	reset(t)
	used := 25.0
	account := quotamonitor.AccountQuota{AccountKey: "private-key", AuthIndex: "private-index", Account: "secret@example.invalid", Provider: "codex", Windows: []quotamonitor.QuotaWindow{{Name: "primary", UsedPercent: &used}}}
	state.mu.Lock()
	state.configured = true
	state.snapshotComplete = true
	state.cachedAt = now()
	state.last = []quotamonitor.AccountQuota{account, {Provider: "codex"}}
	state.mu.Unlock()
	code, body := decodeMgmt(t, handleManagementHandle(request(resourceBase+"/data/accounts")))
	if code != 200 {
		t.Fatal(code)
	}
	rows, ok := body["overview"].([]any)
	if !ok {
		t.Fatal("missing server overview")
	}
	var codex map[string]any
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["provider"] == "codex" {
			codex = row
		}
	}
	if codex["remaining_percent"] != 75.0 || codex["account_count"] != 2.0 || codex["unknown_account_count"] != 1.0 {
		t.Fatalf("bad aggregate %#v", codex)
	}
	raw, _ := json.Marshal(rows)
	for _, secret := range []string{"private-key", "private-index", "secret@example.invalid"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("overview leaked private identity")
		}
	}
	state.mu.Lock()
	state.cachedAt = now().Add(-time.Hour)
	state.mu.Unlock()
	code, body = decodeMgmt(t, handleManagementHandle(request(resourceBase+"/data/accounts")))
	for _, raw := range body["overview"].([]any) {
		if row := raw.(map[string]any); row["provider"] == "codex" && row["stale"] != true {
			t.Fatal("overview lost snapshot staleness")
		}
	}
}
