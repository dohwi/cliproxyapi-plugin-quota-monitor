// Package secret keeps the host management key encrypted at rest.
//
// The key is sealed with AES-256-GCM using a random master key stored next to
// it with 0600 permissions. This keeps the credential out of plaintext config
// files, backups and accidental commits. It does not defend against another
// process running as the same OS user, which could read both files.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	masterFile = "master.key"
	sealedFile = "mgmt-key.enc"
	// MaxKeyLen bounds accepted keys.
	MaxKeyLen = 512
)

// DefaultDir returns the per-user directory, honouring QUOTA_MONITOR_DATA_DIR.
// It returns "" when no usable location exists (the key then stays in memory).
func DefaultDir() string {
	if d := strings.TrimSpace(os.Getenv("QUOTA_MONITOR_DATA_DIR")); d != "" {
		return filepath.Clean(d)
	}
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		return ""
	}
	return filepath.Join(base, "cliproxyapi", "quota-monitor")
}

// ValidKey reports whether key is acceptable to store.
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

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func masterKey(dir string, create bool) ([]byte, error) {
	path := filepath.Join(dir, masterFile)
	if b, err := os.ReadFile(path); err == nil {
		if len(b) == 32 {
			return b, nil
		}
		return nil, errors.New("secret: master key is corrupt")
	} else if !errors.Is(err, os.ErrNotExist) || !create {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := writeAtomic(path, k); err != nil {
		return nil, err
	}
	return k, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Save encrypts and persists key in dir.
func Save(dir, key string) error {
	if dir == "" {
		return errors.New("secret: no storage directory")
	}
	if !ValidKey(key) {
		return errors.New("secret: invalid key")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	mk, err := masterKey(dir, true)
	if err != nil {
		return err
	}
	aead, err := gcm(mk)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nonce, nonce, []byte(key), []byte(sealedFile))
	return writeAtomic(filepath.Join(dir, sealedFile), []byte(base64.StdEncoding.EncodeToString(sealed)))
}

// Load returns the stored key, or "" when none is stored or it cannot be decrypted.
func Load(dir string) string {
	if dir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(dir, sealedFile))
	if err != nil {
		return ""
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return ""
	}
	mk, err := masterKey(dir, false)
	if err != nil {
		return ""
	}
	aead, err := gcm(mk)
	if err != nil || len(sealed) < aead.NonceSize() {
		return ""
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(sealedFile))
	if err != nil || !ValidKey(string(plain)) {
		return ""
	}
	return string(plain)
}

// Clear removes the stored key.
func Clear(dir string) {
	if dir != "" {
		_ = os.Remove(filepath.Join(dir, sealedFile))
	}
}
