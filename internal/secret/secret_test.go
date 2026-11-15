package secret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidKey(t *testing.T) {
	for _, bad := range []string{"", "a\nb", "a\x7fb", strings.Repeat("x", MaxKeyLen+1)} {
		if ValidKey(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
	if !ValidKey("ok-key") {
		t.Fatal("rejected a plain key")
	}
}

func TestPurgeLegacyRemovesOnlyKeyFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quota-monitor")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{legacySealedFile, legacyMasterFile, "keep.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := PurgeLegacy(dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{legacySealedFile, legacyMasterFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Fatalf("%s still present (err %v)", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
}

func TestPurgeLegacyRemovesEmptyDirAndToleratesMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quota-monitor")
	if err := PurgeLegacy(dir); err != nil {
		t.Fatalf("missing dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, legacyMasterFile), make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PurgeLegacy(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("empty legacy dir kept (err %v)", err)
	}
	if err := PurgeLegacy(""); err != nil {
		t.Fatal(err)
	}
}
