package plugin

import (
	"quota-monitor/internal/dashboard"
	"strings"
	"testing"
)

func TestDashboardAccountSummaryRemoved(t *testing.T) {
	for _, token := range []string{`id="kpiGrid"`, ".kpi-grid", ".kpi-card", "skeleton-kpi", "renderKPIs", "최저 가용 잔여율"} {
		if strings.Contains(dashboard.HTML, token) {
			t.Errorf("removed account summary still present: %q", token)
		}
	}
}

func TestDashboardCompactControlAlignment(t *testing.T) {
	for _, token := range []string{
		"padding: 0 14px 16px;", "--bg-body: #ffffff;", "--accent: #000000;",
		".page-heading { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center;",
		"padding: 0 30px 0 10px;", "font-family: inherit; font-size: 14px; font-weight: 500; line-height: 20px; text-align: left; text-align-last: left;",
		".toolbar { display: flex; flex-wrap: wrap; justify-content: flex-start;",
		"#dashboardContent { background: transparent; border: 0; padding: 0; min-width: 0; }",
		"margin: 0 0 12px; width: 100%; max-width: 100%; min-width: 0;",
		"flex: 0 1 210px; width: 210px; max-width: 100%;", ".filter-status-field { flex-basis: 140px; width: 140px; }",
		".filter-field label { display: block; margin-bottom: 6px; }",
		".dashboard-control-panel { --filter-control-height: 44px; }", ".compact-select { font-size: 16px; line-height: 24px; }",
		"min-height: 226px;", "margin-top: 0; min-width: 0;",
		`.cards-grid[data-count="0"] .text-empty { grid-column: 1 / -1; padding: 18px; }`,
		`var columns = Math.min(4, Math.max(1, Math.floor((grid.getBoundingClientRect().width + 10) / 290)));`,
		`container.setAttribute('data-count', String(accounts ? accounts.length : 0));`, ".manual-refresh-status:empty { display: none; }",
		".filter-actions { grid-column: 1 / -1; justify-content: flex-start; }",
		"font-size: 14px; font-weight: 600; line-height: 20px; justify-content: center;",
		".quota-value-number { width: 64px;", "text-align: right; font-variant-numeric: tabular-nums;",
	} {
		if !strings.Contains(dashboard.HTML, token) {
			t.Errorf("missing balanced control contract %q", token)
		}
	}
	if strings.Contains(dashboard.HTML, "font: 500 0.78rem/1.2 inherit") {
		t.Error("invalid font shorthand regressed")
	}
}

// The redesign changes visual hierarchy without changing data-driven sizing or status semantics.
func TestDashboardSemanticSurfaceHierarchy(t *testing.T) {
	for _, token := range []string{
		"--dashboard-top-offset: 88px;", "--border: #d4d4d8;", "--radius: 8px;",
		`<h1 data-i18n="page.heading">계정 쿼터</h1>`,
		`<label for="providerFilter" data-i18n="filter.provider">프로바이더</label>`, `<label for="endpointFilter" data-i18n="filter.endpoint">엔드포인트</label>`,
		".account-card { background: var(--bg-group); border: 1px solid transparent; border-radius: 8px;",
		"--bg-group: #f5f5f7;", "--row-selected: #ffffff;", "--row-selected-border: #e2e2e7;",
		"--status-active-bg: #ecfdf5;", "--status-error-bg: #fef2f2;",
		`.account-card[data-current="true"] { background: var(--row-selected); border: 1px solid var(--row-selected-border); }`,
		`.account-card { min-height: 226px; height: 100%; padding: 9px 10px; }`,
		`.account-card[data-locked="true"] { --reset-primary: var(--muted); --reset-weekly: var(--muted); }`,
		`if (lockedWindowInfo(acc, availability)) card.setAttribute('data-locked', 'true');`,
		"card.setAttribute('role', 'group');", "card.setAttribute('aria-label', String(acc.account || acc.alias || t('card.account')) + ' · ' + availability.label +", "container-type: inline-size;", "@container (max-width: 300px)", "@container (max-width: 240px)",
	} {
		if !strings.Contains(dashboard.HTML, token) {
			t.Errorf("missing intuitive semantic dashboard contract %q", token)
		}
	}
	for _, obsolete := range []string{
		"--card-state-bg:", "border: 2px solid var(--current-border)", "--dashboard-top-offset: 128px;", "--dashboard-top-offset: 96px;", "--dashboard-top-offset: 64px;", ".account-card::before", "--availability-accent:",
		".filter-field label { position: absolute;", "background: #f0fdf4;", "background-color: #f8f7ff;",
		"availabilityEl", ".availability-state", "filter: saturate",
	} {
		if strings.Contains(dashboard.HTML, obsolete) {
			t.Errorf("dashboard retains obsolete visual treatment %q", obsolete)
		}
	}
}
