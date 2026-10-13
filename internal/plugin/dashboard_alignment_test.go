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
		"padding: 0 14px 16px;", "--bg-body: #f7f8fa;", "--accent: #2563eb;",
		".page-heading { display: grid; grid-template-columns: minmax(0, 1fr); align-items: end;",
		"padding: 0 30px 0 10px;", "font-family: inherit; font-size: 0.875rem; font-weight: 500; line-height: 1.2; text-align: left; text-align-last: left;",
		".toolbar { display: flex; flex-wrap: wrap; justify-content: flex-start;",
		"#dashboardContent { background: transparent; border: 0; padding: 0; min-width: 0; }",
		"margin: 0 0 12px; width: 100%; max-width: 100%; min-width: 0;",
		"flex: 0 1 210px; width: 210px; max-width: 100%;", ".filter-status-field { flex-basis: 140px; width: 140px; }",
		".filter-field label { display: block; margin-bottom: 6px; }",
		".dashboard-control-panel { --filter-control-height: 44px; }", ".compact-select { font-size: 1rem; }",
		"min-height: 226px;", "margin-top: 0; min-width: 0;",
		`.cards-grid[data-count="0"] .text-empty { grid-column: 1 / -1; padding: 18px; }`,
		`var columns = Math.min(4, Math.max(1, Math.floor((grid.getBoundingClientRect().width + 10) / 290)));`,
		`container.setAttribute('data-count', String(accounts ? accounts.length : 0));`, ".manual-refresh-status:empty { display: none; }",
		".filter-actions { grid-column: 1 / -1; justify-content: flex-start; }",
		"#fullRefreshBtn { background: #fff; border-color: #dfb7b7; color: #a43a3a; }",
		"#fullRefreshBtn:hover:not(:disabled) { background: #fff5f5; border-color: #c88989; color: #8b2e2e; }",
		"#fullRefreshBtn:focus-visible { outline-color: #a43a3a; }",
		"font-size: 0.875rem; font-weight: 600; line-height: 1.2; justify-content: center;",
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
		"--dashboard-top-offset: 24px;", "--dashboard-top-offset: 16px;", "--border: #e4e7ec;", "--radius: 12px;",
		`<h1>계정 쿼터</h1>`, "계정별 남은 쿼터와 초기화 시간을 한눈에 확인하세요.",
		`<label for="providerFilter">프로바이더</label>`, `<label for="endpointFilter">엔드포인트</label>`,
		".account-card { background: var(--surface); border: 1px solid var(--account-border);",
		`.account-card[data-availability="active"] { --card-state-bg: var(--status-active-bg); }`,
		`.account-card[data-availability="warning"] { --card-state-bg: var(--status-warning-bg); }`,
		`.account-card[data-availability="limited"] { --card-state-bg: var(--status-limited-bg); }`,
		`.account-card[data-availability="unavailable"] { --card-state-bg: var(--status-error-bg); }`,
		`.account-card[data-availability="pending"] { --card-state-bg: var(--status-pending-bg);`,
		`.account-card[data-availability="stale"] { --card-state-bg: var(--status-stale-bg);`,
		`.account-card[data-availability="disabled"] { --card-state-bg: var(--status-disabled-bg);`,
		"background: var(--card-state-bg);", "--account-border: var(--border);",
		"--status-active-bg: #e7f3eb;", "--status-pending-bg: #eaf2f8;",
		"--status-warning-bg: #f8f0d9;", "--status-limited-bg: #faeadc;",
		"--status-error-bg: #f8e5e5;", "--status-stale-bg: #e9eff7;",
		"--status-disabled-bg: #eceff3;", "--status-unknown-bg: #eff3f7;",
		"--current-border: #2563eb;",
		`.account-card[data-current="true"] { border: 2px solid var(--current-border); padding: 9px 10px; }`,
		`.account-card { min-height: 226px; height: 100%; padding: 9px 10px; }`,
		`.account-card[data-current="true"] { padding: 8px 9px; }`,
		`.account-card[data-locked="true"] { --card-state-bg: var(--status-disabled-bg); --reset-primary: #475569; --reset-weekly: #475569; }`,
		`if (lockedWindowInfo(acc, availability)) card.setAttribute('data-locked', 'true');`,
		"card.setAttribute('role', 'group');", "card.setAttribute('aria-label', String(acc.account || acc.alias || '계정') + ' · ' + availability.label +", "container-type: inline-size;", "@container (max-width: 300px)", "@container (max-width: 240px)",
	} {
		if !strings.Contains(dashboard.HTML, token) {
			t.Errorf("missing intuitive semantic dashboard contract %q", token)
		}
	}
	for _, obsolete := range []string{
		"--dashboard-top-offset: 128px;", "--dashboard-top-offset: 96px;", "--dashboard-top-offset: 64px;", ".account-card::before", "--availability-accent:",
		".filter-field label { position: absolute;", "background: #f0fdf4;", "background-color: #f8f7ff;",
		"availabilityEl", ".availability-state", "filter: saturate",
	} {
		if strings.Contains(dashboard.HTML, obsolete) {
			t.Errorf("dashboard retains obsolete visual treatment %q", obsolete)
		}
	}
}
