package plugin

import (
	"os/exec"
	"quota-monitor/internal/dashboard"
	"strings"
	"testing"
)

func TestDashboardUniformCardsAndWrapping(t *testing.T) {
	for _, token := range []string{
		"grid-auto-rows: 1fr;",
		"grid-template-rows: auto auto;",
		".quota-value-number { width: 64px; font-size: 1.18rem; line-height: 1.25;",
		".ag-limit-value { font-size: 0.88rem; line-height: 1.25;",
		"container-type: inline-size;",
		"@container (max-width: 300px)",
		".quota-values { grid-template-columns: minmax(0, 1fr); }",
		".quota-value-row:nth-child(odd) { padding-right: 0; border-right: 0; }",
		"@container (max-width: 240px)",
		".ag-limit-reset { grid-column: 1 / -1; }",
		".ag-limit-item { display: grid; grid-template-columns: minmax(0,1fr) 54px minmax(0, 88px);",
		".ag-limit-item { grid-template-columns: minmax(0, 1fr) 54px; row-gap: 2px; }",
		"--status-active-bg: #f5faf7;", "--status-limited-bg: #fcf8f4;",
		"--account-border: var(--border);",
		"border: 1px solid var(--account-border);",
		".quota-lock-title { display: inline-flex; align-items: center; justify-content: center; gap: 5px;",
		`<div class="quota-lock-title"><svg class="quota-lock-icon"`,
		`.account-card[data-locked="true"] > .credit-summary { filter: grayscale(1); }`,
		".card-quota { flex: 1 0 auto; display: flex; flex-direction: column; justify-content: center;",
		"heading.appendChild(header);", "heading.appendChild(freshnessEl.firstChild);",
		"quotaBody.appendChild(lockEl);", "quotaBody.appendChild(noWin);",

		".card-meta-left { grid-column: 1 / -1;",
		".prov-name { display: inline-flex; flex-wrap: wrap;",
	} {
		if !strings.Contains(dashboard.HTML, token) {
			t.Errorf("missing uniform, wrapping card layout %q", token)
		}
	}
	for _, forbidden := range []string{
		"grid-auto-rows: auto;", "grid-template-rows: 20px 14px;",
		"--card-state-border:", ".quota-pending { margin-top: auto;",
		"gap: 1px; margin-top: auto;", "card.appendChild(lockEl);", "card.appendChild(noWin);",
		".quota-value-row:nth-child(odd) { padding-right: 8px; }",
		".ag-limit-item { display: grid; grid-template-columns: minmax(0,1fr) auto auto;",

		`.cards-grid[data-count="1"] {`, `.cards-grid[data-count="2"] {`, `.cards-grid[data-count="3"] {`,
	} {
		if strings.Contains(dashboard.HTML, forbidden) {
			t.Errorf("content-dependent or clipping layout regressed: %q", forbidden)
		}
	}
	start := strings.Index(dashboard.HTML, ".account-alias {")
	if start < 0 {
		t.Fatal("missing account alias style")
	}
	alias := strings.SplitN(dashboard.HTML[start:], "}", 2)[0]
	if strings.Contains(alias, "overflow: hidden") || strings.Contains(alias, "nowrap") || !strings.Contains(alias, "overflow-wrap: anywhere") {
		t.Error("account aliases must be fully visible, wrapping even unbroken strings")
	}
}

func TestDashboardAntigravityLockedFamilyFlatCentered(t *testing.T) {
	for _, token := range []string{
		".ag-family-section.is-locked { margin: 0; padding: 6px 0; border: 0; border-top: 1px solid rgba(148,163,184,0.28); border-radius: 0; background: transparent; }",
		".ag-family-lock { display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 52px; text-align: center; }",
		".ag-family-lock-title { display: inline-flex; align-items: center; justify-content: center; gap: 5px;",
		".ag-family-lock-time { margin-top: 3px; font-size: 1.02rem; line-height: 1.2; font-weight: 850; letter-spacing: -0.01em; color: #c2410c; font-variant-numeric: tabular-nums; text-align: center; }",
		".ag-family-lock-context { width: min(100%, 240px); margin-top: 5px; padding-top: 5px; border-top: 1px solid rgba(148,163,184,0.28); color: #64748b; text-align: center;",
		".account-card[data-provider=\"antigravity\"][data-locked=\"true\"] > .card-heading,",
		".account-card[data-provider=\"antigravity\"][data-locked=\"true\"] > .card-quota { filter: none; }",
		".account-card[data-provider=\"antigravity\"][data-locked=\"true\"] { --reset-primary: #c2410c; --reset-weekly: #4338ca; }",
		"<div class=\"ag-family-lock-title\">'+lockTitle+'</div>",
	} {
		if !strings.Contains(dashboard.HTML, token) {
			t.Errorf("missing AG flat centered lock style or markup %q", token)
		}
	}
	for _, forbidden := range []string{
		"border: 1px solid #cbd5e1; border-radius: 8px; background: rgba(241,245,249,0.78);",
		"grid-template-columns: 18px minmax(0,1fr);",
		".ag-family-lock > svg.ag-lock-padlock { grid-row:",
	} {
		if strings.Contains(dashboard.HTML, forbidden) {
			t.Errorf("AG lock retains nested box or off-center grid %q", forbidden)
		}
	}
}

func TestDashboardGridWidthIndependentOfFilteredCount(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	script := `
const vm = require('vm');
const source = process.argv[1].match(/function alignAccountGrid\(\) \{[\s\S]*?\n\}/)[0];
let width = 1300, count = 8, columns;
const grid = { style: { setProperty(key, value) { columns = value; } },
  getBoundingClientRect() { return {width}; }, getAttribute() { return String(count); } };
const context = { document: { getElementById() { return grid; } } };
vm.createContext(context); vm.runInContext(source, context);
for (const n of [8, 3, 2, 1, 0]) {
  count = n; context.alignAccountGrid();
  if (columns !== 'repeat(4, minmax(0, 1fr))') throw new Error('filter changed card width: '+columns);
}
for (const [w, expected] of [[250, 1], [590, 2], [900, 3], [1300, 4], [1800, 4]]) {
  width = w; context.alignAccountGrid();
  if (columns !== 'repeat('+expected+', minmax(0, 1fr))') throw new Error('responsive capacity: '+columns);
}
console.log('OK_GRID_WIDTH');
`
	out, err := nodeDashboardCmd(t, node, script).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK_GRID_WIDTH") {
		t.Fatalf("responsive grid width contract: %v\n%s", err, out)
	}
}

func TestDashboardHeaderTimestampRemoved(t *testing.T) {
	for _, token := range []string{
		"updateTimestamp", "updateRelative", "updateLastUpdated", "setFreshnessState",
		"relativeUpdateTimer", "tickRelativeUpdate", "formatDashboardTimestamp",
		"lastUpdateTime", "lastObservedTime", "getLatestObservedTime", "isCacheExpired",
		".update-status", ".update-timestamp", ".update-relative",
	} {
		if strings.Contains(dashboard.HTML, token) {
			t.Errorf("obsolete header timestamp code remains: %q", token)
		}
	}
	for _, token := range []string{
		"function relativeTime", "function getAccountObservedTime", "function renderAccountFreshness",
		"startResetCountdownTick", "data.cached_at", "staleNoticeBox", "aria-busy",
	} {
		if !strings.Contains(dashboard.HTML, token) {
			t.Errorf("account freshness or synchronization behavior missing: %q", token)
		}
	}
}
