package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"quota-monitor/internal/authdir"
	"quota-monitor/internal/availability"
	"quota-monitor/internal/dashboard"
	"quota-monitor/internal/overview"
	quotamonitor "quota-monitor/internal/quota"
)

const (
	pluginName                = "quota-monitor"
	pluginVersion             = "0.2.4"
	pluginAuthor              = "dohwi"
	pluginRepo                = "https://github.com/dohwi/cliproxyapi-plugin-quota-monitor"
	pluginSchemaVersion       = 6
	mgmtBase                  = "/v0/management/plugins/quota-monitor"
	cacheTTL                  = 120 * time.Second
	defaultBackgroundInterval = 60 * time.Second
	backgroundFailureBackoff  = 15 * time.Second
	fetchTimeout              = 10 * time.Second
	collectionTimeout         = 25 * time.Second
	// Management callers may retry once or more when a lifecycle reconfigure
	// invalidates the flight they joined. Keep that retry bounded separately
	// from the per-collection deadline so a racing status/accounts request does
	// not publish the old generation's empty result.
	managementCollectionTimeout = 2*collectionTimeout + time.Second
	// Account probes are independent loopback requests. A bounded pool of 16
	// updates normal homelab-sized account sets in one wave while avoiding an
	// unbounded burst against CLIProxyAPI or upstream providers.
	collectionWorkers     = 16
	maxCollectionAccounts = 512
	maxProbeJobs          = 128
	maxResetDetailJobs    = 16
	// activeProbeTTL is retained only for legacy test compatibility; per-account
	// scheduling supersedes the global TTL for all probe decisions.
	activeProbeTTL = 5 * time.Minute
	forceCooldown  = 30 * time.Second

	// Per-account probe interval tiers (requirement 1).
	probeIntervalCurrent      = 2 * time.Minute  // current/recently-used account
	probeIntervalHealthyKnown = 8 * time.Minute  // healthy, known, idle
	probeIntervalUnknown      = 15 * time.Minute // unknown or stale data
	probeIntervalShortLimited = 20 * time.Minute // short-window rate-limited
	probeIntervalExhausted    = 45 * time.Minute // weekly exhausted / unavailable / auth error
	probeIntervalDisabled     = 2 * time.Hour    // disabled — skip probe

	// Reset deadlines override slow limited-account tiers, without allowing a
	// stale/past deadline to trigger upstream requests on every collection.
	probeIntervalResetPending = time.Minute
	probeResetGrace           = 5 * time.Second

	// currentAccountWindow is the maximum age of audited token usage for an
	// account to be considered the "current" (most recently used) account.
	currentAccountWindow = quotamonitor.RecentUsageWindow

	// maxProbeMapEntries bounds the per-account due map to prevent unbounded
	// memory accumulation when accounts are removed from management.
	maxProbeMapEntries = 1024
)

var now = time.Now

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}
type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func OKEnvelope(v any) []byte {
	b, err := marshalJSON(v)
	if err != nil {
		return ErrorEnvelope("marshal_error", "response encoding failed")
	}
	out, _ := json.Marshal(envelope{OK: true, Result: b})
	return out
}
func ErrorEnvelope(code, message string) []byte {
	out, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return out
}

// probeTier classifies an account for per-account due scheduling.
type probeTier int

const (
	tierDisabled     probeTier = iota // disabled — 2h or skip
	tierExhausted                     // weekly exhausted / unavailable / auth error — 45m
	tierShortLimited                  // short-window rate-limited — 20m
	tierUnknown                       // unknown/stale data — 15m
	tierHealthy                       // healthy, known, idle — 8m
	tierCurrent                       // current / recently-used — 2m
)

// probeIntervalForTier maps a tier to its scheduled probe interval.
func probeIntervalForTier(t probeTier) time.Duration {
	switch t {
	case tierCurrent:
		return probeIntervalCurrent
	case tierHealthy:
		return probeIntervalHealthyKnown
	case tierUnknown:
		return probeIntervalUnknown
	case tierShortLimited:
		return probeIntervalShortLimited
	case tierExhausted:
		return probeIntervalExhausted
	default: // tierDisabled
		return probeIntervalDisabled
	}
}

// classifyAccountTier determines the probe tier for a single account.
// It uses only safe Operational fields, window state, retry/error status
// and the current/recent marker — no auth secrets.
func classifyAccountTier(a quotamonitor.AccountQuota, isCurrent bool) probeTier {
	op := a.Operational

	// Disabled accounts are probed very infrequently.
	if op != nil && op.Disabled != nil && *op.Disabled {
		return tierDisabled
	}

	// Auth error or permanently unavailable → slow tier.
	if a.ErrorKind == "auth" {
		return tierExhausted
	}
	if op != nil && op.Unavailable != nil && *op.Unavailable {
		return tierExhausted
	}

	// Weekly (long-window) exhausted → slow.
	isAG := strings.EqualFold(strings.TrimSpace(a.Provider), "antigravity")
	for _, w := range a.Windows {
		minutes := 0
		if w.WindowMinutes != nil {
			minutes = *w.WindowMinutes
		}
		nameLow := strings.ToLower(w.Name)
		isLong := minutes == 10080 || strings.Contains(nameLow, "week") ||
			strings.Contains(nameLow, "secondary")
		if isLong {
			if isAG && !strings.Contains(nameLow, "gemini") {
				continue
			}
			remaining := float64(100)
			if w.RemainingPercent != nil {
				remaining = *w.RemainingPercent
			}
			if w.LimitReached != nil && *w.LimitReached {
				remaining = 0
			}
			if remaining <= 0 {
				return tierExhausted
			}
		}
	}

	// Retry-after set (short-window rate limit).
	if a.RetryAfter != nil && *a.RetryAfter > 0 {
		return tierShortLimited
	}
	if op != nil && op.NextRetryAfter != nil && now().Before(*op.NextRetryAfter) {
		return tierShortLimited
	}
	// Short-window exhausted.
	for _, w := range a.Windows {
		minutes := 0
		if w.WindowMinutes != nil {
			minutes = *w.WindowMinutes
		}
		nameLow := strings.ToLower(w.Name)
		isShort := minutes == 300 || strings.Contains(nameLow, "five") ||
			strings.Contains(nameLow, "5h") ||
			strings.Contains(nameLow, "primary")
		if isShort {
			if isAG && !strings.Contains(nameLow, "gemini") {
				continue
			}
			remaining := float64(100)
			if w.RemainingPercent != nil {
				remaining = *w.RemainingPercent
			}
			if w.LimitReached != nil && *w.LimitReached {
				remaining = 0
			}
			if remaining <= 0 {
				return tierShortLimited
			}
		}
	}

	// Currently active / recently used.
	if isCurrent {
		return tierCurrent
	}

	// Healthy with known windows.
	if a.Status == quotamonitor.StatusKnown && len(a.Windows) > 0 {
		return tierHealthy
	}

	// Unknown or no data.
	return tierUnknown
}

// nextAccountProbeDue brings exhausted windows forward to their reset deadline.
// A reset is only a reason to recheck upstream, never evidence of restored quota.
func nextAccountProbeDue(a quotamonitor.AccountQuota, isCurrent bool, attemptedAt time.Time) time.Time {
	tier := classifyAccountTier(a, isCurrent)
	due := attemptedAt.Add(probeIntervalForTier(tier))
	if tier == tierDisabled || a.ErrorKind == "auth" {
		return due
	}
	observedAt := a.FetchedAt
	if a.ObservedAt != nil {
		observedAt = *a.ObservedAt
	}
	if a.LastSuccessAt != nil {
		observedAt = *a.LastSuccessAt
	}
	for _, w := range a.Windows {
		exhausted := (w.LimitReached != nil && *w.LimitReached) ||
			(w.Allowed != nil && !*w.Allowed) ||
			(w.RemainingPercent != nil && *w.RemainingPercent <= 0) ||
			(w.UsedPercent != nil && *w.UsedPercent >= 100)
		if !exhausted {
			continue
		}
		resetAt := w.ResetAt
		if resetAt == nil && w.ResetAfterSec != nil && !observedAt.IsZero() {
			resetAt, _ = quotamonitor.ResetAtFromObservedAt(observedAt, *w.ResetAfterSec)
		}
		if resetAt == nil || resetAt.IsZero() {
			continue
		}
		resetDue := resetAt.Add(probeResetGrace)
		if earliest := attemptedAt.Add(probeIntervalResetPending); resetDue.Before(earliest) {
			resetDue = earliest
		}
		if resetDue.Before(due) {
			due = resetDue
		}
	}
	return due
}

// nextPollHintMS returns the suggested next-poll delay in milliseconds for the
// whole account set, based on the minimum tier across all current provider keys.
func nextPollHintMS(accounts []quotamonitor.AccountQuota, currentKeys map[string]string) int64 {
	var minInterval time.Duration
	for _, a := range accounts {
		prov := strings.ToLower(strings.TrimSpace(a.Provider))
		isCurrent := a.AccountKey != "" && currentKeys[prov] == a.AccountKey
		tier := classifyAccountTier(a, isCurrent)
		interval := probeIntervalForTier(tier)
		if minInterval == 0 || interval < minInterval {
			minInterval = interval
		}
	}
	if minInterval == 0 {
		minInterval = probeIntervalUnknown
	}
	return minInterval.Milliseconds()
}

// probeSchedule describes actual network attempts, not passive collection times.
type probeSchedule struct {
	LastAttempt time.Time
	LastSuccess time.Time
	NextDue     time.Time
}

type collectionResult struct {
	accounts   []quotamonitor.AccountQuota
	stale      bool
	err        error // fixed safe errors only; never retain upstream details
	generation uint64
	truncated  bool
	category   string
}

type collectionFlight struct {
	generation  uint64
	runID       uint64
	hadComplete bool
	force       bool
	accountKey  string // non-empty for a single-account collection
	done        chan struct{}
	cancel      context.CancelFunc
	result      collectionResult // immutable after done is closed
}

