// Package authdir resolves safe endpoint and plan metadata from the CLIProxyAPI
// auth directory. Only whitelisted fields are read; raw URLs, paths and
// identifiers never leave this package.
package authdir

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

const (
	// scanTimeout bounds a standalone Refresh scan.
	scanTimeout = 25 * time.Second
	// maxAuthDirEntries bounds directory entries read per scan.
	maxAuthDirEntries = 2048
	// maxAuthDirFiles caps the number of .json files scanned per refresh to
	// prevent unbounded memory use on misconfigured or hostile directories.
	maxAuthDirFiles = 512
	// maxAuthFileSize is the maximum size of a single auth JSON file read
	// during proxy_url extraction. Files larger than this are skipped safely.
	maxAuthFileSize = 256 * 1024 // 256 KiB

	// containerAuthDir is the only hard-coded fallback path. It is used
	// exclusively inside the known container deployment layout when no
	// auth_dir is available from config_yaml or QUOTA_MONITOR_AUTH_DIR.
	// If this path is unreadable the resolver reports a blocker and returns
	// EndpointUnknown for all entries — no error is logged or returned.
	containerAuthDir = "/root/.cli-proxy-api"
)

// Resolver implements EndpointResolver by scanning the auth
// directory once per collect cycle. It reads only the minimal "proxy_url"
// field from each JSON file; raw URLs are never stored beyond the scope of
// the scan function. The result map (filename → EndpointKind enum) is the
// only persistent artifact and contains no sensitive data.
type Resolver struct {
	mu      sync.RWMutex
	authDir string // resolved, cleaned path; empty = unavailable
	blocker string // non-empty when resolver cannot operate
	// cache retains only canonical filename / immutable JSON id → safe enum.
	// It never contains raw URLs, paths, or identifiers in the DTO.
	byName      map[string]quotamonitor.EndpointKind
	byID        map[string]quotamonitor.EndpointKind
	planByName  map[string]string
	planByID    map[string]string
	planByAlias map[string]string
	// ambiguousIDs prevents a duplicate immutable ID from silently selecting a
	// credential. byAlias contains only Codex's explicitly supported -plus
	// compatibility pairs; collisions are omitted and recorded as ambiguous.
	ambiguousIDs     map[string]struct{}
	byAlias          map[string]quotamonitor.EndpointKind
	ambiguousAliases map[string]struct{}
	// scans is a testable count of complete directory scans. It is not exposed.
	scans uint64
}

// New creates a resolver for the given directory path.
// If dir is empty or unreadable, the resolver is in blocker state and
// always returns EndpointUnknown. dir must be an absolute path.
func New(dir string) *Resolver {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return &Resolver{
			blocker:          "auth directory not configured",
			byName:           map[string]quotamonitor.EndpointKind{},
			byID:             map[string]quotamonitor.EndpointKind{},
			planByName:       map[string]string{},
			planByID:         map[string]string{},
			planByAlias:      map[string]string{},
			ambiguousIDs:     map[string]struct{}{},
			byAlias:          map[string]quotamonitor.EndpointKind{},
			ambiguousAliases: map[string]struct{}{},
		}
	}
	clean := filepath.Clean(dir)
	if !filepath.IsAbs(clean) {
		return &Resolver{
			blocker:          "auth directory not accessible",
			byName:           map[string]quotamonitor.EndpointKind{},
			byID:             map[string]quotamonitor.EndpointKind{},
			planByName:       map[string]string{},
			planByID:         map[string]string{},
			planByAlias:      map[string]string{},
			ambiguousIDs:     map[string]struct{}{},
			byAlias:          map[string]quotamonitor.EndpointKind{},
			ambiguousAliases: map[string]struct{}{},
		}
	}
	r := &Resolver{
		authDir:          clean,
		byName:           map[string]quotamonitor.EndpointKind{},
		byID:             map[string]quotamonitor.EndpointKind{},
		planByName:       map[string]string{},
		planByID:         map[string]string{},
		planByAlias:      map[string]string{},
		ambiguousIDs:     map[string]struct{}{},
		byAlias:          map[string]quotamonitor.EndpointKind{},
		ambiguousAliases: map[string]struct{}{},
	}
	// Proactively validate readability; sets blocker if inaccessible.
	if err := r.validateDir(); err != nil {
		r.authDir = ""
		r.blocker = "auth directory not accessible"
	}
	return r
}

func (r *Resolver) validateDir() error {
	info, err := os.Lstat(r.authDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	return nil
}

func (r *Resolver) Blocker() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.blocker
}

// Dir returns the resolved auth directory, or "" when unavailable.
func (r *Resolver) Dir() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.authDir
}

