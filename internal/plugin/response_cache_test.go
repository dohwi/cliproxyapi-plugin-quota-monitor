package plugin

import (
	"bytes"
	"encoding/json"
	"quota-monitor/internal/dashboard"
	"testing"
)

// The cached dashboard envelope must equal a freshly built one byte for byte.
func TestDashboardResponseCachedMatchesFresh(t *testing.T) {
	fresh := OKEnvelope(managementResponse{StatusCode: 200, Headers: dashboardPageHeaders(), Body: []byte(dashboardHTMLForTest())})
	if !bytes.Equal(dashboardResponse(), fresh) {
		t.Fatal("cached dashboard envelope differs from a freshly built one")
	}
	if &dashboardResponse()[0] != &dashboardResponse()[0] {
		t.Fatal("dashboard envelope not reused")
	}
}

func TestMgmtResponseNoStoreHeaders(t *testing.T) {
	var env struct {
		OK     bool
		Result managementResponse
	}
	raw := mgmtResponseNoStore(429, map[string]string{"error": "x"})
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	h := env.Result.Headers
	if env.Result.StatusCode != 429 || h["Cache-Control"][0] != "no-store" || h["Content-Type"][0] != "application/json" || h["X-Content-Type-Options"][0] != "nosniff" {
		t.Fatalf("unexpected response: %+v", env.Result)
	}
	var plain struct{ Result managementResponse }
	_ = json.Unmarshal(mgmtResponse(200, map[string]string{}), &plain)
	if _, ok := plain.Result.Headers["Cache-Control"]; ok {
		t.Fatal("mgmtResponse must not set Cache-Control")
	}
}

func dashboardHTMLForTest() string { return dashboard.HTML }
