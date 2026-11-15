package dashboard

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"regexp"
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

// ScriptSources is the CSP script-src source list for HTML: one 'sha256-…' hash
// per inline <script> block, so the page runs without 'unsafe-inline'.
var ScriptSources = scriptSources(HTML)

func assemble(shell, styles, script string) string {
	page := strings.Replace(shell, "@@STYLE@@\n", styles, 1)
	return strings.Replace(page, "@@SCRIPT@@\n", script, 1)
}

var inlineScript = regexp.MustCompile(`(?s)<script\b[^>]*>(.*?)</script>`)

// InlineScripts returns the bodies of every inline <script> element, exactly as
// the browser hashes them for CSP.
func InlineScripts(page string) []string {
	matches := inlineScript.FindAllStringSubmatch(page, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// ScriptHash is the CSP source expression for one inline script body.
func ScriptHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

func scriptSources(page string) string {
	bodies := InlineScripts(page)
	hashes := make([]string, 0, len(bodies))
	for _, b := range bodies {
		hashes = append(hashes, ScriptHash(b))
	}
	return strings.Join(hashes, " ")
}
