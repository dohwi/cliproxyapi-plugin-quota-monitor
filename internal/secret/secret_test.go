package secret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTripAndEncrypted(t *testing.T) {
	dir := t.TempDir()
	const key = "super-secret-management-key"
	if got := Load(dir); got != "" {
		t.Fatalf("empty dir returned %q", got)
	}
	if err := Save(dir, key); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got != key {
		t.Fatalf("round trip got %q", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, sealedFile))
	if strings.Contains(string(raw), key) {
		t.Fatal("key stored in plaintext")
	}
	if runtimeUnix() {
		for _, f := range []string{sealedFile, masterFile} {
			st, err := os.Stat(filepath.Join(dir, f))
			if err != nil || st.Mode().Perm() != 0o600 {
				t.Fatalf("%s perm %v err %v", f, st.Mode().Perm(), err)
			}
		}
	}
}

func TestLoadRejectsTamperingAndWrongMaster(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "k1"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, sealedFile)
	raw, _ := os.ReadFile(p)
	raw[len(raw)-3] ^= 1
	_ = os.WriteFile(p, raw, 0o600)
	if got := Load(dir); got != "" {
		t.Fatalf("tampered data accepted: %q", got)
	}
	if err := Save(dir, "k2"); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, masterFile))
	if got := Load(dir); got != "" {
		t.Fatalf("missing master accepted: %q", got)
	}
}

func TestValidKeyAndClear(t *testing.T) {
	for _, bad := range []string{"", "a\nb", strings.Repeat("x", MaxKeyLen+1)} {
		if ValidKey(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
	dir := t.TempDir()
	if Save(dir, "bad\nkey") == nil {
		t.Fatal("saved invalid key")
	}
	_ = Save(dir, "ok")
	Clear(dir)
	if Load(dir) != "" {
		t.Fatal("clear failed")
	}
}

func runtimeUnix() bool { return os.PathSeparator == '/' }
