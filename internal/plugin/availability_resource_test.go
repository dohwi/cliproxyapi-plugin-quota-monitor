package plugin

import (
	"strings"
	"testing"

	"quota-monitor/internal/dashboard"
)

func TestResourceSnapshotExposesAvailability(t *testing.T) {
	body := resourceSnapshot()
	if _, ok := body["availability"]; !ok {
		t.Fatal("resource snapshot must carry availability so the dashboard and tab bar share one number")
	}
}

func TestDashboardRendersFleetAvailabilityHeader(t *testing.T) {
	for _, token := range []string{
		`id="fleetAvailability"`, "function renderFleetAvailability(entries)",
		"renderFleetAvailability(data.availability);",
		".fleet-chip-value",
	} {
		if !strings.Contains(dashboard.HTML, token) {
			t.Errorf("dashboard missing fleet availability contract %q", token)
		}
	}
}
