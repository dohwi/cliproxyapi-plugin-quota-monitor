package authdir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

// The portable os.Root based scan must keep the guarantees the former O_NOFOLLOW/openat
// code gave: symlinked files and directories are never followed, special files never
// block the collector, and nothing outside the directory is read.
func TestOpenNeverFollowsSymlinksOrEscapesRoot(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"proxy_url":"socks5://127.0.0.1:1080","id":"leak"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.json"), []byte(`{"proxy_url":"http://warp.invalid","id":"real"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "escape.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	root, err := openAuthRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if f, err := openChildNoFollow(root, "escape.json"); err == nil {
		f.Close()
		t.Fatal("symlink to a file outside the directory must not be opened")
	}
	if f, err := openChildNoFollow(root, "../"+filepath.Base(outside)+"/secret.json"); err == nil {
		f.Close()
		t.Fatal("parent traversal must not be opened")
	}
	if f, err := openChildNoFollow(root, "real.json"); err != nil {
		t.Fatalf("regular file must open: %v", err)
	} else {
		f.Close()
	}
	if f, err := openAuthFileNoFollow(filepath.Join(dir, "escape.json")); err == nil {
		f.Close()
		t.Fatal("path-based open must not follow a final symlink")
	}

	link := filepath.Join(t.TempDir(), "dirlink")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if r, err := openAuthRoot(link); err == nil {
		r.Close()
		t.Fatal("a symlinked auth directory must be refused")
	}

	r := New(dir)
	if _, err := r.RefreshContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if kind := r.Resolve("escape.json", "leak"); kind != quotamonitor.EndpointUnknown {
		t.Fatalf("escaping symlink contributed %v", kind)
	}
}

func TestOpenDoesNotBlockOnSpecialFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs on Windows")
	}
	dir := t.TempDir()
	if err := mkfifo(filepath.Join(dir, "pipe.json")); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := New(dir)
		_, _ = r.RefreshContext(t.Context())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scan blocked on a FIFO named *.json")
	}
}