// Resolve preserves the original exact-only resolver contract for callers
// without management provider/type information. The Codex plan alias is
// deliberately unavailable here because it requires that proof.
func (r *Resolver) Resolve(name, id string) quotamonitor.EndpointKind {
	return r.resolve(name, id, false)
}

// ResolveForProvider implements quota-monitor's optional provider-aware
// contract. Codex's CredentialFileName plan marker is the sole compatibility
// alias and is considered only after exact basename and immutable-ID matching.
func (r *Resolver) ResolveForProvider(name, id, provider, entryType string) quotamonitor.EndpointKind {
	return r.resolve(name, id, isCodexManagementType(provider, entryType))
}

// ResolvePlanForProvider returns only allowlisted Codex plan markers. Team
// requires an exact canonical filename match to a scanned Codex credential;
// older non-Team markers may also use the existing immutable-ID/alias paths.
func (r *Resolver) ResolvePlanForProvider(name, id, provider, entryType string) string {
	if !isCodexManagementType(provider, entryType) {
		return ""
	}
	key, safeName := canonicalAuthFilename(name)
	id = strings.TrimSpace(id)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if safeName {
		if plan := r.planByName[key]; plan != "" {
			return plan
		}
		// Team filename inference is intentionally exact-name only. An alias may
		// preserve older non-Team plan compatibility, but it cannot prove that a
		// raw management -team marker names the verified on-disk credential.
		if plan := r.planByAlias[key]; plan != "team" {
			return plan
		}
	}
	if id != "" {
		if _, ambiguous := r.ambiguousIDs[id]; !ambiguous {
			if plan := r.planByID[id]; plan != "team" {
				return plan
			}
		}
	}
	return ""
}

func (r *Resolver) resolve(name, id string, allowCodexAlias bool) quotamonitor.EndpointKind {
	key, safeName := canonicalAuthFilename(name)
	id = strings.TrimSpace(id)

	r.mu.RLock()
	defer r.mu.RUnlock()
	// Exact safe basename is authoritative, including when the ID or alias is
	// ambiguous. A real disk filename must never be overridden by an alias.
	if safeName {
		if kind, found := r.byName[key]; found {
			return kind
		}
	}
	if id != "" {
		if _, ambiguous := r.ambiguousIDs[id]; ambiguous {
			return quotamonitor.EndpointUnknown
		}
		if kind, found := r.byID[id]; found {
			return kind
		}
	}
	if !safeName || !allowCodexAlias {
		return quotamonitor.EndpointUnknown
	}
	if _, ambiguous := r.ambiguousAliases[key]; ambiguous {
		return quotamonitor.EndpointUnknown
	}
	if kind, found := r.byAlias[key]; found {
		return kind
	}
	return quotamonitor.EndpointUnknown
}

func isCodexManagementType(provider, entryType string) bool {
	// Match Adapter's provider selection: management provider is authoritative
	// when supplied, and type is only its compatibility fallback. A contradictory
	// non-Codex provider must not acquire a Codex filename alias.
	if strings.TrimSpace(provider) != "" {
		return isCodexType(provider)
	}
	return isCodexType(entryType)
}

// isCodexType is deliberately a normalized allowlist rather than a substring
// test: similarly named providers must never receive filename aliases.
func isCodexType(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "codex")
}

// canonicalAuthFilename accepts management's filename form, including a path
// prefix emitted by a file store, and returns its opaque basename. Exact .json
// suffix semantics and Linux case are retained; no lower-casing is allowed.
func canonicalAuthFilename(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", false
	}
	// Relative FileName values may include provider directories, but never may
	// escape auth_dir. Treat backslash as a literal Linux filename character.
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
		return "", false
	}
	base := filepath.Base(name)
	if base == "." || base == ".." || !strings.HasSuffix(base, ".json") {
		return "", false
	}
	return base, true
}

// Refresh scans the auth directory once and rebuilds exact filename and
// immutable JSON-id mappings. It reads only proxy_url and top-level id; raw
// values are never stored. Symlinks, non-.json files, oversized files, and
// unreadable files are silently skipped. No path or content is logged or returned.
func (r *Resolver) Refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()
	_, _ = r.RefreshContext(ctx)
}