type collectionHealth struct {
	Collections          uint64 `json:"collections"`
	Successes            uint64 `json:"successes"`
	Failures             uint64 `json:"failures"`
	Timeouts             uint64 `json:"timeouts"`
	Canceled             uint64 `json:"canceled"`
	ProbeAttempts        uint64 `json:"probe_attempts"`
	ProbeSuccesses       uint64 `json:"probe_successes"`
	ProbeFailures        uint64 `json:"probe_failures"`
	TruncatedCollections uint64 `json:"truncated_collections"`
	LastCategory         string `json:"last_category,omitempty"`
	LastLatencyMS        int64  `json:"last_latency_ms"`
	Truncated            bool   `json:"truncated"`
}

type collectionProgress struct {
	Completed int
	Total     int
	StartedAt time.Time
	UpdatedAt time.Time
}

type stateData struct {
	mu            sync.Mutex
	configured    bool
	reason        string
	client        *quotamonitor.Client
	last          []quotamonitor.AccountQuota
	cachedAt      time.Time
	lastSuccessAt time.Time
	generation    uint64
	flight        *collectionFlight
	latest        collectionResult
	health        collectionHealth
	// runSeq is monotonic for the lifetime of this plugin process. runID is the
	// active/latest collection identity; only an opaque derived token is public.
	runSeq     uint64
	runID      uint64
	inProgress bool
	progress   collectionProgress
	// order and current are committed only when a complete batch succeeds.
	// Progressive account patches must not change either view mid-collection.
	order   []string
	current map[string]currentChoice
	// probeDue is retained as a compatibility view of schedules.NextDue.
	probeDue    map[string]time.Time
	schedules   map[string]probeSchedule
	lastProbeAt time.Time
	lastForceAt time.Time
	// Reserve public refresh attempts before collecting, including failed or
	// overlapping calls, to bound unauthenticated upstream work globally.
	lastResourceRefreshAt time.Time
	accountRefreshAt      map[string]time.Time
	resolver              *authdir.Resolver
	snapshotComplete      bool
	collectorCancel       context.CancelFunc
	collectorDone         chan struct{}
}

var (
	testHookMu           sync.RWMutex
	testBackgroundTickCh chan struct{}
	testCollectorHook    func(gen uint64, success bool)
)

func setTestHooks(tickCh chan struct{}, hook func(gen uint64, success bool)) {
	testHookMu.Lock()
	testBackgroundTickCh = tickCh
	testCollectorHook = hook
	testHookMu.Unlock()
}

func getTestBackgroundTickCh() chan struct{} {
	testHookMu.RLock()
	defer testHookMu.RUnlock()
	return testBackgroundTickCh
}

func callTestCollectorHook(gen uint64, success bool) {
	testHookMu.RLock()
	hook := testCollectorHook
	testHookMu.RUnlock()
	if hook != nil {
		hook(gen, success)
	}
}

func isBackgroundDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("QUOTA_MONITOR_DISABLE_BACKGROUND")))
	if v == "1" || v == "true" || v == "yes" || v == "on" {
		return true
	}
	v = strings.ToLower(strings.TrimSpace(os.Getenv("QUOTA_MONITOR_BACKGROUND_COLLECTOR")))
	if v == "0" || v == "false" || v == "no" || v == "off" || v == "disabled" {
		return true
	}
	return false
}

func stopCollectorLocked() chan struct{} {
	if state.collectorCancel != nil {
		state.collectorCancel()
		state.collectorCancel = nil
	}
	done := state.collectorDone
	state.collectorDone = nil
	return done
}

func startCollectorLocked() {
	if !state.configured || state.client == nil {
		return
	}
	if isBackgroundDisabled() {
		return
	}
	_ = stopCollectorLocked()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	state.collectorCancel = cancel
	state.collectorDone = done
	gen := state.generation

	go runBackgroundCollector(ctx, gen, done)
}

func runBackgroundCollector(ctx context.Context, expectedGen uint64, done chan struct{}) {
	defer close(done)

	// Immediate complete collection on startup
	if ctx.Err() != nil {
		return
	}
	success := executeBackgroundCollection(ctx, expectedGen)
	callTestCollectorHook(expectedGen, success)

	for {
		interval := defaultBackgroundInterval
		if !success {
			interval = backgroundFailureBackoff
		}

		timer := time.NewTimer(interval)
		tickCh := getTestBackgroundTickCh()
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-tickCh:
			timer.Stop()
		}

		if ctx.Err() != nil {
			return
		}

		success = executeBackgroundCollection(ctx, expectedGen)
		callTestCollectorHook(expectedGen, success)
	}
}

func executeBackgroundCollection(ctx context.Context, expectedGen uint64) bool {
	state.mu.Lock()
	if !state.configured || state.generation != expectedGen {
		state.mu.Unlock()
		return false
	}
	state.mu.Unlock()

	res := collectAccounts(ctx, false)
	return res.err == nil
}

var state = &stateData{reason: "QUOTA_MONITOR_MGMT_URL and QUOTA_MONITOR_MGMT_KEY are required", probeDue: make(map[string]time.Time), current: make(map[string]currentChoice)}

func Shutdown() {
	st := state
	st.mu.Lock()
	done := stopCollectorLocked()
	st.generation++
	if st.flight != nil {
		st.flight.cancel()
		st.flight = nil
	}
	st.configured = false
	st.snapshotComplete = false
	st.inProgress = false
	st.progress = collectionProgress{}
	st.reason = "plugin stopped"
	if st.client != nil {
		st.client.CloseIdleConnections()
	}
	st.mu.Unlock()

	if done != nil {
		<-done
	}
}

func HandleMethod(method string, request []byte) ([]byte, bool) {
	switch method {
	case "plugin.register", "register", "plugin.reconfigure", "reconfigure":
		return handleRegister(request), true
	case "plugin.quiesce", "quiesce", "plugin.shutdown", "shutdown":
		Shutdown()
		return OKEnvelope(map[string]any{}), true
	case "management.register":
		return handleManagementRegister(), true
	case "management.handle":
		return handleManagementHandle(request), true
	default:
		return ErrorEnvelope("unknown_method", fmt.Sprintf("unknown method: %s", method)), false
	}
}

type metadata struct {
	Name             string `json:"Name"`
	Version          string `json:"Version"`
	Author           string `json:"Author"`
	GitHubRepository string `json:"GitHubRepository"`
}
type registrationResponse struct {
	SchemaVersion uint32          `json:"schema_version"`
	Metadata      metadata        `json:"metadata"`
	Capabilities  map[string]bool `json:"capabilities"`
}

func handleRegister(request []byte) []byte {
	var req struct {
		SchemaVersion uint32 `json:"schema_version"`
		ConfigYAML    []byte `json:"config_yaml"`
	}
	_ = json.Unmarshal(request, &req)
	sv := uint32(pluginSchemaVersion)
	if req.SchemaVersion > 0 && req.SchemaVersion < sv {
		sv = req.SchemaVersion
	}
	configureFromEnv(req.ConfigYAML)
	return OKEnvelope(registrationResponse{SchemaVersion: sv, Metadata: metadata{Name: pluginName, Version: pluginVersion, Author: pluginAuthor, GitHubRepository: pluginRepo}, Capabilities: map[string]bool{"management_api": true}})
}

func configureFromEnv(configYAML []byte) {
	rawURL, key := strings.TrimSpace(os.Getenv("QUOTA_MONITOR_MGMT_URL")), strings.TrimSpace(os.Getenv("QUOTA_MONITOR_MGMT_KEY"))
	state.mu.Lock()
	done := stopCollectorLocked()
	var previousAuthDir string
	if state.resolver != nil {
		previousAuthDir = state.resolver.Dir()
	}
	previousClient := state.client
	previousLast := state.last
	previousLastSuccessAt := state.lastSuccessAt
	state.mu.Unlock()

	if done != nil {
		<-done
	}

	authDir := authdir.ResolveDirWithFallback(configYAML, previousAuthDir)
	var resolver *authdir.Resolver
	if authDir != "" {
		resolver = authdir.New(authDir)
	} else {
		resolver = authdir.New("") // blocker state
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.generation++
	if state.flight != nil {
		state.flight.cancel()
		state.flight = nil
	}
	state.lastForceAt = time.Time{}
	state.accountRefreshAt = nil
	state.lastProbeAt = time.Time{}
	state.lastSuccessAt = time.Time{}
	state.latest = collectionResult{generation: state.generation}
	state.health = collectionHealth{}
	state.runID = 0
	state.inProgress = false
	state.progress = collectionProgress{}
	state.order = nil
	state.current = make(map[string]currentChoice)
	state.schedules = make(map[string]probeSchedule)
	state.configured = false
	if state.client != nil {
		state.client.CloseIdleConnections()
	}
	state.client = nil
	state.last = nil
	state.cachedAt = time.Time{}
	state.snapshotComplete = false
	state.probeDue = make(map[string]time.Time)
	state.resolver = resolver
	state.reason = "QUOTA_MONITOR_MGMT_URL and QUOTA_MONITOR_MGMT_KEY are required"
	if rawURL == "" || key == "" {
		return
	}
	if err := validateLoopbackURL(rawURL); err != nil {
		state.reason = err.Error()
		return
	}
	newClient := quotamonitor.NewClient(rawURL, key,
		quotamonitor.WithClientAdapter(
			quotamonitor.NewAdapter(quotamonitor.WithEndpointResolver(resolver)),
		),
	)
	state.client = newClient
	state.configured = true
	state.reason = ""
	// If reconfiguring to the exact same management target, retain previous accounts as stale
	// so passive resource reads do not drop to 0 accounts during routine host reconfigure.
	if sameTarget(previousClient, newClient) && len(previousLast) > 0 {
		state.last = cloneAccounts(previousLast)
		state.lastSuccessAt = previousLastSuccessAt
		state.order = accountOrder(previousLast)
		state.current = currentAccountSelection(previousLast)
		state.snapshotComplete = true
		// cachedAt remains zero to ensure next management read re-validates rather than hits cache.
	}
	startCollectorLocked()
}

func validateLoopbackURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("management URL is invalid")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("management URL scheme must be http or https")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("management URL must not contain userinfo, query, or fragment")
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("management URL must use a loopback host")
	}
	return nil
}

type mgmtRoute struct {
	Method string `json:"Method"`
	Path   string `json:"Path"`
}

