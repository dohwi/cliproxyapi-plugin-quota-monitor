package quotamonitor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status represents the evaluated health/freshness of passive quota signals.
type Status string

const (
	StatusKnown       Status = "known"
	StatusUnknown     Status = "unknown"
	StatusStale       Status = "stale"
	StatusUnsupported Status = "unsupported"
	StatusError       Status = "error"
)

// DefaultStaleThreshold is the default duration after which an observed snapshot is considered stale.
const DefaultStaleThreshold = 15 * time.Minute

// RecentUsageWindow bounds audited token-use lookups and dashboard selection.
const RecentUsageWindow = 15 * time.Minute

// MaxBodyBytes is the maximum allowed response size when fetching management auth-files (2MB).
const MaxBodyBytes int64 = 2 * 1024 * 1024

// MaxClockFutureTolerance defines the acceptable clock drift into the future for observed_at.
const MaxClockFutureTolerance = 60 * time.Second

// MaxSignalsCount is the maximum number of headers retained in raw signals.
const MaxSignalsCount = 64

// MaxSignalValueLen is the maximum byte length of any quota signal value.
const MaxSignalValueLen = 512

// QuotaWindow represents a parsed quota window (e.g. primary, secondary, code-review, additional limits).
type QuotaWindow struct {
	Name             string     `json:"name"`
	WindowMinutes    *int       `json:"window_minutes,omitempty"`
	UsedPercent      *float64   `json:"used_percent,omitempty"` // pointer to distinguish explicit 0.0 from missing
	RemainingPercent *float64   `json:"remaining_percent,omitempty"`
	LimitReached     *bool      `json:"limit_reached,omitempty"`
	Allowed          *bool      `json:"allowed,omitempty"`
	LimitName        string     `json:"limit_name,omitempty"`
	ResetAfterSec    *int64     `json:"reset_after_seconds,omitempty"`
	ResetAt          *time.Time `json:"reset_at,omitempty"`
	OverLimitPercent *float64   `json:"over_secondary_limit_percent,omitempty"`
}

// clampPercent clamps an arbitrary percentage value into the [0,100] range,
// treating NaN/Inf as 0 so callers always receive a safe finite number.
func clampPercent(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	if f < 0 {
		return 0
	}
	if f > 100 {
		return 100
	}
	return f
}

// normalizeWindow enforces the canonical used/remaining invariant on a single
// window and returns a pointer to the (possibly mutated) stored value.
//
// Contract:
//   - If limit_reached==true OR allowed==false, the window is fully exhausted:
//     used=100, remaining=0 (this overrides any upstream-provided value).
//   - Otherwise both used and remaining are stored, clamped to [0,100], and
//     kept mutually consistent (remaining = 100 - used). When upstream already
//     provides an explicit remaining value we reconcile: if both used and
//     remaining are present but inconsistent, remaining is normalized to
//     100-used so the two always sum to 100.
func normalizeWindow(w *QuotaWindow) {
	if w == nil {
		return
	}
	limitReached := w.LimitReached != nil && *w.LimitReached
	blocked := w.Allowed != nil && !*w.Allowed
	if limitReached || blocked {
		used := 100.0
		remaining := 0.0
		w.UsedPercent = &used
		w.RemainingPercent = &remaining
		return
	}

	var used, remaining *float64
	if w.UsedPercent != nil {
		v := clampPercent(*w.UsedPercent)
		used = &v
	}
	if w.RemainingPercent != nil {
		v := clampPercent(*w.RemainingPercent)
		remaining = &v
	}

	switch {
	case used != nil && remaining != nil:
		// Both present: force consistency using used as the authoritative value.
		r := 100 - *used
		remaining = &r
	case used != nil:
		r := 100 - *used
		remaining = &r
	case remaining != nil:
		u := 100 - *remaining
		used = &u
	default:
		// Neither provided: nothing to normalize.
		return
	}
	w.UsedPercent = used
	w.RemainingPercent = remaining
}

// normalizeWindows applies normalizeWindow to every window in a slice.
func normalizeWindows(ws []QuotaWindow) {
	for i := range ws {
		normalizeWindow(&ws[i])
	}
}

// OperationalStatus captures runtime health, counts, and operational timestamps
// exposed safely by management auth-files (e.g. v7.2.155) without leaking credentials or tokens.
type OperationalStatus struct {
	Status              string     `json:"status,omitempty"`
	StatusMessage       string     `json:"status_message,omitempty"`
	Disabled            *bool      `json:"disabled,omitempty"`
	Unavailable         *bool      `json:"unavailable,omitempty"`
	Success             *int64     `json:"success,omitempty"`
	Failed              *int64     `json:"failed,omitempty"`
	RecentRequestCount  int        `json:"recent_request_count"`
	RecentRequestLatest *time.Time `json:"recent_request_latest,omitempty"`
	LastRefresh         *time.Time `json:"last_refresh,omitempty"`
	NextRetryAfter      *time.Time `json:"next_retry_after,omitempty"`
	AccountType         string     `json:"account_type,omitempty"`
}

// AccountQuota represents the parsed passive quota snapshot for a single credential.
// ResetCredits is the deliberately minimal, safe reset-rights DTO. Counts
// come from verified provider inventory; nearest_expiry is derived only from
// owned, available reset rights, not ordinary quota reset timestamps.
// It never contains a credit ID, grant, or raw provider response.
type ResetCredits struct {
	AvailableCount           *int       `json:"available_count,omitempty"`
	ApplicableAvailableCount *int       `json:"applicable_available_count,omitempty"`
	NearestExpiry            *time.Time `json:"nearest_expiry,omitempty"`
}

// APICredits is separate from reset rights. It represents only the general
// WHAM credits object and must never be interpreted as quota-reset rights.
type APICredits struct {
	HasCredits *bool    `json:"has_credits,omitempty"`
	Unlimited  *bool    `json:"unlimited,omitempty"`
	Balance    *float64 `json:"balance,omitempty"`
}

type AccountQuota struct {
	AccountKey    string             `json:"account_key"`
	AuthIndex     string             `json:"auth_index,omitempty"`
	Priority      int                `json:"priority,omitempty"`
	Provider      string             `json:"provider"`
	Account       string             `json:"account"`
	Status        Status             `json:"status"`
	ObservedAt    *time.Time         `json:"observed_at,omitempty"`
	FetchedAt     time.Time          `json:"fetched_at"`
	PlanType      string             `json:"plan_type,omitempty"`
	ActiveLimit   string             `json:"active_limit,omitempty"`
	RetryAfter    *int64             `json:"retry_after,omitempty"`
	Credits       map[string]string  `json:"credits,omitempty"` // legacy passive header credits
	APICredits    *APICredits        `json:"api_credits,omitempty"`
	ResetCredits  *ResetCredits      `json:"reset_credits,omitempty"`
	Windows       []QuotaWindow      `json:"windows,omitempty"`
	Operational   *OperationalStatus `json:"operational,omitempty"`
	RawSignals    map[string]string  `json:"raw_signals,omitempty"` // whitelisted signals only
	Error         string             `json:"error,omitempty"`
	Source        string             `json:"source,omitempty"` // active, passive, or both
	LastSuccessAt *time.Time         `json:"last_success_at,omitempty"`
	Stale         bool               `json:"stale,omitempty"`
	ErrorKind     string             `json:"error_kind,omitempty"`
	// Endpoint is a safe allowlisted enum (direct|warp1|warp2|unknown) derived
	// from proxy_url in the raw auth file via EndpointResolver.
	// URL/host/IP/token details are never stored.
	Endpoint  EndpointKind `json:"endpoint,omitempty"`
	projectID string       `json:"-"`
	accountID string       `json:"-"`
	// authFileName is the stable auth file name (e.g. "user.json") used as
	// the join key between management list entries and disk auth files.
	// It is never serialized or logged.
	authFileName string     `json:"-"`
	LastUsedAt   *time.Time `json:"-"` // enriched from usage-audit; never serialized
}

