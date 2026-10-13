package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

func TestClaudeHealthyProbeRecoversDespiteDisabledOverage(t *testing.T) {
	reset(t)
	blocked, allowed := true, false
	zero := 0.0
	recent := now().Add(-time.Minute)
	account := quotamonitor.AccountQuota{
		AccountKey: "claude-recover", AuthIndex: "claude-auth", Provider: "claude", LastUsedAt: &recent,
		Operational: &quotamonitor.OperationalStatus{Unavailable: &blocked, Status: "error"},
		Windows:     []quotamonitor.QuotaWindow{{Name: "overage", RemainingPercent: &zero, Allowed: &allowed}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{StatusCode: 200, Body: `{"five_hour":{"utilization":10},"seven_day":{"utilization":25}}`})
	}))
	defer server.Close()
	got, _, _ := probeAccounts(context.Background(), quotamonitor.NewClient(server.URL, "key"), []quotamonitor.AccountQuota{account}, nil, nil, true)
	if len(got) != 1 || got[0].Operational.Unavailable == nil || *got[0].Operational.Unavailable || got[0].Operational.Status != "active" {
		t.Fatalf("disabled billing overage must not prevent recovery from healthy subscription probe: %+v", got)
	}
	if !sanitizeAccounts(got)[0].CurrentAccount {
		t.Fatal("recovered account must regain recent-use selection")
	}
}

func TestClaudeSuccessfulUnknownInventoryClearsPreviouslyKnownCredits(t *testing.T) {
	reset(t)
	count := 3
	expiry := now().Add(time.Hour)
	previous := quotamonitor.AccountQuota{
		AccountKey: "claude-inventory", AuthIndex: "claude-auth", Provider: "claude",
		ResetCredits: &quotamonitor.ResetCredits{AvailableCount: &count, NearestExpiry: &expiry},
	}
	for _, extra := range []string{
		``, `,"cedar_ember":null`, `,"cedar_ember":{"eligible":false,"grants":[]}`,
		`,"cedar_ember":{"eligible":true,"grants":"invalid"}`,
		`,"cedar_ember":{"eligible":true,"grants":[{"resets_left":51,"paused":false}]}`,
		`,"cedar_ember":{"eligible":true,"grants":[{"resets_left":50,"paused":false},{"resets_left":1,"paused":false}]}`,
	} {
		t.Run(extra, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(quotamonitor.APICallResponse{StatusCode: 200, Body: `{"five_hour":{"utilization":10}` + extra + `}`})
			}))
			defer server.Close()
			published := false
			got, _, _ := probeAccountsProgressive(context.Background(), quotamonitor.NewClient(server.URL, "key"), []quotamonitor.AccountQuota{previous}, []quotamonitor.AccountQuota{previous}, nil, true, nil, func(account quotamonitor.AccountQuota) {
				published = true
				if account.ResetCredits != nil {
					t.Error("progressive update must clear unconfirmed reset rights before final commit")
				}
			})
			if !published || len(got) != 1 || got[0].Status != quotamonitor.StatusKnown || got[0].Stale || got[0].ResetCredits != nil {
				t.Fatalf("unknown optional inventory must keep fresh quota but not pretend old credits were reconfirmed: %+v", got)
			}
		})
	}
}