// resourceRoute mirrors pluginapi.ResourceRoute (JSON field names match the
// host RPC decoder, which uses default Go field names — no json tags).
type resourceRoute struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu"`
	Description string `json:"Description"`
	Method      string `json:"Method,omitempty"`
}

// resourceBase is the host prefix for browser-navigable plugin resources.
// Resource requests are dispatched to management.handle with the full URL path.
const resourceBase = "/v0/resource/plugins/quota-monitor"

// resourceDashboard is the single browser-navigable resource exposed by this
// plugin. Its Menu label makes it appear in the management console sidebar.
var resourceDashboard = resourceRoute{
	Path:        "/dashboard",
	Menu:        "계정 쿼터",
	Description: "OAuth 계정별 남은 쿼터 및 상태 모니터",
}

// /v0/resource/... is unauthenticated, so it only serves the static page shell and
// fonts. Quota data and the refresh action live under /v0/management/....
var hiddenResourceRoutes = []resourceRoute{
	{Path: "/assets/manrope-variable.woff2", Menu: "", Description: "", Method: "GET"},
	{Path: "/assets/suit-regular.woff2", Menu: "", Description: "", Method: "GET"},
	{Path: "/assets/suit-semibold.woff2", Menu: "", Description: "", Method: "GET"},
}

type safeOperational struct {
	Disabled    *bool  `json:"disabled,omitempty"`
	Unavailable *bool  `json:"unavailable,omitempty"`
	Status      string `json:"status,omitempty"`
	AccountType string `json:"account_type,omitempty"`
}
type safeAccount struct {
	RefreshSupported bool                       `json:"refresh_supported"`
	Source           string                     `json:"source,omitempty"`
	AccountKey       string                     `json:"account_key"`
	Provider         string                     `json:"provider"`
	Account          string                     `json:"account"`
	Status           quotamonitor.Status        `json:"status"`
	Stale            bool                       `json:"stale,omitempty"`
	RetryAfter       *int64                     `json:"retry_after,omitempty"`
	ErrorKind        string                     `json:"error_kind,omitempty"`
	LastSuccess      *time.Time                 `json:"last_success,omitempty"`
	ObservedAt       *time.Time                 `json:"observed_at,omitempty"`
	Windows          []quotamonitor.QuotaWindow `json:"windows,omitempty"`
	APICredits       *quotamonitor.APICredits   `json:"api_credits,omitempty"`
	ResetCredits     *quotamonitor.ResetCredits `json:"reset_credits,omitempty"`
	Operational      *safeOperational           `json:"operational,omitempty"`
	// Endpoint is "direct", "unknown", or a sanitized single-label proxy
	// hostname (e.g. a Docker service name such as "warp1"). URLs, ports,
	// credentials, IPs and dotted/FQDN hosts are never included.
	Endpoint quotamonitor.EndpointKind `json:"endpoint"`
	PlanType string                    `json:"plan_type,omitempty"`
	// CurrentAccount marks the single most-recently-used account per provider
	// based on the latest audited token usage within currentAccountWindow.
	// At most one account per provider has this set to true.
	CurrentAccount bool   `json:"current_account,omitempty"`
	CurrentKind    string `json:"current_kind,omitempty"` // recent only
	CurrentReason  string `json:"current_reason,omitempty"`
	SelectedBy     string `json:"selected_by,omitempty"`
	// RecentRequestCount is the count of recent requests from OperationalStatus.
	// Only included when > 0 to avoid leaking unnecessary zero values.
	RecentRequestCount int `json:"recent_request_count,omitempty"`
}

func sanitizePlanType(p string) string {
	return quotamonitor.NormalizeCodexPlanType(p)
}

func maskAccount(s string) string {
	s = strings.TrimSpace(s)
	if at := strings.LastIndex(s, "@"); at > 1 {
		local, domain := s[:at], s[at:]
		if len(local) > 2 {
			return local[:1] + "***" + local[len(local)-1:] + domain
		}
		return "***" + domain
	}
	if len(s) <= 4 {
		return "***"
	}
	return s[:1] + "***" + s[len(s)-1:]
}

func isAccountRoutableHealthy(a quotamonitor.AccountQuota) bool {
	if a.AccountKey == "" {
		return false
	}
	if a.Operational != nil {
		if a.Operational.Disabled != nil && *a.Operational.Disabled {
			return false
		}
		if a.Operational.Unavailable != nil && *a.Operational.Unavailable {
			return false
		}
		opStatus := strings.ToLower(strings.TrimSpace(a.Operational.Status))
		if opStatus == "error" || opStatus == "disabled" || opStatus == "unavailable" || opStatus == "limited" {
			return false
		}
		if a.Operational.NextRetryAfter != nil && now().Before(*a.Operational.NextRetryAfter) {
			return false
		}
	}
	if a.ErrorKind == "auth" || a.Status == quotamonitor.StatusError {
		return false
	}
	// For routing and current-account eligibility:
	// Any quota exhaustion (short 5h limit or weekly secondary) disqualifies the account.
	// For Antigravity, whole-account availability derives exclusively from Gemini windows.
	isAG := strings.EqualFold(strings.TrimSpace(a.Provider), "antigravity")
	isClaude := strings.EqualFold(strings.TrimSpace(a.Provider), "claude")
	for _, w := range a.Windows {
		name := strings.ToLower(w.Name)
		if isClaude && isClaudeBillingWindow(w) {
			continue
		}
		if isAG && !strings.Contains(name, "gemini") {
			// Ignore non-Gemini (e.g. Claude) window exhaustion for AG routing candidate
			continue
		}
		remaining := 100.0
		if w.RemainingPercent != nil {
			remaining = *w.RemainingPercent
		}
		if w.LimitReached != nil && *w.LimitReached {
			remaining = 0
		}
		if w.Allowed != nil && !*w.Allowed {
			remaining = 0
		}
		if remaining <= 0 {
			return false // Limited or unavailable window! Disqualified!
		}
	}
	return true
}

// Match the dashboard's displayedQuotaWindows in both routing and recovery:
// disabled pay-as-you-go overage does not exhaust subscription quota.
func isClaudeBillingWindow(w quotamonitor.QuotaWindow) bool {
	name := strings.ToLower(w.Name)
	return name == "overage" || strings.HasPrefix(name, "overage-") || strings.HasPrefix(name, "overage_") || strings.HasPrefix(name, "overage ")
}

func accountUsableForRouting(a quotamonitor.AccountQuota) bool {
	return isAccountRoutableHealthy(a)
}

type currentChoice struct {
	accountKey string
	kind       string // "recent" only
	reason     string // "recent_usage" only
}

func currentAccountSelection(in []quotamonitor.AccountQuota) map[string]currentChoice {
	result := make(map[string]currentChoice)
	cutoff := now().Add(-currentAccountWindow)

	// Group accounts by lowercase provider
	byProv := make(map[string][]quotamonitor.AccountQuota)
	for _, a := range in {
		p := strings.ToLower(strings.TrimSpace(a.Provider))
		if p != "" {
			byProv[p] = append(byProv[p], a)
		}
	}

	for prov, accs := range byProv {
		// Only audited, token-consuming generations can establish recent use.
		// Operational request counters may include failures and token-count calls.
		var mostRecent *quotamonitor.AccountQuota
		var latestTime time.Time
		for i := range accs {
			a := &accs[i]
			if a.LastUsedAt == nil || a.LastUsedAt.Before(cutoff) {
				continue
			}
			if !isAccountRoutableHealthy(*a) {
				continue
			}
			if mostRecent == nil || a.LastUsedAt.After(latestTime) || (a.LastUsedAt.Equal(latestTime) && a.AccountKey < mostRecent.AccountKey) {
				mostRecent = a
				latestTime = *a.LastUsedAt
			}
		}
		if mostRecent != nil {
			result[prov] = currentChoice{accountKey: mostRecent.AccountKey, kind: "recent", reason: "recent_usage"}
		}
	}

	return result
}

// currentSelectionKeys maps only audited recent-use choices to scheduler keys.
func currentSelectionKeys(sel map[string]currentChoice) map[string]string {
	res := make(map[string]string, len(sel))
	for k, v := range sel {
		if v.kind == "recent" && v.reason == "recent_usage" {
			res[k] = v.accountKey
		}
	}
	return res
}

func currentAccountKeys(in []quotamonitor.AccountQuota) map[string]string {
	return currentSelectionKeys(currentAccountSelection(in))
}

func sanitizeAccounts(in []quotamonitor.AccountQuota) []safeAccount {
	return sanitizeAccountsWithSelection(in, currentAccountSelection(in))
}

func sanitizeAccountsWithSelection(in []quotamonitor.AccountQuota, currentByProv map[string]currentChoice) []safeAccount {
	out := make([]safeAccount, 0, len(in))
	for _, a := range in {
		x := safeAccount{
			RefreshSupported: probeEligible(a),
			Source:           a.Source,
			AccountKey:       a.AccountKey,
			Provider:         a.Provider,
			Account:          maskAccount(a.Account),
			Status:           a.Status,
			Stale:            a.Stale,
			RetryAfter:       a.RetryAfter,
			ErrorKind:        a.ErrorKind,
			LastSuccess:      a.LastSuccessAt,
			ObservedAt:       a.ObservedAt,
			Endpoint:         a.Endpoint,
			PlanType:         sanitizePlanType(a.PlanType),
			APICredits:       a.APICredits,
			ResetCredits:     a.ResetCredits,
		}
		x.Windows = append([]quotamonitor.QuotaWindow(nil), a.Windows...)
		if a.Operational != nil {
			x.Operational = &safeOperational{
				Disabled:    a.Operational.Disabled,
				Unavailable: a.Operational.Unavailable,
				Status:      a.Operational.Status,
				AccountType: a.Operational.AccountType,
			}
			if a.Operational.RecentRequestCount > 0 {
				x.RecentRequestCount = a.Operational.RecentRequestCount
			}
		}
		// Preserve the committed selection during progressive updates, but never
		// publish a border after its audited use expires or the account blocks.
		// This only clears an invalid selection; it does not move it mid-batch.
		provKey := strings.ToLower(strings.TrimSpace(a.Provider))
		recent := a.LastUsedAt != nil && !a.LastUsedAt.Before(now().Add(-currentAccountWindow))
		if choice, ok := currentByProv[provKey]; ok && choice.kind == "recent" && choice.reason == "recent_usage" && choice.accountKey == a.AccountKey && recent && isAccountRoutableHealthy(a) {
			x.CurrentAccount = true
			x.CurrentKind = choice.kind
			x.CurrentReason = choice.reason
			x.SelectedBy = choice.reason
		}
		out = append(out, x)
	}
	return out
}