// Adapter parses auth-files JSON and extracts passive quota observations.
type Adapter struct {
	StaleThreshold time.Duration
	Clock          func() time.Time
	// EndpointResolver resolves the safe endpoint enum for each account
	// by reading proxy_url from raw auth files. When nil, all endpoints
	// default to EndpointUnknown. The resolver must never log or persist
	// raw proxy URLs.
	EndpointResolver EndpointResolver
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithStaleThreshold sets the threshold for marking observations as stale.
func WithStaleThreshold(d time.Duration) Option {
	return func(a *Adapter) {
		if d > 0 {
			a.StaleThreshold = d
		}
	}
}

// WithClock sets a custom clock function (useful for deterministic testing).
func WithClock(clock func() time.Time) Option {
	return func(a *Adapter) {
		if clock != nil {
			a.Clock = clock
		}
	}
}

// WithEndpointResolver sets the EndpointResolver used to derive the safe
// endpoint enum from raw auth file proxy_url values.
func WithEndpointResolver(r EndpointResolver) Option {
	return func(a *Adapter) {
		if r != nil {
			a.EndpointResolver = r
		}
	}
}

// NewAdapter creates a new Adapter instance with defaults.
func NewAdapter(opts ...Option) *Adapter {
	a := &Adapter{
		StaleThreshold: DefaultStaleThreshold,
		Clock:          time.Now,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// ManagementAuthFilesResponse represents the expected envelope or array from /management/auth-files.
// It can be a direct list of auth entries or wrapped in {"files": [...]} or {"items": [...]}.
type rawRecentRequest struct {
	Time    any `json:"time"`
	Success any `json:"success"`
	Failed  any `json:"failed"`
}

// EndpointKind is a safe allowlisted enum identifying which network path an
// auth credential is configured to use. Values are strictly enumerated;
// URL/host/IP/token details are never exposed. "unknown" is the safe default
// when no authoritative mapping can be derived from the auth-file entry.
type EndpointKind string

const (
	EndpointDirect  EndpointKind = "direct"
	EndpointWarp1   EndpointKind = "warp1"
	EndpointWarp2   EndpointKind = "warp2"
	EndpointUnknown EndpointKind = "unknown"
)

// EndpointResolver maps one management auth-files entry to an EndpointKind by
// reading proxy_url from its backing auth file. Implementations must never
// persist, log, or serialize raw proxy URLs; only the safe enum is returned.
//
// Resolve first uses Name as the source-contract filename (canonicalized to its
// basename with an exact .json suffix). If Name is a display value rather than
// a filename, it may use ID only when that ID exactly equals the top-level,
// non-secret JSON "id" field scanned from a file. CLIProxyAPI's plugin AuthData
// contract defines ID as the stable host auth identifier; no account/email,
// ordering, substring, or fuzzy fallback is permitted.
//
// Resolve returns EndpointUnknown when no exact mapping exists, the directory
// is unavailable, or proxy_url cannot be safely parsed. The blocker field is
// non-empty when the resolver cannot operate at all.
type EndpointResolver interface {
	Resolve(name, id string) EndpointKind
	// Blocker returns a non-sensitive description of why the resolver cannot
	// operate, or "" when it is functional. Callers must not expose this string
	// in user-facing output; it is for internal diagnostics only.
	Blocker() string
}

// providerAwareEndpointResolver is intentionally an optional extension of the
// original resolver contract. Codex's -plus filename compatibility alias needs
// proof that the management record is Codex, which Resolve(name, id) alone
// cannot provide. Keeping this private preserves compatibility with existing
// EndpointResolver implementations; resolvers without this extension retain
// exact-name/ID behavior only.
type providerAwareEndpointResolver interface {
	ResolveForProvider(name, id, provider, entryType string) EndpointKind
}

// providerPlanResolver optionally exposes only an allowlisted Codex plan label
// discovered from the credential store. Raw filenames and identifiers never
// leave the resolver.
type providerPlanResolver interface {
	ResolvePlanForProvider(name, id, provider, entryType string) string
}

// noopEndpointResolver is the zero-value resolver that always returns unknown.
// It is used when no auth directory is available or configured.
type noopEndpointResolver struct{ reason string }

func (n *noopEndpointResolver) Resolve(_, _ string) EndpointKind { return EndpointUnknown }
func (n *noopEndpointResolver) Blocker() string                  { return n.reason }

// ParseEndpointKindFromProxyURL maps a raw proxy_url string from an auth file
// to the safe EndpointKind enum using URL hostname matching.
//
// Rules (applied in order):
//  1. Empty string, or the legacy literal "direct" (case-insensitive), → direct.
//  2. Parse all other values as a supported absolute proxy URL with a valid
//     explicit port; malformed URLs, unsupported schemes, and invalid ports
//     → unknown.
//  3. If URL contains credentials (userinfo) → unknown (safe fallback).
//  4. Lowercase hostname exact match: "warp1" → warp1; "warp2" → warp2.
//  5. Any other valid URL → unknown.
//
// In particular, direct:// and values merely containing "direct" are not the
// legacy literal and are therefore unknown. The raw URL is never stored,
// logged, or returned; only the enum is returned.
func ParseEndpointKindFromProxyURL(rawProxyURL string) EndpointKind {
	trimmed := strings.TrimSpace(rawProxyURL)
	if trimmed == "" || strings.EqualFold(trimmed, "direct") {
		return EndpointDirect
	}
	u, err := parseProxyURL(trimmed)
	if err != nil || u.User != nil {
		return EndpointUnknown
	}
	switch strings.ToLower(u.Hostname()) {
	case "warp1":
		return EndpointWarp1
	case "warp2":
		return EndpointWarp2
	default:
		return EndpointUnknown
	}
}

// parseProxyURL validates and parses an absolute HTTP(S)/SOCKS proxy URL. An
// explicit port in the TCP range is required: endpoint names alone are not a
// proxy configuration and must not be classified as a trusted endpoint.
func parseProxyURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("empty proxy URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("malformed proxy URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("unsupported proxy scheme")
	}
	port := u.Port()
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, errors.New("invalid proxy port")
	}
	return u, nil
}

type rawAuthEntry struct {
	AuthIndex any    `json:"auth_index"`
	Priority  any    `json:"priority"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	// Type mirrors the provider in v7.2.155 entries ("type" == auth.Provider) and
	// is the only provider-ish key in the disk-fallback listing variant; used as a
	// tolerant fallback when "provider" is absent. account_type (e.g. "chatgpt")
	// is intentionally NOT used as a provider.
	Type            string             `json:"type"`
	Account         any                `json:"account"`
	Email           string             `json:"email"`
	Alias           string             `json:"alias"`
	PlanType        any                `json:"plan_type"`
	ChatGPTPlanType any                `json:"chatgpt_plan_type"`
	Subscription    any                `json:"subscription"`
	Organization    any                `json:"organization"`
	Organizations   any                `json:"organizations"`
	Workspace       any                `json:"workspace"`
	Workspaces      any                `json:"workspaces"`
	ID              string             `json:"id"`
	ProjectID       string             `json:"project_id"`
	Metadata        map[string]any     `json:"metadata"`
	IDToken         json.RawMessage    `json:"id_token"`
	AccountType     string             `json:"account_type"`
	Status          string             `json:"status"`
	StatusMessage   string             `json:"status_message"`
	Disabled        *bool              `json:"disabled"`
	Unavailable     *bool              `json:"unavailable"`
	Success         *int64             `json:"success"`
	Failed          *int64             `json:"failed"`
	RecentRequests  []rawRecentRequest `json:"recent_requests"`
	LastRefresh     any                `json:"last_refresh"`
	NextRetryAfter  any                `json:"next_retry_after"`
	Quota           *rawQuotaRecord    `json:"quota"`
}

type rawQuotaRecord struct {
	ObservedAt any               `json:"observed_at"`
	Signals    map[string]string `json:"signals"`
}

// NormalizeCodexPlanType converts only explicit, known Codex/ChatGPT plan
// labels to the public canonical values. It deliberately uses an exact
// allowlist (after separator/case normalization): organization/workspace
// presence, names, IDs, roles, or arbitrary strings never imply a paid plan.
func NormalizeCodexPlanType(raw string) string {
	var compact strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			compact.WriteRune(r)
		}
	}
	switch compact.String() {
	case "free", "chatgptfree", "freeplan":
		return "free"
	case "plus", "chatgptplus", "plusplan":
		return "plus"
	case "pro", "chatgptpro", "proplan":
		return "pro"
	case "team", "chatgptteam", "teamplan", "business", "chatgptbusiness", "businessplan":
		// ChatGPT Team was renamed ChatGPT Business; Codex upstream variants
		// have used both labels for the same workspace subscription class.
		return "team"
	case "enterprise", "chatgptenterprise", "enterpriseplan":
		return "enterprise"
	case "k12", "chatgptk12", "k12plan":
		return "k12"
	default:
		return ""
	}
}

func planString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return NormalizeCodexPlanType(s)
}

func planFromFields(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if plan := planString(m[key]); plan != "" {
			return plan
		}
	}
	return ""
}

// findCodexPlanInContainer follows only known plan-bearing response shapes.
// Generic object "type"/"name" values are intentionally ignored; those keys
// are considered only inside an explicit subscription/organization/workspace
// container, preventing a provider type or an organization named "Team" from
// being misclassified.
func findCodexPlanInContainer(v any, depth int, kind string) string {
	if depth < 0 || v == nil {
		return ""
	}
	if plan := planString(v); plan != "" {
		return plan
	}
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if plan := findCodexPlanInContainer(item, depth-1, kind); plan != "" {
				return plan
			}
		}
	case map[string]any:
		keys := []string{"plan_type", "planType", "chatgpt_plan_type", "chatgptPlanType", "plan", "tier"}
		if kind == "subscription" || kind == "organization" || kind == "workspace" {
			keys = append(keys, "type")
		}
		if kind == "subscription" {
			keys = append(keys, "name", "product")
		}
		if plan := planFromFields(x, keys...); plan != "" {
			return plan
		}
		if depth == 0 {
			return ""
		}
		for _, child := range []struct {
			key  string
			kind string
		}{
			{"account", "account"}, {"subscription", "subscription"}, {"subscriptions", "subscription"},
			{"organization", "organization"}, {"organizations", "organization"},
			{"workspace", "workspace"}, {"workspaces", "workspace"},
			{"chatgpt", "account"}, {"auth", "account"}, {"https://api.openai.com/auth", "account"},
		} {
			if plan := findCodexPlanInContainer(x[child.key], depth-1, child.kind); plan != "" {
				return plan
			}
		}
	}
	return ""
}

func decodeJSONOrJWT(raw json.RawMessage) any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return value
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &value) != nil {
		return nil
	}
	return value
}

func codexPlanFromEntry(provider string, entry rawAuthEntry) string {
	if !strings.EqualFold(strings.TrimSpace(provider), "codex") {
		return ""
	}
	for _, candidate := range []any{entry.PlanType, entry.ChatGPTPlanType} {
		if plan := planString(candidate); plan != "" {
			return plan
		}
	}
	for _, candidate := range []struct {
		value any
		kind  string
	}{
		{entry.Account, "account"}, {entry.Subscription, "subscription"},
		{entry.Organization, "organization"}, {entry.Organizations, "organization"},
		{entry.Workspace, "workspace"}, {entry.Workspaces, "workspace"},
		{entry.Metadata, "account"}, {decodeJSONOrJWT(entry.IDToken), "account"},
	} {
		if plan := findCodexPlanInContainer(candidate.value, 5, candidate.kind); plan != "" {
			return plan
		}
	}
	return codexPlanFromFilename(provider, entry.Name)
}

// codexPlanFromFilename returns only explicitly supported, non-sensitive plan
// markers from the raw management filename. Team is deliberately excluded:
// unlike the older compatibility markers, it is accepted only through a
// credential-store resolver that verifies an exact canonical .json basename
// and an on-disk source type of codex.
func codexPlanFromFilename(provider, name string) string {
	if !strings.EqualFold(strings.TrimSpace(provider), "codex") {
		return ""
	}
	base := strings.ToLower(strings.TrimSpace(name))
	for _, plan := range []string{"k12", "plus", "pro", "free"} {
		if strings.HasSuffix(base, "-"+plan+".json") {
			return plan
		}
	}
	return ""
}

// IsProviderSupportedPassive reports whether CLIProxyAPI's upstream coreauth passively captures headers.
// Note: "unsupported" only means passive header capture is not implemented for this provider in CLIProxyAPI,
// not that the provider itself has no quota mechanism or absolute lack of functionality.
func IsProviderSupportedPassive(provider string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	return p == "claude" || p == "codex"
}

func extractChatGPTAccountID(entry rawAuthEntry) string {
	if !strings.EqualFold(entry.Provider, "codex") && !strings.EqualFold(entry.Type, "codex") {
		return ""
	}
	if id := findAccountID(entry.Metadata, 4); id != "" {
		return id
	}
	raw := bytes.TrimSpace(entry.IDToken)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	if s, ok := value.(string); ok {
		parts := strings.Split(s, ".")
		if len(parts) < 2 {
			return ""
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
		var claims map[string]any
		if json.Unmarshal(payload, &claims) != nil {
			return ""
		}
		return findAccountID(claims, 4)
	}
	if object, ok := value.(map[string]any); ok {
		return findAccountID(object, 4)
	}
	return ""
}

func findAccountID(object map[string]any, depth int) string {
	if object == nil || depth < 0 {
		return ""
	}
	for _, key := range []string{"chatgpt_account_id", "account_id"} {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if depth == 0 {
		return ""
	}
	for _, value := range object {
		if nested, ok := value.(map[string]any); ok {
			if id := findAccountID(nested, depth-1); id != "" {
				return id
			}
		}
	}
	return ""
}

// ParseAuthFilesJSON parses raw JSON from the management auth-files endpoint into safe AccountQuota structs.
// Strict whitelist enforcement guarantees NO secrets, tokens, or raw unverified auth fields leak into the output.
func (a *Adapter) ParseAuthFilesJSON(data []byte) ([]AccountQuota, error) {
	now := a.Clock()
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty or invalid JSON payload")
	}

	var raws []json.RawMessage
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, errors.New("invalid auth-files array")
		}
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return nil, errors.New("invalid auth-files object")
		}
		if raw, ok := object["files"]; ok {
			if err := json.Unmarshal(raw, &raws); err != nil {
				return nil, errors.New("invalid auth-files envelope")
			}
		} else if raw, ok := object["items"]; ok {
			if err := json.Unmarshal(raw, &raws); err != nil {
				return nil, errors.New("invalid auth-files envelope")
			}
		} else {
			raws = []json.RawMessage{trimmed}
		}
	default:
		return nil, errors.New("empty or invalid JSON payload")
	}

	results := make([]AccountQuota, 0, len(raws))
	for _, raw := range raws {
		if first := bytes.TrimSpace(raw); len(first) == 0 || first[0] != '{' {
			continue
		}
		var entry rawAuthEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		aq := a.processEntry(entry, now)
		// Resolve endpoint from raw auth file proxy_url via the resolver.
		// The resolver handles all security invariants (no raw URL persisted).
		if a.EndpointResolver != nil {
			// Endpoint and plan resolution must use the identical, non-public
			// credential identity. In particular, use the provider after the
			// provider/type fallback above rather than the possibly empty raw field.
			// Resolvers own their exact-name/immutable-ID precedence, but both
			// resolution paths receive precisely the same identity inputs.
			name, id, provider := aq.authFileName, strings.TrimSpace(entry.ID), aq.Provider
			if resolver, ok := a.EndpointResolver.(providerAwareEndpointResolver); ok {
				aq.Endpoint = resolver.ResolveForProvider(name, id, provider, entry.Type)
			} else {
				aq.Endpoint = a.EndpointResolver.Resolve(name, id)
			}
			if aq.PlanType == "" {
				if resolver, ok := a.EndpointResolver.(providerPlanResolver); ok {
					if plan := resolver.ResolvePlanForProvider(name, id, provider, entry.Type); plan != "" {
						aq.PlanType = plan
					}
				}
			}
		}
		results = append(results, aq)
	}
	return results, nil
}

func (a *Adapter) processEntry(entry rawAuthEntry, now time.Time) AccountQuota {
	// Extract auth_index safely as string if present
	var authIndex string
	if entry.AuthIndex != nil {
		switch v := entry.AuthIndex.(type) {
		case string:
			authIndex = strings.TrimSpace(v)
		case float64:
			authIndex = strconv.FormatInt(int64(v), 10)
		case int64:
			authIndex = strconv.FormatInt(v, 10)
		case int:
			authIndex = strconv.Itoa(v)
		case json.Number:
			authIndex = v.String()
		default:
			authIndex = strings.TrimSpace(fmt.Sprint(v))
		}
	}

	provider := strings.TrimSpace(entry.Provider)
	if provider == "" {
		// Tolerant fallback: v7.2.155 always emits "provider", but the disk-fallback
		// listing (and some older variants) only carries "type" with the same value.
		provider = strings.TrimSpace(entry.Type)
	}

	// Compute non-reversible, unique account_key.
	// Primary discriminator is auth_index, combined with provider and id/name fallback.
	accountKeySeed := fmt.Sprintf("%s|%s|%s|%s", provider, authIndex, strings.TrimSpace(entry.ID), strings.TrimSpace(entry.Name))
	h := sha256.Sum256([]byte(accountKeySeed))
	accountKey := hex.EncodeToString(h[:12]) // 24-character hex string

	// Account alias extraction (never touch token/secret). Some upstream
	// variants make account an object carrying plan metadata; only a literal
	// string is eligible as the public display label.
	account := strings.TrimSpace(entry.Alias)
	if account == "" {
		if value, ok := entry.Account.(string); ok {
			account = strings.TrimSpace(value)
		}
	}
	if account == "" {
		account = strings.TrimSpace(entry.Email)
	}
	// Name is the raw auth-file join key and must never be promoted into the
	// public DTO. ID is likewise an internal matching key, not a display label.
	if account == "" {
		account = "unnamed"
	}

	op := parseOperationalStatus(entry)

	priority := 0
	if entry.Priority != nil {
		switch v := entry.Priority.(type) {
		case float64:
			priority = int(v)
		case json.Number:
			if n, err := strconv.Atoi(v.String()); err == nil {
				priority = n
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				priority = n
			}
		case int:
			priority = v
		case int64:
			priority = int(v)
		}
	}

	res := AccountQuota{
		AccountKey:   accountKey,
		AuthIndex:    authIndex,
		Priority:     priority,
		Provider:     provider,
		Account:      account,
		PlanType:     codexPlanFromEntry(provider, entry),
		Endpoint:     EndpointUnknown, // resolved externally via EndpointResolver after parse
		projectID:    strings.TrimSpace(entry.ProjectID),
		accountID:    extractChatGPTAccountID(entry),
		FetchedAt:    now,
		Operational:  op,
		authFileName: strings.TrimSpace(entry.Name),
	}

	if provider == "" {
		res.Status = StatusError
		res.Error = "missing provider"
		return res
	}

	if !IsProviderSupportedPassive(provider) {
		res.Status = StatusUnsupported
		return res
	}

	if entry.Quota == nil {
		res.Status = StatusUnknown
		return res
	}

	// Parse observed_at through the same seconds-vs-milliseconds and
	// marshal-safe year validation used by every other timestamp field.
	observedAt, observedAtValid := parseAnyTimestamp(entry.Quota.ObservedAt)
	if entry.Quota.ObservedAt != nil && !observedAtValid {
		res.Status = StatusError
		res.Error = "invalid observed_at"
		return res
	}
	res.ObservedAt = observedAt

	// Filter and whitelist signals (only recognized passive quota headers)
	safeSignals := whitelistSignals(provider, entry.Quota.Signals)
	if len(safeSignals) == 0 {
		if observedAt == nil {
			res.Status = StatusUnknown
			return res
		}
	}
	res.RawSignals = safeSignals

	// Check observed_at validity and staleness
	if observedAt != nil {
		if observedAt.Sub(now) > MaxClockFutureTolerance {
			// Future timestamp beyond tolerance: mark unknown/error with reason, NEVER clamp to now
			res.Status = StatusError
			res.Error = fmt.Sprintf("observed_at is too far in the future (%s > clock %s + %s)", observedAt.Format(time.RFC3339), now.Format(time.RFC3339), MaxClockFutureTolerance)
			return res
		}
		if now.Sub(*observedAt) > a.StaleThreshold {
			res.Status = StatusStale
			res.Stale = true
		} else {
			res.Status = StatusKnown
		}
	} else {
		res.Status = StatusKnown
	}

	// Parse provider-specific signals into windows and fields
	a.parseSignals(&res)

	return res
}

func parseAnyTimestamp(v any) (*time.Time, bool) {
	if v == nil {
		return nil, false
	}
	switch val := v.(type) {
	case string:
		if ts, ok := parseTimestamp(val); ok {
			return &ts, true
		}
	case float64:
		if ts, ok := epochNumberToTime(val); ok {
			return &ts, true
		}
	case int64:
		if ts, ok := epochToTime(val); ok {
			return &ts, true
		}
	case int:
		if ts, ok := epochToTime(int64(val)); ok {
			return &ts, true
		}
	case json.Number:
		if ts, ok := parseEpochString(val.String()); ok {
			return &ts, true
		}
	}
	return nil, false
}

func parseOperationalStatus(entry rawAuthEntry) *OperationalStatus {
	var op OperationalStatus
	hasAny := false

	if s := strings.TrimSpace(entry.Status); s != "" {
		op.Status = s
		hasAny = true
	}
	if sm := strings.TrimSpace(entry.StatusMessage); sm != "" {
		op.StatusMessage = sm
		hasAny = true
	}
	if entry.Disabled != nil {
		d := *entry.Disabled
		op.Disabled = &d
		hasAny = true
	}
	if entry.Unavailable != nil {
		u := *entry.Unavailable
		op.Unavailable = &u
		hasAny = true
	}
	if entry.Success != nil {
		s := *entry.Success
		op.Success = &s
		hasAny = true
	}
	if entry.Failed != nil {
		f := *entry.Failed
		op.Failed = &f
		hasAny = true
	}
	if at := strings.TrimSpace(entry.AccountType); at != "" {
		op.AccountType = at
		hasAny = true
	}

	if ts, ok := parseAnyTimestamp(entry.LastRefresh); ok {
		op.LastRefresh = ts
		hasAny = true
	}
	if ts, ok := parseAnyTimestamp(entry.NextRetryAfter); ok {
		op.NextRetryAfter = ts
		hasAny = true
	}

	if len(entry.RecentRequests) > 0 {
		op.RecentRequestCount = len(entry.RecentRequests)
		hasAny = true
		var latest *time.Time
		for _, req := range entry.RecentRequests {
			if t, ok := parseAnyTimestamp(req.Time); ok {
				if latest == nil || t.After(*latest) {
					latest = t
				}
			}
		}
		op.RecentRequestLatest = latest
	}

	if !hasAny {
		return nil
	}
	return &op
}

// normalizeFractionalSeconds truncates an RFC3339-style fractional-seconds
// component to at most 9 digits. Go's time.Parse rejects more than 9 fractional
// digits, so a value like 2026-03-30T12:00:00.1234567890123Z must be trimmed to
// the nanosecond precision that time.Time can actually represent.
func normalizeFractionalSeconds(s string) string {
	i := strings.IndexByte(s, '.')
	if i == -1 || i+1 >= len(s) {
		return s
	}
	// Fractional run is the contiguous digits after '.'.
	j := i + 1
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	digits := j - (i + 1)
	if digits <= 9 {
		return s
	}
	return s[:i+1+9] + s[j:]
}

// The JSON encoder only accepts years in [0, 9999]. Restrict provider times
// to a useful Unix epoch range as well, so a syntactically valid but absurd
// provider value can neither poison freshness calculations nor fail a later
// DTO marshal.
const (
	minQuotaTimestampYear = 1970
	maxQuotaTimestampYear = 9999
)

func validQuotaTime(t time.Time) bool {
	year := t.Year()
	return !t.IsZero() && year >= minQuotaTimestampYear && year <= maxQuotaTimestampYear
}

// parseTimestamp accepts RFC3339-like values and numeric epochs. Every numeric
// representation uses the same rule: absolute value below 1e11 is seconds;
// otherwise it is milliseconds. The result is always UTC and JSON-marshal safe.
func parseTimestamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, ok := parseEpochString(s); ok {
		return t, true
	}
	s = normalizeFractionalSeconds(s)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			t = t.UTC()
			if validQuotaTime(t) {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func parseEpochString(s string) (time.Time, bool) {
	// Epoch strings are deliberately integral: accepting a floating point
	// string would make seconds-vs-milliseconds classification ambiguous.
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	return epochToTime(n)
}

func epochNumberToTime(n float64) (time.Time, bool) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n != math.Trunc(n) || n > float64(math.MaxInt64) {
		return time.Time{}, false
	}
	return epochToTime(int64(n))
}

// epochToTime interprets an integer epoch using the common seconds/milliseconds
// rule and rejects values outside the DTO's safe time range.
func epochToTime(n int64) (time.Time, bool) {
	if n <= 0 {
		return time.Time{}, false
	}
	var t time.Time
	if n < 100_000_000_000 { // < 1e11 => seconds
		t = time.Unix(n, 0).UTC()
	} else {
		t = time.UnixMilli(n).UTC()
	}
	if !validQuotaTime(t) {
		return time.Time{}, false
	}
	return t, true
}

// ResetAtFromObservedAt converts a relative reset duration into an absolute,
// marshal-safe reset time. It is exported for active responses that do not
// carry an observed_at value. A supplied absolute reset_at always takes
// precedence over this derived value.
func ResetAtFromObservedAt(observedAt time.Time, resetAfterSec int64) (*time.Time, bool) {
	if !validQuotaTime(observedAt) || resetAfterSec < 0 || resetAfterSec > math.MaxInt64-observedAt.Unix() {
		return nil, false
	}
	// Do not use time.Duration here: a provider may legitimately send a
	// duration longer than Duration's ~290-year range.
	t := time.Unix(observedAt.Unix()+resetAfterSec, int64(observedAt.Nanosecond())).UTC()
	if !validQuotaTime(t) {
		return nil, false
	}
	return &t, true
}

func normalizeWindowResetAt(w *QuotaWindow, observedAt *time.Time) {
	if w == nil || w.ResetAt != nil || w.ResetAfterSec == nil || observedAt == nil {
		return
	}
	if t, ok := ResetAtFromObservedAt(*observedAt, *w.ResetAfterSec); ok {
		w.ResetAt = t
	}
}

func isValidSignalValue(val string) bool {
	if val == "" || len(val) > MaxSignalValueLen {
		return false
	}
	for _, ch := range val {
		if ch < 0x20 || ch == 0x7f {
			return false
		}
	}
	return true
}

func isValidUsedPercent(val string) bool {
	f, err := strconv.ParseFloat(val, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return f >= 0.0 && f <= 100.0
}

func isValidNumeric(val string) bool {
	f, err := strconv.ParseFloat(val, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return true
}

func isValidBoolSignal(val string) bool {
	v := strings.ToLower(strings.TrimSpace(val))
	return v == "true" || v == "false" || v == "1" || v == "0" || v == "yes" || v == "no"
}

// splitClaudeUnifiedSignal separates a unified rate-limit header into its
// window name and known terminal field. Splitting at the first dash is wrong
// for legitimate names such as five-hour, so only an allowlisted suffix may
// identify a field.
func splitClaudeUnifiedSignal(key string) (window, field string, ok bool) {
	const prefix = "anthropic-ratelimit-unified-"
	if !strings.HasPrefix(key, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, prefix)
	for _, candidate := range []string{"limit", "remaining", "status", "reset"} {
		suffix := "-" + candidate
		if strings.HasSuffix(rest, suffix) && len(rest) > len(suffix) {
			return strings.TrimSuffix(rest, suffix), candidate, true
		}
	}
	return "", "", false
}

// whitelistSignals filters incoming signals against strictly known quota headers and typed validation rules.
// Any unknown header (including x-ratelimit-* which is uncollected/unsupported in passive snapshot)
// or potential secret marker is completely discarded.
func whitelistSignals(provider string, in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	names := make([]string, 0, len(in))
	validatedValues := make(map[string]string, len(in))

	for k, v := range in {
		lk := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		if !isValidSignalValue(val) {
			continue
		}

		// Common
		if lk == "retry-after" {
			if _, err := strconv.ParseInt(val, 10, 64); err == nil {
				if _, exists := validatedValues[lk]; !exists {
					names = append(names, lk)
				}
				validatedValues[lk] = val
			}
			continue
		}

		if p == "claude" {
			// Strictly anthropic-ratelimit-unified-[window]-[field]. The field is
			// recognized from its terminal suffix so hyphenated window names work.
			if _, field, ok := splitClaudeUnifiedSignal(lk); ok {
				valid := false
				switch field {
				case "limit", "remaining":
					valid = isValidNumeric(val)
				case "status":
					// rejected is an authoritative exhaustion signal and must
					// survive the strict raw-signal allowlist.
					valid = strings.EqualFold(val, "rejected")
				case "reset":
					_, okTs := parseTimestamp(val)
					valid = okTs
				}
				if valid {
					if _, exists := validatedValues[lk]; !exists {
						names = append(names, lk)
					}
					validatedValues[lk] = val
				}
			}
		} else if p == "codex" {
			// Note: x-ratelimit-* headers are intentionally NOT collected / unsupported for passive snapshot.
			if lk == "x-codex-plan-type" || lk == "x-codex-active-limit" {
				if _, exists := validatedValues[lk]; !exists {
					names = append(names, lk)
				}
				validatedValues[lk] = val
				continue
			}
			if strings.HasPrefix(lk, "x-codex-credits-") {
				// Must be numeric-looking credit amount, or safe label
				if _, exists := validatedValues[lk]; !exists {
					names = append(names, lk)
				}
				validatedValues[lk] = val
				continue
			}
			if strings.HasPrefix(lk, "x-codex-") {
				for _, marker := range []string{
					"-allowed",
					"-limit-reached",
					"-limit-name",
					"-used-percent",
					"-remaining-percent",
					"-window-minutes",
					"-reset-after-seconds",
					"-reset-at",
					"-over-secondary-limit-percent",
				} {
					if strings.HasSuffix(lk, marker) || strings.Contains(lk, marker) {
						// Typed validation on value according to marker
						valid := false
						switch marker {
						case "-used-percent", "-remaining-percent":
							valid = isValidUsedPercent(val)
						case "-over-secondary-limit-percent":
							valid = isValidNumeric(val)
						case "-window-minutes", "-reset-after-seconds":
							if n, err := strconv.ParseInt(val, 10, 64); err == nil && n >= 0 {
								valid = true
							}
						case "-allowed", "-limit-reached":
							valid = isValidBoolSignal(val)
						case "-reset-at":
							_, ok := parseTimestamp(val)
							valid = ok
						case "-limit-name":
							// Safe label value validation
							valid = len(val) > 0 && len(val) <= 64
						}
						if valid {
							if _, exists := validatedValues[lk]; !exists {
								names = append(names, lk)
							}
							validatedValues[lk] = val
						}
						break
					}
				}
			}
		}
	}

	if len(names) == 0 {
		return nil
	}

	// Deterministic sorting and capping at MaxSignalsCount (64)
	sort.Strings(names)
	if len(names) > MaxSignalsCount {
		names = names[:MaxSignalsCount]
	}

	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = validatedValues[name]
	}
	return out
}

func (a *Adapter) parseSignals(res *AccountQuota) {
	if len(res.RawSignals) == 0 {
		return
	}

	if val, ok := res.RawSignals["retry-after"]; ok {
		if sec, err := strconv.ParseInt(val, 10, 64); err == nil {
			res.RetryAfter = &sec
		}
	}

	p := strings.ToLower(res.Provider)
	if p == "codex" {
		a.parseCodexSignals(res)
	} else if p == "claude" {
		a.parseClaudeSignals(res)
	}
}

func (a *Adapter) parseClaudeSignals(res *AccountQuota) {
	// Anthropic unified ratelimit headers
	// anthropic-ratelimit-unified-5m-limit, anthropic-ratelimit-unified-5m-remaining,
	// anthropic-ratelimit-unified-5m-status, anthropic-ratelimit-unified-5m-reset.
	windowsMap := make(map[string]*QuotaWindow)
	limits := make(map[string]float64)
	remaining := make(map[string]float64)

	for k, v := range res.RawSignals {
		if !strings.HasPrefix(k, "anthropic-ratelimit-unified-") {
			continue
		}
		// e.g. "anthropic-ratelimit-unified-five-hour-remaining".
		windowName, field, ok := splitClaudeUnifiedSignal(k)
		if !ok {
			continue
		}

		w, exists := windowsMap[windowName]
		if !exists {
			w = &QuotaWindow{Name: windowName}
			windowsMap[windowName] = w
		}

		if mins, ok := windowMinutesForName(windowName); ok {
			m := mins
			w.WindowMinutes = &m
		}

		switch field {
		case "limit":
			if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 && !math.IsNaN(n) && !math.IsInf(n, 0) {
				limits[windowName] = n
			}
		case "remaining":
			if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) {
				remaining[windowName] = n
			}
		case "status":
			if strings.EqualFold(v, "rejected") {
				blocked := false
				reached := true
				w.Allowed, w.LimitReached = &blocked, &reached
			}
		case "reset":
			if t, ok := parseTimestamp(v); ok {
				w.ResetAt = &t
			}
		}
	}

	for name, limit := range limits {
		if left, ok := remaining[name]; ok {
			w := windowsMap[name]
			// Provider counters can briefly exceed a limit; clamp only after
			// converting them into the canonical percentage representation.
			r := clampPercent(left / limit * 100)
			u := 100 - r
			w.RemainingPercent, w.UsedPercent = &r, &u
		}
	}

	if len(windowsMap) > 0 {
		names := make([]string, 0, len(windowsMap))
		for name := range windowsMap {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			w := windowsMap[name]
			normalizeWindowResetAt(w, res.ObservedAt)
			res.Windows = append(res.Windows, *w)
		}
		normalizeWindows(res.Windows)
	}
}

// windowMinutesForName maps a window identifier (used by both the Claude
// unified-header suffixes and the Antigravity bucket "window" enum) into a fixed
// duration in minutes. Supported aliases include five_hour/5_hour/5h => 300,
// weekly/seven_day/7d => 10080, daily/24_hour/24h => 1440 and the compact
// "<n>m"/"<n>h" suffix form. Returns (0,false) when no mapping applies.
func windowMinutesForName(raw string) (int, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return 0, false
	}
	switch s {
	case "five_hour", "five-hour", "5_hour", "5-hour", "5h", "5_hours", "five_hours":
		return 300, true
	case "hourly", "one_hour", "one-hour", "1_hour", "1-hour", "1h", "one_hours":
		return 60, true
	case "daily", "one_day", "one-day", "24_hour", "24-hour", "24h", "day":
		return 1440, true
	case "weekly", "seven_day", "seven-day", "7d", "week":
		return 10080, true
	case "monthly", "thirty_day", "thirty-day", "30d", "month":
		return 43200, true
	}
	// Compact numeric suffix forms: "<n>m" (minutes) or "<n>h" (hours).
	if strings.HasSuffix(s, "m") {
		if mins, err := strconv.Atoi(strings.TrimSuffix(s, "m")); err == nil && mins >= 0 {
			return mins, true
		}
	}
	if strings.HasSuffix(s, "h") {
		if hrs, err := strconv.Atoi(strings.TrimSuffix(s, "h")); err == nil && hrs >= 0 {
			return hrs * 60, true
		}
	}
	return 0, false
}

func (a *Adapter) parseCodexSignals(res *AccountQuota) {
	if raw, ok := res.RawSignals["x-codex-plan-type"]; ok {
		if plan := NormalizeCodexPlanType(raw); plan != "" {
			res.PlanType = plan
		}
	}
	if active, ok := res.RawSignals["x-codex-active-limit"]; ok {
		res.ActiveLimit = active
	}

	credits := make(map[string]string)
	windowsMap := make(map[string]*QuotaWindow)
	// Some passive Codex responses report admission at the quota scope rather
	// than repeating it for primary. Retain that authoritative signal and apply
	// it to primary only when a concrete primary observation exists.
	var globalAllowed, globalLimitReached *bool
	if v, ok := res.RawSignals["x-codex-allowed"]; ok {
		b := parseBoolSignal(v)
		globalAllowed = &b
	}
	if v, ok := res.RawSignals["x-codex-limit-reached"]; ok {
		b := parseBoolSignal(v)
		globalLimitReached = &b
	}

	getWindow := func(prefix string) *QuotaWindow {
		w, ok := windowsMap[prefix]
		if !ok {
			w = &QuotaWindow{Name: prefix}
			windowsMap[prefix] = w
		}
		return w
	}

	// First pass: collect limit-name if present to attach as safe label
	limitNames := make(map[string]string)
	for k, v := range res.RawSignals {
		if strings.HasPrefix(k, "x-codex-") && strings.Contains(k, "-limit-name") {
			kTrim := strings.TrimPrefix(k, "x-codex-")
			idx := strings.Index(kTrim, "-limit-name")
			if idx != -1 {
				prefix := kTrim[:idx]
				limitNames[prefix] = v
			}
		}
	}

	for k, v := range res.RawSignals {
		if strings.HasPrefix(k, "x-codex-credits-") {
			creditName := strings.TrimPrefix(k, "x-codex-credits-")
			credits[creditName] = v
			continue
		}

		if !strings.HasPrefix(k, "x-codex-") {
			continue
		}

		// If this is only -limit-name, skip creating an empty quota window if no other limit fields exist yet,
		// but if a window exists or is created, store limit name as a safe label.
		if strings.Contains(k, "-limit-name") {
			continue
		}
		// These are scope-level flags handled above. Treating their empty prefix
		// as a window would incorrectly manufacture an empty quota observation.
		if k == "x-codex-allowed" || k == "x-codex-limit-reached" {
			continue
		}

		// Header format: x-codex-[namespace-...]marker
		// e.g. x-codex-primary-used-percent
		// x-codex-secondary-window-minutes
		// x-codex-code-review-allowed
		// x-codex-bengalfox-primary-used-percent
		// x-codex-additional-foo-used-percent
		kTrim := strings.TrimPrefix(k, "x-codex=") // just in case
		kTrim = strings.TrimPrefix(k, "x-codex-")

		for _, marker := range []string{
			"-over-secondary-limit-percent",
			"-reset-after-seconds",
			"-reset-at",
			"-window-minutes",
			"-used-percent",
			"-remaining-percent",
			"-limit-reached",
			"-allowed",
		} {
			idx := strings.Index(kTrim, marker)
			if idx != -1 {
				windowName := kTrim[:idx]
				field := marker[1:] // drop leading '-'

				w := getWindow(windowName)
				if ln, ok := limitNames[windowName]; ok {
					w.LimitName = ln
				}
				switch field {
				case "used-percent":
					if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0.0 && f <= 100.0 {
						w.UsedPercent = &f
					}
				case "remaining-percent":
					if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0.0 && f <= 100.0 {
						w.RemainingPercent = &f
					}
				case "window-minutes":
					if m, err := strconv.Atoi(v); err == nil && m >= 0 {
						w.WindowMinutes = &m
					}
				case "limit-reached":
					b := parseBoolSignal(v)
					w.LimitReached = &b
				case "allowed":
					b := parseBoolSignal(v)
					w.Allowed = &b
				case "reset-after-seconds":
					if s, err := strconv.ParseInt(v, 10, 64); err == nil && s >= 0 {
						w.ResetAfterSec = &s
					}
				case "reset-at":
					if t, ok := parseTimestamp(v); ok {
						w.ResetAt = &t
					}
				case "over-secondary-limit-percent":
					if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(f) {
						w.OverLimitPercent = &f
					}
				}
				break
			}
		}
	}

	if len(credits) > 0 {
		res.Credits = credits
	}
	if primary, ok := windowsMap["primary"]; ok {
		if primary.Allowed == nil && globalAllowed != nil {
			primary.Allowed = globalAllowed
		}
		if primary.LimitReached == nil && globalLimitReached != nil {
			primary.LimitReached = globalLimitReached
		}
	}

	if len(windowsMap) > 0 {
		names := make([]string, 0, len(windowsMap))
		for name := range windowsMap {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			w := windowsMap[name]
			normalizeWindowResetAt(w, res.ObservedAt)
			res.Windows = append(res.Windows, *w)
		}
		normalizeWindows(res.Windows)
	}
}

func quotaNumber(v any) (float64, bool) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case json.Number:
		var err error
		n, err = x.Float64()
		if err != nil {
			return 0, false
		}
	case string:
		var err error
		n, err = strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}

func quotaPercent(v any) (float64, bool) {
	n, ok := quotaNumber(v)
	if !ok {
		return 0, false
	}
	if n < 0 {
		n = 0
	}
	if n > 100 {
		n = 100
	}
	return n, true
}

func quotaResetAfterSeconds(v any) (int64, bool) {
	n, ok := quotaNumber(v)
	if !ok || n < 0 || n != math.Trunc(n) || n > float64(math.MaxInt64) {
		return 0, false
	}
	return int64(n), true
}

func windowMinutesFromSeconds(seconds int64) (int, bool) {
	if seconds <= 0 {
		return 0, false
	}
	minutes := seconds / 60
	if seconds%60 != 0 {
		minutes++
	}
	if minutes > int64(math.MaxInt) {
		return 0, false
	}
	return int(minutes), true
}

func quotaTime(v any) (*time.Time, bool) {
	if s, ok := v.(string); ok {
		if t, ok := parseTimestamp(s); ok {
			return &t, true
		}
		return nil, false
	}
	n, ok := quotaNumber(v)
	if !ok {
		return nil, false
	}
	if t, ok := epochNumberToTime(n); ok {
		return &t, true
	}
	return nil, false
}
func objectValue(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

// ParseAntigravityQuota parses the active quota response without retaining raw data.
func ParseAntigravityQuota(data []byte) ([]QuotaWindow, error) {
	var root struct {
		Groups []map[string]any `json:"groups"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, &APICallError{Kind: "parse"}
	}
	out := []QuotaWindow{}
	for _, g := range root.Groups {
		groupName := strings.TrimSpace(fmt.Sprint(objectValue(g, "displayName", "display_name")))
		if groupName == "<nil>" {
			groupName = ""
		}
		bs, _ := objectValue(g, "buckets").([]any)
		for _, raw := range bs {
			b, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			disp := strings.TrimSpace(fmt.Sprint(objectValue(b, "displayName", "display_name")))
			if disp == "<nil>" {
				disp = ""
			}
			bid := strings.TrimSpace(fmt.Sprint(objectValue(b, "bucketId", "bucket_id")))
			if bid == "<nil>" {
				bid = ""
			}
			bucketLabel := disp
			if bucketLabel == "" {
				bucketLabel = bid
			}
			if bucketLabel == "" {
				continue
			}
			name := bucketLabel
			if groupName != "" && groupName != bucketLabel {
				name = groupName + " / " + bucketLabel
			}
			w := QuotaWindow{Name: name}
			if winRaw := objectValue(b, "window"); winRaw != nil {
				if mins, ok := windowMinutesForName(fmt.Sprint(winRaw)); ok {
					m := mins
					w.WindowMinutes = &m
				}
			}
			// remainingFraction is always a fraction (including when the provider
			// serializes it as a numeric string); remaining_percent is always a
			// percentage. Do not infer units from the Go value type.
			if v := objectValue(b, "remainingFraction", "remaining_fraction"); v != nil {
				if n, ok := quotaNumber(v); ok && n >= 0 && n <= 1 {
					// float64() forbids fusing n*100 into 100-r (FMA on arm64), which
					// would turn a fraction of 0.8 into used=19.999999999999996.
					r := float64(n * 100)
					u := 100 - r
					w.RemainingPercent, w.UsedPercent = &r, &u
				}
			} else if v := objectValue(b, "remaining_percent"); v != nil {
				if r, ok := quotaPercent(v); ok {
					u := 100 - r
					w.RemainingPercent, w.UsedPercent = &r, &u
				}
			}
			if t, ok := quotaTime(objectValue(b, "resetTime", "reset_time")); ok {
				w.ResetAt = t
			}
			if s, ok := quotaResetAfterSeconds(objectValue(b, "resetAfterSeconds", "reset_after_seconds")); ok {
				w.ResetAfterSec = &s
			}
			normalizeWindow(&w)
			out = append(out, w)
		}
	}
	return out, nil
}

