package authdir

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

// TestEndpointResolverV72157NameAndIDContracts pins the v7.2.157 join rules:
// management name is Auth.FileName (possibly relative), and management id may
// join only the exact top-level JSON id. The fixture contains no real identity.
func TestEndpointResolverV72157NameAndIDContracts(t *testing.T) {
	dir := t.TempDir()
	kinds := []quotamonitor.EndpointKind{
		quotamonitor.EndpointDirect, quotamonitor.EndpointDirect, quotamonitor.EndpointDirect,
		quotamonitor.EndpointWarp1, quotamonitor.EndpointWarp1, quotamonitor.EndpointWarp1, quotamonitor.EndpointWarp1,
		quotamonitor.EndpointWarp2, quotamonitor.EndpointWarp2, quotamonitor.EndpointWarp2,
	}
	proxies := map[quotamonitor.EndpointKind]string{
		quotamonitor.EndpointDirect: "",
		quotamonitor.EndpointWarp1:  "http://warp1:8080",
		quotamonitor.EndpointWarp2:  "socks5://warp2:1080",
	}
	for i, kind := range kinds {
		name := fmt.Sprintf("fixture-%02d.json", i)
		body := fmt.Sprintf(`{"id":"fixture-id-%02d","proxy_url":%q}`, i, proxies[kind])
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolver := New(dir)
	resolver.Refresh()
	if got := resolver.ScanCount(); got != 1 {
		t.Fatalf("scans = %d, want one", got)
	}
	counts := map[quotamonitor.EndpointKind]int{}
	for i, want := range kinds {
		name := fmt.Sprintf("provider/subdir/fixture-%02d.json", i)
		got := resolver.Resolve(name, "")
		if got != want {
			t.Fatalf("relative name #%d = %q, want %q", i, got, want)
		}
		counts[got]++
	}
	if counts[quotamonitor.EndpointDirect] != 3 || counts[quotamonitor.EndpointWarp1] != 4 || counts[quotamonitor.EndpointWarp2] != 3 || counts[quotamonitor.EndpointUnknown] != 0 {
		t.Fatalf("distribution = %#v, want direct3/warp1=4/warp2=3/unknown0", counts)
	}

	// Exercise the actual management parser: v7.2.157 returns Auth.FileName in
	// name (relative paths are legal) and Auth.ID in id. All ten must join.
	var payload strings.Builder
	payload.WriteString(`{"files":[`)
	for i := range kinds {
		if i > 0 {
			payload.WriteByte(',')
		}
		fmt.Fprintf(&payload, `{"provider":"codex","auth_index":"%d","name":"provider/subdir/fixture-%02d.json","id":"fixture-id-%02d","quota":{"signals":{}}}`, i, i, i)
	}
	payload.WriteString(`]}`)
	accounts, err := quotamonitor.NewAdapter(quotamonitor.WithEndpointResolver(resolver)).ParseAuthFilesJSON([]byte(payload.String()))
	if err != nil || len(accounts) != 10 {
		t.Fatalf("management fixture join = %d accounts, err=%v; want 10", len(accounts), err)
	}
	joined := map[quotamonitor.EndpointKind]int{}
	for _, account := range accounts {
		joined[account.Endpoint]++
	}
	if joined[quotamonitor.EndpointDirect] != 3 || joined[quotamonitor.EndpointWarp1] != 4 || joined[quotamonitor.EndpointWarp2] != 3 || joined[quotamonitor.EndpointUnknown] != 0 {
		t.Fatalf("management distribution = %#v, want direct3/warp1=4/warp2=3/unknown0", joined)
	}
	if got := resolver.Resolve("display value", "fixture-id-03"); got != quotamonitor.EndpointWarp1 {
		t.Fatalf("exact JSON id fallback = %q, want warp1", got)
	}
	for _, name := range []string{"../fixture-00.json", "/tmp/fixture-00.json", "x/../fixture-00.json", "fixture-00.JSON", "fixture-00.json\x00"} {
		if got := resolver.Resolve(name, ""); got != quotamonitor.EndpointUnknown {
			t.Errorf("unsafe or case-mismatched name = %q, want unknown", got)
		}
	}
	if got := resolver.Resolve("missing.json", "missing-id"); got != quotamonitor.EndpointUnknown {
		t.Errorf("unmatched entry = %q, want unknown", got)
	}
	// A duplicate immutable ID is intentionally not first-wins.
	if err := os.WriteFile(filepath.Join(dir, "duplicate.json"), []byte(`{"id":"fixture-id-03","proxy_url":"http://warp2:8080"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver.Refresh()
	if got := resolver.Resolve("display value", "fixture-id-03"); got != quotamonitor.EndpointUnknown {
		t.Errorf("duplicate JSON id = %q, want unknown", got)
	}
	// Resolution state never contains raw proxy, filename, or path in a DTO.
	if strings.Contains(fmt.Sprintf("%#v", resolver), "warp1:8080") {
		t.Fatal("resolver retained raw proxy URL")
	}
}

func TestEndpointResolverV52CodexPlusContract(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixtures := []struct {
		file, proxy, management string
		want                    quotamonitor.EndpointKind
	}{
		{"direct-a-plus.json", "direct", "direct-a.json", quotamonitor.EndpointDirect},
		{"direct-b-plus.json", " DIRECT ", "direct-b.json", quotamonitor.EndpointDirect},
		{"direct-c.json", "", "direct-c.json", quotamonitor.EndpointDirect},
		{"warp1-a.json", "http://warp1:8080", "warp1-a.json", quotamonitor.EndpointWarp1},
		{"warp1-b.json", "http://warp1:8080", "warp1-b.json", quotamonitor.EndpointWarp1},
		{"warp1-c.json", "http://warp1:8080", "warp1-c.json", quotamonitor.EndpointWarp1},
		{"warp1-d.json", "http://warp1:8080", "warp1-d.json", quotamonitor.EndpointWarp1},
		{"warp2-a.json", "socks5://warp2:1080", "warp2-a.json", quotamonitor.EndpointWarp2},
		{"warp2-b.json", "socks5://warp2:1080", "warp2-b.json", quotamonitor.EndpointWarp2},
		{"warp2-c.json", "socks5://warp2:1080", "warp2-c.json", quotamonitor.EndpointWarp2},
	}
	for _, f := range fixtures {
		write(f.file, fmt.Sprintf(`{"type":"codex","proxy_url":%q}`, f.proxy))
	}
	resolver := New(dir)
	resolver.Refresh()

	var payload strings.Builder
	payload.WriteString(`{"files":[`)
	for i, f := range fixtures {
		if i > 0 {
			payload.WriteByte(',')
		}
		fmt.Fprintf(&payload, `{"provider":"codex","auth_index":%q,"name":%q,"quota":{"signals":{}}}`, fmt.Sprint(i), f.management)
	}
	payload.WriteString(`]}`)
	accounts, err := quotamonitor.NewAdapter(quotamonitor.WithEndpointResolver(resolver)).ParseAuthFilesJSON([]byte(payload.String()))
	if err != nil || len(accounts) != 10 {
		t.Fatalf("accounts=%d err=%v", len(accounts), err)
	}
	counts := map[quotamonitor.EndpointKind]int{}
	for i, account := range accounts {
		if account.Endpoint != fixtures[i].want {
			t.Errorf("account %d endpoint=%q want=%q", i, account.Endpoint, fixtures[i].want)
		}
		counts[account.Endpoint]++
	}
	if counts[quotamonitor.EndpointDirect] != 3 || counts[quotamonitor.EndpointWarp1] != 4 || counts[quotamonitor.EndpointWarp2] != 3 || counts[quotamonitor.EndpointUnknown] != 0 {
		t.Fatalf("distribution=%#v, want direct3/warp1=4/warp2=3/unknown0", counts)
	}
	if resolver.ScanCount() != 1 {
		t.Fatalf("scans=%d, want 1", resolver.ScanCount())
	}

	write("ag-plus.json", `{"type":"antigravity","proxy_url":"http://warp2:8080"}`)
	write("other-plus.json", `{"type":"claude","proxy_url":"http://warp2:8080"}`)
	resolver.Refresh()
	for _, tc := range []struct{ name, provider string }{
		{"ag.json", "antigravity"}, {"other.json", "claude"},
		{"../direct-a.json", "codex"}, {"/tmp/direct-a.json", "codex"},
		{"direct-a.JSON", "codex"},
	} {
		if got := resolver.ResolveForProvider(tc.name, "", tc.provider, ""); got != quotamonitor.EndpointUnknown {
			t.Errorf("%s/%s = %q, want unknown", tc.provider, tc.name, got)
		}
	}
}

func TestEndpointResolverV52AliasAmbiguityAndExactPrecedence(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Exact dup.json must win over a conflicting alias from dup-plus.json.
	write("dup.json", `{"type":"codex","proxy_url":"http://warp1:8080"}`)
	write("dup-plus.json", `{"type":"codex","proxy_url":"direct"}`)
	// Only the exact terminal -plus marker is an alias; extra markers are not.
	write("foo-plus.json", `{"type":"codex","proxy_url":"direct"}`)
	write("foo-plus-plus.json", `{"type":"codex","proxy_url":"socks5://warp2:1080"}`)
	write("bar-plus-plus.json", `{"type":"codex","proxy_url":"socks5://warp2:1080"}`)
	write("generic.json", `{"type":"codex","proxy_url":"socks5://warp1:8080"}`)
	write("id-a.json", `{"type":"codex","id":"duplicate","proxy_url":"direct"}`)
	write("id-b.json", `{"type":"codex","id":"duplicate","proxy_url":"socks5://warp2:1080"}`)
	resolver := New(dir)
	resolver.Refresh()

	if got := resolver.ResolveForProvider("dup.json", "duplicate", "codex", ""); got != quotamonitor.EndpointWarp1 {
		t.Errorf("exact beats ambiguous ID/alias = %q, want warp1", got)
	}
	if got := resolver.ResolveForProvider("foo.json", "", "codex", ""); got != quotamonitor.EndpointDirect {
		t.Errorf("exact -plus alias = %q, want direct", got)
	}
	if got := resolver.ResolveForProvider("generic-plus.json", "", "codex", ""); got != quotamonitor.EndpointUnknown {
		t.Errorf("generic plus stripping = %q, want unknown", got)
	}
	if got := resolver.ResolveForProvider("display", "duplicate", "codex", ""); got != quotamonitor.EndpointUnknown {
		t.Errorf("duplicate immutable ID = %q, want unknown", got)
	}
	if got := resolver.Resolve("dup.json", ""); got != quotamonitor.EndpointWarp1 {
		t.Errorf("exact-only legacy resolver = %q, want warp1", got)
	}
}

// TestDashboardEndpointSortUI verifies the dashboard HTML contains the
// endpoint sort segmented control and the JS sorting logic.
func TestAuthDirResolverGuardsAndStableFilenameJoin(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("direct.json", `{}`)
	write("warp.json", `{"proxy_url":"socks5://WARP1:1080"}`)
	write("bad.json", `{not json`)
	write("large.json", `{"proxy_url":"http://warp2:8080"}`+strings.Repeat(" ", maxAuthFileSize))
	write("note.txt", `{"proxy_url":"http://warp2:8080"}`)
	if err := os.Symlink(filepath.Join(dir, "warp.json"), filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}

	resolver := New(dir)
	resolver.Refresh()
	if got := resolver.ScanCount(); got != 1 {
		t.Fatalf("scan count = %d, want 1", got)
	}
	for name, want := range map[string]quotamonitor.EndpointKind{
		"direct.json": quotamonitor.EndpointDirect,
		"warp.json":   quotamonitor.EndpointWarp1,
		"bad.json":    quotamonitor.EndpointUnknown,
		"large.json":  quotamonitor.EndpointUnknown,
		"link.json":   quotamonitor.EndpointUnknown,
		"note.txt":    quotamonitor.EndpointUnknown,
	} {
		if got := resolver.Resolve(name, ""); got != want {
			t.Errorf("ResolveByName(%q) = %q, want %q", name, got, want)
		}
	}
	// CLIProxyAPI FileName may be relative under auth_dir; its basename joins.
	if got := resolver.Resolve("provider/warp.json", ""); got != quotamonitor.EndpointWarp1 {
		t.Errorf("relative filename endpoint = %q, want warp1", got)
	}
	for _, unsafeName := range []string{"../warp.json", "/tmp/warp.json", "nested/../warp.json", "warp.JSON", "warp.json\x00"} {
		if got := resolver.Resolve(unsafeName, ""); got != quotamonitor.EndpointUnknown {
			t.Errorf("unsafe/case-mismatched filename endpoint = %q, want unknown", got)
		}
	}
	resolver.Refresh()
	if got := resolver.ScanCount(); got != 2 {
		t.Fatalf("explicit second refresh scans = %d, want 2", got)
	}
}

func TestResolveAuthDirPrecedence(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_AUTH_DIR", "/from-env")
	if got := ResolveDir([]byte(`auth_dir: /from-yaml`)); got != "/from-yaml" {
		t.Fatalf("YAML auth_dir = %q, want /from-yaml", got)
	}
	if got := ResolveDir([]byte(`{"auth_dir":"/from-json"}`)); got != "/from-json" {
		t.Fatalf("JSON auth_dir = %q, want /from-json", got)
	}
	if got := ResolveDir(nil); got != "/from-env" {
		t.Fatalf("env auth_dir = %q, want /from-env", got)
	}
}

func TestAuthScanBoundedAndNoSymlinkFollow(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(target, []byte(`{"proxy_url":"socks5://warp1:1080"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "linked.json")); err != nil {
		t.Fatal(err)
	}
	if kind, _, _ := readAuthFileEndpointData(filepath.Join(dir, "linked.json")); kind != quotamonitor.EndpointUnknown {
		t.Fatal("followed credential symlink")
	}
	tooLarge := filepath.Join(dir, "oversized.json")
	if err := os.WriteFile(tooLarge, []byte(strings.Repeat(" ", maxAuthFileSize+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if kind, _, _ := readAuthFileEndpointData(tooLarge); kind != quotamonitor.EndpointUnknown {
		t.Fatal("read oversized credential")
	}
	for i := 0; i < maxAuthDirEntries+1; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d.txt", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	resolver := New(dir)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	truncated, err := resolver.RefreshContext(ctx)
	if err != nil || !truncated {
		t.Fatalf("unbounded directory scan: %v %v", truncated, err)
	}
	link := filepath.Join(t.TempDir(), "auth-dir")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if New(link).Blocker() == "" {
		t.Fatal("accepted symlink directory")
	}
	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	before := resolver.ScanCount()
	if _, err := resolver.RefreshContext(canceled); err == nil || resolver.ScanCount() != before {
		t.Fatal("canceled scan committed")
	}
}

// TestEndpointResolverTopologyMapping verifies that endpoint resolution maps:
// - synthetic direct accounts -> direct
// - synthetic Plus and Codex Team accounts -> warp1
// - synthetic school accounts -> warp2
func TestCodexTeamPlanMarkerResolverRequiresExactCodexSource(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"codex-exact-team.json":    `{"type":"codex","id":"team-id","proxy_url":"http://warp1:8080"}`,
		"noncodex-exact-team.json": `{"type":"antigravity","id":"other-id","proxy_url":"http://warp1:8080"}`,
		"codex-explicit-plus.json": `{"type":"codex","id":"plus-id","proxy_url":"http://warp1:8080"}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolver := New(dir)
	if truncated, err := resolver.RefreshContext(context.Background()); err != nil || truncated {
		t.Fatalf("refresh=%v truncated=%v", err, truncated)
	}

	adapter := quotamonitor.NewAdapter(quotamonitor.WithEndpointResolver(resolver))
	accounts, err := adapter.ParseAuthFilesJSON([]byte(`[
		{"provider":"codex","name":"codex-exact-team.json","id":"team-id","alias":"verified"},
		{"provider":"codex","name":"missing-team.json","alias":"missing"},
		{"provider":"codex","name":"noncodex-exact-team.json","id":"other-id","alias":"contradictory"},
		{"provider":"codex","name":"codex-exact.json","id":"team-id","alias":"id-only"},
		{"provider":"codex","name":"../codex-exact-team.json","id":"","alias":"unsafe"},
		{"provider":"codex","name":"codex-exact-team.JSON","id":"","alias":"case-mismatch"},
		{"provider":"codex","name":"codex-explicit-plus.json","id":"plus-id","alias":"explicit-wins","plan_type":"pro"},
		{"provider":"codex","name":"missing-team.json","alias":"header-wins","quota":{"signals":{"x-codex-plan-type":"team"}}}
	]`))
	if err != nil || len(accounts) != 8 {
		t.Fatalf("accounts=%#v err=%v", accounts, err)
	}
	want := []string{"team", "", "", "", "", "", "pro", "team"}
	for i := range want {
		if accounts[i].PlanType != want[i] {
			t.Errorf("account %d plan=%q want=%q", i, accounts[i].PlanType, want[i])
		}
	}

	withoutResolver, err := quotamonitor.NewAdapter().ParseAuthFilesJSON([]byte(`[{"provider":"codex","name":"codex-exact-team.json"}]`))
	if err != nil || len(withoutResolver) != 1 || withoutResolver[0].PlanType != "" {
		t.Fatalf("unavailable resolver inferred Team: %#v err=%v", withoutResolver, err)
	}
}

func TestEndpointResolverTopologyMapping(t *testing.T) {
	dir := t.TempDir()

	// Write mock auth files matching the post-change topology:
	files := map[string]string{
		"antigravity-direct@example.invalid.json":                   `{"type":"antigravity","proxy_url":"direct"}`,
		"codex-11111111-direct@example.invalid-plus.json":           `{"type":"codex","proxy_url":""}`,
		"codex-22222222-proxy-plus@example.invalid-plus.json":   `{"type":"codex","proxy_url":"socks5://warp1:1080"}`,
		"codex-33333333-proxy-team@example.invalid-team.json":             `{"type":"codex","proxy_url":"http://warp1:8080"}`,
		"codex-44444444-school-a@example.invalid-k12.json":    `{"type":"codex","proxy_url":"socks5://warp2:1080"}`,
		"codex-55555555-school-b@example.invalid-k12.json": `{"type":"codex","proxy_url":"socks5://warp2:1080"}`,
	}

	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	resolver := New(dir)
	truncated, err := resolver.RefreshContext(context.Background())
	if err != nil || truncated {
		t.Fatalf("RefreshContext error: %v, truncated: %v", err, truncated)
	}

	tests := []struct {
		filename string
		provider string
		wantEP   quotamonitor.EndpointKind
	}{
		{"antigravity-direct@example.invalid.json", "antigravity", quotamonitor.EndpointDirect},
		{"codex-11111111-direct@example.invalid-plus.json", "codex", quotamonitor.EndpointDirect},
		{"codex-22222222-proxy-plus@example.invalid-plus.json", "codex", quotamonitor.EndpointWarp1},
		{"codex-33333333-proxy-team@example.invalid-team.json", "codex", quotamonitor.EndpointWarp1},
		{"codex-44444444-school-a@example.invalid-k12.json", "codex", quotamonitor.EndpointWarp2},
		{"codex-55555555-school-b@example.invalid-k12.json", "codex", quotamonitor.EndpointWarp2},
	}

	for _, tc := range tests {
		got := resolver.ResolveForProvider(tc.filename, "", tc.provider, tc.provider)
		if got != tc.wantEP {
			t.Errorf("file %s (%s): got endpoint %q, want %q", tc.filename, tc.provider, got, tc.wantEP)
		}
	}
}