func accountOrder(accounts []quotamonitor.AccountQuota) []string {
	order := make([]string, 0, len(accounts))
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		if account.AccountKey == "" {
			continue
		}
		if _, ok := seen[account.AccountKey]; ok {
			continue
		}
		seen[account.AccountKey] = struct{}{}
		order = append(order, account.AccountKey)
	}
	return order
}

func orderAccounts(accounts []quotamonitor.AccountQuota, order []string) []quotamonitor.AccountQuota {
	byKey := make(map[string]quotamonitor.AccountQuota, len(accounts))
	unkeyed := make([]quotamonitor.AccountQuota, 0)
	for _, account := range accounts {
		if account.AccountKey == "" {
			unkeyed = append(unkeyed, account)
			continue
		}
		byKey[account.AccountKey] = account
	}
	out := make([]quotamonitor.AccountQuota, 0, len(accounts))
	for _, key := range order {
		if account, ok := byKey[key]; ok {
			out = append(out, account)
			delete(byKey, key)
		}
	}
	for _, account := range accounts {
		if account.AccountKey == "" {
			continue
		}
		if remaining, ok := byKey[account.AccountKey]; ok {
			out = append(out, remaining)
			delete(byKey, account.AccountKey)
		}
	}
	return append(out, unkeyed...)
}

func opaqueRunID(generation, runID uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("quota-monitor:%d:%d", generation, runID)))
	return hex.EncodeToString(sum[:8])
}

func handleManagementRegister() []byte {
	resources := append([]resourceRoute{resourceDashboard}, hiddenResourceRoutes...)
	return OKEnvelope(map[string]any{"routes": []mgmtRoute{{"GET", mgmtBase + "/status"}, {"GET", mgmtBase + "/accounts"}, {"POST", mgmtBase + "/refresh"}, {"GET", mgmtBase + "/dashboard"}, {"GET", mgmtBase + "/data/accounts"}, {"POST", mgmtBase + "/data/refresh"}}, "resources": resources})
}

type managementRequest struct {
	Method string              `json:"Method"`
	Path   string              `json:"Path"`
	Query  map[string][]string `json:"Query"`
	Body   []byte              `json:"Body"`
}
type managementResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers"`
	Body       []byte              `json:"Body"`
}

func handleManagementHandle(raw []byte) []byte {
	var req managementRequest
	if json.Unmarshal(raw, &req) != nil {
		return mgmtResponse(400, map[string]string{"error": "bad request"})
	}
	method, path := strings.ToUpper(strings.TrimSpace(req.Method)), strings.TrimRight(strings.TrimSpace(req.Path), "/")
	if method == "GET" && path == mgmtBase+"/data/accounts" {
		// Dashboard data: sanitized (no auth_index), snapshot only, even when empty,
		// expired, forced, or another collection is in flight. Management-authenticated.
		return addNoStore(mgmtResponse(200, resourceSnapshot()))
	}

	if method == "POST" && path == mgmtBase+"/data/refresh" {
		// Explicit dashboard refresh (management-authenticated) with a global
		// cooldown; it requires a confirmation query so a stray call never fans out
		// upstream work. Snapshot GETs remain read-only.
		if values := req.Query["confirm"]; len(values) != 1 || values[0] != "1" {
			return addNoStore(mgmtResponse(400, map[string]string{"error": "explicit confirmation required"}))
		}
		if values, present := req.Query["account_key"]; present {
			if len(values) != 1 || strings.TrimSpace(values[0]) == "" || len(values[0]) > 512 {
				return addNoStore(mgmtResponse(400, map[string]string{"error": "invalid account key"}))
			}
			code, body := refreshSingleAccount(values[0])
			return addNoStore(mgmtResponse(code, body))
		}
		// Reserve before collection so failures and concurrent requests cannot
		// bypass the global cooldown or fan out upstream work.
		st := state
		st.mu.Lock()
		at := now()
		if !st.lastResourceRefreshAt.IsZero() && at.Sub(st.lastResourceRefreshAt) < forceCooldown {
			st.mu.Unlock()
			return addNoStore(mgmtResponse(429, map[string]string{"error": "refresh cooldown"}))
		}
		st.lastResourceRefreshAt = at
		st.mu.Unlock()
		result, _ := collectManagementSnapshot(true)
		body := map[string]any{"refreshed": result.err == nil, "count": len(result.accounts), "stale": result.stale, "generation": result.generation, "truncated": result.truncated, "reason": result.category}
		if result.err != nil {
			body["error"] = "refresh failed"
		}
		return addNoStore(mgmtResponse(200, body))
	}

	if method == "POST" && path == mgmtBase+"/refresh" {
		if values, present := req.Query["account_key"]; present {
			if len(values) != 1 || strings.TrimSpace(values[0]) == "" || len(values[0]) > 512 {
				return mgmtResponse(400, map[string]string{"error": "invalid account key"})
			}
			code, body := refreshSingleAccount(values[0])
			return mgmtResponse(code, body)
		}
		result, _ := collectManagementSnapshot(true)
		body := map[string]any{"refreshed": result.err == nil, "count": len(result.accounts), "stale": result.stale, "generation": result.generation, "truncated": result.truncated, "reason": result.category}
		if result.err != nil {
			body["error"] = "refresh failed"
		}
		return mgmtResponse(200, body)
	}
	if method != "GET" {
		return mgmtResponse(404, map[string]string{"error": "not found"})
	}
	switch path {
	// Dashboard is reachable both as a management API route and as a
	// browser-navigable resource under the host resource prefix. The bare
	// "/dashboard" form is accepted defensively for relative dispatches.
	case resourceBase + "/assets/manrope-variable.woff2":
		return fontResponse(dashboard.Manrope)
	case resourceBase + "/assets/suit-regular.woff2":
		return fontResponse(dashboard.SUITRegular)
	case resourceBase + "/assets/suit-semibold.woff2":
		return fontResponse(dashboard.SUITSemibold)
	case mgmtBase + "/dashboard", resourceBase + "/dashboard", "/dashboard":
		// The resource form is unauthenticated, so it must never start upstream
		// work. Only the authenticated management form may warm an empty cache.
		if path != resourceBase+"/dashboard" {
			st := state
			st.mu.Lock()
			shouldWarm := st.configured && len(st.last) == 0 && st.flight == nil
			st.mu.Unlock()
			if shouldWarm {
				go func() {
					_ = collectManagement(false)
				}()
			}
		}
		return OKEnvelope(managementResponse{
			StatusCode: 200,
			Headers:    dashboardPageHeaders(),
			Body:       []byte(dashboard.HTML),
		})
	case mgmtBase + "/status":
		result, body := collectManagementSnapshot(false)
		configured, _ := body["configured"].(bool)
		body["configured"], body["accounts"], body["stale"] = configured, len(result.accounts), result.stale
		if result.err != nil {
			body["reason"] = "quota lookup failed"
		}
		if len(result.accounts) > 0 {
			body["count"] = len(result.accounts)
		}
		return mgmtResponse(200, body)
	case mgmtBase + "/accounts":
		result, snapshot := collectManagementSnapshot(false)
		configured, _ := snapshot["configured"].(bool)
		reason, _ := snapshot["reason"].(string)
		// Management API retains its authenticated contract, including internal auth_index fields.
		body := map[string]any{"configured": configured, "accounts": result.accounts, "stale": result.stale, "generation": result.generation, "truncated": result.truncated, "availability": availability.Compute(result.accounts), "overview": snapshot["overview"]}
		if reason != "" {
			body["reason"] = reason
		}
		if result.err != nil {
			body["reason"] = result.category
		}
		return mgmtResponse(200, body)
	default:
		return mgmtResponse(404, map[string]string{"error": "not found"})
	}
}

func snapshotConfig() (bool, string, time.Time) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.configured, state.reason, state.cachedAt
}

// normalizeSnapshotWindowResetAt fills missing absolute reset deadlines only in
// the resource DTO copy. cachedAt is the collection's fixed observation time,
// preventing stale snapshots from sliding a relative deadline on each request.
func normalizeSnapshotWindowResetAt(accounts []quotamonitor.AccountQuota, cachedAt time.Time) {
	for i := range accounts {
		observedAt := cachedAt
		if accounts[i].ObservedAt != nil {
			observedAt = *accounts[i].ObservedAt
		}
		for j := range accounts[i].Windows {
			window := &accounts[i].Windows[j]
			if window.ResetAt != nil || window.ResetAfterSec == nil {
				continue
			}
			if resetAt, ok := quotamonitor.ResetAtFromObservedAt(observedAt, *window.ResetAfterSec); ok {
				window.ResetAt = resetAt
			}
		}
	}
}

