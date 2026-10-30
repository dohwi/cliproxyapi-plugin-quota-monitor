package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quota-monitor/internal/secret"
)

func TestLearnManagementKeyFromAuthenticatedRequest(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "")
	secret.Clear(secret.DefaultDir())
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
	if got := secret.Load(secret.DefaultDir()); got != "learned-key-123" {
		t.Fatalf("not persisted: %q", got)
	}
	enc, _ := os.ReadFile(filepath.Join(secret.DefaultDir(), "mgmt-key.enc"))
	if strings.Contains(string(enc), "learned-key-123") {
		t.Fatal("plaintext on disk")
	}

	// A fresh registration (host restart) restores the key from the encrypted store.
	state.mu.Lock()
	state.learnedKey = ""
	state.mu.Unlock()
	configureFromEnv(nil)
	state.mu.Lock()
	restored := state.configured && state.client.ManagementKey == "learned-key-123"
	state.mu.Unlock()
	if !restored {
		t.Fatal("key not restored after re-register")
	}
}

func TestLearnIgnoresResourceRoutesAndEnvWins(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "")
	secret.Clear(secret.DefaultDir())
	configureFromEnv(nil)
	// Resource (unauthenticated) route must never teach a key.
	handleManagementHandle([]byte(`{"Method":"GET","Path":"` + resourceBase + `/dashboard","Headers":{"Authorization":["Bearer evil"]}}`))
	state.mu.Lock()
	configured := state.configured
	state.mu.Unlock()
	if configured || secret.Load(secret.DefaultDir()) != "" {
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
