package plugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"quota-monitor/internal/secret"
)

// filesUnder lists every regular file below dir (relative paths).
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

func TestLearnManagementKeyKeepsItInMemoryOnly(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "")
	home := t.TempDir()
	t.Setenv("QUOTA_MONITOR_DATA_DIR", filepath.Join(home, "data"))
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	state.mu.Lock()
	state.learnedKey = ""
	state.mu.Unlock()
	configureFromEnv(nil)
	state.mu.Lock()
	if state.configured {
		state.mu.Unlock()
		t.Fatal("should be unconfigured without a key")
	}
	state.mu.Unlock()

	raw := []byte(`{"Method":"GET","Path":"` + mgmtBase + `/data/accounts","Headers":{"Authorization":["Bearer learned-key-123"]}}`)
	handleManagementHandle(raw)

	state.mu.Lock()
	ok := state.configured && state.client != nil && state.client.ManagementKey == "learned-key-123" && state.client.BaseURL == "http://127.0.0.1:8317/v0"
	state.mu.Unlock()
	if !ok {
		t.Fatal("key was not learned / client not configured with default URL")
	}

	// Nothing may be written to disk: not the key, not a master key, not anything.
	if files := filesUnder(t, home); len(files) != 0 {
		t.Fatalf("learning the key wrote files: %v", files)
	}

	// A host restart forgets the key: with no in-memory copy and no env var the
	// collector stays unconfigured until the next authenticated request.
	state.mu.Lock()
	state.learnedKey = ""
	state.mu.Unlock()
	configureFromEnv(nil)
	state.mu.Lock()
	configured := state.configured
	state.mu.Unlock()
	if configured {
		t.Fatal("key survived a restart without being persisted anywhere")
	}
}

func TestRegisterRemovesLegacyEncryptedKey(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "")
	dir := filepath.Join(t.TempDir(), "legacy")
	t.Setenv("QUOTA_MONITOR_DATA_DIR", dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"mgmt-key.enc", "master.key"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	purgeLegacyKeyOnce = sync.Once{}
	handleRegister([]byte(`{"schema_version":1}`))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("legacy key directory still present (err %v): %v", err, filesUnder(t, dir))
	}
	if secret.LegacyDir() != filepath.Clean(dir) {
		t.Fatal("legacy dir does not honour QUOTA_MONITOR_DATA_DIR")
	}
}

func TestLearnIgnoresResourceRoutesAndEnvWins(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "")
	state.mu.Lock()
	state.learnedKey = ""
	state.mu.Unlock()
	configureFromEnv(nil)
	// Resource (unauthenticated) route must never teach a key.
	handleManagementHandle([]byte(`{"Method":"GET","Path":"` + resourceBase + `/dashboard","Headers":{"Authorization":["Bearer evil"]}}`))
	state.mu.Lock()
	configured := state.configured
	learned := state.learnedKey
	state.mu.Unlock()
	if configured || strings.Contains(learned, "evil") {
		t.Fatal("resource route taught a key")
	}
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "env-key")
	configureFromEnv(nil)
	handleManagementHandle([]byte(`{"Method":"GET","Path":"` + mgmtBase + `/data/accounts","Headers":{"X-Management-Key":["other"]}}`))
	state.mu.Lock()
	k := state.client.ManagementKey
	state.mu.Unlock()
	if k != "env-key" {
		t.Fatalf("env key overridden: %q", k)
	}
}