// resourceSnapshot never invokes any privileged client or disk scan.
func resourceSnapshot() map[string]any {
	st := state
	st.mu.Lock()
	accounts := orderAccounts(cloneAccounts(st.last), st.order)
	configured, reason := st.configured, st.reason
	cachedAt, lastSuccess := st.cachedAt, st.lastSuccessAt
	result, health, generation := st.latest, st.health, st.generation
	snapshotComplete := st.snapshotComplete
	inProgress, runID, progress := st.inProgress, st.runID, st.progress
	current := make(map[string]currentChoice, len(st.current))
	for provider, choice := range st.current {
		current[provider] = choice
	}
	st.mu.Unlock()
	normalizeSnapshotWindowResetAt(accounts, cachedAt)
	stale := result.stale || cachedAt.IsZero() || now().Sub(cachedAt) >= cacheTTL
	if reason == "" {
		switch {
		case result.err != nil:
			reason = result.category
		case cachedAt.IsZero():
			reason = "not_collected"
		case stale:
			reason = "cache_expired"
		}
	}
	body := map[string]any{
		"configured":             configured,
		"reason":                 reason,
		"accounts":               sanitizeAccountsWithSelection(accounts, current),
		"availability":           availability.Compute(accounts),
		"overview":               overview.Build(accounts, stale),
		"count":                  len(accounts),
		"stale":                  stale,
		"generation":             generation,
		"truncated":              result.truncated,
		"health":                 health,
		"refreshed":              false,
		"next_poll_hint_ms":      nextPollHintMS(accounts, currentSelectionKeys(current)),
		"snapshot_complete":      snapshotComplete,
		"collection_in_progress": inProgress,
	}
	if runID != 0 {
		body["run_id"] = opaqueRunID(generation, runID)
	}
	if inProgress || !progress.StartedAt.IsZero() {
		progressBody := map[string]any{
			"completed": progress.Completed,
			"total":     progress.Total,
		}
		if !progress.StartedAt.IsZero() {
			progressBody["started_at"] = progress.StartedAt.UTC()
		}
		if !progress.UpdatedAt.IsZero() {
			progressBody["updated_at"] = progress.UpdatedAt.UTC()
		}
		body["progress"] = progressBody
	}
	if !cachedAt.IsZero() {
		body["cached_at"] = cachedAt.UTC()
		body["cachedAt"] = cachedAt.UTC()
		body["collected_at"] = cachedAt.UTC()
	}
	if !lastSuccess.IsZero() {
		body["last_success_at"] = lastSuccess.UTC()
		body["last_success"] = lastSuccess.UTC()
	}
	return body
}

// collectManagement is the authenticated management read path. A
// plugin.reconfigure can cancel a flight after a management request has joined
// it; that flight must still return configuration_changed to its old waiters,
// but the management caller should retry against the current generation rather
// than expose configured=true with zero/stale accounts. Resource callers never
// use this function.
func sameTarget(a, b *quotamonitor.Client) bool {
	if a == nil || b == nil {
		return false
	}
	return a.BaseURL == b.BaseURL && a.ManagementKey == b.ManagementKey
}

func collectManagement(force bool) collectionResult {
	ctx, cancel := context.WithTimeout(context.Background(), managementCollectionTimeout)
	defer cancel()
	for {
		result := collectAccounts(ctx, force)
		st := state
		st.mu.Lock()
		curGen := st.generation
		isConfigured := st.configured
		hasCache := !st.cachedAt.IsZero() && len(st.last) > 0
		st.mu.Unlock()
		if isConfigured && (result.category == "configuration_changed" || (result.generation != curGen && !hasCache)) {
			if ctx.Err() != nil {
				return result
			}
			continue
		}
		return result
	}
}

// collectManagementSnapshot pairs a management collection with the state
// snapshot that describes it. Reconfigure can legally happen immediately after
// a collection commits; reject that mixed-generation pair and retry once
// rather than returning a new configured state with an old/empty health view.
func collectManagementSnapshot(force bool) (collectionResult, map[string]any) {
	for attempt := 0; attempt < 3; attempt++ {
		result := collectManagement(force)
		body := resourceSnapshot()
		generation, ok := body["generation"].(uint64)
		if ok && generation == result.generation {
			return result, body
		}
	}
	result := collectManagement(force)
	return result, resourceSnapshot()
}

func getAccounts() ([]quotamonitor.AccountQuota, bool, error) {
	result := collectManagement(false)
	return result.accounts, result.stale, result.err
}

func copyResult(r collectionResult) collectionResult {
	r.accounts = cloneAccounts(r.accounts)
	return r
}

func collectAccounts(ctx context.Context, force bool) collectionResult {
	st := state
	st.mu.Lock()
	if !st.configured {
		r := collectionResult{accounts: []quotamonitor.AccountQuota{}, generation: st.generation}
		if force {
			r.err = errors.New("not configured")
			r.category = "not_configured"
		}
		st.mu.Unlock()
		return r
	}
	if flight := st.flight; flight != nil {
		st.mu.Unlock()
		waitCtx, waitCancel := context.WithTimeout(ctx, collectionTimeout+time.Second)
		defer waitCancel()
		select {
		case <-flight.done:
			if ctx.Err() != nil {
				r := collectionResult{generation: flight.generation}
				setCollectionContextError(&r, ctx.Err())
				return r
			}
			// A one-card result must never masquerade as a complete collection.
			if flight.accountKey != "" {
				return collectAccounts(ctx, force)
			}
			// A forced refresh must not silently inherit a passive flight that
			// skipped not-yet-due accounts. Once that flight completes, start a
			// real forced collection (subject to the normal force cooldown).
			if force && !flight.force && ctx.Err() == nil {
				return collectAccounts(ctx, true)
			}
			return copyResult(flight.result)
		case <-waitCtx.Done():
			return collectionResult{stale: true, err: errors.New("collection canceled"), category: "canceled", generation: flight.generation}
		}
	}
	hasCache := !st.cachedAt.IsZero()
	collectedInGen := st.health.Collections > 0
	probeIsDue := false
	for _, a := range st.last {
		if due := st.probeDue[a.AccountKey]; probeEligible(a) && !due.IsZero() && !now().Before(due) {
			probeIsDue = true
			break
		}
	}
	if (!force && hasCache && !probeIsDue && now().Sub(st.cachedAt) < cacheTTL) ||
		(force && collectedInGen && !st.lastForceAt.IsZero() && now().Sub(st.lastForceAt) < forceCooldown) {
		r := st.latest
		r.accounts = cloneAccounts(st.last)
		r.generation = st.generation
		st.mu.Unlock()
		return r
	}
	if force {
		st.lastForceAt = now()
	}
	workCtx, cancel := context.WithTimeout(ctx, collectionTimeout)
	st.runSeq++
	runID := st.runSeq
	startedAt := now()
	flight := &collectionFlight{
		generation:  st.generation,
		runID:       runID,
		hadComplete: st.snapshotComplete,
		force:       force,
		done:        make(chan struct{}),
		cancel:      cancel,
	}
	st.flight = flight
	st.runID = runID
	st.inProgress = true
	st.progress = collectionProgress{StartedAt: startedAt, UpdatedAt: startedAt}
	generation, client, resolver := st.generation, st.client, st.resolver
	previous := cloneAccounts(st.last)
	due := make(map[string]time.Time, len(st.probeDue))
	for k, v := range st.probeDue {
		due[k] = v
	}
	st.mu.Unlock()

	publishBaseline := func(accounts []quotamonitor.AccountQuota, total int) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if generation != st.generation || runID != st.runID || st.flight != flight {
			return
		}
		st.progress.Total = total
		st.progress.UpdatedAt = now()
		if !flight.hadComplete {
			return
		}
		baseline := cloneAccounts(accounts)
		byKey := make(map[string]quotamonitor.AccountQuota, len(baseline))
		for _, account := range baseline {
			byKey[account.AccountKey] = account
		}
		for i := range st.last {
			if account, ok := byKey[st.last[i].AccountKey]; ok {
				// Preserve current-selection inputs until final commit. Otherwise a
				// progressive worker could indirectly move current badges.
				account.LastUsedAt = clonePtr(st.last[i].LastUsedAt)
				if account.Operational != nil && st.last[i].Operational != nil {
					account.Operational.RecentRequestLatest = clonePtr(st.last[i].Operational.RecentRequestLatest)
				}
				st.last[i] = account
			}
		}
	}
	publishAccount := func(account quotamonitor.AccountQuota) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if generation != st.generation || runID != st.runID || st.flight != flight {
			return
		}
		st.progress.Completed++
		st.progress.UpdatedAt = now()
		if !flight.hadComplete {
			return
		}
		for i := range st.last {
			if st.last[i].AccountKey == account.AccountKey {
				st.last[i] = cloneAccounts([]quotamonitor.AccountQuota{account})[0]
				break
			}
		}
	}

	started := time.Now()
	defer cancel()
	result, attempts := runCollection(workCtx, client, resolver, previous, due, force, publishBaseline, publishAccount)
	result.generation = generation
	st.mu.Lock()
	defer st.mu.Unlock()
	if workCtx.Err() != nil {
		setCollectionContextError(&result, workCtx.Err())
	}
	if generation != st.generation || runID != st.runID || st.flight != flight {
		result = collectionResult{accounts: []quotamonitor.AccountQuota{}, stale: true, err: errors.New("configuration changed"), category: "configuration_changed", generation: generation}
	} else {
		st.inProgress = false
		st.progress.UpdatedAt = now()
		st.health.Collections++
		st.health.LastCategory = result.category
		st.health.LastLatencyMS = time.Since(started).Milliseconds()
		st.health.Truncated = result.truncated
		if result.truncated {
			st.health.TruncatedCollections++
		}
		if result.err != nil {
			st.health.Failures++
			if result.category == "timeout" {
				st.health.Timeouts++
			}
			if result.category == "canceled" {
				st.health.Canceled++
			}
			// Attempts are operational facts even when the batch deadline/fetch
			// fails, but deletion/order/current/cachedAt are not finalized.
			if len(attempts) > 0 {
				recordProbeAttempts(st, result.accounts, attempts)
			}
			result.accounts = cloneAccounts(st.last)
			for i := range result.accounts {
				result.accounts[i].Stale = true
				result.accounts[i].Status = quotamonitor.StatusStale
				result.accounts[i].Error = "quota lookup failed"
			}
			result.stale = true
		} else {
			st.health.Successes++
			st.lastSuccessAt = now()
			recordProbeAttempts(st, result.accounts, attempts)
			st.current = currentAccountSelection(result.accounts)
			st.last = cloneAccounts(result.accounts)
			st.order = accountOrder(result.accounts)
			st.cachedAt = now()
			st.snapshotComplete = true
		}
		st.latest = copyResult(result)
	}
	flight.result = copyResult(result)
	if st.flight == flight {
		st.flight = nil
	}
	close(flight.done)
	return copyResult(result)
}

