package dashboard

// HTML is the self-contained dashboard page for quota-monitor.
const HTML = `<!DOCTYPE html>
<html lang="ko" data-motion="on" data-motion-style="material">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>계정별 남은 쿼터</title>
<style>
  :root {
    --bg-body: #f7f8fa;
    --surface: #ffffff;
    --surface-lavender: #faf8ff;
    --border: #e4e7ec;
    --border-subtle: #edf2f7;
    --text: #0f172a;
    --muted: #475569;
    --accent: #2563eb;
    --accent-bg: #eef4fb;
    --accent-hover: #1d4ed8;
    --radius: 12px;
    --filter-control-height: 38px;
    --filter-control-radius: 8px;
    --filter-control-border: #c7ced8;
    --filter-control-bg: #ffffff;
    --filter-control-hover-bg: #f3f7fc;
    --filter-control-focus: #2f6fb0;
    --filter-label-color: #667085;
    --filter-gap: 10px;
    /* Keep the page heading close to the content on every viewport. */
    --dashboard-top-offset: 24px;

    /* Provider palette tokens — badge text/bg only, NOT card bg/border */
    --prov-ag-bg: #f2efff;
    --prov-ag-border: #ddd6fe;
    --prov-ag-text: #5b4bb7;
    --prov-codex-bg: #eaf8f6;
    --prov-codex-border: #bfe8e2;
    --prov-codex-text: #147a72;
    --prov-claude-text: #a44a2c;
    --prov-neutral-bg: #f1f5f9;
    --prov-neutral-border: #cbd5e1;
    --prov-neutral-text: #334155;

    /* Semantic card surfaces make available, limited and unavailable states scannable. */
    --status-active-bg: #f5faf7;
    --status-active-border: #d3e6db;
    --status-active-text: #065f46;

    --status-pending-bg: #f7fafc;
    --status-pending-border: #d6e5ee;
    --status-pending-text: #0c4a6e;

    --status-warning-bg: #fcfaf3;
    --status-warning-border: #e9dfbf;
    --status-warning-text: #78350f;

    --status-limited-bg: #fcf8f4;
    --status-limited-border: #eadbcd;
    --status-limited-text: #9a3412;

    --status-error-bg: #fcf6f6;
    --status-error-border: #ead3d3;
    --status-error-text: #991b1b;

    --status-stale-bg: #f6f8fb;
    --status-stale-border: #d6e0ed;
    --status-stale-text: #1e3a5f;

    --status-disabled-bg: #f8f9fb;
    --status-disabled-border: #cbd5e1;
    --status-disabled-text: #334155;

    --status-unknown-bg: #f8fafc;
    --status-unknown-border: #94a3b8;
    --status-unknown-text: #475569;

    /* Numeric remaining-percentage threshold colors */
    --pct-high: #047857;
    --pct-med: #2563eb;
    --pct-warn: #b45309;
    --pct-low: #dc2626;
    --pct-unknown: #64748b;

    /* Recent usage stays independent of quota availability. */
    --account-border: var(--border);
    --current-border: #2563eb;
    /* Reset countdown accent colors */
    --reset-primary: #c2410c;
    --reset-weekly: #4338ca;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  @font-face { font-family: 'Manrope'; src: url('/v0/resource/plugins/quota-monitor/assets/manrope-variable.woff2') format('woff2'); font-style: normal; font-weight: 200 800; font-display: swap; unicode-range: U+0000-024F, U+1E00-1EFF, U+2000-206F, U+20A0-20CF; }
  @font-face { font-family: 'SUIT'; src: url('/v0/resource/plugins/quota-monitor/assets/suit-regular.woff2') format('woff2'); font-style: normal; font-weight: 400; font-display: swap; }
  @font-face { font-family: 'SUIT'; src: url('/v0/resource/plugins/quota-monitor/assets/suit-semibold.woff2') format('woff2'); font-style: normal; font-weight: 600; font-display: swap; }
  html { scrollbar-gutter: stable; }
  body {
    background-color: var(--bg-body);
    color: var(--text);
    font-family: 'Manrope', 'SUIT', -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    line-height: 1.45;
    padding: 0 14px 16px;
    font-variant-numeric: tabular-nums;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .container { max-width: 1480px; margin: 0 auto; padding-top: var(--dashboard-top-offset); min-width: 0; }
  .page-heading { display: grid; grid-template-columns: minmax(0, 1fr); align-items: end; gap: 16px; margin-bottom: 18px; min-width: 0; }
  .page-heading-copy { min-width: 0; }
  .page-heading h1 { font-size: 1.5rem; font-weight: 800; line-height: 1.3; letter-spacing: -0.04em; }
  .page-heading p { margin-top: 4px; font-size: 0.8125rem; line-height: 20px; color: var(--muted); }
  @media (max-width: 767px) {
    .page-heading { grid-template-columns: minmax(0, 1fr); gap: 12px; margin-bottom: 14px; }
    .page-heading h1 { font-size: 1.3rem; }
  }
  /* Specificity beats .ui-ripple-host { position: relative } so the icon stays pinned to the card corner. */
  .account-card > .card-heading .card-refresh, .account-card .card-refresh.ui-ripple-host { position: absolute; top: 7px; right: 7px; z-index: 1; width: 28px; height: 28px; min-height: 28px; padding: 0; border: 1px solid transparent; border-radius: 8px; background: transparent; color: var(--muted); }
  .card-refresh svg { width: 15px; height: 15px; flex-shrink: 0; }
  .account-card .card-refresh:hover:not(:disabled) { color: var(--text); background: rgba(255,255,255,0.7); border-color: var(--border); }
  .card-refresh[aria-busy="true"] svg { animation: card-refresh-spin 0.9s linear infinite; }
  @keyframes card-refresh-spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .card-refresh[aria-busy="true"] svg { animation: none; } }
  .account-card .card-meta-left { padding-right: 32px; min-height: 24px; }
  .card-refresh .ico-check { display: none; }
  .card-refresh.is-done .ico-refresh { display: none; }
  .card-refresh.is-done .ico-check { display: block; }
  .account-card .card-refresh.is-done { color: #16a34a; }
  .card-refresh-status { font-size: .75rem; color: var(--muted); overflow-wrap: anywhere; }
  .card-refresh-status:empty { display: none; }
  /* Fleet availability is one panel; providers are split by thin separators. */
  .fleet-availability { display: flex; align-items: stretch; margin: 0 0 12px; padding: 0; list-style: none; background: var(--surface); border: 1px solid var(--border); border-radius: var(--filter-control-radius); overflow: hidden; }
  .fleet-availability:empty { display: none; }
  .fleet-chip { flex: 1 1 0; display: flex; align-items: center; gap: 8px; min-width: 0; height: var(--filter-control-height); padding: 0 14px; }
  .fleet-chip + .fleet-chip { border-left: 1px solid var(--border); }
  @media (max-width: 760px) {
    .fleet-availability { flex-direction: column; }
    .fleet-chip + .fleet-chip { border-left: 0; border-top: 1px solid var(--border); }
  }
  .fleet-chip-label { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 0.8125rem; font-weight: 600; color: var(--filter-label-color); }
  .fleet-chip-value { flex: 0 0 auto; font-size: 0.875rem; font-weight: 800; letter-spacing: -0.01em; font-variant-numeric: tabular-nums; }
  .fleet-chip-sep { color: var(--muted); font-weight: 500; margin: 0 8px; }
  .fleet-chip-count { flex: 0 0 auto; font-size: 0.75rem; color: var(--muted); white-space: nowrap; }
  .card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; margin-bottom: 12px; }
  .btn { background-color: var(--accent); color: #ffffff; border: none; padding: 6px 12px; font-size: 0.8125rem; font-weight: 500; border-radius: 6px; cursor: pointer; transition: background-color 0.15s ease; display: inline-flex; align-items: center; justify-content: center; gap: 6px; white-space: nowrap; font-family: inherit; line-height: 1.2; }
  .btn:hover:not(:disabled) { background-color: var(--accent-hover); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-secondary { background-color: #ffffff; color: #1e293b; border: 1px solid var(--border); }
  .btn-secondary:hover:not(:disabled) { background-color: var(--surface-lavender); border-color: #cbd5e1; }
  .input { padding: 6px 10px; border: 1px solid var(--filter-control-border); border-radius: var(--filter-control-radius); font-size: 0.8125rem; outline: none; background: var(--filter-control-bg); color: var(--text); min-width: 0; }
  .input:focus-visible, .btn:focus-visible { outline: 2px solid var(--filter-control-focus); outline-offset: 2px; border-color: var(--filter-control-focus); }
  .warning-box { background-color: var(--status-warning-bg); border: 1px solid var(--status-warning-border); color: var(--status-warning-text); padding: 8px 12px; border-radius: 8px; margin-bottom: 12px; font-size: 0.8125rem; }
  .error-box { background-color: var(--status-error-bg); border: 1px solid var(--status-error-border); color: var(--status-error-text); padding: 8px 12px; border-radius: 8px; margin-bottom: 12px; font-size: 0.8125rem; }
  .stale-notice { background-color: var(--status-warning-bg); border: 1px solid var(--status-warning-border); color: var(--status-warning-text); padding: 8px 12px; border-radius: 8px; margin-bottom: 12px; font-size: 0.8125rem; display: flex; align-items: center; justify-content: space-between; gap: 8px; flex-wrap: wrap; }
  .sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }

  /* One top control panel for filters and manual refresh. */
  #dashboardContent { background: transparent; border: 0; padding: 0; min-width: 0; }
  .dashboard-control-panel { padding: 12px; background: var(--surface); border: 1px solid var(--border); border-radius: 12px; margin: 0 0 12px; width: 100%; max-width: 100%; min-width: 0; }
  .toolbar { display: flex; flex-wrap: wrap; justify-content: flex-start; align-items: end; gap: var(--filter-gap); min-width: 0; }
  .filter-field { flex: 0 1 210px; width: 210px; max-width: 100%; min-width: 0; position: relative; }
  .filter-endpoint-field { flex-basis: 175px; width: 175px; }
  .filter-status-field { flex-basis: 140px; width: 140px; }
  .filter-field label { display: block; margin-bottom: 6px; }
  .filter-label, .filter-field label { color: var(--filter-label-color); font-size: 0.75rem; font-weight: 600; line-height: 1.2; white-space: nowrap; text-align: left; }
  .compact-select-wrap { position: relative; min-width: 0; }
  .compact-select-wrap::after { position: absolute; top: 50%; right: 10px; width: 7px; height: 7px; border-right: 1.5px solid var(--filter-label-color); border-bottom: 1.5px solid var(--filter-label-color); content: ""; pointer-events: none; transform: translateY(-65%) rotate(45deg); opacity: 0.75; }
  .compact-select { width: 100%; min-width: 0; height: var(--filter-control-height); min-height: var(--filter-control-height); padding: 0 30px 0 10px; border: 1px solid var(--filter-control-border); border-radius: var(--filter-control-radius); outline: none; appearance: none; -webkit-appearance: none; background: var(--filter-control-bg); color: var(--text); font-family: inherit; font-size: 0.875rem; font-weight: 500; line-height: 1.2; text-align: left; text-align-last: left; text-overflow: ellipsis; white-space: nowrap; transition: border-color 0.15s ease, background-color 0.15s ease; }
  .compact-select:hover:not(:disabled) { background: var(--filter-control-hover-bg); border-color: #98a2b3; }
  .compact-select:focus-visible { outline: 2px solid var(--filter-control-focus); outline-offset: 2px; border-color: var(--filter-control-focus); }
  .compact-select:disabled { background: #f2f4f7; color: var(--filter-label-color); cursor: not-allowed; opacity: 0.75; }
  .filter-actions { display: flex; flex-wrap: wrap; justify-content: flex-start; gap: var(--filter-gap); min-width: 0; }
  .filter-actions .filter-reset-button { width: auto; min-width: 86px; }
  .manual-refresh-status:empty { display: none; }
  .manual-refresh-status { margin-top: 8px; color: var(--muted); font-size: 0.8125rem; text-align: left; }
  .filter-reset-button { min-height: var(--filter-control-height); height: var(--filter-control-height); padding: 0 13px; border: 1px solid var(--filter-control-border); border-radius: var(--filter-control-radius); background: var(--filter-control-bg); color: #182230; font-family: inherit; font-size: 0.875rem; font-weight: 600; line-height: 1.2; justify-content: center; }
  #fullRefreshBtn { background: #fff; border-color: #dfb7b7; color: #a43a3a; }
  #fullRefreshBtn:hover:not(:disabled) { background: #fff5f5; border-color: #c88989; color: #8b2e2e; }
  #fullRefreshBtn:focus-visible { outline-color: #a43a3a; }
  .filter-reset-button:hover:not(:disabled) { background: var(--filter-control-hover-bg); border-color: #98a2b3; }
  .filter-reset-button:focus-visible { outline: 2px solid var(--filter-control-focus); outline-offset: 2px; border-color: var(--filter-control-focus); }


  .segmented-group { display: inline-flex; background: #f1f5f9; border-radius: 7px; padding: 2px; gap: 2px; flex-wrap: wrap; }
  .seg-btn { border: 1px solid transparent; background: transparent; padding: 4px 10px; font-size: 0.75rem; font-weight: 500; color: #334155; border-radius: 5px; cursor: pointer; transition: all 0.15s ease; display: inline-flex; align-items: center; gap: 5px; line-height: 1; white-space: nowrap; }
  .seg-btn:hover { color: var(--text); background: rgba(255,255,255,0.6); }
  .seg-btn.active { background: #ffffff; color: var(--accent); border-color: #cbd5e1; font-weight: 600; }
  .seg-btn:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; border-color: var(--accent); }
  .seg-btn svg { width: 14px; height: 14px; display: inline-flex; align-self: center; flex-shrink: 0; }

  /* All rows share the tallest content-driven card height, including sparse states. */
  .cards-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 245px), 1fr)); grid-auto-rows: 1fr; align-items: stretch; gap: 10px; }
  @media (min-width: 1280px) { .cards-grid { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
  @media (min-width: 900px) and (max-width: 1279px) { .cards-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
  @media (min-width: 560px) and (max-width: 899px) { .cards-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  @media (max-width: 559px) { .cards-grid { grid-template-columns: 1fr; } }

  /* Three stable regions: identity above, quota centered, optional credits below. */
  .card-heading { display: flex; flex-direction: column; gap: 6px; min-width: 0; }
  .card-quota { flex: 1 0 auto; display: flex; flex-direction: column; justify-content: center; min-width: 0; }
  .quota-groups { display: flex; flex-direction: column; gap: 5px; margin-top: 0; min-width: 0; }
  /* Fill the shared row while allowing it to grow for the largest card's wrapped content. */
  .account-card { background: var(--surface); border: 1px solid var(--account-border); border-radius: var(--radius); padding: 10px 11px; display: flex; flex-direction: column; gap: 6px; min-width: 0; width: 100%; min-height: 226px; height: 100%; position: relative; overflow-wrap: anywhere; container-type: inline-size; }
  /* Color the complete surface, not a tiny line; explicit labels remain authoritative. */
  /* Deepen card surfaces only; notices retain their softer semantic backgrounds. */
  .account-card {
    --status-active-bg: #e7f3eb;
    --status-pending-bg: #eaf2f8;
    --status-warning-bg: #f8f0d9;
    --status-limited-bg: #faeadc;
    --status-error-bg: #f8e5e5;
    --status-stale-bg: #e9eff7;
    --status-disabled-bg: #eceff3;
    --status-unknown-bg: #eff3f7;
    --card-state-bg: var(--status-unknown-bg); background: var(--card-state-bg);
  }
  .account-card[data-availability="active"] { --card-state-bg: var(--status-active-bg); }
  .account-card[data-availability="warning"] { --card-state-bg: var(--status-warning-bg); }
  .account-card[data-availability="limited"] { --card-state-bg: var(--status-limited-bg); }
  .account-card[data-availability="unavailable"] { --card-state-bg: var(--status-error-bg); }
  .account-card[data-availability="pending"] { --card-state-bg: var(--status-pending-bg); }
  .account-card[data-availability="stale"] { --card-state-bg: var(--status-stale-bg); }
  .account-card[data-availability="disabled"] { --card-state-bg: var(--status-disabled-bg); }
  /* Locked quotas are visually inactive; keep countdowns readable and text selectable. */
  .account-card[data-locked="true"] { --card-state-bg: var(--status-disabled-bg); --reset-primary: #475569; --reset-weekly: #475569; }
  .account-card[data-locked="true"] > .card-heading,
  .account-card[data-locked="true"] > .card-quota,
  .account-card[data-locked="true"] > .credit-summary { filter: grayscale(1); }
  /* Multi-model card isolation: preserve sibling model visibility when only one family is exhausted */
  .account-card[data-provider="antigravity"][data-locked="true"] > .card-heading,
  .account-card[data-provider="antigravity"][data-locked="true"] > .card-quota { filter: none; }
  .account-card[data-provider="antigravity"][data-locked="true"] { --reset-primary: #c2410c; --reset-weekly: #4338ca; }
  /* Only recent usage gets a strong border, with a matching content inset. */
  .account-card[data-current="true"] { border: 2px solid var(--current-border); padding: 9px 10px; }

  /* Numeric quota rows integrated into the card: no meter/bar and no nested card. */
  .quota-group { min-width: 0; margin-top: 0; padding: 6px 0 0; border: 0; border-top: 1px solid rgba(148,163,184,0.30); border-radius: 0; background: transparent; }
  .quota-group:first-child { padding-top: 6px; border-top: 1px solid rgba(148,163,184,0.30); }
  .quota-group[data-group-status="disabled"] { opacity: 0.75; }
  .quota-lock-state { display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 58px; text-align: center; }
  .quota-lock-icon { width: 14px; height: 14px; flex: 0 0 auto; color: #64748b; }
  .quota-lock-title { display: inline-flex; align-items: center; justify-content: center; gap: 5px; max-width: 100%; font-size: 0.82rem; font-weight: 800; color: #334155; }
  .quota-lock-time { margin-top: 3px; font-size: 1.05rem; line-height: 1.15; font-weight: 800; color: #c2410c; font-variant-numeric: tabular-nums; }
  .quota-lock-time.quota-reset-primary, .ag-family-lock-time.quota-reset-primary, .quota-reset-primary { color: var(--reset-primary); }
  .quota-lock-time.quota-reset-weekly, .ag-family-lock-time.quota-reset-weekly, .quota-reset-weekly { color: var(--reset-weekly); }
  .quota-lock-note { margin-top: 3px; font-size: 0.72rem; color: #475569; }
  .quota-lock-weekly { width: min(100%, 240px); margin-top: 5px; padding-top: 5px; border-top: 1px solid rgba(148,163,184,0.35); color: #64748b; text-align: center; }
  .quota-lock-weekly-summary { display: inline; font-size: 0.65rem; font-weight: 700; line-height: 1.35; overflow-wrap: anywhere; }
  .quota-lock-weekly strong { font-size: 0.78rem; color: #334155; font-variant-numeric: tabular-nums; }
  .quota-lock-weekly small { font-size: 0.65rem; font-weight: 600; }
  .quota-values { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); column-gap: 10px; row-gap: 2px; align-items: start; }
  .quota-value-row { display: grid; grid-template-columns: minmax(0, 1fr) 64px; grid-template-rows: auto auto; align-items: baseline; column-gap: 6px; padding: 4px 0; min-width: 0; }
  .quota-value-row:nth-child(odd) { padding-right: 12px; border-right: 1px solid rgba(148,163,184,0.28); }
  .quota-value-label { min-width: 0; font-size: 0.7rem; font-weight: 700; color: #475569; white-space: normal; overflow-wrap: anywhere; }
  .quota-value-number { width: 64px; font-size: 1.18rem; line-height: 1.25; font-weight: 800; color: var(--text); text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; letter-spacing: -0.035em; }
  .quota-value-reset { grid-column: 1 / -1; align-self: end; min-width: 0; color: var(--muted); font-size: 0.6rem; line-height: 1.35; text-align: right; white-space: normal; overflow-wrap: anywhere; }

  /* Antigravity: one card, split into Gemini/Claude top-bottom sections. */
  .ag-family-stack { display: flex; flex-direction: column; gap: 8px; }
  .ag-family-section { min-width: 0; padding-top: 8px; border-top: 1px solid rgba(148,163,184,0.28); display: flex; flex-direction: column; gap: 4px; }
  .ag-family-section:first-child { padding-top: 0; border-top: 0; }
  /* family heading: inline icon only, title tooltip and section aria-label provide names */
  .ag-family-head { margin: 0 0 1px 0; display: inline-flex; align-items: center; width: fit-content; line-height: 1; color: #334155; }
  .ag-family-icon { width: 14px; height: 14px; flex-shrink: 0; display: inline-block; vertical-align: middle; }
  .ag-family-limits { display: flex; flex-direction: column; gap: 3px; width: 100%; min-width: 0; }
  .ag-limit-item { display: grid; grid-template-columns: minmax(0,1fr) 54px minmax(0, 88px); align-items: baseline; column-gap: 6px; min-width: 0; width: 100%; }
  .ag-limit-label { min-width: 0; font-size: 0.65rem; font-weight: 650; color: #64748b; white-space: normal; overflow-wrap: anywhere; }
  .ag-limit-value { font-size: 0.88rem; line-height: 1.25; font-weight: 800; font-variant-numeric: tabular-nums; white-space: nowrap; text-align: right; }
  .ag-limit-reset { min-width: 0; font-size: 0.58rem; line-height: 1.35; color: #64748b; text-align: right; white-space: normal; overflow-wrap: anywhere; }
  .ag-family-section.is-locked { margin: 0; padding: 6px 0; border: 0; border-top: 1px solid rgba(148,163,184,0.28); border-radius: 0; background: transparent; }
  .ag-family-section.is-locked:first-child { padding-top: 0; border-top: 0; }
  .ag-family-section.is-locked .ag-family-lock { width: 100%; }
  .ag-family-lock { display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 52px; text-align: center; }
  .ag-family-lock-title { display: inline-flex; align-items: center; justify-content: center; gap: 5px; max-width: 100%; font-size: 0.78rem; font-weight: 800; color: #334155; }
  .ag-family-lock-title .ag-lock-padlock { width: 13px; height: 13px; flex: 0 0 auto; color: #64748b; }
  .ag-family-lock-title .ag-family-icon { width: 13px; height: 13px; flex: 0 0 auto; }
  .ag-family-lock-time { margin-top: 3px; font-size: 1.02rem; line-height: 1.2; font-weight: 850; letter-spacing: -0.01em; color: #c2410c; font-variant-numeric: tabular-nums; text-align: center; }
  .ag-family-lock-context { width: min(100%, 240px); margin-top: 5px; padding-top: 5px; border-top: 1px solid rgba(148,163,184,0.28); color: #64748b; text-align: center; font-size: 0.65rem; line-height: 1.35; overflow-wrap: anywhere; }
  .ag-family-lock-context strong { font-size: 0.78rem; font-weight: 750; color: #1e293b; font-variant-numeric: tabular-nums; }
  .ag-family-lock-context .quota-reset-weekly { color: var(--reset-weekly); font-weight: 650; }
  .quota-pending { margin-top: 0; padding-top: 5px; border-top: 1px solid rgba(148,163,184,0.24); color: var(--muted); font-size: 0.64rem; }
  .credit-summary { display: flex; flex-direction: column; gap: 4px; margin-top: 0; padding-top: 4px; border-top: 1px solid var(--border-subtle); font-size: 0.75rem; color: var(--muted); min-width: 0; max-width: 100%; overflow-wrap: anywhere; }
  .credit-summary strong { color: var(--text); }
  .credit-reset-summary { display: flex; flex-direction: column; align-items: stretch; min-width: 0; max-width: 100%; overflow-wrap: anywhere; }
  .credit-api-summary { display: flex; flex-wrap: wrap; align-items: baseline; min-width: 0; max-width: 100%; overflow-wrap: anywhere; }
  .reset-credit-summary { min-width: 0; max-width: 100%; overflow-wrap: anywhere; }
  .credit-reset-primary { display: flex; flex-wrap: wrap; align-items: baseline; min-width: 0; max-width: 100%; overflow-wrap: anywhere; }
  .credit-summary-detail { min-width: 0; max-width: 100%; overflow-wrap: anywhere; }
  .credit-reset-expiry { display: inline-flex; flex-wrap: nowrap; align-items: flex-start; gap: 0; width: 100%; min-width: 0; padding-top: 0; color: var(--muted); line-height: 1.3; white-space: normal; }
  .credit-reset-expiry-tree { flex: 0 0 auto; white-space: pre; }
  .credit-reset-expiry-text { flex: 1 1 auto; min-width: 0; overflow-wrap: anywhere; }
  /* Observation time is secondary metadata, not a status badge. */
  .card-freshness { display: flex; align-items: center; gap: 5px; min-width: 0; margin-top: -2px; color: #475569; font-size: 0.75rem; font-weight: 500; line-height: 1.3; }
  .card-freshness::before { content: ''; width: 5px; height: 5px; flex: 0 0 auto; border-radius: 50%; background: #16a34a; }
  .card-freshness.is-aging { color: #a16207; }
  .card-freshness.is-aging::before { background: #f59e0b; }
  .card-freshness.is-old { color: #c2410c; font-weight: 600; }
  .card-freshness.is-old::before { background: #f97316; }
  .card-freshness.is-failed { color: #b91c1c; font-weight: 600; }
  .card-freshness.is-failed::before { background: #dc2626; }
  .card-freshness.is-unknown { color: #94a3b8; }
  .card-freshness.is-unknown::before { background: #94a3b8; }

  .card-header { display: block; min-width: 0; min-height: 38px; }
  .card-identity { display: grid; grid-template-columns: minmax(0, 1fr) auto; grid-template-rows: auto auto; align-items: center; gap: 3px 8px; min-width: 0; }

  /* Provider is the primary top-left identity; account alias is secondary. */
  .card-meta-left { grid-column: 1 / -1; grid-row: 1; display: flex; flex-wrap: wrap; align-items: center; gap: 5px 7px; min-width: 0; }
  .badge-provider { display: inline-flex; align-items: center; gap: 5px; padding: 0; border: 0; font-size: 0.9rem; font-weight: 750; line-height: 1.2; background: transparent; width: fit-content; min-width: 0; }
  .badge-provider-antigravity { color: var(--prov-ag-text); }
  .badge-provider-codex       { color: var(--prov-codex-text); }
  .badge-provider-claude      { color: var(--prov-claude-text); }
  .badge-provider-default     { color: var(--prov-neutral-text); }
  .badge-provider svg { width: 17px; height: 17px; display: inline-flex; align-self: center; flex-shrink: 0; }
  .prov-name { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 4px; min-width: 0; }
  .prov-sep { opacity: 0.45; font-size: 0.72rem; padding: 0 1px; user-select: none; }
  .prov-plan { font-size: 0.75rem; font-weight: 650; opacity: 0.88; }
  .account-alias { grid-column: 1; grid-row: 2; font-size: 0.78rem; font-weight: 550; color: var(--muted); min-width: 0; white-space: normal; overflow-wrap: anywhere; }
  /* Endpoint is plain metadata; reserve pills for exceptional states/actions. */
  .badge-endpoint { grid-column: 2; grid-row: 2; justify-self: end; display: inline-flex; align-items: center; color: #475569; font-size: 0.72rem; font-weight: 600; line-height: 1.2; white-space: nowrap; }
  .badge-endpoint-direct, .badge-endpoint-warp1, .badge-endpoint-warp2 { color: #64748b; }
  .badge-endpoint-unknown { color: var(--muted); }

  .skeleton-card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 10px 11px; min-height: 226px; height: 100%; display: flex; flex-direction: column; gap: 6px; }
  .skeleton-meta { display: flex; justify-content: space-between; align-items: center; }
  .skeleton-quota-stack { flex: 1; display: flex; flex-direction: column; gap: 4px; padding-top: 4px; border-top: 1px solid var(--border-subtle); }
  .skeleton-family { display: flex; flex-direction: column; gap: 3px; }
  .skeleton-family-values { display: flex; flex-direction: column; gap: 2px; }
  .skeleton-line { background: #e2e8f0; border-radius: 4px; animation: pulse 1.5s infinite ease-in-out; }
  @keyframes pulse { 0%, 100% { opacity: 0.6; } 50% { opacity: 0.25; } }
  .text-empty { color: #64748b; font-style: normal; font-weight: 400; }
  .cards-grid[data-count="0"] .text-empty { grid-column: 1 / -1; padding: 18px; }
  /* Reflow quota details by actual card width, not just viewport width. */
  @container (max-width: 300px) {
    .quota-values { grid-template-columns: minmax(0, 1fr); }
    .quota-value-row:nth-child(odd) { padding-right: 0; border-right: 0; }
  }
  @container (max-width: 240px) {
    .ag-limit-item { grid-template-columns: minmax(0, 1fr) 54px; row-gap: 2px; }
    .ag-limit-reset { grid-column: 1 / -1; }
  }

  /* Snapshot-only change cues: only the changed value/status receives this class. */
  .value-flash { animation: valueFlash 1s ease-out both; border-radius: 3px; }
  .value-flash.flash-good { background-color: #eaf5ee; color: #166534 !important; }
  .value-flash.flash-bad { background-color: #f7f1df; color: #92400e !important; }
  .value-flash.flash-danger { background-color: #f9eaea; color: #991b1b !important; }
  .value-flash.flash-neutral { background-color: #edf0f4; }
  @keyframes valueFlash { 0% { opacity: .5; } 100% { opacity: 1; } }
  /* Brief market-ticker style cue when a server observation or value changes. */
  .account-card.card-pulse { animation: cardPulse 1.35s ease-out both; }
  .account-card.card-pulse::after { content: ''; position: absolute; inset: 0; z-index: 2; border-radius: inherit; pointer-events: none; background: rgb(var(--pulse-rgb)); animation: cardPulseOverlay 1.35s ease-out both; }
  .account-card.card-pulse.flash-sync { --pulse-rgb: 37, 99, 235; }
  .account-card.card-pulse.flash-good { --pulse-rgb: 22, 163, 74; }
  .account-card.card-pulse.flash-bad { --pulse-rgb: 217, 119, 6; }
  .account-card.card-pulse.flash-danger { --pulse-rgb: 220, 38, 38; }
  .account-card.card-pulse.flash-neutral { --pulse-rgb: 100, 116, 139; }
  @keyframes cardPulse { 0%, 100% { outline: 0 solid rgba(var(--pulse-rgb), 0); } 22% { outline: 1px solid rgba(var(--pulse-rgb), 0.18); } 55% { outline: 1px solid rgba(var(--pulse-rgb), 0.08); } }
  @keyframes cardPulseOverlay { 0%, 100% { opacity: 0; } 18% { opacity: 0.045; } 42% { opacity: 0.015; } }
  @media (prefers-reduced-motion: reduce) {
    .skeleton-line { animation: none; opacity: 0.4; }
    .value-flash { animation: none; }
    .account-card.card-pulse { animation: none; }
  }
  /* Labels and controls remain left aligned; quota numerals remain right aligned. */
  /* Responsive */
  @media (max-width: 767px) {
    :root { --dashboard-top-offset: 16px; }
    body { padding: 0 10px 12px; }
    .container { width: 100%; }
    .dashboard-control-panel { padding: 10px; border-radius: 12px; }
    .toolbar { display: grid; grid-template-columns: minmax(0, 1fr); justify-content: start; gap: 12px 10px; }
    .dashboard-control-panel { --filter-control-height: 44px; }
    .filter-field { width: 100%; max-width: 100%; justify-self: stretch; }
    .filter-endpoint-field { width: 100%; }
    .compact-select { font-size: 1rem; }
    .filter-reset-button { min-height: 44px; }
    .compact-select-wrap, .compact-select { width: 100%; min-width: 0; }
    .filter-status-field { width: 100%; }
    .filter-actions .filter-reset-button { flex: 0 1 auto; width: auto; justify-content: center; }
    .cards-grid { grid-template-columns: minmax(0, 1fr); grid-auto-rows: 1fr; gap: 8px; }
    .account-card { min-height: 226px; height: 100%; padding: 9px 10px; }
    .account-card[data-current="true"] { padding: 8px 9px; }
    .card-meta-left { flex-wrap: wrap; gap: 5px; }
    .quota-values { column-gap: 8px; }
    .quota-value-row { grid-template-columns: minmax(0,1fr) 62px; column-gap: 4px; }
    .quota-value-number { width: 62px; font-size: 1.18rem; }
    .ag-family-stack { gap: 4px; }
    .ag-family-section { padding-top: 4px; }
    .ag-family-limits { gap: 2px; }
    .ag-limit-item { column-gap: 5px; }
    .ag-limit-label { font-size: 0.62rem; }
    .ag-limit-value { font-size: 0.85rem; }
    .ag-family-lock-context { font-size: 0.56rem; }
    .quota-lock-state { min-height: 62px; }
    .credit-summary { overflow-wrap: anywhere; }
  }
  @media (min-width: 360px) and (max-width: 767px) {
    .toolbar { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .filter-actions { grid-column: 1 / -1; justify-content: flex-start; }
    .filter-actions .filter-reset-button { flex: 0 1 auto; }
  }
  @media (min-width: 560px) and (max-width: 767px) {
    .cards-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  }
  @media (max-width: 359px) {
    .dashboard-control-panel { padding-right: 8px; padding-left: 8px; }
  }
  @media (min-width: 768px) and (max-width: 1199px) {
    :root { --dashboard-top-offset: 20px; }
    body { padding: 0 14px 16px; }
  }
  @media (min-width: 1200px) {
    :root { --dashboard-top-offset: 24px; }
    body { padding: 0 14px 16px; }
  }
</style>
<style data-dohwi-ui="native">
  /* Native edition of dohwi-ui Select (ui-select-trigger / content / item) and Button.
     Radix is not available in this single-file panel, so the vanilla listbox below
     mirrors its classes, keyboard model and selected-item indicator. Colors and
     control sizes still come from this dashboard's existing tokens. */
  :root { --primary: var(--accent); --primary-foreground: #ffffff; --secondary: var(--filter-control-hover-bg); --secondary-foreground: var(--text); --focus-border: var(--filter-control-focus); }
  .compact-select-wrap.is-enhanced > select.compact-select { position: absolute; inset: 0; width: 100%; height: 100%; opacity: 0; pointer-events: none; }
  .ui-select-trigger { display: block; cursor: pointer; overflow: hidden; }
  .ui-select-trigger[aria-expanded="true"] { border-color: var(--filter-control-focus); }
  .ui-select-trigger:disabled { cursor: not-allowed; }
  .ui-select-content { position: fixed; z-index: 100; overflow: hidden; display: flex; flex-direction: column; border: 1px solid var(--filter-control-border); border-radius: var(--filter-control-radius); background: var(--filter-control-bg); color: var(--text); outline: none; }
  .ui-select-viewport { min-width: 0; overflow-y: auto; overscroll-behavior: contain; padding: 4px; }
  .ui-select-item { position: relative; display: flex; align-items: center; min-height: 32px; padding: 6px 32px 6px 10px; border-radius: 6px; font-size: 0.875rem; font-weight: 500; line-height: 1.3; cursor: default; user-select: none; overflow-wrap: anywhere; }
  .ui-select-item[data-highlighted] { background: var(--secondary); color: var(--secondary-foreground); }
  .ui-select-item[aria-selected="true"] { font-weight: 600; }
  .ui-select-item-indicator { position: absolute; inset-inline-end: 10px; display: flex; color: var(--accent); }
  .ui-button:focus-visible { outline: 2px solid var(--focus-border); outline-offset: 2px; }
  .ui-button:disabled { opacity: .5; cursor: not-allowed; }
  .ui-button--default:hover:not(:disabled) { opacity: .9; }
  @media (max-width: 600px), (pointer: coarse) { .ui-select-item { min-height: 44px; font-size: max(1rem, 16px); } }
  @media (prefers-reduced-motion: reduce) { .ui-select-content { animation: none; } }
</style>
<style data-dohwi-motion="material">
  /* Material motion tokens copied from dohwi-ui/src/components/ui/motion.css.
     Only transform and opacity are animated; colors, borders and sizes never change. */
  :root {
    --motion-fast: 180ms; --motion-normal: 360ms; --motion-slow: 460ms; --motion-ripple: 620ms;
    --motion-ease: cubic-bezier(.22, 1, .36, 1); --motion-spring: cubic-bezier(.34, 1.56, .64, 1);
    --motion-dialog-shift: 18px; --motion-dialog-scale: .94; --motion-popup-scale: .9;
  }
  /* Sliding indicator: the active box travels between options instead of jumping. */
  .ui-slide-group { position: relative; isolation: isolate; }
  .ui-slide-group > .ui-slide-item { position: relative; z-index: 1; }
  .ui-slide-indicator { position: absolute; top: 0; left: 0; z-index: 0; width: 0; height: 0; pointer-events: none; opacity: 0; background: var(--slide-bg); border: 1px solid var(--slide-border); border-radius: var(--slide-radius); }
  .ui-slide-group[data-slide-ready] > .ui-slide-indicator { opacity: 1; }
  .ui-slide-group[data-slide-ready] > .ui-slide-item.active { background: transparent; border-color: transparent; }
  .ui-slide-group[data-slide-ready] > .ui-slide-item.active:hover { background: transparent; }
  .ui-button-ripple-layer { position: absolute; inset: 0; border-radius: inherit; overflow: hidden; pointer-events: none; }
  .ui-button-ripple { position: absolute; border-radius: 50%; background: currentColor; opacity: 0; pointer-events: none; transform: translate(-50%, -50%) scale(0); }
  .ui-ripple-host { position: relative; overflow: hidden; }
  @media (prefers-reduced-motion: no-preference) {
    .ui-slide-group[data-slide-animate] > .ui-slide-indicator { transition: transform var(--motion-normal) var(--motion-spring), width var(--motion-normal) var(--motion-ease), height var(--motion-normal) var(--motion-ease); }
    .ui-ripple-host:active:not(:disabled) { transform: scale(.96); }
    .ui-ripple-host { transition: transform var(--motion-fast) var(--motion-ease); }
    .ui-ripple-host .ui-button-ripple { animation: ui-material-ripple var(--motion-ripple) var(--motion-ease); }
    .is-entering { animation: ui-content-in var(--motion-slow) var(--motion-ease) both; animation-delay: var(--enter-delay, 0ms); }
    .ui-select-content { transform-origin: top center; animation: ui-popup-in var(--motion-normal) var(--motion-ease); }
    .ui-select-content[data-side='top'] { transform-origin: bottom center; --popup-y: 4px; }
    .ui-select-content[data-side='bottom'] { --popup-y: -4px; }
    .ui-select-item-indicator { animation: ui-material-indicator var(--motion-normal) var(--motion-ease); }
    .compact-select-wrap::after { transition: transform var(--motion-normal) var(--motion-spring); }
    .compact-select-wrap:has(.ui-select-trigger[aria-expanded='true'])::after { transform: translateY(-35%) rotate(225deg); }
    @keyframes ui-content-in { from { opacity: 0; transform: translate(0, 14px); } to { opacity: 1; transform: translate(0, 0); } }
    @keyframes ui-popup-in { from { opacity: 0; transform: translate(0, var(--popup-y, -4px)) scale(var(--motion-popup-scale)); } to { opacity: 1; transform: translate(0, 0) scale(1); } }
    @keyframes ui-material-ripple { 0% { opacity: .24; transform: translate(-50%, -50%) scale(0); } 45% { opacity: .16; } 100% { opacity: 0; transform: translate(-50%, -50%) scale(1); } }
    @keyframes ui-material-indicator { 0% { opacity: 0; transform: scale(.4); } 65% { opacity: 1; transform: scale(1.15); } 100% { opacity: 1; transform: scale(1); } }
    .account-card.is-entering { will-change: transform, opacity; }
  }
</style>
</head>
<body>
<div class="container">
  <header class="page-heading">
    <div class="page-heading-copy"><h1>계정 쿼터</h1><p>계정별 남은 쿼터와 초기화 시간을 한눈에 확인하세요.</p></div>
  </header>
  <div id="unconfiguredBox" class="warning-box" style="display:none;" role="alert">
    <span id="unconfiguredText">환경변수 미설정 (QUOTA_MONITOR_MGMT_URL, QUOTA_MONITOR_MGMT_KEY)</span>
  </div>
  <div id="staleNoticeBox" class="stale-notice" style="display:none;" role="status">
    <span id="staleNoticeText">데이터 갱신 실패로 이전 데이터를 표시 중입니다.</span>
    <button type="button" class="btn btn-secondary" onclick="loadDashboard(false)" aria-label="데이터 다시 시도">다시 시도</button>
  </div>
  <div id="errorBox" class="error-box" style="display:none;" role="alert" aria-live="assertive"></div>
  <div id="accountStatusLive" class="sr-only" role="status" aria-live="polite" aria-atomic="true"></div>

  <div id="dashboardContent" style="display:none;">
    <div class="dashboard-control-panel" role="toolbar" aria-label="대시보드 컨트롤">
      <div class="toolbar">
        <div class="filter-field">
          <label for="providerFilter">프로바이더</label>
          <div class="compact-select-wrap">
            <select id="providerFilter" class="compact-select" aria-label="Provider 필터">
              <option value="all">전체</option>
            </select>
          </div>
        </div>
        <div class="filter-field filter-endpoint-field">
          <label for="endpointFilter">엔드포인트</label>
          <div class="compact-select-wrap">
            <select id="endpointFilter" class="compact-select" aria-label="Endpoint 필터">
              <option value="all">전체</option>
              <option value="direct">Direct</option>
              <option value="warp1">WARP 1</option>
              <option value="warp2">WARP 2</option>
            </select>
          </div>
        </div>
        <div class="filter-field filter-status-field">
          <label for="statusFilter">상태</label>
          <div class="compact-select-wrap">
            <select id="statusFilter" class="compact-select" aria-label="상태 필터">
              <option value="all">전체</option>
              <option value="active">활성</option>
              <option value="pending">측정 대기</option>
              <option value="limited">제한됨</option>
              <option value="warning">주의</option>
              <option value="error">오류</option>
              <option value="disabled">비활성</option>
              <option value="stale">Stale</option>
              <option value="unknown">미확인</option>
            </select>
          </div>
        </div>
        <div class="filter-actions">
          <button type="button" class="btn btn-secondary filter-reset-button" id="resetFiltersBtn" aria-label="쿼터 필터 초기화">초기화</button>
          <button type="button" class="btn btn-secondary filter-reset-button" id="fullRefreshBtn">전체 갱신</button>
        </div>
      </div>
      <div id="manualRefreshStatus" class="manual-refresh-status" role="status" aria-live="polite"></div>
    </div>

    <ul id="fleetAvailability" class="fleet-availability" aria-label="전체 가용률"></ul>

    <div id="accountsList" class="cards-grid" role="region" aria-label="계정 목록"></div>
  </div>
</div>

<script>
var cachedAccounts = [];
var pollTimer = null;
var isFetching = false;
// A GET begun before a manual refresh finishes must never publish afterward.
var snapshotEpoch = 0;
var acceptedSnapshotVersion = null;
var isManualRefreshing = false;
var refreshingAccountKey = null;
var accountRefreshState = Object.create(null);
var manualRefreshCooldownUntil = 0;
var manualRefreshButtonTimer = null;
var manualRefreshSnapshotPending = false;
var MANUAL_REFRESH_TIMEOUT_MS = 60000;
var isDisplayingStale = false;
var prevHealthFailures = null;
var resetCountdownTimer = null;
var quotaSnapshot = null;
var pendingQuotaChanges = null;
var selectedProvider = 'all';
var selectedEndpoint = 'all';
var FETCH_TIMEOUT_MS = 16000;
var POLL_IN_PROGRESS_MS = 2000;
// Bounded fallback intervals for adaptive polling (ms)
var POLL_FALLBACK_ACTIVE_MS = 30000;   // active/current data
var POLL_FALLBACK_HEALTHY_MS = 90000;  // healthy accounts
var POLL_FALLBACK_STALE_MS = 300000;   // pending/stale
var POLL_FALLBACK_SLOW_MS = 900000;    // all limited/unavailable/disabled
var POLL_MAX_MS = 900000;              // 15m cap

// Req 4: Memoize static provider SVG
var _iconCache = {};

function esc(str) {
  if (str == null) return '';
  var s = String(str);
  if (typeof document !== 'undefined' && document.createElement) {
    var div = document.createElement('div');
    div.textContent = s;
    if (typeof div.innerHTML === 'string' && div.innerHTML.length > 0) return div.innerHTML;
  }
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function formatNum(n) {
  if (n == null || isNaN(n)) return '0';
  return Number(n).toLocaleString('ko-KR');
}

function fmtPct(x) {
  if (x == null || isNaN(x)) return '—';
  var s = Number(x).toFixed(1);
  if (s.slice(-2) === '.0') s = s.slice(0, -2);
  return s + '%';
}

function formatProviderLabel(p) {
  if (!p) return 'unknown';
  var lower = String(p).toLowerCase().trim();
  if (lower === 'claude') return 'claude';
  if (lower === 'openai compatible-b.ai' || lower === 'compatible-b.ai' || lower === 'b.ai') return 'b.ai';
  return p;
}

function getProviderKey(p) {
  if (!p) return 'unknown';
  var lower = String(p).toLowerCase().trim();
  if (lower === 'antigravity' || lower.indexOf('antigravity') !== -1) return 'antigravity';
  if (lower === 'codex' || lower.indexOf('codex') !== -1) return 'codex';
  if (lower === 'claude') return 'claude';
  return 'unknown';
}

function providerIcon(provider, size) {
  var s = (size && typeof size === 'number') ? size : 16;
  var cacheKey = getProviderKey(provider) + ':' + s;
  if (_iconCache[cacheKey]) return _iconCache[cacheKey];
  var result;
  var pk = getProviderKey(provider);
  if (pk === 'antigravity') {
    result = '<svg width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="currentColor" fill-rule="evenodd" aria-hidden="true" focusable="false"><path d="M21.751 22.607c1.34 1.005 3.35.335 1.508-1.508C17.73 15.74 18.904 1 12.037 1 5.17 1 6.342 15.74.815 21.1c-2.01 2.009.167 2.511 1.507 1.506 5.192-3.517 4.857-9.714 9.715-9.714 4.857 0 4.522 6.197 9.714 9.715z"></path></svg>';
  } else if (pk === 'codex') {
    result = '<svg width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="currentColor" fill-rule="evenodd" aria-hidden="true" focusable="false"><path d="M9.205 8.658v-2.26c0-.19.072-.333.238-.428l4.543-2.616c.619-.357 1.356-.523 2.117-.523 2.854 0 4.662 2.212 4.662 4.566 0 .167 0 .357-.024.547l-4.71-2.759a.797.797 0 00-.856 0l-5.97 3.473zm10.609 8.8V12.06c0-.333-.143-.57-.429-.737l-5.97-3.473 1.95-1.118a.433.433 0 01.476 0l4.543 2.617c1.309.76 2.189 2.378 2.189 3.948 0 1.808-1.07 3.473-2.76 4.163zM7.802 12.703l-1.95-1.142c-.167-.095-.239-.238-.239-.428V5.899c0-2.545 1.95-4.472 4.591-4.472 1 0 1.927.333 2.712.928L8.23 5.067c-.285.166-.428.404-.428.737v6.898zM12 15.128l-2.795-1.57v-3.33L12 8.658l2.795 1.57v3.33L12 15.128zm1.796 7.23c-1 0-1.927-.332-2.712-.927l4.686-2.712c.285-.166.428-.404.428-.737v-6.898l1.974 1.142c.167.095.238.238.238.428v5.233c0 2.545-1.974 4.472-4.614 4.472zm-5.637-5.303l-4.544-2.617c-1.308-.761-2.188-2.378-2.188-3.948A4.482 4.482 0 014.21 6.327v5.423c0 .333.143.571.428.738l5.947 3.449-1.95 1.118a.432.432 0 01-.476 0zm-.262 3.9c-2.688 0-4.662-2.021-4.662-4.519 0-.19.024-.38.047-.57l4.686 2.71c.286.167.571.167.856 0l5.97-3.448v2.26c0 .19-.07.333-.237.428l-4.543 2.616c-.619.357-1.356.523-2.117.523zm5.899 2.83a5.947 5.947 0 005.827-4.756C22.287 18.339 24 15.84 24 13.296c0-1.665-.713-3.282-1.998-4.448.119-.5.19-.999.19-1.498 0-3.401-2.759-5.947-5.946-5.947-.642 0-1.26.095-1.88.31A5.962 5.962 0 0010.205 0a5.947 5.947 0 00-5.827 4.757C1.713 5.447 0 7.945 0 10.49c0 1.666.713 3.283 1.998 4.448-.119.5-.19 1-.19 1.499 0 3.401 2.759 5.946 5.946 5.946.642 0 1.26-.095 1.88-.309a5.96 5.96 0 004.162 1.713z"></path></svg>';
  } else if (pk === 'claude') {
    result = agFamilyIcon('claude').replace('class="ag-family-icon"', 'width="'+s+'" height="'+s+'"');
  } else {
    result = '<svg width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" focusable="false"><circle cx="12" cy="12" r="8"></circle></svg>';
  }
  _iconCache[cacheKey] = result;
  return result;
}

function effectiveUsed(w) {
  if (!w) return null;
  if (w.limit_reached === true || w.allowed === false) return 100;
  if (w.used_percent !== null && w.used_percent !== undefined && !isNaN(Number(w.used_percent))) {
    return Math.min(100, Math.max(0, Number(w.used_percent)));
  }
  if (w.remaining_percent !== null && w.remaining_percent !== undefined && !isNaN(Number(w.remaining_percent))) {
    return 100 - Math.min(100, Math.max(0, Number(w.remaining_percent)));
  }
  return null;
}

// Req 2: Canonical status — precedence: disabled>error>limited>warning>stale>active>pending>unknown
function getCanonicalStatus(acc) {
  if (!acc) return { key: 'unknown', label: '미확인', tone: 'chip-unknown' };
  var op = acc.operational || {};
  var windows = Array.isArray(acc.windows) ? acc.windows : [];

  if (op.disabled === true) return { key: 'disabled', label: '비활성', tone: 'chip-disabled' };

  var hasKnownWindows = acc.status === 'known' && windows.length > 0;
  var hasError = (acc.status === 'error' || (!hasKnownWindows && op.status === 'error'));
  var isStale  = (acc.stale === true || acc.status === 'stale');
  if (hasError) {
    if (isStale) return { key: 'stale', label: 'Stale', tone: 'chip-stale' };
    return { key: 'error', label: '오류', tone: 'chip-error' };
  }

  var isLimited = false;
  var pKey = getProviderKey(acc.provider);
  for (var i = 0; i < windows.length; i++) {
    var w = windows[i] || {};
    if (pKey === 'antigravity' && !/gemini/i.test(String(w.name || ''))) continue;
    if (w.limit_reached === true || w.allowed === false) isLimited = true;
    var weu = effectiveUsed(w);
    if (weu !== null && weu >= 100) isLimited = true;
  }
  if (!hasKnownWindows) {
    if (op.unavailable === true) isLimited = true;
    if (acc.retry_after != null && Number(acc.retry_after) > 0) isLimited = true;
    if (op.next_retry_after != null && op.next_retry_after !== '') {
      var retrySec = Number(op.retry_after_seconds || op.retry_after || 0);
      if (retrySec > 0) isLimited = true;
      else { var rtDate = new Date(op.next_retry_after).getTime(); if (!isNaN(rtDate) && rtDate > Date.now()) isLimited = true; }
    }
    if (Number(op.retry_after_seconds || op.retry_after || 0) > 0) isLimited = true;
  }
  if (isLimited) return { key: 'limited', label: '제한됨', tone: 'chip-limited' };

  var isWarning = false;
  for (var j = 0; j < windows.length; j++) {
    var w2 = windows[j] || {};
    if (pKey === 'antigravity' && !/gemini/i.test(String(w2.name || ''))) continue;
    var weu2 = effectiveUsed(w2);
    if (weu2 !== null && weu2 >= 80) { isWarning = true; break; }
  }
  if (isWarning) return { key: 'warning', label: '주의', tone: 'chip-warning' };

  if (isStale) return { key: 'stale', label: 'Stale', tone: 'chip-stale' };

  if (acc.status === 'known' && windows.length > 0) {
    var hasValue = false;
    for (var k = 0; k < windows.length; k++) { if (effectiveUsed(windows[k]) !== null) { hasValue = true; break; } }
    if (hasValue) return { key: 'active', label: '활성', tone: 'chip-active' };
    return { key: 'pending', label: '측정 대기', tone: 'chip-pending' };
  }
  if (acc.status === 'known' && windows.length === 0) return { key: 'pending', label: '측정 대기', tone: 'chip-pending' };

  return { key: 'unknown', label: '미확인', tone: 'chip-unknown' };
}

// Status rank is the secondary sort key within each endpoint group.
// active=0, pending=1, warning=2, stale=3, limited=4, error=5, disabled=6, unknown=7
function statusRank(acc) {
  var key = getCanonicalStatus(acc).key;
  var order = { active:0, pending:1, warning:2, stale:3, limited:4, error:5, disabled:6, unknown:7 };
  return (key in order) ? order[key] : 7;
}

// Endpoint rank — canonical group order: Direct → WARP 1 → WARP 2 → Unknown.
var EP_BASE = { direct:0, warp1:1, warp2:2, unknown:3 };
function endpointRank(ep) {
  return (ep in EP_BASE) ? EP_BASE[ep] : 3;
}

function filterAccountsByEndpoint(accounts, selectedEp) {
  if (!selectedEp || selectedEp === 'all') return accounts.slice();
  return accounts.filter(function(acc) {
    return String(acc && acc.endpoint || 'unknown').toLowerCase() === selectedEp;
  });
}

function endpointLabel(ep) {
  return ep === 'direct' ? 'Direct' : ep === 'warp1' ? 'WARP 1' : ep === 'warp2' ? 'WARP 2' : 'Endpoint 미확인';
}

function displayedQuotaWindows(acc) {
  var windows = acc && Array.isArray(acc.windows) ? acc.windows : [];
  // Disabled pay-as-you-go overage is not an exhausted subscription window.
  if (getProviderKey(acc && acc.provider) === 'claude') {
    return windows.filter(function(w) { return !/^overage(?:[-_ ]|$)/i.test(String(w && w.name || '')); });
  }
  return windows;
}

function quotaWindowRole(w, provider) {
  var name = String(w && w.name || '').toLowerCase();
  var minutes = Number(w && w.window_minutes);
  if (getProviderKey(provider) === 'codex') {
    if (/secondary|weekly/.test(name) || minutes === 10080) return 'long';
    if (/primary|five[- ]hour|5h/.test(name) || minutes === 300) return 'short';
  } else {
    if (/weekly/.test(name) || minutes === 10080) return 'long';
    if (/five[- ]hour|5h/.test(name) || minutes === 300) return 'short';
  }
  return 'other';
}

function displayedQuotaState(acc) {
  var wins = displayedQuotaWindows(acc);
  // Antigravity card-level background/status follows Gemini only. Claude keeps
  // its independent section lock without dimming or marking the whole card.
  // When Gemini windows are absent, do NOT fall back to Claude windows; return pending.
  if (getProviderKey(acc && acc.provider) === 'antigravity') {
    wins = wins.filter(function(w) { return /gemini/i.test(String(w && w.name || '')); });
  }
  var hasValue = false, minRemaining = null, shortRemaining = null, longRemaining = null;
  for (var i = 0; i < wins.length; i++) {
    var rem = quotaNumber(wins[i]);
    if (rem === null || !isFinite(rem)) continue;
    hasValue = true;
    if (minRemaining === null || rem < minRemaining) minRemaining = rem;
    var role = quotaWindowRole(wins[i], acc && acc.provider);
    if (role === 'short' && (shortRemaining === null || rem < shortRemaining)) shortRemaining = rem;
    if (role === 'long' && (longRemaining === null || rem < longRemaining)) longRemaining = rem;
  }
  if (!hasValue) return { key:'pending', label:'측정 대기', remaining:null };
  // Stable state contract: weekly/long-term exhaustion blocks the account,
  // while 5-hour/short-term exhaustion is a temporary quota-limit state.
  if (longRemaining !== null && longRemaining <= 0) return { key:'unavailable', label:'주간 제한', remaining:longRemaining, reason:'weekly' };
  if (shortRemaining !== null && shortRemaining <= 0) return { key:'limited', label:'한도 도달', remaining:shortRemaining, reason:'5h' };
  if (minRemaining < 20) return { key:'warning', label:'소진 임박', remaining:minRemaining };
  return { key:'active', label:'활성', remaining:minRemaining };
}

function getAvailabilityState(acc) {
  var op = acc && acc.operational || {};
  var canonical = getCanonicalStatus(acc);
  if (op.disabled === true || canonical.key === 'disabled') return { key:'disabled', label:'비활성' };
  var pKey = getProviderKey(acc && acc.provider);
  var quotaState = displayedQuotaState(acc);
  if (pKey === 'antigravity') {
    // Antigravity whole-card state & background derives exclusively from Gemini windows.
    // Claude exhaustion/failure must not induce unavailable/error on the whole card.
    // If Gemini is exhausted (weekly or 5h), return that state.
    if (quotaState.key === 'unavailable' || quotaState.key === 'limited') return quotaState;
    // Missing Gemini windows => pending (never fall back to Claude).
    if (quotaState.key === 'pending') return quotaState;
    if (quotaState.key === 'warning') return quotaState;
    if (canonical.key === 'stale') return { key:'stale', label:'정보 지연' };
    return quotaState; // active
  }
  if (quotaState.key === 'unavailable' || quotaState.key === 'limited') return quotaState;
  if (quotaState.key === 'active' || quotaState.key === 'warning') {
    if (canonical.key === 'stale') return { key:'stale', label:'정보 지연' };
    return quotaState;
  }
  if (op.unavailable === true || op.status === 'error' || (acc && acc.status === 'error')) return { key:'unavailable', label:'사용 불가' };
  if (quotaState.key === 'pending') return quotaState;
  if (canonical.key === 'stale') return { key:'stale', label:'정보 지연' };
  if (op.status === 'active') return quotaState;
  return { key:'unknown', label:'상태 미확인' };
}

// Sort accounts — provider group primary, endpoint secondary, status tertiary.
function providerRank(provider) {
  var key = getProviderKey(provider);
  return key === 'antigravity' ? 0 : key === 'codex' ? 1 : key === 'claude' ? 2 : 3;
}
function sortAccountsByEndpoint(accounts) {
  var arr = accounts.slice();
  arr.sort(function(a, b) {
    var pr = providerRank(a.provider) - providerRank(b.provider);
    if (pr !== 0) return pr;
    var aep = String(a.endpoint || 'unknown').toLowerCase();
    var bep = String(b.endpoint || 'unknown').toLowerCase();
    var er = endpointRank(aep) - endpointRank(bep);
    if (er !== 0) return er;
    var sr = statusRank(a) - statusRank(b);
    if (sr !== 0) return sr;
    var ak = String(a.account_key || '');
    var bk = String(b.account_key || '');
    return ak < bk ? -1 : ak > bk ? 1 : 0;
  });
  return arr;
}

function showError(msg) {
  var el = document.getElementById('errorBox');
  if (!el) return;
  el.innerHTML = '';
  var span = document.createElement('span');
  span.textContent = msg || '데이터 사용 불가';
  el.appendChild(span);
  var btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'btn btn-secondary';
  btn.style.marginLeft = '10px';
  btn.textContent = '다시 시도';
  btn.setAttribute('aria-label', '데이터 다시 시도');
  btn.onclick = function() { loadDashboard(false); };
  el.appendChild(btn);
  el.style.display = 'block';
}

function clearError() {
  var el = document.getElementById('errorBox');
  if (!el) return;
  el.style.display = 'none';
  el.innerHTML = '';
}

function humanizeSeconds(sec) {
  if (sec == null || isNaN(sec)) return '—';
  var s = Math.round(sec);
  if (s <= 0) return '0초';
  if (s >= 86400) { var d = Math.floor(s/86400); var rh = Math.floor((s%86400)/3600); return d+'일'+(rh>0?' '+rh+'시간':''); }
  if (s >= 3600)  { var h = Math.floor(s/3600);  var m = Math.floor((s%3600)/60);   return h+'시간'+(m>0?' '+m+'분':''); }
  if (s >= 60)    { var mn = Math.floor(s/60); var rs = s%60; return mn+'분'+(rs>0?' '+rs+'초':''); }
  return s+'초';
}

function resetSeconds(w) {
  if (!w) return null;
  if (w.reset_at) {
    var t = new Date(w.reset_at).getTime();
    if (!isNaN(t)) return Math.max(0, Math.round((t - Date.now()) / 1000));
  }
  if (w.reset_after_seconds !== null && w.reset_after_seconds !== undefined && !isNaN(Number(w.reset_after_seconds))) {
    return Math.max(0, Number(w.reset_after_seconds));
  }
  return null;
}

function formatResetCountdownSeconds(sec) {
  if (sec === null || sec === undefined || isNaN(sec)) return '초기화 미확인';
  if (Number(sec) <= 0) return '초기화 시각 도달 · 갱신 대기';
  return humanizeSeconds(sec) + ' 후 초기화';
}

function formatRelativeReset(w) {
  if (!w) return '초기화 미확인';
  return formatResetCountdownSeconds(resetSeconds(w));
}

function formatResetExpiryKo(ts) {
  if (!ts) return '—';
  try {
    var d = new Date(ts);
    if (!isNaN(d.getTime())) {
      var hour24 = d.getHours();
      var hour12 = hour24 % 12 || 12;
      var period = hour24 < 12 ? '오전' : '오후';
      var minute = String(d.getMinutes()).padStart(2, '0');
      var second = String(d.getSeconds()).padStart(2, '0');
      return d.getFullYear()+'. '+(d.getMonth()+1)+'. '+d.getDate()+'. '+period+' '+hour12+':'+minute+':'+second;
    }
  } catch(e) {}
  return '—';
}

function formatTimestamp(ts) {
  return formatResetExpiryKo(ts);
}

function relativeTime(date) {
  if (!date) return '';
  var diff = Math.round((Date.now() - date.getTime()) / 1000);
  if (diff < 5) return '방금';
  if (diff < 60) return diff+'초 전';
  if (diff < 3600) return Math.floor(diff/60)+'분 전';
  if (diff < 86400) return Math.floor(diff/3600)+'시간 전';
  return Math.floor(diff/86400)+'일 전';
}

function getAccountObservedTime(account) {
  if (!account) return null;
  var latest = 0;
  var candidates = [account.last_success, account.observed_at];
  if (account.windows && Array.isArray(account.windows)) {
    for (var w = 0; w < account.windows.length; w++) {
      if (account.windows[w] && account.windows[w].observed_at) candidates.push(account.windows[w].observed_at);
    }
  }
  for (var c = 0; c < candidates.length; c++) {
    if (!candidates[c]) continue;
    var t = new Date(candidates[c]).getTime();
    if (!isNaN(t) && t > latest) latest = t;
  }
  return latest > 0 ? new Date(latest) : null;
}

function renderAccountFreshness(acc) {
  var observed = getAccountObservedTime(acc);
  if (!observed) {
    return '<div class="card-freshness is-unknown" title="이 계정의 측정 시각이 아직 없습니다">아직 확인되지 않음</div>';
  }
  var ageSeconds = Math.max(0, Math.floor((Date.now() - observed.getTime()) / 1000));
  var freshnessClass = '';
  var suffix = '';
  var availability = getAvailabilityState(acc || {});
  if (acc && (acc.error || acc.stale === true || availability.key === 'stale' || availability.key === 'error')) {
    freshnessClass = ' is-failed';
    suffix = ' · 이전 데이터';
  } else if (ageSeconds >= 2700) {
    freshnessClass = ' is-old';
    suffix = ' · 오래된 정보';
  } else if (ageSeconds >= 900) {
    freshnessClass = ' is-aging';
  }
  var exact = formatTimestamp(observed.toISOString());
  return '<div class="card-freshness'+freshnessClass+'" title="계정 상태 확인: '+esc(exact)+'">마지막 확인 '+esc(relativeTime(observed) || '방금')+esc(suffix)+'</div>';
}

// ── Adaptive one-shot setTimeout polling ──────────────────────────────────────
function computeAdaptivePollMs(nextPollHintMs, accounts) {
  // Server hints and fallback intervals remain the normal polling cadence.
  var pollMs = nextPollHintMs && nextPollHintMs > 0 ? Math.min(nextPollHintMs, POLL_MAX_MS) : null;
  // Fallback: classify accounts client-side when no server hint is available.
  var hasCurrent = false, hasActive = false, hasLimited = false, hasStale = false;
  var accs = accounts || [];
  for (var i = 0; i < accs.length; i++) {
    var acc = accs[i];
    if (acc && acc.current_account === true && acc.current_kind === 'recent' && acc.current_reason === 'recent_usage') hasCurrent = true;
    var av = getAvailabilityState(acc);
    if (av.key === 'active' || av.key === 'warning') hasActive = true;
    if (av.key === 'limited' || av.key === 'unavailable' || av.key === 'disabled') hasLimited = true;
    if (av.key === 'unknown' || av.key === 'pending') hasStale = true;
  }
  if (pollMs === null) {
    pollMs = hasCurrent ? POLL_FALLBACK_ACTIVE_MS :
             hasActive ? POLL_FALLBACK_HEALTHY_MS :
             hasStale ? POLL_FALLBACK_STALE_MS : POLL_FALLBACK_SLOW_MS;
  }
  // Only snapshot GETs are accelerated: quota reset deadlines never trigger probes.
  for (var j = 0; j < accs.length; j++) {
    var resetAcc = accs[j];
    if (!resetAcc || (resetAcc.operational || {}).disabled === true) continue;
    var wins = displayedQuotaWindows(resetAcc);
    for (var k = 0; k < wins.length; k++) {
      var w = wins[k];
      if (effectiveUsed(w) !== 100) continue;
      var sec;
      if (w.reset_at) {
        var resetAt = new Date(w.reset_at).getTime();
        if (!isFinite(resetAt)) continue;
        sec = (resetAt - Date.now()) / 1000;
      } else {
        if (w.reset_after_seconds == null || w.reset_after_seconds === '' || !isFinite(Number(w.reset_after_seconds))) continue;
        sec = resetSeconds(w);
      }
      if (sec === null || !isFinite(sec)) continue;
      var resetPollMs = sec > 0 ? Math.max(5000, (sec + 5) * 1000) : 30000;
      pollMs = Math.min(pollMs, resetPollMs);
    }
  }
  return pollMs;
}

function scheduleNextPoll(delayMs) {
  if (isManualRefreshing || manualRefreshSnapshotPending) delayMs = POLL_IN_PROGRESS_MS;
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
  if (delayMs <= 0) return;
  pollTimer = setTimeout(function() {
    pollTimer = null;
    if (document.hidden) return; // paused; will reload on visibilitychange
    if (!isFetching) loadDashboard(false);
  }, delayMs);
}

var manualRefreshStatusTimer = null;
function setManualRefreshStatus(message, dismissAfterMs) {
  if (manualRefreshStatusTimer !== null) {
    clearTimeout(manualRefreshStatusTimer);
    manualRefreshStatusTimer = null;
  }
  var status = document.getElementById('manualRefreshStatus');
  if (status) status.textContent = message;
  if (message && dismissAfterMs > 0) {
    manualRefreshStatusTimer = setTimeout(function() {
      manualRefreshStatusTimer = null;
      if (status) status.textContent = '';
    }, dismissAfterMs);
  }
}

function updateManualRefreshButton() {
  updateAccountRefreshButtons();
  if (manualRefreshButtonTimer) {
    clearTimeout(manualRefreshButtonTimer);
    manualRefreshButtonTimer = null;
  }
  var button = document.getElementById('fullRefreshBtn');
  if (!button) return;
  var seconds = Math.max(0, Math.ceil((manualRefreshCooldownUntil - Date.now()) / 1000));
  button.disabled = isManualRefreshing || seconds > 0;
  button.textContent = isManualRefreshing ? (refreshingAccountKey ? '계정 갱신 중…' : '갱신 중…') : seconds > 0 ? '전체 갱신 (' + seconds + 's)' : '전체 갱신';
  button.setAttribute('aria-busy', isManualRefreshing ? 'true' : 'false');
  if (!isManualRefreshing && seconds > 0) {
    manualRefreshButtonTimer = setTimeout(updateManualRefreshButton, 1000);
  }
}

function refreshAllAccounts() { return refreshAccounts(null); }
function refreshAccount(accountKey) { return refreshAccounts(accountKey); }
async function refreshAccounts(accountKey) {
  updateManualRefreshButton();
  var cardState = accountKey ? (accountRefreshState[accountKey] || (accountRefreshState[accountKey] = { until: 0, message: '' })) : null;
  if (isManualRefreshing || Date.now() < (cardState ? cardState.until : manualRefreshCooldownUntil)) return;
  var report = function(message, dismissAfterMs) {
    if (cardState) { cardState.message = message; updateAccountRefreshButtons(); }
    else setManualRefreshStatus(message, dismissAfterMs);
  };
  var cooldown = function() {
    if (cardState) cardState.until = Date.now() + 30000;
    else manualRefreshCooldownUntil = Date.now() + 30000;
  };
  if (typeof AbortController === 'undefined') {
    report('이 브라우저에서는 안전하게 갱신을 요청할 수 없습니다. 다른 브라우저에서 다시 시도해 주세요.');
    return;
  }

  isManualRefreshing = true;
  refreshingAccountKey = accountKey;
  updateManualRefreshButton();
  report('');
  scheduleNextPoll(POLL_IN_PROGRESS_MS);
  var controller = new AbortController();
  var timedOut = false;
  // Keep the timeout active through response parsing, beyond the 51s management deadline.
  var timer = setTimeout(function() {
    timedOut = true;
    controller.abort();
  }, MANUAL_REFRESH_TIMEOUT_MS);
  try {
    // Host resources support GET only; this confirmed action is never polled.
    var response = await fetch('/v0/resource/plugins/quota-monitor/data/refresh?confirm=1' + (accountKey ? '&account_key=' + encodeURIComponent(accountKey) : ''), {
      method: 'GET',
      redirect: 'error',
      cache: 'no-store',
      credentials: 'omit',
      signal: controller.signal
    });
    if (response.status === 429) {
      cooldown();
      report('갱신 요청 간격은 30초입니다. 잠시 후 다시 시도해 주세요.');
      return;
    }
    if (accountKey && [409, 404, 422, 503].indexOf(response.status) >= 0) {
      report(response.status === 409 ? '다른 수집이 진행 중입니다. 잠시 후 다시 시도해 주세요.' : response.status === 404 ? '계정이 목록에서 제거되었습니다. 전체 갱신으로 확인해 주세요.' : response.status === 422 ? '이 계정은 개별 갱신을 지원하지 않습니다.' : '플러그인 설정을 확인해 주세요.');
      return;
    }
    if (!response.ok) throw new Error('refresh failed');
    var data = await response.json();
    if (!data || data.refreshed !== true || data.error) {
      if (cardState) cooldown();
      report('갱신이 완료되지 않았습니다. 이전 데이터를 유지합니다. 잠시 후 다시 시도해 주세요.');
      return;
    }
    cooldown();
    if (cardState && data.truncated !== true && data.stale !== true) {
      // Plain success is signalled by a brief check icon, not persistent card text.
      cardState.message = '';
      cardState.doneUntil = Date.now() + 2500;
      updateAccountRefreshButtons();
      return;
    }
    var message = data.truncated === true
      ? '수집이 일부만 완료되었습니다(최대 128개 계정). 잠시 후 다시 시도해 주세요.'
      : '갱신 완료';
    if (data.stale === true) message += ' 이전 데이터가 포함되어 있습니다. 잠시 후 다시 시도해 주세요.';
    report(message, data.truncated === true || data.stale === true ? 0 : 3000);
  } catch (err) {
    report(timedOut
      ? '갱신 요청 시간이 초과되었습니다. 서버에서는 아직 수집 중일 수 있습니다. 잠시 후 확인하고 다시 시도해 주세요.'
      : '갱신 요청에 실패했습니다. 이전 데이터를 유지합니다. 잠시 후 다시 시도해 주세요.');
  } finally {
    clearTimeout(timer);
    isManualRefreshing = false;
    refreshingAccountKey = null;
    updateManualRefreshButton();
    snapshotEpoch++;
    manualRefreshSnapshotPending = true;
    if (isFetching) scheduleNextPoll(POLL_IN_PROGRESS_MS);
    else loadDashboard(false);
  }
}

var accountRefreshButtonTimer = null;
function updateAccountRefreshButtons() {
  if (accountRefreshButtonTimer) { clearTimeout(accountRefreshButtonTimer); accountRefreshButtonTimer = null; }
  if (!document.querySelectorAll) return;
  var ticking = false;
  document.querySelectorAll('[data-refresh-account]').forEach(function(button) {
    var key = button.getAttribute('data-refresh-account');
    var state = accountRefreshState[key] || { until: 0, message: '' };
    var seconds = Math.max(0, Math.ceil((state.until - Date.now()) / 1000));
    var busy = isManualRefreshing && refreshingAccountKey === key;
    button.disabled = isManualRefreshing || seconds > 0;
    var refreshLabel = busy ? '갱신 중…' : seconds > 0 ? '새로고침 (' + seconds + 's)' : '새로고침';
    button.title = refreshLabel;
    button.setAttribute('aria-busy', busy ? 'true' : 'false');
    var done = !busy && Date.now() < (state.doneUntil || 0);
    button.classList.toggle('is-done', done);
    if (done) button.title = '갱신 완료';
    ticking = ticking || seconds > 0 || done;
  });
  document.querySelectorAll('[data-refresh-status]').forEach(function(el) {
    var state = accountRefreshState[el.getAttribute('data-refresh-status')];
    el.textContent = state ? state.message : '';
  });
  if (ticking) accountRefreshButtonTimer = setTimeout(updateAccountRefreshButtons, 1000);
}
function initManualRefreshControls() {
  var button = document.getElementById('fullRefreshBtn');
  if (button) button.onclick = refreshAllAccounts;
  updateManualRefreshButton();
}

function startResetCountdownTick() {
  if (resetCountdownTimer) clearTimeout(resetCountdownTimer);
  resetCountdownTimer = setTimeout(function tickCountdown() {
    if (!document.hidden && cachedAccounts.length > 0) {
      applyFilters();
    }
    resetCountdownTimer = setTimeout(tickCountdown, 30000);
  }, 30000);
}

// ─────────────────────────────────────────────────────────────────────────────

function captureRenderState() {
  var active = document.activeElement;
  var path = null;
  if (active) {
    if (active.id) path = { id: active.id };
    else {
      var cursor = active;
      while (cursor && cursor.getAttribute) {
        var key = cursor.getAttribute('data-account-key');
        if (key) {
          path = { accountKey: key };
          var child = active, indices = [];
          while (child && child !== cursor && child.parentNode) {
            var siblings = child.parentNode.children || [];
            var idx = Array.prototype.indexOf.call(siblings, child);
            indices.unshift(idx);
            child = child.parentNode;
          }
          path.indices = indices;
          break;
        }
        cursor = cursor.parentNode;
      }
    }
  }
  return {
    scrollTop: (typeof window !== 'undefined' && typeof window.pageYOffset === 'number') ? window.pageYOffset : ((document.documentElement && document.documentElement.scrollTop) || (document.body && document.body.scrollTop) || 0),
    active: path,
    selectionStart: active && typeof active.selectionStart === 'number' ? active.selectionStart : null,
    selectionEnd: active && typeof active.selectionEnd === 'number' ? active.selectionEnd : null
  };
}

function restoreRenderState(saved) {
  if (!saved) return;
  var target = null;
  if (saved.active && saved.active.id) target = document.getElementById(saved.active.id);
  if (!target && saved.active && saved.active.accountKey && document.querySelector) {
    target = document.querySelector('[data-account-key="' + String(saved.active.accountKey).replace(/"/g, '\\"') + '"]');
    var indices = saved.active.indices || [];
    for (var i = 0; target && i < indices.length; i++) {
      target = target.children && target.children[indices[i]];
    }
  }
  if (target && target.focus && document.activeElement !== target) {
    try { target.focus(); } catch(e) {}
    if (saved.selectionStart !== null && target.setSelectionRange) {
      try { target.setSelectionRange(saved.selectionStart, saved.selectionEnd); } catch(e) {}
    }
  }
  if (typeof window !== 'undefined' && window.scrollTo) {
    try { window.scrollTo(0, saved.scrollTop); } catch(e) {}
  } else if (document.documentElement) {
    document.documentElement.scrollTop = saved.scrollTop;
  }
}

function renderSkeletons() {
  var content = document.getElementById('dashboardContent');
  var container = document.getElementById('accountsList');
  if (content) { content.style.display = 'block'; content.setAttribute('aria-busy','true'); }
  if (!container) return;
  var html = '';
  for (var i = 0; i < 8; i++) {
    html += '<div class="skeleton-card" aria-hidden="true">'+
      '<div class="skeleton-meta"><div class="skeleton-line" style="width:42%;height:16px;"></div><div class="skeleton-line" style="width:20%;height:13px;"></div></div>'+
      '<div class="skeleton-line" style="width:58%;height:9px;"></div>'+
      '<div class="skeleton-quota-stack">'+
        '<div class="skeleton-family"><div class="skeleton-line" style="width:14px;height:14px;border-radius:3px;"></div><div class="skeleton-family-values"><div class="skeleton-line" style="height:14px;"></div><div class="skeleton-line" style="height:14px;"></div></div></div>'+
        '<div class="skeleton-family"><div class="skeleton-line" style="width:14px;height:14px;border-radius:3px;"></div><div class="skeleton-family-values"><div class="skeleton-line" style="height:14px;"></div><div class="skeleton-line" style="height:14px;"></div></div></div>'+
      '</div></div>';
  }
  container.innerHTML = html;
}

function acceptSnapshotVersion(data) {
  var generation = data && typeof data.generation === 'number' && isFinite(data.generation) ? data.generation : null;
  var collections = data && data.health && typeof data.health.collections === 'number' && isFinite(data.health.collections) ? data.health.collections : null;
  var observed = Date.parse(data && (data.collected_at || data.cached_at || data.cachedAt) || '');
  var version = { generation: isFinite(generation) ? generation : null, collections: isFinite(collections) ? collections : null, observed: isFinite(observed) ? observed : null };
  var previous = acceptedSnapshotVersion;
  if (previous) {
    if (version.observed !== null && previous.observed !== null && version.observed < previous.observed) return false;
    // Collection counters/generations restart with the process. A newer server
    // timestamp is authoritative even when those counters reset on deployment.
    var newerObservation = version.observed !== null && previous.observed !== null && version.observed > previous.observed;
    if (!newerObservation && version.generation !== null && previous.generation !== null && version.generation < previous.generation) return false;
    var sameGeneration = version.generation === previous.generation || version.generation === null || previous.generation === null;
    if (sameGeneration) {
      if (!newerObservation && version.collections !== null && previous.collections !== null && version.collections < previous.collections) return false;
    }
  }
  // Responses omitting optional ordering metadata must not erase our watermark.
  acceptedSnapshotVersion = {
    generation: version.generation !== null ? version.generation : previous && previous.generation,
    collections: version.collections !== null ? version.collections : previous && previous.collections,
    observed: version.observed !== null ? version.observed : previous && previous.observed
  };
  return true;
}

function loadDashboard(isInitial) {
  if (isFetching) return;
  var requestEpoch = snapshotEpoch;
  manualRefreshSnapshotPending = false;
  isFetching = true;
  clearError();
  var content = document.getElementById('dashboardContent');
  if (cachedAccounts.length === 0) {
    renderSkeletons();
  } else if (content) {
    content.setAttribute('aria-busy', 'true');
  }

  var controller = typeof AbortController !== 'undefined' ? new AbortController() : null;
  var fetchTimer = controller ? setTimeout(function() {
    try { controller.abort(); } catch(e) {}
  }, FETCH_TIMEOUT_MS) : null;

  function clearFetchTimer() {
    if (fetchTimer) { clearTimeout(fetchTimer); fetchTimer = null; }
  }

  function finishFetch() {
    clearFetchTimer();
    isFetching = false;
    var c = document.getElementById('dashboardContent');
    if (c) {
      c.style.display = 'block';
      c.setAttribute('aria-busy', 'false');
    }
  }

  function handleFailure(errText) {
    if (cachedAccounts.length > 0) {
      isDisplayingStale = true;
      var staleBox = document.getElementById('staleNoticeBox');
      var staleText = document.getElementById('staleNoticeText');
      if (staleBox) {
        if (staleText) staleText.textContent = '데이터 갱신 실패로 이전 데이터를 표시 중입니다' + (errText ? ' (' + errText + ')' : '');
        staleBox.style.display = 'flex';
      }
      applyFilters();
    } else {
      showError('데이터 로드 실패: ' + (errText || 'unknown'));
      renderAccounts([]);
    }
    scheduleNextPoll(POLL_FALLBACK_STALE_MS);
  }

  fetch('/v0/resource/plugins/quota-monitor/data/accounts', { cache: 'no-store', signal: controller ? controller.signal : undefined })
    .then(function(res) {
      clearFetchTimer();
      if (!res.ok) throw new Error('API 호출 실패 (' + res.status + ')');
      return res.json();
    })
    .then(function(data) {
      if (!data) {
        handleFailure('데이터 없음');
        return null;
      }
      // Resource data is deliberately snapshot-only. The first privileged
      // collection is owned by authenticated management /status, /accounts,
      // or POST /refresh; a browser resource must never become an implicit
      // credential/probe trigger.
      return data;
    })
    .then(function(data) {
      if (!data) return;
      if (requestEpoch !== snapshotEpoch || !acceptSnapshotVersion(data)) {
        scheduleNextPoll(POLL_IN_PROGRESS_MS);
        return;
      }

      var unconfBox = document.getElementById('unconfiguredBox');
      var unconfText = document.getElementById('unconfiguredText');
      if (data.configured === false) {
        var reasonMsg = '환경변수 미설정 (QUOTA_MONITOR_MGMT_URL, QUOTA_MONITOR_MGMT_KEY)';
        if (data.reason) reasonMsg += ': ' + data.reason;
        if (unconfText) unconfText.textContent = reasonMsg;
        if (unconfBox) unconfBox.style.display = 'block';

        if (cachedAccounts.length > 0) {
          isDisplayingStale = true;
          var staleBox = document.getElementById('staleNoticeBox');
          var staleText = document.getElementById('staleNoticeText');
          if (staleBox) {
            if (staleText) staleText.textContent = '환경 미설정으로 이전 데이터를 표시 중입니다' + (data.reason ? ' (' + data.reason + ')' : '');
            staleBox.style.display = 'flex';
          }
          applyFilters();
        } else {
          renderAccounts([]);
        }
        scheduleNextPoll(POLL_FALLBACK_STALE_MS);
        return;
      } else if (unconfBox) {
        unconfBox.style.display = 'none';
      }

      if (data.error) {
        handleFailure(data.error);
        return;
      }

      var incomingAccounts = Array.isArray(data.accounts) ? data.accounts : [];
      if (Array.isArray(data.availability)) renderFleetAvailability(data.availability);

      // Empty startup remains atomic. After the first complete snapshot, an
      // in-progress refresh may expose account-local progressive updates while
      // the backend keeps committed order/current selection fixed until finalize.
      if (data.snapshot_complete !== true) {
        if (cachedAccounts.length > 0) {
          isDisplayingStale = true;
          var staleBox = document.getElementById('staleNoticeBox');
          var staleText = document.getElementById('staleNoticeText');
          if (staleBox) {
            if (staleText) staleText.textContent = '데이터 갱신 중입니다. 이전 데이터를 표시 중입니다.';
            staleBox.style.display = 'flex';
          }
          applyFilters();
        } else {
          renderSkeletons();
        }
        scheduleNextPoll(POLL_IN_PROGRESS_MS);
        return;
      }

      if (data.collection_in_progress === true) {
        var savedRenderState = captureRenderState();
        cachedAccounts = incomingAccounts;
        populateProviderSegmented(cachedAccounts);
        applyFilters();
        restoreRenderState(savedRenderState);
        scheduleNextPoll(POLL_IN_PROGRESS_MS);
        return;
      }

      var health = data.health || {};
      var lastCat = (typeof health.last_category === 'string') ? health.last_category : '';
      var curFailures = (typeof health.failures === 'number') ? health.failures : 0;
      var failuresIncreased = (prevHealthFailures !== null && curFailures > prevHealthFailures);
      prevHealthFailures = curFailures;

      var isFailureCategory = (lastCat !== '' && lastCat !== 'success');
      var isExplicitFailureReason = (data.reason && data.reason !== 'cache_expired' && data.reason !== 'not_collected' && data.reason !== '');
      var isRealFailure = failuresIncreased || isFailureCategory || isExplicitFailureReason;

      isDisplayingStale = isRealFailure;

      var sBox = document.getElementById('staleNoticeBox');
      if (sBox) {
        if (isRealFailure) {
          var sText = document.getElementById('staleNoticeText');
          var failMsg = '데이터 갱신 실패로 이전 데이터를 표시 중입니다.';
          if (data.reason && data.reason !== 'cache_expired') {
            failMsg += ' (' + data.reason + ')';
          }
          if (sText) sText.textContent = failMsg;
          sBox.style.display = 'flex';
        } else {
          sBox.style.display = 'none';
        }
      }

      pendingQuotaChanges = diffQuotaSnapshots(quotaSnapshot, incomingAccounts);
      quotaSnapshot = buildQuotaSnapshot(incomingAccounts);
      cachedAccounts = incomingAccounts;
      populateProviderSegmented(cachedAccounts);
      applyFilters();
      pendingQuotaChanges = null;

      var hint = (typeof data.next_poll_hint_ms === 'number') ? data.next_poll_hint_ms : 0;
      scheduleNextPoll(computeAdaptivePollMs(hint, cachedAccounts));
    })
    .catch(function(err) {
      clearFetchTimer();
      if (requestEpoch !== snapshotEpoch) { scheduleNextPoll(POLL_IN_PROGRESS_MS); return; }
      var msg = err && err.message ? err.message : (err && err.name === 'AbortError' ? '요청 시간 초과' : 'unknown');
      handleFailure(msg);
    })
    .finally(function() {
      finishFetch();
    });
}

// Stable fallback identity prevents a one-account array from resetting duplicate keys to index 0.
function quotaAccountKeys(accounts) { var seen={}, keys=[]; (accounts||[]).forEach(function(acc,index){acc=acc||{}; var base=String(acc.account_key || acc.endpoint || acc.name || 'account'); var n=seen[base]||0;seen[base]=n+1;keys[index]=base+'#'+n;}); return keys; }
function quotaWindowMap(acc) { var seen={}, out={}; (acc.windows||[]).forEach(function(w){w=w||{};var base=String(w.name||w.kind||'window'),n=seen[base]||0;seen[base]=n+1;out[base+'#'+n]=[quotaNumber(w),String(w.reset_at||w.reset_time||w.resets_at||'')];});return out; }
// Only server snapshots enter this comparison; countdown/filter renders never do.
function buildQuotaSnapshot(accounts) { var out={}, keys=quotaAccountKeys(accounts); (accounts||[]).forEach(function(acc,index) { acc=acc||{}; var key=keys[index], credits=acc.api_credits||{}, reset=acc.reset_credits||{}, observed=getAccountObservedTime(acc); out[key]={observed_at:observed?observed.toISOString():'',availability:getAvailabilityState(acc).key,locked:!!lockedWindowInfo(acc,getAvailabilityState(acc)),windows:quotaWindowMap(acc),credits:[credits.balance,credits.has_credits,credits.unlimited,reset.available_count,reset.applicable_available_count,reset.nearest_expiry]}; }); return out; }
function quotaTone(before, after, inverse) { var a=Number(before),b=Number(after);if(isFinite(a)&&isFinite(b)&&a!==b)return (b>a)!==!!inverse?'flash-good':'flash-bad';return 'flash-neutral'; }
function diffQuotaSnapshots(previous, accounts) { if(!previous)return {};var changed={},next=buildQuotaSnapshot(accounts),keys=quotaAccountKeys(accounts);keys.forEach(function(key){var old=previous[key],now=next[key];if(!old||!now)return;var fields={};if(old.observed_at!==now.observed_at&&now.observed_at)fields.sync='flash-sync';if(old.availability!==now.availability)fields.availability='flash-neutral';if(old.locked!==now.locked)fields.lock=now.locked?'flash-danger':'flash-good';Object.keys(old.windows).forEach(function(windowKey){var before=old.windows[windowKey],after=now.windows[windowKey];if(!after||before[0]!==after[0])fields['window:'+windowKey]=quotaTone(before[0],after&&after[0],false);if(after&&before[1]!==after[1])fields['reset:'+windowKey]='flash-neutral';});old.credits.forEach(function(v,i){if(v!==now.credits[i])fields['credit:'+i]=quotaTone(v,now.credits[i],false);});if(Object.keys(fields).length)changed[key]=fields;});return changed; }
function applyValueFlash(el, tone, label) { if(!el||!tone||!el.classList)return;var tones=['flash-good','flash-bad','flash-danger','flash-neutral'];if(el._flashTimer)clearTimeout(el._flashTimer);tones.forEach(function(c){el.classList.remove(c);});el.classList.remove('value-flash');var aria=el.getAttribute('aria-label'),title=el.getAttribute('title');el.classList.add('value-flash',tone);el.setAttribute('title',label||'서버 데이터가 변경됨');el.setAttribute('aria-label',(aria||el.textContent||'')+' · 서버 업데이트로 변경됨');el._flashTimer=setTimeout(function(){el.classList.remove('value-flash');tones.forEach(function(c){el.classList.remove(c);});if(title===null)el.removeAttribute('title');else el.setAttribute('title',title);if(aria===null)el.removeAttribute('aria-label');else el.setAttribute('aria-label',aria);el._flashTimer=null;},1000); }
function strongestQuotaTone(changes) { var rank={'flash-sync':0,'flash-neutral':1,'flash-good':2,'flash-bad':3,'flash-danger':4},best='flash-sync';Object.keys(changes||{}).forEach(function(key){var tone=changes[key];if((rank[tone]||0)>(rank[best]||0))best=tone;});return best; }
function applyCardPulse(card, tone) { if(!card||!tone||!card.classList)return;var tones=['flash-sync','flash-good','flash-bad','flash-danger','flash-neutral'];if(card._cardPulseTimer)clearTimeout(card._cardPulseTimer);tones.forEach(function(c){card.classList.remove(c);});card.classList.remove('card-pulse');void card.offsetWidth;card.classList.add('card-pulse',tone);card._cardPulseTimer=setTimeout(function(){card.classList.remove('card-pulse');tones.forEach(function(c){card.classList.remove(c);});card._cardPulseTimer=null;},1400); }
function decorateQuotaChanges(card, accountKey) { var changes=pendingQuotaChanges&&pendingQuotaChanges[String(accountKey)];if(!changes)return;applyCardPulse(card,strongestQuotaTone(changes));applyValueFlash(card.querySelector('.quota-lock-state'),changes.lock,'잠금 상태가 서버 업데이트로 변경됨');card.querySelectorAll('.ag-family-lock').forEach(function(el){applyValueFlash(el,changes.lock,'잠금 상태가 서버 업데이트로 변경됨');});var seen={};card.querySelectorAll('[data-window-name]').forEach(function(el){var base=el.getAttribute('data-window-name'),n=seen[base]||0;if(el.getAttribute('data-window-role')==='value'){seen[base]=n+1;}var key=base+'#'+n;applyValueFlash(el,changes[el.getAttribute('data-window-role')+':'+key],el.getAttribute('data-window-role')==='reset'?'재설정 기준이 서버 업데이트로 변경됨':'쿼터 값이 서버 업데이트로 변경됨');});if(Object.keys(changes).some(function(k){return k.indexOf('credit:')===0;}))applyValueFlash(card.querySelector('.credit-summary'),'flash-neutral','크레딧이 서버 업데이트로 변경됨'); }

function populateProviderSegmented(accounts) {
  var select = document.getElementById('providerFilter');
  if (!select) return;
  var accs = accounts || [];
  var provMap = {};
  var provCounts = {};
  for (var i = 0; i < accs.length; i++) {
    var pKey = formatProviderLabel(accs[i].provider || 'unknown');
    provMap[pKey] = true;
    provCounts[pKey] = (provCounts[pKey] || 0) + 1;
  }
  if (selectedProvider !== 'all' && !provMap[selectedProvider]) selectedProvider = 'all';
  while (select.firstChild) select.removeChild(select.firstChild);
  function appendOption(value, text) {
    var option = document.createElement('option');
    option.value = value;
    option.textContent = text;
    select.appendChild(option);
  }
  appendOption('all', '전체 (' + accs.length + ')');
  Object.keys(provMap).sort().forEach(function(provider) {
    appendOption(provider, provider + ' (' + (provCounts[provider] || 0) + ')');
  });
  select.value = selectedProvider;
  updateFilterTitles();
}

function updateFilterTitles() {
  ['providerFilter', 'endpointFilter', 'statusFilter'].forEach(function(id) {
    var select = document.getElementById(id);
    var option = select && select.options && select.options[select.selectedIndex];
    if (select) select.title = option ? option.textContent : '';
  });
}

function initLoadingState() {
  if (cachedAccounts.length === 0) renderSkeletons();
}

function resetFilters() {
  selectedProvider = 'all';
  selectedEndpoint = 'all';
  var provider = document.getElementById('providerFilter');
  var endpoint = document.getElementById('endpointFilter');
  var status = document.getElementById('statusFilter');
  if (provider) provider.value = 'all';
  if (endpoint) endpoint.value = 'all';
  if (status) status.value = 'all';
  applyFilters();
}

function initFilterControls() {
  var provider = document.getElementById('providerFilter');
  var endpoint = document.getElementById('endpointFilter');
  var status = document.getElementById('statusFilter');
  var reset = document.getElementById('resetFiltersBtn');
  if (provider) provider.addEventListener('change', function() { selectedProvider = provider.value || 'all'; applyFilters(); });
  if (endpoint) endpoint.addEventListener('change', function() { selectedEndpoint = endpoint.value || 'all'; applyFilters(); });
  if (status) status.addEventListener('change', applyFilters);
  if (reset) reset.addEventListener('click', resetFilters);
}

// Fleet availability is computed server-side (availability.go) so this header
// and the Herdr tab bar can never disagree. Always reflects every account,
// independent of the provider/status filters below.
function renderFleetAvailability(entries) {
  var el = document.getElementById('fleetAvailability');
  if (!el) return;
  var list = Array.isArray(entries) ? entries : [];
  // Antigravity's Gemini and Claude/GPT families collapse into one "AGY" chip.
  var chips = [];
  var agy = null;
  for (var i = 0; i < list.length; i++) {
    var e = list[i] || {};
    var rem = Number(e.remaining_percent);
    if (!isFinite(rem)) continue;
    var count = Number(e.accounts) || 0;
    if (e.key === 'antigravity_gemini' || e.key === 'antigravity_claude') {
      if (!agy) {
        agy = { label: 'AGY', title: '', values: [], accounts: 0 };
        chips.push(agy);
      }
      agy.values.push(rem);
      agy.accounts = Math.max(agy.accounts, count);
      agy.title += (agy.title ? ' / ' : '') + (e.label || e.key) + ' ' + (Math.round(rem * 10) / 10) + '%';
      continue;
    }
    chips.push({ label: e.label || e.key || '', title: (e.label || e.key || '') + ' · ' + count + '개 계정', values: [rem], accounts: count });
  }
  var html = '';
  for (var k = 0; k < chips.length; k++) {
    var c = chips[k];
    var vals = '';
    for (var v = 0; v < c.values.length; v++) {
      if (v > 0) vals += '<span class="fleet-chip-sep" aria-hidden="true">·</span>';
      vals += '<span style="color:' + remainingTone(c.values[v]) + '">' + esc(String(Math.round(c.values[v] * 10) / 10)) + '%</span>';
    }
    html += '<li class="fleet-chip" title="' + esc(c.title + (c.values.length > 1 ? ' · ' + c.accounts + '개 계정' : '')) + '"><span class="fleet-chip-label">' + esc(c.label) + '</span>' +
      '<span class="fleet-chip-value">' + vals + '</span>' +
      '<span class="fleet-chip-count">' + esc(String(c.accounts)) + '개</span></li>';
  }
  el.innerHTML = html;
}

function applyFilters() {
  updateFilterTitles();
  var stEl = document.getElementById('statusFilter');
  var st = (stEl && stEl.value) ? stEl.value : 'all';
  var filtered = [];
  for (var i = 0; i < cachedAccounts.length; i++) {
    var acc = cachedAccounts[i];
    if (selectedProvider !== 'all') {
      var normP = formatProviderLabel(acc.provider);
      if (normP !== selectedProvider && acc.provider !== selectedProvider) continue;
    }
    if (st !== 'all') {
      var canonKey = getCanonicalStatus(acc).key;
      var availKey = getAvailabilityState(acc).key;
      if (canonKey !== st && availKey !== st) continue;
    }
    filtered.push(acc);
  }

  filtered = filterAccountsByEndpoint(filtered, selectedEndpoint);
  filtered = sortAccountsByEndpoint(filtered);
  renderAccounts(filtered);
  if (typeof window !== 'undefined' && window.motionConsumeEnter) window.motionConsumeEnter();
}

function quotaNumber(w) {
  if (!w) return null;
  if (w.remaining_percent != null && !isNaN(Number(w.remaining_percent))) return Math.max(0, Math.min(100, Number(w.remaining_percent)));
  var used = effectiveUsed(w);
  return used === null ? null : 100 - used;
}

// Numeric percentage tone by remaining quota — finite/clamped, no gradients.
function remainingTone(rem) {
  if (rem === null || rem === undefined || !isFinite(rem)) return 'var(--pct-unknown)';
  var r = Math.max(0, Math.min(100, rem));
  if (r >= 60) return 'var(--pct-high)';
  if (r >= 30) return 'var(--pct-med)';
  if (r >= 10) return 'var(--pct-warn)';
  return 'var(--pct-low)';
}

function groupQuotaWindows(windows, provider) {
  var list = Array.isArray(windows) ? windows : [];
  var key = getProviderKey(provider);
  var groups = [], byKey = Object.create(null), seen = Object.create(null);
  function add(k, label, kind, win) {
    var g = byKey[k];
    if (!g) { g = { key:k, label:label||'쿼터 그룹', windows:[] }; byKey[k]=g; groups.push(g); }
    g.windows.push({ kind:kind, window:win });
  }
  list.forEach(function(w, i) {
    w = w || {};
    var name = String(w.name||'').trim(), low = name.toLowerCase(), kind = null, group = name;
    if (key === 'antigravity') {
      var family = /gemini/i.test(low) ? 'gemini' : (/claude|gpt/i.test(low) ? 'claude' : 'other');
      var duration = /weekly/i.test(low)||Number(w.window_minutes)===10080 ? 'weekly' :
                     (/five hour|5h/i.test(low)||Number(w.window_minutes)===300 ? 'five-hour' : null);
      kind = duration ? family+'-'+duration : family;
      group = 'Antigravity 쿼터';
    } else if (key === 'claude') {
      low = low.replace(/_/g, '-').replace(/^seven-day(?=-|$)/, 'weekly');
      if (/^(5h|5-hour|5-hours|five-hours)$/.test(low)) low = 'five-hour';
      if (low === '7d') low = 'weekly';
      kind = /^(five-hour|weekly(?:-(?:opus|sonnet|oauth-apps|cowork|sessions))?)$/.test(low) ? low :
             (Number(w.window_minutes)===300 ? 'five-hour' : (Number(w.window_minutes)===10080 ? low || 'weekly' : null));
      group = 'Claude 쿼터';
      if (kind) { add('claude', group, kind, w); return; }
    } else if (key === 'codex') {
      kind = /secondary/i.test(low)?'secondary':(/primary/i.test(low)?'primary':null);
      group = '요청 한도';
    } else {
      kind = /secondary|weekly/i.test(low)||Number(w.window_minutes)===10080 ? 'secondary' :
             (/primary|five hour|5h/i.test(low)||Number(w.window_minutes)===300 ? 'primary' : null);
      group = kind ? '요청 한도' : '미확인';
    }
    if (kind && !seen[group.toLowerCase()+'|'+kind]) {
      seen[group.toLowerCase()+'|'+kind] = true;
      add(group.toLowerCase(), group, kind, w);
    } else {
      add('unknown-'+i, '미확인', 'unknown', w);
    }
  });
  return groups;
}

function quotaKindLabel(kind) {
  var labels = {
    'gemini-weekly':'주간 제한', 'gemini-five-hour':'5시간 제한',
    'claude-weekly':'주간 제한', 'claude-five-hour':'5시간 제한',
    secondary:'주간 제한', primary:'5시간 제한',
    weekly:'주간 제한', 'five-hour':'5시간 제한',
    'weekly-opus':'Opus · 주간 제한', 'weekly-sonnet':'Sonnet · 주간 제한',
    'weekly-oauth-apps':'OAuth 앱 · 주간 제한', 'weekly-cowork':'Cowork · 주간 제한',
    'weekly-sessions':'Sessions · 주간 제한'
  };
  return labels[kind] || '쿼터';
}

function lockedWindowInfo(acc, availability) {
  if (!availability || (availability.key !== 'limited' && availability.key !== 'unavailable')) return null;
  var role = availability.key === 'unavailable' ? 'long' : 'short';
  var wins = displayedQuotaWindows(acc);
  var chosen = null, bestSeconds = null;
  for (var i = 0; i < wins.length; i++) {
    if (quotaWindowRole(wins[i], acc.provider) !== role) continue;
    var rem = quotaNumber(wins[i]);
    if (rem === null || rem > 0) continue;
    var sec = resetSeconds(wins[i]);
    if (chosen === null || (sec !== null && (bestSeconds === null || sec < bestSeconds))) { chosen = wins[i]; bestSeconds = sec; }
  }
  return chosen ? { window:chosen, seconds:bestSeconds, role:role } : null;
}

function renderQuotaLock(acc, availability) {
  var info = lockedWindowInfo(acc, availability);
  if (!info) return '';
  var isWeekly = info.role === 'long';
  var roleClass = isWeekly ? 'quota-reset-weekly' : 'quota-reset-primary';
  var title = isWeekly ? '주간 제한' : '5시간 제한';
  if (getProviderKey(acc.provider) === 'claude') {
    var lockGroups = groupQuotaWindows([info.window], acc.provider);
    if (lockGroups.length && lockGroups[0].windows.length) title = quotaKindLabel(lockGroups[0].windows[0].kind);
  }
  var countdown = formatResetCountdownSeconds(info.seconds);
  var ariaParts = [title+' 잠금', countdown];
  var weeklyExtra = '';
  if (info.role === 'short') {
    var wins = displayedQuotaWindows(acc);
    for (var i = 0; i < wins.length; i++) {
      if (quotaWindowRole(wins[i], acc.provider) !== 'long') continue;
      var weeklyRemaining = quotaNumber(wins[i]);
      var weeklyValue = weeklyRemaining===null?'—':fmtPct(weeklyRemaining);
      var weeklyCountdown = formatResetCountdownSeconds(resetSeconds(wins[i]));
      var weeklyAria = '주간 잔여 '+weeklyValue+', '+weeklyCountdown;
      ariaParts.push(weeklyAria);
      weeklyExtra = '<div class="quota-lock-weekly" role="group" aria-label="'+esc(weeklyAria)+'"><span class="quota-lock-weekly-summary">주간 잔여 <strong>'+esc(weeklyValue)+'</strong><span aria-hidden="true"> · </span><small class="quota-reset-weekly">'+esc(weeklyCountdown)+'</small></span></div>';
      break;
    }
  }
  return '<div class="quota-lock-state" role="status" aria-label="'+esc(ariaParts.join(', '))+'">'+
    '<div class="quota-lock-title"><svg class="quota-lock-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="5" y="10" width="14" height="10" rx="2"></rect><path d="M8 10V7a4 4 0 0 1 8 0v3"></path></svg><span>'+esc(title)+'</span></div><div class="quota-lock-time '+roleClass+'">'+esc(countdown)+'</div><div class="quota-lock-note">현재 계정 잠금</div>'+weeklyExtra+'</div>';
}

function percentageState(window) {
  var rem = quotaNumber(window);
  var value = rem === null || !isFinite(rem) ? null : Math.max(0, Math.min(100, rem));
  return { remaining: value, color: remainingTone(value) };
}

function agFamilyIcon(family) {
  if (family === 'gemini') {
    return '<svg class="ag-family-icon" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" focusable="false" role="img">'
      +'<title>안티그래비티</title>'
      +'<path d="M11.04 19.32Q12 21.51 12 24q0-2.49.93-4.68.96-2.19 2.58-3.81t3.81-2.55Q21.51 12 24 12q-2.49 0-4.68-.93a12.3 12.3 0 0 1-3.81-2.58 12.3 12.3 0 0 1-2.58-3.81Q12 2.49 12 0q0 2.49-.96 4.68-.93 2.19-2.55 3.81a12.3 12.3 0 0 1-3.81 2.58Q2.49 12 0 12q2.49 0 4.68.96 2.19.93 3.81 2.55t2.55 3.81"/>'
      +'</svg>';
  }
  return '<svg class="ag-family-icon" viewBox="0 0 94 94" fill="currentColor" aria-hidden="true" focusable="false" role="img">'
    +'<title>Claude</title>'
    +'<path d="M18.7657 62.4437L37.1822 52.1167L37.4857 51.2122L37.1822 50.7085H36.2715L33.1852 50.5208L22.6615 50.2391L13.5545 49.8636L4.70044 49.3942L2.47428 48.9248L0.399902 46.1553L0.602281 44.794L2.47428 43.5266L5.15579 43.7613L11.0754 44.1837L19.98 44.794L26.4055 45.1695L35.9679 46.1553H37.4857L37.6881 45.545L37.1822 45.1695L36.7774 44.794L27.5692 38.5508L17.6021 31.9791L12.3908 28.1769L9.60812 26.2524L8.19147 24.4686L7.58433 20.5256L10.1141 17.7091L13.5545 17.9438L14.4146 18.1785L17.9056 20.8542L25.343 26.6279L35.0572 33.7629L36.4739 34.9364L37.0443 34.5514L37.1316 34.2792L36.4739 33.1996L31.212 23.6706L25.596 13.9539L23.0663 9.91695L22.4086 7.52296C22.1538 6.51831 22.0038 5.68714 22.0038 4.65957L24.8877 0.716544L26.5067 0.200195L30.4025 0.716544L32.0215 2.12477L34.4501 7.66379L38.3458 16.3478L44.4172 28.1769L46.188 31.6975L47.1493 34.9364L47.5035 35.9222H48.1106V35.3589L48.6166 28.6933L49.5273 20.5256L50.438 10.0108L50.7415 7.05356L52.2088 3.48605L55.1433 1.56148L57.42 2.64112L59.292 5.31674L59.039 7.05356L57.926 14.2824L55.7504 25.5952L54.3337 33.1996H55.1433L56.1046 32.2138L59.9497 27.1442L66.3752 19.0704L69.2085 15.8784L72.5478 12.3579L74.6728 10.668H78.7203L81.6548 15.0804L80.3394 19.6337L76.1906 24.8911L72.7502 29.3504L67.8172 35.9595L64.7562 41.2734L65.0307 41.7118L65.7681 41.6489L76.8989 39.255L82.9197 38.1753L90.1041 36.9549L93.3422 38.457L93.6963 40.006L92.4315 43.151L84.7411 45.0287L75.7353 46.8594L62.3244 50.0164L62.1759 50.1358L62.3512 50.3958L68.399 50.9432L70.9794 51.084H77.3037L89.0922 51.9759L92.1785 53.9944L93.9999 56.4822L93.6963 58.4068L88.9404 60.8008L82.5655 59.2987L67.6401 55.7312L62.5301 54.4638H61.8217V54.8862L66.0717 59.064L73.9139 66.1051L83.6786 75.2116L84.1845 77.4648L82.9197 79.2485L81.6042 79.0608L73.0032 72.5829L69.6639 69.6726L62.1759 63.3356H61.67V63.9928L63.3902 66.5276L72.5478 80.2812L73.0032 84.5059L72.3454 85.8672L69.9675 86.7121L67.3871 86.2427L61.9735 78.6852L56.4587 70.2359L52.0064 62.6315L51.4687 62.971L48.8189 91.2654L47.6047 92.7206L44.7714 93.8002L42.3934 92.0164L41.1286 89.1061L42.3934 83.3324L43.9113 75.8219L45.1255 69.8604L46.2386 62.4437L46.9184 59.9661L46.8583 59.8003L46.3153 59.8916L40.7238 67.5603L32.2239 79.0608L25.4948 86.2427L23.8758 86.8999L21.0931 85.4447L21.3461 82.863L22.9145 80.5629L32.2239 68.7338L37.8399 61.3641L41.4594 57.1337L41.4242 56.5218L41.2244 56.5048L16.489 72.6299L12.0873 73.1932L10.1647 71.4094L10.4176 68.4991L11.3283 67.5603L18.7657 62.4437Z"/>'
    +'</svg>';
}

function findAGWindow(windows, family, role) {
  var familyRE = family === 'claude' ? /claude|gpt/i : /gemini/i;
  for (var i = 0; i < windows.length; i++) {
    if (familyRE.test(String(windows[i].name || '')) && quotaWindowRole(windows[i], 'antigravity') === role) return windows[i];
  }
  return null;
}

function renderAGLimitItem(label, win) {
  win = win || {}; var st = percentageState(win);
  return '<div class="ag-limit-item"><span class="ag-limit-label">'+esc(label)+'</span><strong class="ag-limit-value" data-window-name="'+esc(String(win.name||win.kind||''))+'" data-window-role="value" style="color:'+st.color+'">'+esc(st.remaining===null?'—':fmtPct(st.remaining))+'</strong><span class="ag-limit-reset" data-window-name="'+esc(String(win.name||win.kind||''))+'" data-window-role="reset">'+esc(formatRelativeReset(win))+'</span></div>';
}

function renderAGFamilySection(family, windows) {
  var shortWin = findAGWindow(windows, family, 'short');
  var longWin = findAGWindow(windows, family, 'long');
  var shortRem = quotaNumber(shortWin), longRem = quotaNumber(longWin);
  var familyLabel = family === 'claude' ? 'Claude' : '안티그래비티';
  var sectionAria = familyLabel + ' 쿼터';
  var icon = agFamilyIcon(family);
  var lockRole = longRem !== null && longRem <= 0 ? 'long' : (shortRem !== null && shortRem <= 0 ? 'short' : '');
  if (lockRole) {
    var isWeekly = lockRole === 'long';
    var roleClass = isWeekly ? 'quota-reset-weekly' : 'quota-reset-primary';
    var locked = isWeekly ? longWin : shortWin;
    var timeText = formatResetCountdownSeconds(resetSeconds(locked));
    var context = '';
    var lockAria = familyLabel+' '+(isWeekly?'주간 제한':'5시간 제한')+', '+timeText;
    if (lockRole === 'short') {
      var weeklyValue = longRem===null?'—':fmtPct(longRem);
      var weeklyCountdown = formatResetCountdownSeconds(resetSeconds(longWin));
      var weeklyAria = '주간 잔여 '+weeklyValue+', '+weeklyCountdown;
      lockAria += ', '+weeklyAria;
      context = '<div class="ag-family-lock-context" role="group" aria-label="'+esc(weeklyAria)+'">주간 잔여 <strong>'+esc(weeklyValue)+'</strong><span aria-hidden="true"> · </span><span class="quota-reset-weekly">'+esc(weeklyCountdown)+'</span></div>';
    }
    var padlockSvg = '<svg class="ag-lock-padlock" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="5" y="10" width="14" height="10" rx="2"></rect><path d="M8 10V7a4 4 0 0 1 8 0v3"></path></svg>';
    var lockTitle = padlockSvg+icon+'<span>'+esc(familyLabel)+' · '+(isWeekly?'주간 제한':'5시간 제한')+'</span>';
    return '<section class="ag-family-section is-locked" aria-label="'+esc(sectionAria)+'"><div class="ag-family-lock" role="status" aria-label="'+esc(lockAria)+'">'
      +'<div class="ag-family-lock-title">'+lockTitle+'</div><div class="ag-family-lock-time '+roleClass+'">'+esc(timeText)+'</div>'+context+'</div></section>';
  }
  return '<section class="ag-family-section" aria-label="'+esc(sectionAria)+'">'
    +'<div class="ag-family-head" title="'+esc(familyLabel)+'">'+icon+'</div>'
    +'<div class="ag-family-limits">'+renderAGLimitItem('5시간 제한',shortWin)+renderAGLimitItem('주간 제한',longWin)+'</div>'
    +'</section>';
}

function renderAntigravityQuota(windows) {
  return '<div class="quota-group"><div class="ag-family-stack">'+renderAGFamilySection('gemini',windows)+renderAGFamilySection('claude',windows)+'</div></div>';
}

function renderQuotaPercentages(group, provider, accountStatus) {
  var items = (group && Array.isArray(group.windows)) ? group.windows.slice() : [];
  var key = getProviderKey(provider);
  var order = key === 'codex'
    ? { primary:0, secondary:1, unknown:9 }
    : key === 'claude' ? { 'five-hour':0, weekly:1, 'weekly-opus':2, 'weekly-sonnet':3, 'weekly-oauth-apps':4, 'weekly-cowork':5, 'weekly-sessions':6, unknown:9 }
    : { 'gemini-five-hour':0, 'gemini-weekly':1, 'claude-five-hour':2, 'claude-weekly':3, 'five-hour':4, weekly:5, unknown:9 };
  items.sort(function(a, b) {
    return (order[a.kind] == null ? 2 : order[a.kind]) - (order[b.kind] == null ? 2 : order[b.kind]);
  });

  var aria = (group.label || '쿼터 그룹') + ': ' + items.map(function(item) {
    var st = percentageState(item.window);
    return quotaKindLabel(item.kind)+' 잔여 '+(st.remaining===null?'미확인':fmtPct(st.remaining));
  }).join(', ');

  var html = '<section class="quota-group" data-group-status="'+esc(accountStatus && accountStatus.key || 'unknown')+'" aria-label="'+esc(aria)+'"><div class="quota-values">';

  items.forEach(function(item) {
    var st = percentageState(item.window);
    var unknown = st.remaining === null;
    var locked = st.remaining !== null && st.remaining <= 0;
    var family = String(item.kind || '').indexOf('gemini-') === 0 ? '안티그래비티 · ' : (String(item.kind || '').indexOf('claude-') === 0 ? 'Claude · ' : '');
    var label = family + quotaKindLabel(item.kind);
    html += '<div class="quota-value-row'+(locked?' is-locked':'')+'" data-kind="'+esc(item.kind)+'">'+
      '<span class="quota-value-label">'+esc(label)+'</span>'+
      '<span class="quota-value-number" data-window-name="'+esc(String(item.window.name||item.window.kind||''))+'" data-window-role="value" style="color:'+st.color+';" aria-label="'+esc(label+' 잔여 '+(unknown?'미확인':fmtPct(st.remaining)))+'">'+esc(unknown?'—':(locked?'잠금':fmtPct(st.remaining)))+'</span>'+
      '<span class="quota-value-reset" data-window-name="'+esc(String(item.window.name||item.window.kind||''))+'" data-window-role="reset">'+esc(formatRelativeReset(item.window))+'</span></div>';
  });
  return html+'</div></section>';
}

function renderQuotaGroups(windows, provider, status) {
  var providerKey = getProviderKey(provider);
  if (providerKey === 'antigravity') return '<div class="quota-groups provider-antigravity">'+renderAntigravityQuota(windows)+'</div>';
  var groups = groupQuotaWindows(windows, provider);
  var html = '<div class="quota-groups provider-'+esc(providerKey)+'">';
  groups.forEach(function(g) { html += renderQuotaPercentages(g, provider, status); });
  return html+'</div>';
}

function renderCreditSummary(acc) {
  var reset = acc && acc.reset_credits;
  var api = acc && acc.api_credits;
  var out = [];
  var accessibleRows = [];
  var availableCount = reset && reset.available_count != null ? Number(reset.available_count) : null;
  if (availableCount !== null && isFinite(availableCount) && availableCount > 0) {
    var resetPrimaryLabel = '리셋권 '+formatNum(availableCount)+'장 보유';
    var resetLabel = resetPrimaryLabel;
    var resetPrimaryText = '<strong>'+esc(resetPrimaryLabel)+'</strong>';
    var expiryRow = '';
    var expiryText = reset.nearest_expiry ? formatResetExpiryKo(reset.nearest_expiry) : '—';
    if (expiryText !== '—') {
      var expiryLabel = '가장 빠른 만료일: '+expiryText;
      resetLabel += '. '+expiryLabel;
      expiryRow = '<div class="credit-reset-expiry"><span class="credit-reset-expiry-tree" aria-hidden="true">└ </span><span class="credit-reset-expiry-text">'+esc(expiryLabel)+'</span></div>';
    }
    out.push('<div class="credit-reset-summary reset-credit-summary" role="group" aria-label="'+esc(resetLabel)+'"><div class="credit-reset-primary">'+resetPrimaryText+'</div>'+expiryRow+'</div>');
    accessibleRows.push(resetLabel);
  }
  if (api) {
    var parts = [];
    if (api.unlimited === true) {
      parts.push('무제한');
    } else if (api.balance != null && isFinite(Number(api.balance))) {
      parts.push('잔액 ' + formatNum(api.balance));
    } else if (api.has_credits === true) {
      parts.push('사용 가능');
    } else if (api.has_credits === false) {
      parts.push('잔액 0');
    }
    if (parts.length) {
      var apiLabel = 'API 크레딧 · '+parts.join(' · ');
      out.push('<div class="credit-api-summary" role="group" aria-label="'+esc(apiLabel)+'"><strong>API 크레딧</strong> · '+esc(parts.join(' · '))+'</div>');
      accessibleRows.push(apiLabel);
    }
  }
  return out.length ? '<div class="credit-summary" role="group" aria-label="'+esc(accessibleRows.join('. '))+'">'+out.join('')+'</div>' : '';
}

function alignAccountGrid() {
  var grid = document.getElementById('accountsList');
  if (!grid || !grid.style || !grid.getBoundingClientRect) return;
  // Preserve card width when filtering down to a few accounts; leave empty columns.
  var columns = Math.min(4, Math.max(1, Math.floor((grid.getBoundingClientRect().width + 10) / 290)));
  grid.style.setProperty('grid-template-columns', 'repeat(' + columns + ', minmax(0, 1fr))');
}

function renderAccounts(accounts) {
  var container = document.getElementById('accountsList');
  if (!container) return;

  container.setAttribute('data-count', String(accounts ? accounts.length : 0));
  alignAccountGrid();
  var savedScrollTop = (typeof window !== 'undefined' && typeof window.pageYOffset === 'number') ? window.pageYOffset : ((document.documentElement && document.documentElement.scrollTop) || 0);
  var activeEl = document.activeElement;
  var activeId = activeEl ? activeEl.id : null;
  var selStart = (activeEl && typeof activeEl.selectionStart === 'number') ? activeEl.selectionStart : null;
  var selEnd = (activeEl && typeof activeEl.selectionEnd === 'number') ? activeEl.selectionEnd : null;

  var liveEl = document.getElementById('accountStatusLive');
  if (liveEl) {
    liveEl.textContent = '계정 ' + (accounts ? accounts.length : 0) + '개 표시 중';
  }

  if (!accounts || accounts.length === 0) {
    var emptyCard = document.createElement('div');
    emptyCard.className = 'card text-empty';
    emptyCard.style.textAlign = 'center';
    emptyCard.textContent = '등록된 계정이 없거나 필터 조건에 일치하는 항목이 없습니다.';
    if (container.replaceChildren) { container.replaceChildren(emptyCard); } else { container.innerHTML = ''; container.appendChild(emptyCard); }
    if (activeId) {
      var targetEmpty = document.getElementById(activeId);
      if (targetEmpty && targetEmpty.focus && document.activeElement !== targetEmpty) {
        targetEmpty.focus();
        if (selStart !== null && targetEmpty.setSelectionRange) { try { targetEmpty.setSelectionRange(selStart, selEnd); } catch(e) {} }
      }
    }
    return;
  }

  var frag = document.createDocumentFragment ? document.createDocumentFragment() : null;
  var nodes = [];

  for (var i = 0; i < accounts.length; i++) {
    var acc = accounts[i] || {};
    var canon = getCanonicalStatus(acc);
    var windows = displayedQuotaWindows(acc);
    var pKey = getProviderKey(acc.provider);
    var isCurrent = acc.current_account === true && acc.current_kind === 'recent' && acc.current_reason === 'recent_usage';

    var card = document.createElement('div');
    card.className = 'account-card';
    card.setAttribute('data-status', canon.key);
    var availability = getAvailabilityState(acc);
    card.setAttribute('data-availability', availability.key);
    if (lockedWindowInfo(acc, availability)) card.setAttribute('data-locked', 'true');
    // Color supplies the visual state; keep its meaning available without a redundant chip.
    card.setAttribute('role', 'group');
    card.setAttribute('aria-label', String(acc.account || acc.alias || '계정') + ' · ' + availability.label + (isCurrent ? ' · 최근 15분 내 토큰이 보고된 완료 요청의 계정 (실시간 진행 중 요청 아님)' : ''));
    card.setAttribute('data-provider', pKey);
    var cardEndpoint = String(acc.endpoint || 'unknown').toLowerCase();
    if (!(cardEndpoint in EP_BASE)) cardEndpoint = 'unknown';
    card.setAttribute('data-endpoint', cardEndpoint);
    if (isCurrent) card.setAttribute('data-current', 'true');
    if (acc.account_key) card.setAttribute('data-account-key', acc.account_key);

    var header = document.createElement('div');
    header.className = 'card-header';

    var iden = document.createElement('div');
    iden.className = 'card-identity';

    var metaLeft = document.createElement('span');
    metaLeft.className = 'card-meta-left';
    var provEl = document.createElement('span');
    var provBadgeClass = pKey === 'antigravity' ? 'badge-provider-antigravity' : (pKey === 'codex' ? 'badge-provider-codex' : (pKey === 'claude' ? 'badge-provider-claude' : 'badge-provider-default'));
    provEl.className = 'badge-provider ' + provBadgeClass;
    var provBaseLabel = formatProviderLabel(acc.provider);
    var provHTML = providerIcon(acc.provider, 14);
    if (pKey === 'codex' && acc.plan_type) {
      var planKey = String(acc.plan_type).toLowerCase();
      var planLabels = { k12: 'K12', plus: 'Plus', pro: 'Pro', team: 'Team', free: 'Free', enterprise: 'Enterprise' };
      var planDisplay = planLabels[planKey] || String(acc.plan_type);
      provHTML += '<span class="prov-name"><span>' + esc(provBaseLabel) + '</span><span class="prov-sep" aria-hidden="true">·</span><span class="prov-plan">' + esc(planDisplay) + '</span></span>';
      provEl.setAttribute('aria-label', provBaseLabel + ' ' + planDisplay);
    } else {
      provHTML += '<span>' + esc(provBaseLabel) + '</span>';
    }
    provEl.innerHTML = provHTML;
    metaLeft.appendChild(provEl);

    iden.appendChild(metaLeft);

    var aliasEl = document.createElement('span');
    aliasEl.className = 'account-alias';
    var labelText = acc.account || acc.alias || '—';
    aliasEl.textContent = labelText;
    aliasEl.title = labelText;
    iden.appendChild(aliasEl);

    var endpointKey = String(acc.endpoint || 'unknown').toLowerCase();
    if (!(endpointKey in EP_BASE)) endpointKey = 'unknown';
    var endpointEl = document.createElement('span');
    endpointEl.className = 'badge-endpoint badge-endpoint-' + endpointKey;
    endpointEl.textContent = endpointLabel(endpointKey);
    iden.appendChild(endpointEl);

    header.appendChild(iden);
    if (acc.account_key && acc.refresh_supported !== false && ['codex', 'claude', 'antigravity'].indexOf(pKey) >= 0 && availability.key !== 'disabled') {
      var refreshBtn = document.createElement('button');
      refreshBtn.type = 'button';
      refreshBtn.className = 'btn btn-secondary card-refresh';
      refreshBtn.id = 'refresh-' + encodeURIComponent(acc.account_key);
      refreshBtn.setAttribute('data-refresh-account', acc.account_key);
      refreshBtn.setAttribute('aria-label', labelText + ' 쿼터 새로고침');
      refreshBtn.title = '새로고침';
      refreshBtn.innerHTML = '<svg class="ico-refresh" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12a9 9 0 1 1-2.64-6.36"/><path d="M21 3v6h-6"/></svg>' +
        '<svg class="ico-check" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';
      refreshBtn.onclick = (function(key) { return function() { refreshAccount(key); }; })(acc.account_key);
      header.appendChild(refreshBtn);
    }
    var heading = document.createElement('div');
    heading.className = 'card-heading';
    heading.appendChild(header);
    card.appendChild(heading);
    var refreshStatus = document.createElement('div');
    refreshStatus.className = 'card-refresh-status';
    refreshStatus.setAttribute('role', 'status');
    refreshStatus.setAttribute('aria-live', 'polite');
    refreshStatus.setAttribute('data-refresh-status', String(acc.account_key || ''));
    heading.appendChild(refreshStatus);

    // Keep the provider observation time near the account identity so it is
    // not mistaken for the dashboard synchronization time in the toolbar.
    var freshnessEl = document.createElement('div');
    freshnessEl.innerHTML = renderAccountFreshness(acc);
    if (freshnessEl.firstChild) heading.appendChild(freshnessEl.firstChild);

    var quotaBody = document.createElement('div');
    quotaBody.className = 'card-quota';
    card.appendChild(quotaBody);
    var lockHTML = (pKey === 'codex' || pKey === 'claude') ? renderQuotaLock(acc, availability) : '';
    if (lockHTML) {
      var lockEl = document.createElement('div');
      lockEl.className = 'quota-group';
      lockEl.innerHTML = lockHTML;
      quotaBody.appendChild(lockEl);
      // Keep model-specific Claude weekly quotas visible even while the account is locked.
      if (pKey === 'claude') {
        var modelWindows = windows.filter(function(w) { return /^weekly-/.test(String(w.name || '').toLowerCase()); });
        if (modelWindows.length) {
          var modelQuotaEl = document.createElement('div');
          modelQuotaEl.innerHTML = renderQuotaGroups(modelWindows, acc.provider, canon);
          quotaBody.appendChild(modelQuotaEl);
        }
      }
    } else if (windows.length > 0) {
      var quotaEl = document.createElement('div');
      quotaEl.innerHTML = renderQuotaGroups(windows, acc.provider, canon);
      if (quotaEl.firstChild) {
        while (quotaEl.firstChild) quotaBody.appendChild(quotaEl.firstChild);
      } else {
        quotaBody.appendChild(quotaEl);
      }
    } else {
      var noWin = document.createElement('div');
      noWin.className = 'quota-pending';
      noWin.textContent = availability.key === 'unavailable' || availability.key === 'disabled' ? '쿼터 없음' : '측정 대기';
      quotaBody.appendChild(noWin);
    }

    var creditsHTML = renderCreditSummary(acc);
    if (creditsHTML) {
      var creditsEl = document.createElement('div');
      creditsEl.innerHTML = creditsHTML;
      if (creditsEl.firstChild) {
        while (creditsEl.firstChild) card.appendChild(creditsEl.firstChild);
      } else {
        card.appendChild(creditsEl);
      }
    }

    decorateQuotaChanges(card, quotaAccountKeys(cachedAccounts)[i]);
    nodes.push(card);
    if (frag) frag.appendChild(card);
  }

  if (frag && container.replaceChildren) {
    container.replaceChildren(frag);
  } else if (frag) {
    container.innerHTML = '';
    container.appendChild(frag);
  } else {
    container.innerHTML = '';
    for (var n = 0; n < nodes.length; n++) container.appendChild(nodes[n]);
  }

  updateAccountRefreshButtons();
  if (activeId) {
    var target = document.getElementById(activeId);
    if (target && target.focus && document.activeElement !== target) {
      target.focus();
      if (selStart !== null && target.setSelectionRange) {
        try { target.setSelectionRange(selStart, selEnd); } catch(e) {}
      }
    }
  }
  if (typeof window !== 'undefined' && window.scrollTo) {
    try { window.scrollTo(0, savedScrollTop); } catch(e) {}
  } else if (document.documentElement) {
    document.documentElement.scrollTop = savedScrollTop;
  }
}

document.addEventListener('DOMContentLoaded', function() {
  if (typeof window !== 'undefined' && window.addEventListener) window.addEventListener('resize', alignAccountGrid);
  initLoadingState();
  initFilterControls();
  initManualRefreshControls();
  startResetCountdownTick();

  document.addEventListener('visibilitychange', function() {
    if (!document.hidden) {
      updateManualRefreshButton();
      if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
      if (!isFetching) loadDashboard(false);
      startResetCountdownTick();
    }
  });

  loadDashboard(true);
});
</script>
<script data-dohwi-ui="select">
(function() {
  if (!window.HTMLSelectElement) return;
  var valueDesc = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value');
  var CHECK = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5"></path></svg>';
  var current = null;
  function make(tag, cls) { var node = document.createElement(tag); if (cls) node.className = cls; return node; }
  function closeCurrent(restoreFocus) { if (current) current.close(restoreFocus); }
  document.addEventListener('pointerdown', function(event) { if (current && !current.contains(event.target)) closeCurrent(false); }, true);
  window.addEventListener('resize', function() { closeCurrent(false); });
  window.addEventListener('scroll', function(event) { if (current && !current.list.contains(event.target)) closeCurrent(false); }, true);

  function enhance(select) {
    var wrap = select.parentNode;
    if (!wrap || select.getAttribute('data-ui-select')) return;
    select.setAttribute('data-ui-select', 'true');
    var trigger = make('button', 'compact-select ui-select-trigger');
    trigger.type = 'button';
    trigger.id = select.id + 'Trigger';
    trigger.setAttribute('role', 'combobox');
    trigger.setAttribute('aria-haspopup', 'listbox');
    trigger.setAttribute('aria-expanded', 'false');
    trigger.setAttribute('aria-label', select.getAttribute('aria-label') || '');
    var text = make('span', 'ui-select-value');
    trigger.appendChild(text);
    wrap.classList.add('is-enhanced');
    wrap.insertBefore(trigger, select);
    select.tabIndex = -1;
    select.setAttribute('aria-hidden', 'true');

    function sync() {
      var option = select.options[select.selectedIndex];
      text.textContent = option ? option.textContent : '';
      trigger.disabled = select.disabled;
      trigger.title = select.title || '';
    }
    Object.defineProperty(select, 'value', {
      configurable: true,
      get: function() { return valueDesc.get.call(this); },
      set: function(next) { valueDesc.set.call(this, next); sync(); }
    });
    select.addEventListener('change', sync);
    new MutationObserver(sync).observe(select, { childList: true, subtree: true, attributes: true, attributeFilter: ['title', 'disabled'] });
    var label = document.querySelector('label[for="' + select.id + '"]');
    if (label) label.addEventListener('click', function(event) { event.preventDefault(); trigger.focus(); });
    sync();

    var list = null, items = [], active = -1, typed = '', typedAt = 0;
    var api = {
      list: null,
      contains: function(node) { return trigger.contains(node) || (list && list.contains(node)); },
      close: function(restoreFocus) {
        if (!list) return;
        list.remove(); list = null; api.list = null; items = []; current = null;
        trigger.setAttribute('aria-expanded', 'false');
        trigger.removeAttribute('aria-controls');
        trigger.removeAttribute('aria-activedescendant');
        if (restoreFocus) trigger.focus();
      }
    };
    function highlight(index) {
      if (!items.length) return;
      active = (index + items.length) % items.length;
      items.forEach(function(item, i) { if (i === active) item.setAttribute('data-highlighted', ''); else item.removeAttribute('data-highlighted'); });
      trigger.setAttribute('aria-activedescendant', items[active].id);
      items[active].scrollIntoView({ block: 'nearest' });
    }
    function choose(index) {
      var option = select.options[index];
      if (!option) return;
      var changed = select.selectedIndex !== index;
      select.selectedIndex = index;
      sync();
      api.close(true);
      if (changed) select.dispatchEvent(new Event('change', { bubbles: true }));
    }
    function open() {
      if (list || select.disabled) return;
      closeCurrent(false);
      list = make('div', 'ui-select-content');
      list.setAttribute('role', 'listbox');
      list.id = select.id + 'Listbox';
      list.setAttribute('aria-label', trigger.getAttribute('aria-label'));
      var viewport = make('div', 'ui-select-viewport');
      list.appendChild(viewport);
      Array.prototype.forEach.call(select.options, function(option, index) {
        var item = make('div', 'ui-select-item');
        item.id = select.id + 'Option' + index;
        item.setAttribute('role', 'option');
        item.setAttribute('aria-selected', index === select.selectedIndex ? 'true' : 'false');
        var label = make('span'); label.textContent = option.textContent; item.appendChild(label);
        if (index === select.selectedIndex) { var mark = make('span', 'ui-select-item-indicator'); mark.innerHTML = CHECK; item.appendChild(mark); }
        // Keep focus on the trigger until click commits; otherwise blur removes the option
        // between a human pointer press and release. Prevent only mouse focus, not touch scrolling.
        item.addEventListener('mousedown', function(event) { event.preventDefault(); });
        item.addEventListener('pointermove', function() { if (active !== index) highlight(index); });
        item.addEventListener('click', function() { choose(index); });
        viewport.appendChild(item);
        items.push(item);
      });
      document.body.appendChild(list);
      var rect = trigger.getBoundingClientRect();
      var below = window.innerHeight - rect.bottom - 8, above = rect.top - 8;
      var height = Math.min(list.scrollHeight, 320);
      var placeAbove = below < Math.min(height, 160) && above > below;
      var room = Math.max(96, placeAbove ? above : below);
      list.style.minWidth = Math.min(rect.width, window.innerWidth - 16) + 'px';
      list.style.maxWidth = (window.innerWidth - 16) + 'px';
      list.style.maxHeight = Math.min(320, room) + 'px';
      list.style.left = Math.max(8, Math.min(rect.left, window.innerWidth - list.offsetWidth - 8)) + 'px';
      list.style.top = placeAbove ? Math.max(8, rect.top - 4 - list.offsetHeight) + 'px' : (rect.bottom + 4) + 'px';
      api.list = list; current = api;
      trigger.setAttribute('aria-expanded', 'true');
      trigger.setAttribute('aria-controls', list.id);
      highlight(Math.max(0, select.selectedIndex));
    }
    trigger.addEventListener('click', function() { if (list) api.close(false); else open(); });
    trigger.addEventListener('keydown', function(event) {
      var key = event.key;
      if (!list) {
        if (key === 'ArrowDown' || key === 'ArrowUp' || key === 'Enter' || key === ' ') { event.preventDefault(); open(); }
        return;
      }
      if (key === 'ArrowDown') { event.preventDefault(); highlight(active + 1); }
      else if (key === 'ArrowUp') { event.preventDefault(); highlight(active - 1); }
      else if (key === 'Home') { event.preventDefault(); highlight(0); }
      else if (key === 'End') { event.preventDefault(); highlight(items.length - 1); }
      else if (key === 'Enter' || key === ' ') { event.preventDefault(); choose(active); }
      else if (key === 'Escape') { event.preventDefault(); event.stopPropagation(); api.close(true); }
      else if (key === 'Tab') { api.close(false); }
      else if (key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
        var now = Date.now(); typed = (now - typedAt > 700 ? '' : typed) + key.toLowerCase(); typedAt = now;
        for (var i = 1; i <= items.length; i++) {
          var at = (active + i) % items.length;
          if (items[at].textContent.trim().toLowerCase().indexOf(typed) === 0) { highlight(at); break; }
        }
      }
    });
    trigger.addEventListener('blur', function() { if (list) setTimeout(function() { if (list && document.activeElement !== trigger) api.close(false); }, 0); });
  }
  Array.prototype.forEach.call(document.querySelectorAll('.compact-select-wrap > select.compact-select'), enhance);
})();
</script>
<script data-dohwi-motion="material">
(function() {
  var root = document.documentElement;
  var reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
  function motionOn() { return !reduced.matches && !document.hidden; }

  /* ---- Sliding indicator (dohwi-ui Tabs indicator, vanilla edition) ---- */
  function slider(group, itemSelector) {
    if (!group || group.getAttribute('data-slide-bound')) return;
    group.setAttribute('data-slide-bound', 'true');
    group.classList.add('ui-slide-group');
    var indicator = document.createElement('span');
    indicator.className = 'ui-slide-indicator';
    indicator.setAttribute('aria-hidden', 'true');
    group.insertBefore(indicator, group.firstChild);
    var first = true;
    function items() { return Array.prototype.slice.call(group.querySelectorAll(itemSelector)); }
    function place() {
      var active = items().filter(function(el) { return el.classList.contains('active'); })[0];
      if (!active || !active.offsetWidth) { group.removeAttribute('data-slide-ready'); first = true; return; }
      var animate = !first && motionOn();
      if (!animate) group.removeAttribute('data-slide-animate');
      indicator.style.width = active.offsetWidth + 'px';
      indicator.style.height = active.offsetHeight + 'px';
      indicator.style.transform = 'translate(' + active.offsetLeft + 'px, ' + active.offsetTop + 'px)';
      group.setAttribute('data-slide-ready', '');
      if (animate) group.setAttribute('data-slide-animate', '');
      else requestAnimationFrame(function() { group.setAttribute('data-slide-animate', ''); });
      first = false;
    }
    items().forEach(function(el) { el.classList.add('ui-slide-item'); });
    new MutationObserver(function() { place(); }).observe(group, { attributes: true, attributeFilter: ['class'], subtree: true });
    if (window.ResizeObserver) new ResizeObserver(function() { var keep = first; first = true; place(); first = keep; }).observe(group);
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(function() { first = true; place(); });
    place();
  }


  /* ---- Material ripple (dohwi-ui vanilla edition) ---- */
  var live = new Map();
  function clearRipple(ripple) {
    clearTimeout(live.get(ripple)); live.delete(ripple);
    var layer = ripple.parentElement; ripple.remove();
    if (layer && !layer.childElementCount) layer.remove();
  }
  function addRipple(button, x, y) {
    if (!motionOn() || button.disabled || button.getAttribute('aria-disabled') === 'true') return;
    var rect = button.getBoundingClientRect();
    if (!rect.width || !rect.height) return;
    var px = x === null ? button.clientWidth / 2 : (x - rect.left) * button.offsetWidth / rect.width;
    var py = y === null ? button.clientHeight / 2 : (y - rect.top) * button.offsetHeight / rect.height;
    var layer = button.querySelector(':scope > .ui-button-ripple-layer');
    if (!layer) { layer = document.createElement('span'); layer.className = 'ui-button-ripple-layer'; layer.setAttribute('aria-hidden', 'true'); button.appendChild(layer); }
    while (layer.childElementCount >= 4) clearRipple(layer.firstElementChild);
    var ripple = document.createElement('span');
    ripple.className = 'ui-button-ripple';
    var size = Math.hypot(Math.max(px, button.offsetWidth - px), Math.max(py, button.offsetHeight - py)) * 2;
    ripple.style.left = px + 'px'; ripple.style.top = py + 'px'; ripple.style.width = size + 'px'; ripple.style.height = size + 'px';
    layer.appendChild(ripple);
    live.set(ripple, setTimeout(function() { clearRipple(ripple); }, 900));
  }
  var RIPPLE = '.btn, .seg-btn';
  function bindRipples(scope) {
    Array.prototype.forEach.call((scope || document).querySelectorAll(RIPPLE), function(el) { el.classList.add('ui-ripple-host'); });
  }
  bindRipples(document);
  new MutationObserver(function() { bindRipples(document); }).observe(document.body, { childList: true, subtree: true });
  document.addEventListener('pointerdown', function(event) {
    if (event.button !== 0 || !event.target.closest) return;
    var button = event.target.closest('.ui-ripple-host');
    if (button) addRipple(button, event.clientX, event.clientY);
  }, true);
  document.addEventListener('click', function(event) {
    if (event.detail !== 0 || !event.target.closest) return;
    var button = event.target.closest('.ui-ripple-host');
    if (button) addRipple(button, null, null);
  }, true);
  document.addEventListener('animationend', function(event) { if (live.has(event.target)) clearRipple(event.target); }, true);

  /* ---- Staggered entrance for freshly rendered panels ---- */
  window.motionEnter = function(nodes) {
    if (!motionOn()) return;
    Array.prototype.forEach.call(nodes, function(node, index) {
      if (!node || !node.classList) return;
      node.style.setProperty('--enter-delay', Math.min(index, 8) * 40 + 'ms');
      node.classList.remove('is-entering');
      void node.offsetWidth;
      node.classList.add('is-entering');
      var done = function() { node.classList.remove('is-entering'); node.style.removeProperty('--enter-delay'); node.removeEventListener('animationend', done); };
      node.addEventListener('animationend', done);
    });
  };
  var enterNext = true;
  document.addEventListener('change', function(event) { if (event.target && event.target.id && /Filter$/.test(event.target.id)) enterNext = true; }, true);
  document.addEventListener('click', function(event) { if (event.target.closest && event.target.closest('#resetFiltersBtn')) enterNext = true; }, true);
  window.motionConsumeEnter = function() {
    if (!enterNext) return;
    var cards = document.querySelectorAll('#accountsList > .account-card');
    if (!cards.length) return;
    enterNext = false;
    window.motionEnter(cards);
  };
})();
</script>
</body>
</html>
`