// RefreshContext is Refresh bound to ctx. The returned flag reports a
// truncated scan.
func (r *Resolver) RefreshContext(ctx context.Context) (bool, error) {
	r.mu.RLock()
	dir := r.authDir
	r.mu.RUnlock()
	if dir == "" {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Open the directory and each child without following symlinks. openat binds
	// children to this FD even if the configured directory is renamed mid-scan.
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false, errors.New("auth directory not accessible")
	}
	directory := os.NewFile(uintptr(fd), "auth-directory")
	defer directory.Close()
	entries, err := directory.ReadDir(maxAuthDirEntries + 1)
	if err != nil && err != io.EOF {
		return false, errors.New("auth directory not accessible")
	}
	truncated := len(entries) > maxAuthDirEntries
	if truncated {
		entries = entries[:maxAuthDirEntries]
	}

	newByName := make(map[string]quotamonitor.EndpointKind, len(entries))
	newByID := make(map[string]quotamonitor.EndpointKind, len(entries))
	newPlanByName := make(map[string]string, len(entries))
	newPlanByID := make(map[string]string, len(entries))
	newPlanByAlias := make(map[string]string, len(entries))
	ambiguousIDs := make(map[string]struct{})
	newByAlias := make(map[string]quotamonitor.EndpointKind)
	ambiguousAliases := make(map[string]struct{})
	addAlias := func(alias string, kind quotamonitor.EndpointKind) {
		if _, ambiguous := ambiguousAliases[alias]; ambiguous {
			return
		}
		if _, exists := newByAlias[alias]; exists {
			delete(newByAlias, alias)
			ambiguousAliases[alias] = struct{}{}
			return
		}
		newByAlias[alias] = kind
	}
	scanned := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return true, err
		}
		if scanned >= maxAuthDirFiles {
			truncated = true
			break
		}
		// Skip directories and symlinks. Type bits are not always populated by
		// every filesystem, so verify the resolved entry is a regular file too.
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			continue
		}
		name := entry.Name()
		// os.ReadDir supplies a basename. Keep extension and case exactly as
		// supplied by Linux; a different-case name is a different credential.
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		kind, immutableID, diskType := readAuthFileEndpointAt(fd, name)
		newByName[name] = kind
		plan := ""
		if isCodexType(diskType) {
			plan = codexPlanMarker(name)
			if plan != "" {
				newPlanByName[name] = plan
				if plan != "team" {
					if alias, ok := codexPlanBase(name, plan); ok {
						if previous, exists := newPlanByAlias[alias]; !exists || previous == plan {
							newPlanByAlias[alias] = plan
						} else {
							delete(newPlanByAlias, alias)
						}
					}
				}
			}
			// CredentialFileName's normalized plan marker is a meaningful Codex
			// contract: only a disk filename ending in the exact "-plus.json"
			// marker may alias its one corresponding unmarked filename. Never
			// manufacture aliases for arbitrary filenames or generic suffixes.
			if base, ok := codexPlusBase(name); ok {
				addAlias(base, kind)
			}
		}
		if immutableID != "" {
			// Any duplicate is ambiguous, even if both proxy classifications match.
			if _, ambiguous := ambiguousIDs[immutableID]; ambiguous {
				// already rejected
			} else if _, exists := newByID[immutableID]; exists {
				delete(newByID, immutableID)
				delete(newPlanByID, immutableID)
				ambiguousIDs[immutableID] = struct{}{}
			} else {
				newByID[immutableID] = kind
				if plan != "" && plan != "team" {
					newPlanByID[immutableID] = plan
				}
			}
		}
		scanned++
	}

	if err := ctx.Err(); err != nil {
		return true, err
	}
	r.mu.Lock()
	r.byName = newByName
	r.byID = newByID
	r.planByName = newPlanByName
	r.planByID = newPlanByID
	r.planByAlias = newPlanByAlias
	r.ambiguousIDs = ambiguousIDs
	r.byAlias = newByAlias
	r.ambiguousAliases = ambiguousAliases
	r.scans++
	r.mu.Unlock()
	return truncated, nil
}

// ScanCount is intentionally package-private for focused one-scan tests.
func (r *Resolver) ScanCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.scans
}

// codexPlusBase removes exactly one terminal Codex plan marker. It does not
// normalize arbitrary "plus" strings or non-.json names.
func codexPlusBase(name string) (string, bool) {
	const suffix = "-plus.json"
	if !strings.HasSuffix(name, suffix) || len(name) == len(suffix) {
		return "", false
	}
	return strings.TrimSuffix(name, suffix) + ".json", true
}

func codexPlanMarker(name string) string {
	for _, plan := range []string{"k12", "plus", "pro", "team", "free"} {
		if strings.HasSuffix(name, "-"+plan+".json") {
			return plan
		}
	}
	return ""
}

