package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"quota-monitor/internal/dashboard"
	"testing"
)

// nodeDashboardCmd runs a Node script that reads the dashboard HTML as process.argv[1].
//
// Linux caps a single execve argument at 128KiB (MAX_ARG_STRLEN). The dashboard
// document is larger than that, so passing it directly fails with "argument list
// too long". The HTML is written to a temporary file instead, and a one-line
// preamble swaps process.argv[1] for its contents, leaving the scripts unchanged.
func nodeDashboardCmd(t *testing.T, node, script string) *exec.Cmd {
	t.Helper()
	file := filepath.Join(t.TempDir(), "dashboard.html")
	if err := os.WriteFile(file, []byte(dashboard.HTML), 0o600); err != nil {
		t.Fatalf("write dashboard html: %v", err)
	}
	const preamble = "process.argv[1] = require('fs').readFileSync(process.argv[1], 'utf8');\n"
	return exec.Command(node, "-e", preamble+script, "--", file)
}
