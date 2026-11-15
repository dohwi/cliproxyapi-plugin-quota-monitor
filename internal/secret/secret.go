// Package secret validates the host management key and removes the encrypted
// key files that versions before 0.6.2 wrote to disk.
//
// The plugin no longer persists the management key anywhere: a learned key lives
// only in process memory, and after a host restart it is learned again from the
// next host-authenticated dashboard request (or taken from QUOTA_MONITOR_MGMT_KEY).
package secret

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	// Files written by versions before 0.6.2. Only ever deleted now.
	legacyMasterFile = "master.key"
	legacySealedFile = "mgmt-key.enc"
	// MaxKeyLen bounds accepted keys.
	MaxKeyLen = 512
)

// LegacyDir returns the directory older versions stored the encrypted key in,
// honouring QUOTA_MONITOR_DATA_DIR. It returns "" when none can be resolved.
func LegacyDir() string {
	if d := strings.TrimSpace(os.Getenv("QUOTA_MONITOR_DATA_DIR")); d != "" {
		return filepath.Clean(d)
	}
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		return ""
	}
	return filepath.Join(base, "cliproxyapi", "quota-monitor")
}

// ValidKey reports whether key is acceptable to use.
func ValidKey(key string) bool {
	if key == "" || len(key) > MaxKeyLen {
		return false
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// PurgeLegacy deletes the encrypted key and its master key left by older
// versions in dir, then removes dir itself if it is left empty. Missing files
// are not an error; nothing outside those two names is touched.
func PurgeLegacy(dir string) error {
	if dir == "" {
		return nil
	}
	var errs []error
	for _, name := range []string{legacySealedFile, legacyMasterFile} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	// Remove fails on a non-empty directory, which is exactly what we want.
	_ = os.Remove(dir)
	return errors.Join(errs...)
}