// ParseClaudeQuota extracts only allowlisted OAuth usage windows. Utilization
// is already a percentage (not a fraction); null windows are not zero usage.
// Extra-usage billing and unknown response fields are deliberately not retained.
func ParseClaudeQuota(data []byte) ([]QuotaWindow, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		return nil, &APICallError{Kind: "parse"}
	}
	out := make([]QuotaWindow, 0, 7)
	for _, spec := range []struct {
		key, name string
		minutes   int
	}{
		{"five_hour", "five-hour", 300},
		{"seven_day", "weekly", 10080},
		{"seven_day_opus", "weekly-opus", 10080},
		{"seven_day_sonnet", "weekly-sonnet", 10080},
		{"seven_day_oauth_apps", "weekly-oauth-apps", 10080},
		{"seven_day_cowork", "weekly-cowork", 10080},
		{"seven_day_sessions", "weekly-sessions", 10080},
	} {
		raw, ok := root[spec.key]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var usage struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    *string  `json:"resets_at"`
		}
		if err := json.Unmarshal(raw, &usage); err != nil {
			return nil, &APICallError{Kind: "parse"}
		}
		if usage.Utilization == nil {
			continue
		}
		used := clampPercent(*usage.Utilization)
		remaining := 100 - used
		minutes := spec.minutes
		w := QuotaWindow{Name: spec.name, WindowMinutes: &minutes, UsedPercent: &used, RemainingPercent: &remaining}
		if usage.ResetsAt != nil {
			if reset, ok := parseTimestamp(*usage.ResetsAt); ok {
				w.ResetAt = &reset
			}
		}
		normalizeWindow(&w)
		out = append(out, w)
	}
	return out, nil
}