func codexPlanBase(name, plan string) (string, bool) {
	suffix := "-" + plan + ".json"
	if (plan != "k12" && plan != "plus" && plan != "pro" && plan != "team" && plan != "free") || !strings.HasSuffix(name, suffix) || len(name) == len(suffix) {
		return "", false
	}
	return strings.TrimSuffix(name, suffix) + ".json", true
}

// readAuthFileEndpointData reads only proxy_url, top-level immutable id, and
// type. All transient raw values are discarded before return; type is used
// solely for the normalized Codex alias allowlist.
func readAuthFileEndpointData(path string) (quotamonitor.EndpointKind, string, string) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return quotamonitor.EndpointUnknown, "", ""
	}
	return readAuthEndpointFD(fd)
}

func readAuthFileEndpointAt(dirFD int, name string) (quotamonitor.EndpointKind, string, string) {
	fd, err := syscall.Openat(dirFD, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return quotamonitor.EndpointUnknown, "", ""
	}
	return readAuthEndpointFD(fd)
}

func readAuthEndpointFD(fd int) (quotamonitor.EndpointKind, string, string) {
	file := os.NewFile(uintptr(fd), "auth-file")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxAuthFileSize {
		return quotamonitor.EndpointUnknown, "", ""
	}
	data, err := io.ReadAll(io.LimitReader(file, maxAuthFileSize+1))
	if err != nil || len(data) > maxAuthFileSize {
		return quotamonitor.EndpointUnknown, "", ""
	}
	var minimal struct {
		ProxyURL string `json:"proxy_url"`
		ID       string `json:"id"`
		Type     string `json:"type"`
	}
	if json.Unmarshal(data, &minimal) != nil {
		return quotamonitor.EndpointUnknown, "", ""
	}
	kind := quotamonitor.ParseEndpointKindFromProxyURL(minimal.ProxyURL)
	id := strings.TrimSpace(minimal.ID)
	typeName := strings.TrimSpace(minimal.Type)
	minimal.ProxyURL = ""
	minimal.ID = ""
	minimal.Type = ""
	return kind, id, typeName
}

// ResolveDir determines the auth directory from (in priority order):
//  1. config_yaml "auth-dir" or "auth_dir" key (passed by host at plugin.register)
//  2. QUOTA_MONITOR_AUTH_DIR environment variable
//  3. previously configured valid directory (preserved across reconfigures)
//  4. containerAuthDir hard-coded fallback (only if readable)
//
// Returns "" if no valid directory is found.
func ResolveDir(configYAML []byte) string {
	return ResolveDirWithFallback(configYAML, "")
}

func ResolveDirWithFallback(configYAML []byte, previousDir string) string {
	// 1. config_yaml key. CLIProxyAPI uses auth-dir; auth_dir remains a
	// compatibility spelling for plugin-local JSON configurations.
	if len(configYAML) > 0 {
		var cfg struct {
			AuthDir       string `json:"auth_dir"`
			AuthDirDashed string `json:"auth-dir"`
		}
		// Try JSON first (plugin.go uses JSON internally).
		if json.Unmarshal(configYAML, &cfg) == nil {
			if d := strings.TrimSpace(cfg.AuthDir); d != "" {
				return filepath.Clean(d)
			}
			if d := strings.TrimSpace(cfg.AuthDirDashed); d != "" {
				return filepath.Clean(d)
			}
		}
		// Try YAML scalar spellings via simple line scan (no yaml dep).
		if d := extractYAMLScalar(configYAML, "auth-dir"); d != "" {
			return filepath.Clean(d)
		}
		if d := extractYAMLScalar(configYAML, "auth_dir"); d != "" {
			return filepath.Clean(d)
		}
	}
	// 2. Environment variable
	if d := strings.TrimSpace(os.Getenv("QUOTA_MONITOR_AUTH_DIR")); d != "" {
		return filepath.Clean(d)
	}
	// 3. Previously configured directory (if not empty and not container fallback)
	if previousDir != "" && previousDir != containerAuthDir {
		return filepath.Clean(previousDir)
	}
	// 4. Container fallback. Resolver construction performs the readability
	// check and enters blocker state when the path is unavailable.
	return containerAuthDir
}

// extractYAMLScalar is a minimal, zero-dependency extractor for a scalar
// mapping value from YAML bytes. It handles the common "key: value" form.
// It does NOT handle multiline, anchors, or complex YAML; returns "" on any
// ambiguity. No YAML library is imported to avoid dependency bloat.
func extractYAMLScalar(data []byte, key string) string {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key+":") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
		// Strip optional quotes.
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		val = strings.TrimSpace(val)
		if val != "" && !strings.ContainsAny(val, "{[") {
			return val
		}
	}
	return ""
}