func runCollection(ctx context.Context, client *quotamonitor.Client, resolver *authdir.Resolver, previous []quotamonitor.AccountQuota, due map[string]time.Time, force bool, onBaseline func([]quotamonitor.AccountQuota, int), onAccount func(quotamonitor.AccountQuota)) (collectionResult, map[string]probeSchedule) {
	result := collectionResult{category: "success"}
	if resolver != nil {
		truncated, err := resolver.RefreshContext(ctx)
		result.truncated = truncated
		if err != nil {
			result.category = "auth_directory_unavailable"
		}
	}
	var err error
	fetchCtx, fetchCancel := context.WithTimeout(ctx, fetchTimeout)
	result.accounts, err = client.FetchAuthFiles(fetchCtx)
	fetchContextErr := fetchCtx.Err()
	fetchCancel()
	if err == nil {
		if len(result.accounts) > maxCollectionAccounts {
			result.accounts = result.accounts[:maxCollectionAccounts]
			result.truncated = true
		}
		usageCtx, usageCancel := context.WithTimeout(ctx, fetchTimeout)
		recent, usageErr := client.FetchRecentUsageAuths(usageCtx, 200)
		usageCancel()
		if usageErr != nil {
			result.category = "usage_unavailable"
		}
		var attempts map[string]probeSchedule
		var truncated bool
		result.accounts, attempts, truncated = probeAccountsProgressive(ctx, client, result.accounts, previous, due, force, onBaseline, onAccount)
		// Probe merging can restore old LastUsedAt values from the previous quota
		// snapshot. Only this collection's complete audit read may establish use.
		// If the audit endpoint failed, mark no account as recently used.
		for i := range result.accounts {
			result.accounts[i].LastUsedAt = nil
			if usageErr == nil {
				key := strings.ToLower(strings.TrimSpace(result.accounts[i].Provider)) + "\x00" + strings.TrimSpace(result.accounts[i].AuthIndex)
				if t, ok := recent[key]; ok {
					result.accounts[i].LastUsedAt = clonePtr(&t)
				}
			}
		}
		result.truncated = result.truncated || truncated
		// Account-local passive staleness and active-probe failures are represented
		// on their account DTO only. A successful auth-files/global collection
		// commit remains fresh; only collection-level failures, cancellation,
		// timeout, or cache expiry set the top-level stale flag.
		if ctx.Err() != nil {
			setCollectionContextError(&result, ctx.Err())
		}
		return result, attempts
	}
	result.err = errors.New("quota lookup failed")
	result.stale = true
	result.category = "auth_files_failed"
	if fetchContextErr != nil {
		setCollectionContextError(&result, fetchContextErr)
	}
	if ctx.Err() != nil {
		setCollectionContextError(&result, ctx.Err())
	}
	return result, nil
}

func setCollectionContextError(result *collectionResult, err error) {
	result.stale = true
	result.truncated = true
	result.category = "canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		result.category = "timeout"
	}
	result.err = errors.New("collection " + result.category)
}

func refreshAccounts() (int, error) {
	result := collectAccounts(context.Background(), true)
	return len(result.accounts), result.err
}

func recordProbeAttempts(st *stateData, accounts []quotamonitor.AccountQuota, attempts map[string]probeSchedule) {
	if st.probeDue == nil {
		st.probeDue = make(map[string]time.Time)
	}
	if st.schedules == nil {
		st.schedules = make(map[string]probeSchedule)
	}
	current := currentAccountKeys(accounts)
	recorded := make(map[string]bool)
	for _, a := range accounts {
		if recorded[a.AccountKey] {
			continue
		}
		recorded[a.AccountKey] = true
		attempt, ok := attempts[a.AccountKey]
		if !ok {
			continue
		}
		attempt.NextDue = nextAccountProbeDue(a, current[strings.ToLower(strings.TrimSpace(a.Provider))] == a.AccountKey, attempt.LastAttempt)
		if attempt.LastSuccess.IsZero() {
			st.health.ProbeFailures++
			attempt.LastSuccess = st.schedules[a.AccountKey].LastSuccess
		} else {
			st.health.ProbeSuccesses++
		}
		st.schedules[a.AccountKey] = attempt
		st.probeDue[a.AccountKey] = attempt.NextDue
		st.lastProbeAt = attempt.LastAttempt
		st.health.ProbeAttempts++
	}
	updateAccountProbeDue(st, accounts)
}

// Cleanup only: passive reads must never move an account's next due time.
func updateAccountProbeDue(st *stateData, accounts []quotamonitor.AccountQuota) {
	live := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		live[a.AccountKey] = probeEligible(a)
	}
	for k := range st.probeDue {
		if !live[k] {
			delete(st.probeDue, k)
			delete(st.schedules, k)
		}
	}
	for len(st.probeDue) > maxProbeMapEntries {
		var oldest string
		var at time.Time
		for k, due := range st.probeDue {
			if oldest == "" || due.Before(at) {
				oldest, at = k, due
			}
		}
		delete(st.probeDue, oldest)
		delete(st.schedules, oldest)
	}
}

func probeEligible(a quotamonitor.AccountQuota) bool {
	return a.AccountKey != "" && a.AuthIndex != "" &&
		(strings.EqualFold(a.Provider, "codex") || strings.EqualFold(a.Provider, "antigravity") || strings.EqualFold(a.Provider, "claude")) &&
		!(a.Operational != nil && a.Operational.Disabled != nil && *a.Operational.Disabled)
}

func probeActivePerAccount(client *quotamonitor.Client, accounts, previous []quotamonitor.AccountQuota, due map[string]time.Time, force bool) []quotamonitor.AccountQuota {
	ctx, cancel := context.WithTimeout(context.Background(), collectionTimeout)
	defer cancel()
	out, _, _ := probeAccounts(ctx, client, accounts, previous, due, force)
	return out
}

func probeAccounts(ctx context.Context, client *quotamonitor.Client, accounts, previous []quotamonitor.AccountQuota, due map[string]time.Time, force bool) ([]quotamonitor.AccountQuota, map[string]probeSchedule, bool) {
	return probeAccountsProgressive(ctx, client, accounts, previous, due, force, nil, nil)
}

func probeAccountsProgressive(ctx context.Context, client *quotamonitor.Client, accounts, previous []quotamonitor.AccountQuota, due map[string]time.Time, force bool, onBaseline func([]quotamonitor.AccountQuota, int), onAccount func(quotamonitor.AccountQuota)) ([]quotamonitor.AccountQuota, map[string]probeSchedule, bool) {
	truncated := len(accounts) > maxCollectionAccounts
	if truncated {
		accounts = accounts[:maxCollectionAccounts]
	}
	accounts = applyLastGoodWindows(cloneAccounts(accounts), previous)
	attempts := make(map[string]probeSchedule)
	indices := make([]int, 0, maxProbeJobs)
	seen := make(map[string]bool)
	for i, a := range accounts {
		if !probeEligible(a) || seen[a.AccountKey] || (!force && now().Before(due[a.AccountKey])) {
			continue
		}
		seen[a.AccountKey] = true
		if len(indices) == maxProbeJobs {
			truncated = true
			continue
		}
		indices = append(indices, i)
	}
	if onBaseline != nil {
		onBaseline(cloneAccounts(accounts), len(indices))
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex // attempts and shared optional-detail budget only
	detailJobs := 0
	for worker := 0; worker < collectionWorkers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case i, ok := <-jobs:
					if !ok || ctx.Err() != nil {
						return
					}
					a := &accounts[i] // unique index per job; no shared account writes
					attempt := probeSchedule{LastAttempt: now()}
					probeCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
					var wins []quotamonitor.QuotaWindow
					var err error
					if strings.EqualFold(a.Provider, "codex") {
						var plan string
						var api *quotamonitor.APICredits
						var reset *quotamonitor.ResetCredits
						wins, plan, api, reset, err = client.FetchCodexUsage(probeCtx, *a)
						if plan != "" {
							a.PlanType = plan
						}
						if api != nil {
							a.APICredits = mergeAPICredits(a.APICredits, api)
						}
						if reset != nil {
							a.ResetCredits = mergeResetCredits(a.ResetCredits, reset)
						}
					} else if strings.EqualFold(a.Provider, "claude") {
						var reset *quotamonitor.ResetCredits
						wins, reset, err = client.FetchClaudeUsage(probeCtx, *a)
						if err == nil {
							// Claude inventory is a complete snapshot, not partial WHAM
							// enrichment. Unknown inventory must not reconfirm old rights;
							// failed/skipped probes still retain last-good data as stale.
							a.ResetCredits = mergeResetCredits(nil, reset)
						}
					} else {
						wins, err = client.FetchActiveQuota(probeCtx, *a)
					}
					cancel()
					if err == nil && len(wins) > 0 {
						attempt.LastSuccess = now()
						anchorWindowResets(wins, attempt.LastSuccess)
						a.Windows = mergeWindows(a.Windows, wins)
						a.Source, a.Status = "active", quotamonitor.StatusKnown
						a.LastSuccessAt = clonePtr(&attempt.LastSuccess)
						a.ObservedAt = clonePtr(&attempt.LastSuccess)
						a.FetchedAt = attempt.LastSuccess
						a.Stale, a.Error, a.ErrorKind, a.RetryAfter = false, "", "", nil
						if a.Operational != nil {
							hasAvailableQuota := false
							isExhausted := false
							for _, w := range a.Windows {
								if strings.EqualFold(a.Provider, "claude") && isClaudeBillingWindow(w) {
									continue
								}
								if w.RemainingPercent != nil && *w.RemainingPercent > 0 {
									hasAvailableQuota = true
								}
								if (w.LimitReached != nil && *w.LimitReached) ||
									(w.Allowed != nil && !*w.Allowed) ||
									(w.UsedPercent != nil && *w.UsedPercent >= 100) ||
									(w.RemainingPercent != nil && *w.RemainingPercent <= 0) {
									isExhausted = true
								}
							}
							if hasAvailableQuota && !isExhausted {
								f := false
								a.Operational.Unavailable = &f
								a.Operational.Status = "active"
								a.Operational.NextRetryAfter = nil
							}
						}
					} else {
						a.ErrorKind = "active"
						var ae *quotamonitor.APICallError
						if errors.As(err, &ae) && ae.Kind == "auth" {
							a.ErrorKind = "auth"
						}
						// Account-local failure: keep last-good values and mark only this
						// account stale/error. No global or sibling stale propagation.
						if len(a.Windows) > 0 || a.LastSuccessAt != nil {
							a.Status, a.Stale, a.Error = quotamonitor.StatusStale, true, "quota lookup failed"
						} else {
							a.Status, a.Error = quotamonitor.StatusError, "quota lookup failed"
						}
					}
					mu.Lock()
					attempts[a.AccountKey] = attempt
					detail := !attempt.LastSuccess.IsZero() && ctx.Err() == nil && strings.EqualFold(a.Provider, "codex") && a.ResetCredits != nil && a.ResetCredits.AvailableCount != nil && *a.ResetCredits.AvailableCount > 0
					if detail {
						if detailJobs < maxResetDetailJobs {
							detailJobs++
						} else {
							detail = false
							truncated = true
						}
					}
					mu.Unlock()
					if detail {
						detailCtx, detailCancel := context.WithTimeout(ctx, fetchTimeout)
						expiry, detailErr := client.FetchCodexResetCreditsExpiry(detailCtx, *a)
						detailCancel()
						if detailErr == nil && expiry != nil {
							a.ResetCredits.NearestExpiry = expiry
						}
					}
					if onAccount != nil {
						onAccount(*a)
					}
				}
			}
		}()
	}