// ParseCodexPlanType extracts a canonical plan from known WHAM/account,
// subscription, organization, and workspace response variants. It never
// infers Team merely from the existence/name/ID/role of an organization.
func ParseCodexPlanType(data []byte) (string, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return "", &APICallError{Kind: "invalid quota response"}
	}
	return findCodexPlanInContainer(root, 5, "account"), nil
}

// ParseCodexQuota parses active usage windows, tolerating API naming variants.
// ParseCodexUsageExtras parses only typed, non-identifying WHAM usage extras.
// Wrong types, non-finite values and implausible counts are omitted rather than
// coerced. Reset credits intentionally have no IDs or grant metadata here.
func ParseCodexUsageExtras(data []byte) (*APICredits, *ResetCredits, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, nil, &APICallError{Kind: "invalid quota response"}
	}
	var api *APICredits
	if credits, ok := root["credits"].(map[string]any); ok {
		out := &APICredits{}
		if b, ok := quotaBool(credits["has_credits"]); ok {
			out.HasCredits = &b
		}
		if b, ok := quotaBool(credits["unlimited"]); ok {
			out.Unlimited = &b
		}
		if n, ok := quotaNumber(credits["balance"]); ok && n >= 0 && n <= 1_000_000_000_000 {
			out.Balance = &n
		}
		if out.HasCredits != nil || out.Unlimited != nil || out.Balance != nil {
			api = out
		}
	}
	var reset *ResetCredits
	// WHAM versions have placed this field both at the response root and
	// within rate_limit. Prefer the documented root shape, then tolerate the
	// nested form without merging ambiguous payloads.
	rawReset, _ := root["rate_limit_reset_credits"].(map[string]any)
	if rawReset == nil {
		if rl, ok := root["rate_limit"].(map[string]any); ok {
			rawReset, _ = rl["rate_limit_reset_credits"].(map[string]any)
		}
	}
	if rawReset != nil {
		out := &ResetCredits{}
		if n, ok := quotaSafeCount(rawReset["available_count"]); ok {
			out.AvailableCount = &n
		}
		if n, ok := quotaSafeCount(rawReset["applicable_available_count"]); ok {
			out.ApplicableAvailableCount = &n
		}
		if out.AvailableCount != nil || out.ApplicableAvailableCount != nil {
			reset = out
		}
	}
	return api, reset, nil
}

