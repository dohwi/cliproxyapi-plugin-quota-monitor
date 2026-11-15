package plugin

import (
	"os"
	"testing"
)

// TestMain points the legacy key directory at a temp dir so registration's
// cleanup of pre-0.6.2 key files never touches the real per-user config directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "quota-monitor-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("QUOTA_MONITOR_DATA_DIR", dir)
	os.Unsetenv("QUOTA_MONITOR_MGMT_KEY")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