enqueue:
	for _, i := range indices {
		select {
		case <-ctx.Done():
			break enqueue
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		truncated = true
	}
	return accounts, attempts, truncated
}

func probeActive(client *quotamonitor.Client, accounts []quotamonitor.AccountQuota, previous []quotamonitor.AccountQuota) []quotamonitor.AccountQuota {
	// probeActive is retained for backward compatibility with existing tests that
	// call it directly. It delegates to probeActivePerAccount with force=true
	// (all eligible accounts are probed regardless of per-account due time).
	return probeActivePerAccount(client, accounts, previous, nil, true)
}

// applyLastGoodWindows merges passive observations without rolling back a newer
// confirmed active observation. Auth-file identity, disabled state and usage
// metadata remain fresh; quota values retain their original observation time.
func applyLastGoodWindows(accounts, previous []quotamonitor.AccountQuota) []quotamonitor.AccountQuota {
	retained := make(map[string]quotamonitor.AccountQuota, len(previous))
	for _, p := range cloneAccounts(previous) {
		if p.AccountKey != "" {
			retained[p.AccountKey] = p
		}
	}
	for i := range accounts {
		a := &accounts[i]
		if p, ok := retained[a.AccountKey]; ok {
			confirmed := (p.Source == "active" || p.Source == "both") && p.LastSuccessAt != nil
			retainActive := confirmed && (a.ObservedAt == nil || !a.ObservedAt.After(*p.LastSuccessAt) || a.Stale)
			anchorAccountResets(&p)
			anchorAccountResets(a)
			if retainActive {
				// Fill missing fields, but never replace a confirmed value with an
				// older passive signal merely because auth-files was fetched later.
				a.Windows = mergeWindowFields(a.Windows, p.Windows)
				a.FetchedAt = p.FetchedAt
				if p.PlanType != "" {
					a.PlanType = p.PlanType
				}
				a.APICredits = mergeAPICredits(a.APICredits, p.APICredits)
				a.ResetCredits = mergeResetCredits(a.ResetCredits, p.ResetCredits)
				if p.Credits != nil {
					a.Credits = cloneStringMap(p.Credits)
				}
				// Only undo the host's old quota-availability state. New disabled
				// or error/auth state and operational usage counters are authoritative.
				if op, old := a.Operational, p.Operational; op != nil && old != nil &&
					old.Unavailable != nil && !*old.Unavailable && old.Status == "active" &&
					(op.Disabled == nil || !*op.Disabled) &&
					(op.LastRefresh == nil || !op.LastRefresh.After(*p.LastSuccessAt)) {
					switch strings.ToLower(strings.TrimSpace(op.Status)) {
					case "", "active", "unavailable", "limited":
						op.Unavailable = clonePtr(old.Unavailable)
						op.Status = old.Status
						op.NextRetryAfter = clonePtr(old.NextRetryAfter)
					}
				}
			} else {
				a.Windows = mergeWindowFields(p.Windows, a.Windows)
			}
			if len(a.Windows) == 0 || retainActive || (!confirmed && (p.Source == "active" || p.Source == "both")) || p.Stale || p.ErrorKind != "" {
				a.Status, a.Stale, a.Error, a.ErrorKind = p.Status, p.Stale, p.Error, p.ErrorKind
				a.Source, a.LastSuccessAt, a.ObservedAt = p.Source, p.LastSuccessAt, p.ObservedAt
				a.RetryAfter, a.ActiveLimit = p.RetryAfter, p.ActiveLimit
			} else {
				if a.ObservedAt == nil {
					a.ObservedAt = clonePtr(p.ObservedAt)
				}
				if a.LastSuccessAt == nil {
					a.LastSuccessAt = clonePtr(p.LastSuccessAt)
				}
				if a.RetryAfter == nil {
					a.RetryAfter = clonePtr(p.RetryAfter)
				}
				if a.ActiveLimit == "" {
					a.ActiveLimit = p.ActiveLimit
				}
			}
			if a.PlanType == "" && p.PlanType != "" {
				a.PlanType = p.PlanType
			}
			a.APICredits = mergeAPICredits(p.APICredits, a.APICredits)
			a.ResetCredits = mergeResetCredits(p.ResetCredits, a.ResetCredits)
			if a.Credits == nil && p.Credits != nil {
				a.Credits = cloneStringMap(p.Credits)
			}
			if a.LastUsedAt == nil && p.LastUsedAt != nil {
				a.LastUsedAt = clonePtr(p.LastUsedAt)
			}
		}
	}
	return accounts
}

// Materialize relative deadlines before merging observations from different times.
func anchorWindowResets(windows []quotamonitor.QuotaWindow, observed time.Time) {
	if observed.IsZero() {
		return
	}
	for i := range windows {
		w := &windows[i]
		if w.ResetAt == nil && w.ResetAfterSec != nil {
			if reset, ok := quotamonitor.ResetAtFromObservedAt(observed, *w.ResetAfterSec); ok {
				w.ResetAt = reset
			}
		}
	}
}

func anchorAccountResets(a *quotamonitor.AccountQuota) {
	observed := a.FetchedAt
	if a.ObservedAt != nil {
		observed = *a.ObservedAt
	} else if a.LastSuccessAt != nil {
		observed = *a.LastSuccessAt
	}
	anchorWindowResets(a.Windows, observed)
}

func mergeAPICredits(previous, incoming *quotamonitor.APICredits) *quotamonitor.APICredits {
	if incoming == nil {
		if previous == nil {
			return nil
		}
		return &quotamonitor.APICredits{
			HasCredits: clonePtr(previous.HasCredits),
			Unlimited:  clonePtr(previous.Unlimited),
			Balance:    clonePtr(previous.Balance),
		}
	}
	merged := &quotamonitor.APICredits{
		HasCredits: clonePtr(incoming.HasCredits),
		Unlimited:  clonePtr(incoming.Unlimited),
		Balance:    clonePtr(incoming.Balance),
	}
	if previous != nil {
		if merged.HasCredits == nil {
			merged.HasCredits = clonePtr(previous.HasCredits)
		}
		if merged.Unlimited == nil {
			merged.Unlimited = clonePtr(previous.Unlimited)
		}
		if merged.Balance == nil {
			merged.Balance = clonePtr(previous.Balance)
		}
	}
	return merged
}

func mergeResetCredits(previous, incoming *quotamonitor.ResetCredits) *quotamonitor.ResetCredits {
	if incoming == nil {
		if previous == nil {
			return nil
		}
		return &quotamonitor.ResetCredits{
			AvailableCount:           clonePtr(previous.AvailableCount),
			ApplicableAvailableCount: clonePtr(previous.ApplicableAvailableCount),
			NearestExpiry:            clonePtr(previous.NearestExpiry),
		}
	}
	merged := &quotamonitor.ResetCredits{
		AvailableCount:           clonePtr(incoming.AvailableCount),
		ApplicableAvailableCount: clonePtr(incoming.ApplicableAvailableCount),
		NearestExpiry:            clonePtr(incoming.NearestExpiry),
	}
	if previous != nil {
		if merged.AvailableCount == nil {
			merged.AvailableCount = clonePtr(previous.AvailableCount)
		}
		if merged.ApplicableAvailableCount == nil {
			merged.ApplicableAvailableCount = clonePtr(previous.ApplicableAvailableCount)
		}
		if merged.NearestExpiry == nil {
			merged.NearestExpiry = clonePtr(previous.NearestExpiry)
		}
	}
	return merged
}

// normalizeWindowText makes provider labels comparable without allowing
// punctuation, whitespace, or control characters to create different keys.
func normalizeWindowText(s string) string {
	var b strings.Builder
	separator := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsControl(r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if separator && b.Len() > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
			separator = false
			continue
		}
		separator = b.Len() > 0
	}
	return strings.Trim(b.String(), "_")
}