func quotaSafeCount(v any) (int, bool) {
	n, ok := quotaNumber(v)
	if !ok || n < 0 || n != math.Trunc(n) || n > 1_000_000 {
		return 0, false
	}
	return int(n), true
}

// ParseCodexResetCreditsExpiry retains only the nearest future expires_at from
// entries whose status is exactly "available". IDs and every other raw field
// are discarded immediately.
func ParseCodexResetCreditsExpiry(data []byte, clock time.Time) (*time.Time, error) {
	var root struct {
		Credits []struct {
			Status    string `json:"status"`
			ExpiresAt any    `json:"expires_at"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, &APICallError{Kind: "invalid reset credits response"}
	}
	var nearest *time.Time
	for _, credit := range root.Credits {
		if !strings.EqualFold(strings.TrimSpace(credit.Status), "available") {
			continue
		}
		t, ok := quotaTime(credit.ExpiresAt)
		if !ok || !t.After(clock) {
			continue
		}
		if nearest == nil || t.Before(*nearest) {
			nearest = t
		}
	}
	return nearest, nil
}

func ParseCodexQuota(data []byte) ([]QuotaWindow, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, &APICallError{Kind: "invalid quota response"}
	}
	container := root
	if x := objectValue(root, "rate_limit", "rateLimit"); x != nil {
		if m, ok := x.(map[string]any); ok {
			container = m
		}
	}
	out := []QuotaWindow{}
	for _, pair := range []struct {
		name string
		keys []string
	}{{"primary", []string{"primary_window", "primaryWindow"}}, {"secondary", []string{"secondary_window", "secondaryWindow"}}} {
		x := objectValue(container, pair.keys...)
		m, ok := x.(map[string]any)
		if !ok || len(m) == 0 {
			// An empty object is a schema placeholder, not a quota observation.
			continue
		}
		w := QuotaWindow{Name: pair.name}
		if seconds, ok := quotaResetAfterSeconds(objectValue(m, "limit_window_seconds", "limitWindowSeconds")); ok {
			if minutes, ok := windowMinutesFromSeconds(seconds); ok {
				w.WindowMinutes = &minutes
			}
		}
		if v := objectValue(m, "used_percent"); v != nil {
			if n, ok := quotaPercent(v); ok {
				w.UsedPercent = &n
			}
		} else if un, ok := quotaNumber(objectValue(m, "used_tokens")); ok {
			if ln, lok := quotaNumber(objectValue(m, "limit")); lok && ln > 0 {
				p := un / ln * 100
				if !math.IsNaN(p) && !math.IsInf(p, 0) {
					w.UsedPercent = &p
				}
			}
		}
		if w.UsedPercent == nil {
			if n, ok := quotaPercent(objectValue(m, "remaining_percent")); ok {
				p := 100 - n
				w.UsedPercent = &p
			}
		}
		if v := objectValue(m, "limit_reached"); v != nil {
			if b, ok := quotaBool(v); ok {
				w.LimitReached = &b
			}
		}
		if v := objectValue(m, "allowed"); v != nil {
			if b, ok := quotaBool(v); ok {
				w.Allowed = &b
			}
		}
		// WHAM also emits these flags at rate_limit scope. They apply to the
		// primary window unless a concrete primary_window value overrides them.
		if pair.name == "primary" {
			if w.LimitReached == nil {
				if b, ok := quotaBool(objectValue(container, "limit_reached", "limitReached")); ok {
					w.LimitReached = &b
				}
			}
			if w.Allowed == nil {
				if b, ok := quotaBool(objectValue(container, "allowed")); ok {
					w.Allowed = &b
				}
			}
			if w.WindowMinutes == nil {
				if seconds, ok := quotaResetAfterSeconds(objectValue(container, "limit_window_seconds", "limitWindowSeconds")); ok {
					if minutes, ok := windowMinutesFromSeconds(seconds); ok {
						w.WindowMinutes = &minutes
					}
				}
			}
		}
		if t, ok := quotaTime(objectValue(m, "reset_at", "reset_time", "resets_at")); ok {
			w.ResetAt = t
		}
		if s, ok := quotaResetAfterSeconds(objectValue(m, "reset_after_seconds")); ok {
			w.ResetAfterSec = &s
		}
		normalizeWindow(&w)
		out = append(out, w)
	}
	return out, nil
}

func quotaBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		if s == "true" || s == "1" || s == "yes" {
			return true, true
		}
		if s == "false" || s == "0" || s == "no" {
			return false, true
		}
	case float64:
		if x == 1 {
			return true, true
		}
		if x == 0 {
			return false, true
		}
	case json.Number:
		return quotaBool(x.String())
	}
	return false, false
}

func parseBoolSignal(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "true" || v == "1" || v == "yes"
}

// Client fetches management auth-files from a CLIProxyAPI instance.
type Client struct {
	BaseURL       string
	ManagementKey string
	// HTTPClient is the caller-owned template client retained for compatibility.
	// Requests use managementHTTPClient, a single hardened copy created once by
	// NewClient; neither this client nor its transport is mutated.
	HTTPClient               *http.Client
	Adapter                  *Adapter
	managementHTTPClient     *http.Client
	managementHTTPClientOnce sync.Once
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient overrides the default http.Client.
func WithHTTPClient(c *http.Client) ClientOption {
	return func(client *Client) {
		if c != nil {
			client.HTTPClient = c
		}
	}
}

// WithClientAdapter overrides the default adapter in Client.
func WithClientAdapter(a *Adapter) ClientOption {
	return func(client *Client) {
		if a != nil {
			client.Adapter = a
		}
	}
}

// NewClient creates a new Client. Security rule: ManagementKey is kept memory-only
// and never written to disk or logs.
func NewClient(baseURL, managementKey string, opts ...ClientOption) *Client {
	c := &Client{
		BaseURL:       strings.TrimRight(baseURL, "/"),
		ManagementKey: managementKey,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		Adapter: NewAdapter(),
	}
	for _, opt := range opts {
		opt(c)
	}
	// Harden once, rather than cloning a transport for every request. The
	// resulting transport safely pools loopback connections and remains private
	// to this Client, while the caller-supplied client is never modified.
	c.managementClient()
	return c
}

func (c *Client) managementClient() *http.Client {
	// Also make manually constructed Clients persistent and race-safe on first
	// use. As with http.Client itself, callers must not mutate configuration
	// fields while requests are in flight.
	c.managementHTTPClientOnce.Do(func() {
		c.managementHTTPClient = cloneHTTPClient(c.HTTPClient)
	})
	return c.managementHTTPClient
}

// CloseIdleConnections releases idle loopback connections held by this client.
// It is safe to call during shutdown and does not alter caller-owned clients.
func (c *Client) CloseIdleConnections() {
	if client := c.managementClient(); client != nil {
		client.CloseIdleConnections()
	}
}

// cloneHTTPClient creates a shallow copy of an http.Client, enforces a strict
// CheckRedirect policy that rejects ALL redirects (cross-host, same-host different port,
// or same-host same-port) to avoid leaking sensitive management keys, and hardens
// loopback management calls by clearing Proxy so environment proxies (HTTP_PROXY, etc.)
// are never contacted.
//
// Under the existing invariant that management BaseURL is validated as a loopback host,
// loopback management requests must never route through an external/environment proxy.
// If Transport is nil (or http.DefaultTransport), a clone of http.DefaultTransport with
// Proxy=nil is used. If Transport is a custom *http.Transport, it is cloned with Proxy=nil.
// Non-*http.Transport custom RoundTripper implementations are preserved as-is so mock/custom
// test injection is not broken.
// The original caller-provided client and global transports are never mutated.
func cloneHTTPClient(orig *http.Client) *http.Client {
	var c http.Client
	if orig != nil {
		c = *orig
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	switch t := c.Transport.(type) {
	case nil:
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			cloned := dt.Clone()
			cloned.Proxy = nil
			c.Transport = cloned
		}
	case *http.Transport:
		cloned := t.Clone()
		cloned.Proxy = nil
		c.Transport = cloned
	default:
		// Non-*http.Transport custom RoundTripper (e.g. test mock) is kept verbatim.
	}

	return &c
}

// FetchAuthFiles queries the CLIProxyAPI management endpoint /management/auth-files and parses quotas.
// It uses a cloned HTTP client with a strict redirect rejection policy to guarantee that the Authorization
// header (management key) is never forwarded on any redirect.
// APICall invokes the local management API-call proxy. URL is intentionally
// restricted to the two compile-time provider endpoints below.
type APICallResponse struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header"`
	Body       string              `json:"body"`
}

type APICallError struct {
	Kind       string
	StatusCode int // upstream HTTP status only; never contains response data
}

func (e *APICallError) Error() string { return e.Kind }

const (
	antigravityDailyURL   = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	antigravitySandboxURL = "https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary"
	antigravityProdURL    = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	codexQuotaURL         = "https://chatgpt.com/backend-api/wham/usage"
	codexResetCreditsURL  = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	claudeQuotaURL        = "https://api.anthropic.com/api/oauth/usage"
	claudeResetUsageURL   = claudeQuotaURL + "?cedar_ember=1"

	antigravityUserAgent = "antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)"
)

var antigravityQuotaURLs = [...]string{
	antigravityDailyURL,
	antigravitySandboxURL,
	antigravityProdURL,
}

// Per-model quota. Google stopped returning the "Gemini Models" group from
// retrieveUserQuotaSummary; Gemini remaining fractions only exist here.
const (
	antigravityUserQuotaDailyURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	antigravityUserQuotaProdURL  = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"

	antigravityGeminiFiveHourName = "Gemini Models / Five Hour Limit Remaining"
)

var antigravityUserQuotaURLs = [...]string{
	antigravityUserQuotaDailyURL,
	antigravityUserQuotaProdURL,
}

// ParseAntigravityModelQuota derives the Gemini five-hour window from the
// per-model retrieveUserQuota response: the lowest remaining fraction across
// gemini-* models (and its reset time). Non-Gemini and internal buckets are
// ignored. Returns no windows when no Gemini bucket carries a fraction.
func ParseAntigravityModelQuota(data []byte) ([]QuotaWindow, error) {
	var root struct {
		Buckets []map[string]any `json:"buckets"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, &APICallError{Kind: "parse"}
	}
	var best *float64
	var bestReset *time.Time
	for _, b := range root.Buckets {
		id := strings.ToLower(strings.TrimSpace(fmt.Sprint(objectValue(b, "modelId", "model_id"))))
		if !strings.HasPrefix(id, "gemini-") {
			continue
		}
		n, ok := quotaNumber(objectValue(b, "remainingFraction", "remaining_fraction"))
		if !ok || n < 0 || n > 1 {
			continue
		}
		r := float64(n * 100)
		if best == nil || r < *best {
			best = &r
			bestReset = nil
			if t, ok := quotaTime(objectValue(b, "resetTime", "reset_time")); ok {
				bestReset = t
			}
		}
	}
	if best == nil {
		return []QuotaWindow{}, nil
	}
	mins := 300
	w := QuotaWindow{Name: antigravityGeminiFiveHourName, WindowMinutes: &mins, RemainingPercent: best, ResetAt: bestReset}
	u := 100 - *best
	w.UsedPercent = &u
	normalizeWindow(&w)
	return []QuotaWindow{w}, nil
}

func isAllowedEndpoint(endpoint string) bool {
	if endpoint == codexQuotaURL || endpoint == codexResetCreditsURL || endpoint == claudeQuotaURL || endpoint == claudeResetUsageURL {
		return true
	}
	for _, u := range antigravityUserQuotaURLs {
		if endpoint == u {
			return true
		}
	}
	for _, u := range antigravityQuotaURLs {
		if endpoint == u {
			return true
		}
	}
	return false
}

func (c *Client) APICall(ctx context.Context, authIndex, method, endpoint string, header map[string]string, data string) (APICallResponse, error) {
	if !isAllowedEndpoint(endpoint) {
		return APICallResponse{}, &APICallError{Kind: "invalid endpoint"}
	}
	if method != http.MethodGet && method != http.MethodPost {
		return APICallResponse{}, &APICallError{Kind: "invalid method"}
	}
	payload, err := json.Marshal(struct {
		AuthIndex string            `json:"auth_index"`
		Method    string            `json:"method"`
		URL       string            `json:"url"`
		Header    map[string]string `json:"header"`
		Data      string            `json:"data"`
	}{authIndex, method, endpoint, header, data})
	if err != nil {
		return APICallResponse{}, &APICallError{Kind: "request encoding failed"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/management/api-call", bytes.NewReader(payload))
	if err != nil {
		return APICallResponse{}, &APICallError{Kind: "request failed"}
	}
	req.Header.Set("Content-Type", "application/json")
	if c.ManagementKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.ManagementKey)
		req.Header.Set("X-Management-Key", c.ManagementKey)
	}
	resp, err := c.managementClient().Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return APICallResponse{}, &APICallError{Kind: "timeout"}
		}
		return APICallResponse{}, &APICallError{Kind: "request failed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return APICallResponse{}, &APICallError{Kind: "management status"}
	}
	var out APICallResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err := dec.Decode(&out); err != nil {
		return APICallResponse{}, &APICallError{Kind: "invalid response"}
	}
	if out.StatusCode < 200 || out.StatusCode >= 300 {
		kind := "upstream status"
		if out.StatusCode == http.StatusUnauthorized {
			kind = "auth"
		}
		return APICallResponse{}, &APICallError{Kind: kind, StatusCode: out.StatusCode}
	}
	return out, nil
}

// appendMissingWindows appends incoming windows whose names are not present yet.
func appendMissingWindows(dst, incoming []QuotaWindow) []QuotaWindow {
	seen := make(map[string]struct{}, len(dst)+len(incoming))
	for _, w := range dst {
		seen[w.Name] = struct{}{}
	}
	for _, w := range incoming {
		if _, ok := seen[w.Name]; ok {
			continue
		}
		seen[w.Name] = struct{}{}
		dst = append(dst, w)
	}
	return dst
}

// antigravityFamiliesComplete reports whether another endpoint could add
// nothing: either neither known family is present (unknown layout, trust the
// response) or both Gemini and Claude/GPT windows are present.
func antigravityFamiliesComplete(ws []QuotaWindow) bool {
	gemini, third := false, false
	for _, w := range ws {
		n := strings.ToLower(w.Name)
		if strings.HasPrefix(n, "gemini models") {
			gemini = true
		} else if strings.HasPrefix(n, "claude and gpt models") {
			third = true
		}
	}
	return gemini == third
}

func hasGeminiWindow(ws []QuotaWindow) bool {
	for _, w := range ws {
		if strings.HasPrefix(strings.ToLower(w.Name), "gemini models") {
			return true
		}
	}
	return false
}

func (c *Client) FetchActiveQuota(ctx context.Context, account AccountQuota) ([]QuotaWindow, error) {
	p := strings.ToLower(strings.TrimSpace(account.Provider))
	if p == "claude" {
		windows, _, err := c.FetchClaudeUsage(ctx, account)
		return windows, err
	}
	if p == "antigravity" {
		data := `{}`
		if account.projectID != "" {
			b, _ := json.Marshal(map[string]string{"project": account.projectID})
			data = string(b)
		}
		overallCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		headers := map[string]string{
			"Content-Type":  "application/json",
			"User-Agent":    antigravityUserAgent,
			"Authorization": "Bearer $TOKEN$",
		}

		var lastErrKind string
		var merged []QuotaWindow
		allAuth := true
		anyAttempt := 0

		for _, endpoint := range antigravityQuotaURLs {
			if overallCtx.Err() != nil {
				if lastErrKind == "" {
					lastErrKind = "timeout"
				}
				break
			}
			anyAttempt++
			r, err := c.APICall(overallCtx, account.AuthIndex, http.MethodPost, endpoint, headers, data)
			if err != nil {
				if overallCtx.Err() != nil {
					lastErrKind = "timeout"
					break
				}
				ae, ok := err.(*APICallError)
				if ok {
					if ae.Kind != "auth" {
						allAuth = false
					}
					lastErrKind = ae.Kind
				} else {
					allAuth = false
					lastErrKind = "upstream"
				}
				continue
			}
			allAuth = false
			windows, parseErr := ParseAntigravityQuota([]byte(r.Body))
			if parseErr != nil {
				lastErrKind = "parse"
				continue
			}
			if len(windows) == 0 {
				lastErrKind = "empty"
				continue
			}
			// Upstream endpoints are not equivalent: daily/sandbox may return
			// only the Claude/GPT family while prod also returns Gemini. Keep
			// the first response and fill in missing windows from later
			// endpoints until both known families are present.
			merged = appendMissingWindows(merged, windows)
			if antigravityFamiliesComplete(merged) {
				return merged, nil
			}
		}
		// The summary API no longer returns Gemini; fill it from per-model quota.
		if overallCtx.Err() == nil && len(merged) > 0 && !hasGeminiWindow(merged) {
			for _, endpoint := range antigravityUserQuotaURLs {
				if overallCtx.Err() != nil {
					break
				}
				r, err := c.APICall(overallCtx, account.AuthIndex, http.MethodPost, endpoint, headers, data)
				if err != nil {
					continue
				}
				gw, perr := ParseAntigravityModelQuota([]byte(r.Body))
				if perr != nil || len(gw) == 0 {
					continue
				}
				merged = appendMissingWindows(merged, gw)
				break
			}
		}
		if len(merged) > 0 {
			return merged, nil
		}

		if anyAttempt > 0 && allAuth && lastErrKind == "auth" {
			return nil, &APICallError{Kind: "auth"}
		}
		if overallCtx.Err() != nil || lastErrKind == "timeout" {
			return nil, &APICallError{Kind: "timeout"}
		}
		if lastErrKind == "parse" {
			return nil, &APICallError{Kind: "parse"}
		}
		if lastErrKind == "empty" {
			return nil, &APICallError{Kind: "empty"}
		}
		return nil, &APICallError{Kind: "upstream"}
	}
	if p == "codex" {
		windows, _, _, _, err := c.FetchCodexUsage(ctx, account)
		return windows, err
	}
	return nil, &APICallError{Kind: "unsupported provider"}
}

// FetchClaudeUsage requests usage and reset inventory in a single read-only
// call. Unsupported inventory queries fall back once to ordinary usage within
// the same deadline. Authentication/rate-limit/server failures never retry.
func (c *Client) FetchClaudeUsage(ctx context.Context, account AccountQuota) ([]QuotaWindow, *ResetCredits, error) {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	headers := map[string]string{
		"Authorization": "Bearer $TOKEN$", "Accept": "application/json",
		"Anthropic-Beta": "oauth-2025-04-20", "User-Agent": "claude-cli/2.1.0 (external, cli)",
	}
	r, err := c.APICall(callCtx, account.AuthIndex, http.MethodGet, claudeResetUsageURL, headers, "")
	var ae *APICallError
	if errors.As(err, &ae) && (ae.StatusCode == http.StatusBadRequest || ae.StatusCode == http.StatusForbidden) && callCtx.Err() == nil {
		headers["User-Agent"] = "claude-code/2.1.0"
		r, err = c.APICall(callCtx, account.AuthIndex, http.MethodGet, claudeQuotaURL, headers, "")
	}
	if err != nil {
		return nil, nil, err
	}
	body := []byte(r.Body)
	windows, err := ParseClaudeQuota(body)
	if err != nil {
		return nil, nil, err
	}
	if len(windows) == 0 {
		return nil, nil, &APICallError{Kind: "empty"}
	}
	reset, err := ParseClaudeResetCredits(body, time.Now().UTC())
	if err != nil {
		return nil, nil, err
	}
	return windows, reset, nil
}

// FetchCodexUsage performs one WHAM usage request and returns the normal quota
// windows plus canonical plan, separately typed API credits, and reset-right
// counts. It shares the existing loopback api-call/auth-index/token-injection path.
func (c *Client) FetchCodexUsage(ctx context.Context, account AccountQuota) ([]QuotaWindow, string, *APICredits, *ResetCredits, error) {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	h := map[string]string{"User-Agent": "quota-monitor/1.0", "Authorization": "Bearer $TOKEN$"}
	if account.accountID != "" {
		h["ChatGPT-Account-Id"] = account.accountID
	}
	r, err := c.APICall(callCtx, account.AuthIndex, http.MethodGet, codexQuotaURL, h, "")
	if err != nil {
		return nil, "", nil, nil, err
	}
	body := []byte(r.Body)
	windows, err := ParseCodexQuota(body)
	if err != nil {
		return nil, "", nil, nil, err
	}
	plan, err := ParseCodexPlanType(body)
	if err != nil {
		return nil, "", nil, nil, err
	}
	api, reset, err := ParseCodexUsageExtras(body)
	if err != nil {
		return nil, "", nil, nil, err
	}
	return windows, plan, api, reset, nil
}

// FetchCodexResetCreditsExpiry makes the optional second WHAM call. The
// endpoint is allowlisted and failure is deliberately left to callers as a
// nonfatal enrichment failure.
func (c *Client) FetchCodexResetCreditsExpiry(ctx context.Context, account AccountQuota) (*time.Time, error) {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	h := map[string]string{"User-Agent": "quota-monitor/1.0", "Authorization": "Bearer $TOKEN$"}
	if account.accountID != "" {
		h["ChatGPT-Account-Id"] = account.accountID
	}
	r, err := c.APICall(callCtx, account.AuthIndex, http.MethodGet, codexResetCreditsURL, h, "")
	if err != nil {
		return nil, err
	}
	return ParseCodexResetCreditsExpiry([]byte(r.Body), time.Now().UTC())
}

// FetchRecentUsageAuths returns the most recent completed, successful generation
// with reported token consumption per provider and stable AuthIndex. The audit API
// sorts by request time, so every page in the time window must be inspected: a
// noisy account must not hide another account behind the first page. No session,
// model, token count or client identifiers escape this method.
func (c *Client) FetchRecentUsageAuths(ctx context.Context, limit int) (map[string]time.Time, error) {
	if c.BaseURL == "" {
		return nil, errors.New("baseURL is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	// Bound network work. Never publish a partial result as an accurate selection.
	const maxPages = 20
	from := time.Now().UTC().Add(-RecentUsageWindow).Format(time.RFC3339Nano)
	out := make(map[string]time.Time)
	cursor := ""
	for page := 0; page < maxPages; page++ {
		u := fmt.Sprintf("%s/management/plugins/usage-audit/records?limit=%d&status=success&from=%s", c.BaseURL, limit, url.QueryEscape(from))
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, errors.New("failed to create usage records request")
		}
		if c.ManagementKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.ManagementKey)
			req.Header.Set("X-Management-Key", c.ManagementKey)
		}
		resp, err := c.managementClient().Do(req)
		if err != nil {
			return nil, errors.New("usage records request failed")
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("usage records status: %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
		resp.Body.Close()
		if err != nil || int64(len(body)) > MaxBodyBytes {
			return nil, errors.New("usage records response invalid")
		}
		var payload struct {
			Records []struct {
				Provider    string    `json:"provider"`
				AuthIndex   string    `json:"auth_index"`
				RequestedAt time.Time `json:"requested_at"`
				T           time.Time `json:"t"`
				Failed      bool      `json:"failed"`
				Generate    bool      `json:"generate"`
				TotalTokens int64     `json:"total_tokens"`
			} `json:"records"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if json.Unmarshal(body, &payload) != nil {
			return nil, errors.New("usage records response invalid")
		}
		for _, r := range payload.Records {
			provider, idx := strings.ToLower(strings.TrimSpace(r.Provider)), strings.TrimSpace(r.AuthIndex)
			if provider == "" || idx == "" || r.Failed || !r.Generate || r.TotalTokens <= 0 {
				continue
			}
			t := r.RequestedAt
			if t.IsZero() {
				t = r.T
			}
			if t.IsZero() {
				continue
			}
			key := provider + "\x00" + idx
			if prev, ok := out[key]; !ok || t.After(prev) {
				out[key] = t
			}
		}
		if !payload.HasMore {
			return out, nil
		}
		if payload.NextCursor == "" || payload.NextCursor == cursor {
			return nil, errors.New("usage records pagination invalid")
		}
		cursor = payload.NextCursor
	}
	return nil, errors.New("usage records pagination limit exceeded")
}

func (c *Client) FetchAuthFiles(ctx context.Context) ([]AccountQuota, error) {
	if c.BaseURL == "" {
		return nil, errors.New("baseURL is required")
	}

	reqURL := c.BaseURL + "/management/auth-files"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.ManagementKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.ManagementKey)
		req.Header.Set("X-Management-Key", c.ManagementKey)
	}

	resp, err := c.managementClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from management endpoint: %d", resp.StatusCode)
	}

	// Limit reader to avoid unbounded memory exhaustion
	limitReader := io.LimitReader(resp.Body, MaxBodyBytes+1)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if int64(len(body)) > MaxBodyBytes {
		return nil, fmt.Errorf("response body exceeded limit of %d bytes", MaxBodyBytes)
	}

	return c.Adapter.ParseAuthFilesJSON(body)
}
