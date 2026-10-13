package dashboard

import (
	_ "embed"
	"strings"
)

// The dashboard is authored as three plain files so each can be edited, diffed and
// linted with normal tooling, then assembled once at start-up into a single
// self-contained page (no CDN, no extra requests).
//
//go:embed dashboard.html
var shell string

//go:embed style.css
var styles string

//go:embed app.js
var script string

// HTML is the self-contained dashboard page for quota-monitor.
var HTML = assemble(shell, styles, script)

func assemble(shell, styles, script string) string {
	page := strings.Replace(shell, "@@STYLE@@\n", styles, 1)
	return strings.Replace(page, "@@SCRIPT@@\n", script, 1)
}