func canonicalWindowKey(w quotamonitor.QuotaWindow) string {
	name := normalizeWindowText(w.Name)
	// Claude's passive unified headers use seven-day aliases while its OAuth
	// usage response exposes the same windows as weekly. Merge recovery into
	// the existing window instead of retaining a stale exhausted duplicate.
	if name == "seven_day" || strings.HasPrefix(name, "seven_day_") {
		name = "weekly" + strings.TrimPrefix(name, "seven_day")
	}
	switch name {
	case "5h", "5_hour", "5_hours", "five_hours":
		name = "five_hour"
	case "7d":
		name = "weekly"
	}
	minutes := 0
	if w.WindowMinutes != nil {
		minutes = *w.WindowMinutes
	}

	// Codex uses several labels for the same two logical windows. Do this
	// independently of provider metadata because mergeWindows has no provider.
	if name == "primary" || strings.Contains(name, "primary_window") || strings.Contains(name, "primary_limit") {
		return "codex:primary"
	}
	if name == "secondary" || strings.Contains(name, "secondary_window") || strings.Contains(name, "secondary_limit") {
		return "codex:secondary"
	}

	bucket := ""
	base := name
	for _, suffix := range []string{"five_hour_limit_remaining", "five_hour", "five_h", "5h", "weekly_limit_remaining", "weekly", "week"} {
		if strings.HasSuffix(name, suffix) && (len(name) == len(suffix) || name[len(name)-len(suffix)-1] == '_') {
			bucket = "5h"
			if strings.HasPrefix(suffix, "weekly") || suffix == "week" {
				bucket = "weekly"
			}
			base = strings.Trim(name[:len(name)-len(suffix)], "_")
			break
		}
	}
	if bucket == "" && minutes == 300 {
		bucket = "5h"
	}
	if bucket == "" && minutes == 10080 {
		bucket = "weekly"
	}
	if bucket != "" {
		if base == "" {
			return fmt.Sprintf("unknown:%s:%d", name, minutes)
		}
		return "ag:" + base + ":" + bucket
	}

	kind := normalizeWindowText(w.LimitName)
	return fmt.Sprintf("unknown:%s:%d:%s", name, minutes, kind)
}

func canonicalWindows(in []quotamonitor.QuotaWindow) []quotamonitor.QuotaWindow {
	out := make([]quotamonitor.QuotaWindow, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, w := range in {
		key := canonicalWindowKey(w)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, w)
	}
	return out
}

func mergeWindowFields(previous, fresh []quotamonitor.QuotaWindow) []quotamonitor.QuotaWindow {
	out := canonicalWindows(previous)
	index := make(map[string]int, len(out))
	for i := range out {
		index[canonicalWindowKey(out[i])] = i
	}
	for _, incoming := range canonicalWindows(fresh) {
		key := canonicalWindowKey(incoming)
		if i, ok := index[key]; ok {
			base := out[i]
			if incoming.Name != "" {
				base.Name = incoming.Name
			}
			if incoming.WindowMinutes != nil {
				base.WindowMinutes = clonePtr(incoming.WindowMinutes)
			}
			if incoming.UsedPercent != nil {
				base.UsedPercent = clonePtr(incoming.UsedPercent)
			}
			if incoming.RemainingPercent != nil {
				base.RemainingPercent = clonePtr(incoming.RemainingPercent)
			}
			if incoming.LimitReached != nil {
				base.LimitReached = clonePtr(incoming.LimitReached)
			}
			if incoming.Allowed != nil {
				base.Allowed = clonePtr(incoming.Allowed)
			}
			if incoming.LimitName != "" {
				base.LimitName = incoming.LimitName
			}
			if incoming.ResetAfterSec != nil {
				base.ResetAfterSec = clonePtr(incoming.ResetAfterSec)
			}
			if incoming.ResetAt != nil {
				base.ResetAt = clonePtr(incoming.ResetAt)
			}
			if incoming.OverLimitPercent != nil {
				base.OverLimitPercent = clonePtr(incoming.OverLimitPercent)
			}
			out[i] = base
			continue
		}
		index[key] = len(out)
		out = append(out, incoming)
	}
	return out
}

func mergeWindows(passive, active []quotamonitor.QuotaWindow) []quotamonitor.QuotaWindow {
	// Active values win per field while missing active fields retain passive
	// values. Fresh active ordering is preserved, followed by passive-only rows.
	active = append([]quotamonitor.QuotaWindow(nil), active...)
	for i := range active {
		w := &active[i]
		// A percentage is authoritative for this window only. Explicit provider
		// flags win, including contradictory flags; a reset deadline alone never
		// proves recovery. Unobserved sibling windows remain unchanged.
		observed := false
		exhausted := false
		if w.UsedPercent != nil && *w.UsedPercent >= 0 && *w.UsedPercent <= 100 {
			observed, exhausted = true, *w.UsedPercent >= 100
			if w.RemainingPercent == nil {
				remaining := 100 - *w.UsedPercent
				w.RemainingPercent = &remaining
			}
		}
		if w.RemainingPercent != nil && *w.RemainingPercent >= 0 && *w.RemainingPercent <= 100 {
			observed = true
			exhausted = exhausted || *w.RemainingPercent <= 0
			if w.UsedPercent == nil {
				used := 100 - *w.RemainingPercent
				w.UsedPercent = &used
			}
		}
		contradictsRecovery := (w.LimitReached != nil && *w.LimitReached) || (w.Allowed != nil && !*w.Allowed)
		if observed && (exhausted || !contradictsRecovery) {
			if w.LimitReached == nil {
				w.LimitReached = clonePtr(&exhausted)
			}
			if w.Allowed == nil {
				allowed := !exhausted
				w.Allowed = &allowed
			}
			if !exhausted && w.OverLimitPercent == nil {
				zero := 0.0
				w.OverLimitPercent = &zero
			}
		}
	}
	merged := mergeWindowFields(passive, active)
	activeKeys := make(map[string]struct{}, len(active))
	out := make([]quotamonitor.QuotaWindow, 0, len(merged))
	byKey := make(map[string]quotamonitor.QuotaWindow, len(merged))
	for _, w := range merged {
		byKey[canonicalWindowKey(w)] = w
	}
	for _, w := range canonicalWindows(active) {
		key := canonicalWindowKey(w)
		activeKeys[key] = struct{}{}
		out = append(out, byKey[key])
	}
	for _, w := range canonicalWindows(passive) {
		key := canonicalWindowKey(w)
		if _, ok := activeKeys[key]; !ok {
			out = append(out, byKey[key])
		}
	}
	return out
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Struct assignment also preserves quota package's private immutable identity
// strings. JSON roundtrips silently dropped those fields and LastUsedAt.
func cloneAccounts(in []quotamonitor.AccountQuota) []quotamonitor.AccountQuota {
	out := make([]quotamonitor.AccountQuota, len(in))
	for i, a := range in {
		a.ObservedAt, a.LastSuccessAt, a.LastUsedAt = clonePtr(a.ObservedAt), clonePtr(a.LastSuccessAt), clonePtr(a.LastUsedAt)
		a.RetryAfter = clonePtr(a.RetryAfter)
		a.Credits, a.RawSignals = cloneStringMap(a.Credits), cloneStringMap(a.RawSignals)
		if a.Windows != nil {
			a.Windows = append([]quotamonitor.QuotaWindow{}, a.Windows...)
			for j := range a.Windows {
				w := &a.Windows[j]
				w.WindowMinutes = clonePtr(w.WindowMinutes)
				w.UsedPercent, w.RemainingPercent, w.OverLimitPercent = clonePtr(w.UsedPercent), clonePtr(w.RemainingPercent), clonePtr(w.OverLimitPercent)
				w.LimitReached, w.Allowed = clonePtr(w.LimitReached), clonePtr(w.Allowed)
				w.ResetAfterSec, w.ResetAt = clonePtr(w.ResetAfterSec), clonePtr(w.ResetAt)
			}
		}
		a.Operational = clonePtr(a.Operational)
		if o := a.Operational; o != nil {
			o.Disabled, o.Unavailable = clonePtr(o.Disabled), clonePtr(o.Unavailable)
			o.Success, o.Failed = clonePtr(o.Success), clonePtr(o.Failed)
			o.RecentRequestLatest, o.LastRefresh, o.NextRetryAfter = clonePtr(o.RecentRequestLatest), clonePtr(o.LastRefresh), clonePtr(o.NextRetryAfter)
		}
		a.APICredits = clonePtr(a.APICredits)
		if c := a.APICredits; c != nil {
			c.HasCredits, c.Unlimited, c.Balance = clonePtr(c.HasCredits), clonePtr(c.Unlimited), clonePtr(c.Balance)
		}
		a.ResetCredits = clonePtr(a.ResetCredits)
		if c := a.ResetCredits; c != nil {
			c.AvailableCount, c.ApplicableAvailableCount, c.NearestExpiry = clonePtr(c.AvailableCount), clonePtr(c.ApplicableAvailableCount), clonePtr(c.NearestExpiry)
		}
		out[i] = a
	}
	return out
}

func marshalJSON(v any) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := strings.TrimSuffix(buf.String(), "\n")
	return []byte(out), nil
}
func addNoStore(raw []byte) []byte {
	var e envelope
	if json.Unmarshal(raw, &e) == nil {
		var r managementResponse
		if json.Unmarshal(e.Result, &r) == nil {
			if r.Headers == nil {
				r.Headers = map[string][]string{}
			}
			r.Headers["Cache-Control"] = []string{"no-store"}
			e.Result, _ = marshalJSON(r)
			out, _ := json.Marshal(e)
			return out
		}
	}
	return raw
}
func mgmtResponse(status int, body any) []byte {
	b, err := marshalJSON(body)
	if err != nil {
		b = []byte(`{"error":"response encoding failed"}`)
	}
	return OKEnvelope(managementResponse{StatusCode: status, Headers: map[string][]string{"Content-Type": {"application/json"}, "X-Content-Type-Options": {"nosniff"}}, Body: b})
}

// dashboardSecurityHeaders hardens the self-contained page. The page is a single
// HTML document with inline script/style, so script-src must allow 'unsafe-inline';
// everything else is locked down: no external origins, no framing, no sniffing,
// no referrer, no base-tag or form-target hijacking.
var dashboardSecurityHeaders = map[string][]string{
	"Content-Security-Policy": {"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"},
	"X-Content-Type-Options":  {"nosniff"},
	"Referrer-Policy":         {"no-referrer"},
	"X-Frame-Options":         {"DENY"},
}

func dashboardPageHeaders() map[string][]string {
	h := map[string][]string{"Content-Type": {"text/html; charset=utf-8"}, "Cache-Control": {"no-store"}}
	for k, v := range dashboardSecurityHeaders {
		h[k] = v
	}
	return h
}

func fontResponse(font []byte) []byte {
	return OKEnvelope(managementResponse{StatusCode: 200, Headers: map[string][]string{"Content-Type": {"font/woff2"}, "Cache-Control": {"public, max-age=31536000, immutable"}, "X-Content-Type-Options": {"nosniff"}}, Body: font})
}
