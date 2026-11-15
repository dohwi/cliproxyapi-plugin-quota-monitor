package plugin

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// The served page must run under a CSP without script 'unsafe-inline': every
// inline <script> body is allowed by its sha256 hash and nothing else can run.
func TestDashboardCSPHashesEveryInlineScript(t *testing.T) {
	reset(t)
	for _, path := range []string{resourceBase + "/dashboard", mgmtBase + "/dashboard"} {
		req, _ := json.Marshal(managementRequest{Method: "GET", Path: path})
		var env struct {
			Result managementResponse `json:"result"`
		}
		if err := json.Unmarshal(handleManagementHandle(req), &env); err != nil {
			t.Fatal(err)
		}
		page := string(env.Result.Body)
		csp := strings.Join(env.Result.Headers["Content-Security-Policy"], "")
		var scriptSrc string
		for _, d := range strings.Split(csp, ";") {
			if d = strings.TrimSpace(d); strings.HasPrefix(d, "script-src ") {
				scriptSrc = d
			}
		}
		if scriptSrc == "" || strings.Contains(scriptSrc, "'unsafe-inline'") || strings.Contains(scriptSrc, "'unsafe-eval'") {
			t.Fatalf("%s: script-src must list hashes only: %q", path, scriptSrc)
		}
		if !strings.Contains(csp, "style-src 'unsafe-inline'") || !strings.Contains(csp, "font-src 'self'") {
			t.Fatalf("%s: style/font policy changed: %q", path, csp)
		}
		// Parse the page independently of the production helper.
		bodies := regexp.MustCompile(`(?s)<script\b[^>]*>(.*?)</script>`).FindAllStringSubmatch(page, -1)
		if len(bodies) < 3 {
			t.Fatalf("%s: expected the app, select and motion scripts, got %d", path, len(bodies))
		}
		allowed := strings.Fields(strings.TrimPrefix(scriptSrc, "script-src "))
		if len(allowed) != len(bodies) {
			t.Fatalf("%s: %d script hashes for %d inline scripts", path, len(allowed), len(bodies))
		}
		for i, m := range bodies {
			sum := sha256.Sum256([]byte(m[1]))
			want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
			if !strings.Contains(scriptSrc, want) {
				t.Errorf("%s: inline script #%d hash %s missing from %q", path, i, want, scriptSrc)
			}
		}
		if strings.Contains(page, "<script src") {
			t.Errorf("%s: external scripts are not allowed", path)
		}
	}
}

// Hash-based CSP blocks inline handlers and javascript: URLs; the page must not
// rely on them (nor on eval/new Function).
func TestDashboardHasNoInlineHandlersOrEval(t *testing.T) {
	page := dashboardPage(t)
	tags := regexp.MustCompile(`<[a-zA-Z][^>]*>`).FindAllString(regexp.MustCompile(`(?s)<script\b[^>]*>.*?</script>|(?s)<style\b[^>]*>.*?</style>`).ReplaceAllString(page, ""), -1)
	onAttr := regexp.MustCompile(`\son[a-z]+\s*=`)
	for _, tag := range tags {
		if onAttr.MatchString(tag) {
			t.Errorf("inline event handler attribute: %s", tag)
		}
	}
	// Markup built in scripts must not carry handlers either.
	for _, bad := range []*regexp.Regexp{regexp.MustCompile(`<[a-z][^>]*\son[a-z]+=`), regexp.MustCompile(`(?i)javascript:`), regexp.MustCompile(`\beval\(`), regexp.MustCompile(`new Function\(`), regexp.MustCompile(`setTimeout\(\s*['"]`)} {
		if loc := bad.FindStringIndex(page); loc != nil {
			t.Errorf("forbidden pattern %s at %q", bad, page[max(0, loc[0]-40):min(len(page), loc[1]+40)])
		}
	}
}

func dashboardPage(t *testing.T) string {
	t.Helper()
	req, _ := json.Marshal(managementRequest{Method: "GET", Path: resourceBase + "/dashboard"})
	var env struct {
		Result managementResponse `json:"result"`
	}
	if err := json.Unmarshal(handleManagementHandle(req), &env); err != nil {
		t.Fatal(err)
	}
	return string(env.Result.Body)
}
