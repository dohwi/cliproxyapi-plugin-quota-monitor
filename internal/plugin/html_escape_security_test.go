package plugin

import (
	"os/exec"
	"strings"
	"testing"

	"quota-monitor/internal/dashboard"
)

// The browser's textContent -> innerHTML serialization escapes <, > and & but
// does not escape quotes. esc is also used in HTML attributes, so exercise that
// real serialization behavior instead of the older quote-escaping DOM mocks.
func TestEscapeProtectsHTMLAttributesWithBrowserSerialization(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := `
const assert = require('node:assert/strict');
const vm = require('node:vm');
const html = process.argv[1];
const match = html.match(/function esc\(str\) \{[\s\S]*?\n\}/);
assert(match, 'esc helper is present');
const ctx = {
  document: { createElement() { return {
    textContent: '',
    get innerHTML() { return String(this.textContent).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }
  }; } }
};
vm.createContext(ctx);
vm.runInContext(match[0], ctx);
const attack = '" onmouseover="alert(1)\' <img src=x onerror=alert(2)>&';
const output = ctx.esc(attack);
assert(!/[<>"']/.test(output), 'all HTML/attribute delimiters are escaped: ' + output);
assert(output.includes('&quot;') && output.includes('&#39;'), 'both quote styles escaped');
assert.equal(ctx.esc('safe@example.invalid'), 'safe@example.invalid');
// The no-DOM fallback must have the same guarantee.
ctx.document = undefined;
assert.equal(ctx.esc(attack), output);
`
	if !strings.Contains(dashboard.HTML, "function esc(str)") {
		t.Fatal("dashboard escape helper missing")
	}
	if out, err := nodeDashboardCmd(t, "node", script).CombinedOutput(); err != nil {
		t.Fatalf("HTML attribute escaping regression: %v\n%s", err, out)
	}
}
