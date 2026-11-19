package availability

import (
	"math"
	"regexp"
	"strings"

	quotamonitor "quota-monitor/internal/quota"
)

// Entry is the fleet-wide availability of one provider family:
// the mean remaining percentage over every account that reports it.
// It is computed once here so the dashboard header and external status
// surfaces (e.g. the Herdr tab bar) always show the same number.
type Entry struct {
	Key              string  `json:"key"`
	Label            string  `json:"label"`
	Short            string  `json:"short"`
	RemainingPercent float64 `json:"remaining_percent"`
	Accounts         int     `json:"accounts"`
}

const (
	windowFiveHourMinutes = 300
	windowWeeklyMinutes   = 10080
)

var (
	antigravityGeminiPattern = regexp.MustCompile(`(?i)^Gemini Models\s*/`)
	antigravityClaudePattern = regexp.MustCompile(`(?i)^Claude and GPT models\s*/`)
)

func windowMinutes(w quotamonitor.QuotaWindow) int {
	if w.WindowMinutes == nil {
		return 0
	}
	return *w.WindowMinutes
}

func clampPercentValue(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Max(0, math.Min(100, v))
}

// windowRemaining returns the reported remaining percent. A reached or denied
// limit is 0; absent/invalid percentages do not establish availability.
func windowRemaining(w quotamonitor.QuotaWindow) (float64, bool) {
	if (w.LimitReached != nil && *w.LimitReached) || (w.Allowed != nil && !*w.Allowed) {
		return 0, true
	}
	if w.RemainingPercent != nil && !math.IsNaN(*w.RemainingPercent) && !math.IsInf(*w.RemainingPercent, 0) {
		return clampPercentValue(*w.RemainingPercent), true
	}
	if w.UsedPercent != nil && !math.IsNaN(*w.UsedPercent) && !math.IsInf(*w.UsedPercent, 0) {
		return 100 - clampPercentValue(*w.UsedPercent), true
	}
	return 0, false
}

// accountCountsTowardAvailability keeps exhausted-but-unavailable accounts in the
// denominator as 0% instead of silently dropping them; disabled accounts never count.
func accountCountsTowardAvailability(a quotamonitor.AccountQuota, windows []quotamonitor.QuotaWindow) bool {
	op := a.Operational
	if op != nil && ((op.Disabled != nil && *op.Disabled) || strings.EqualFold(strings.TrimSpace(op.Status), "disabled")) {
		return false
	}
	if (op != nil && op.Unavailable != nil && *op.Unavailable) || a.Status == quotamonitor.StatusError {
		for _, w := range windows {
			if remaining, known := windowRemaining(w); known && remaining == 0 {
				return true
			}
		}
		return false
	}
	return true
}

func findWindow(windows []quotamonitor.QuotaWindow, minutes int, names ...string) *quotamonitor.QuotaWindow {
	// Exact names win: a model-scoped weekly window (weekly-sonnet) shares the
	// weekly duration but has its own quota and must not stand in for the
	// account-wide window.
	for i := range windows {
		if _, known := windowRemaining(windows[i]); !known {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(windows[i].Name))
		for _, n := range names {
			if name == n {
				return &windows[i]
			}
		}
	}
	for i := range windows {
		if _, known := windowRemaining(windows[i]); known && windowMinutes(windows[i]) == minutes && !modelScopedWindow(windows[i]) {
			return &windows[i]
		}
	}
	return nil
}

// modelScopedWindow reports whether a window meters one model (or app surface)
// rather than the whole account, e.g. Claude's weekly-sonnet / weekly-opus.
func modelScopedWindow(w quotamonitor.QuotaWindow) bool {
	name := strings.ToLower(strings.TrimSpace(w.Name))
	for _, suffix := range []string{"opus", "sonnet", "oauth-apps", "oauth_apps", "cowork", "sessions"} {
		if strings.Contains(name, suffix) {
			return true
		}
	}
	return false
}

func claudeAvailability(a quotamonitor.AccountQuota) (float64, bool) {
	primary := findWindow(a.Windows, windowFiveHourMinutes, "five-hour", "5h")
	return primarySecondaryAvailability(a, primary, findWindow(a.Windows, windowWeeklyMinutes, "weekly", "7d"))
}

func codexAvailability(a quotamonitor.AccountQuota) (float64, bool) {
	windows := quotamonitor.DistinctCodexWindows(a.Windows)
	var primary, secondary *quotamonitor.QuotaWindow
	for i := range windows {
		w := &windows[i]
		if !quotamonitor.IsCodexAccountWindow(*w) {
			continue
		}
		switch quotamonitor.CodexWindowKind(*w) {
		case quotamonitor.CodexFiveHour:
			primary = w
		case quotamonitor.CodexWeekly:
			secondary = w
		}
	}
	return primarySecondaryAvailability(a, primary, secondary)
}

// The measured 5h window decides when available; a depleted weekly window
// always blocks the account. Without measured 5h, use the known weekly value.
func primarySecondaryAvailability(a quotamonitor.AccountQuota, primary, secondary *quotamonitor.QuotaWindow) (float64, bool) {
	var considered []quotamonitor.QuotaWindow
	for _, w := range []*quotamonitor.QuotaWindow{primary, secondary} {
		if w != nil {
			considered = append(considered, *w)
		}
	}
	if !accountCountsTowardAvailability(a, considered) {
		return 0, false
	}
	var weekly float64
	var weeklyKnown bool
	if secondary != nil {
		weekly, weeklyKnown = windowRemaining(*secondary)
		if weeklyKnown && weekly == 0 {
			return 0, true
		}
	}
	if primary != nil {
		if short, known := windowRemaining(*primary); known {
			return short, true
		}
	}
	return weekly, weeklyKnown
}

func antigravityAvailability(a quotamonitor.AccountQuota, family *regexp.Regexp) (float64, bool) {
	var short, weekly *quotamonitor.QuotaWindow
	minShort, minWeekly := 101.0, 101.0
	for i := range a.Windows {
		w := &a.Windows[i]
		if !family.MatchString(w.Name) {
			continue
		}
		remaining, known := windowRemaining(*w)
		if !known {
			continue
		}
		switch windowMinutes(*w) {
		case windowFiveHourMinutes:
			if remaining < minShort {
				short, minShort = w, remaining
			}
		case windowWeeklyMinutes:
			if remaining < minWeekly {
				weekly, minWeekly = w, remaining
			}
		}
	}
	return primarySecondaryAvailability(a, short, weekly)
}

func roundTenth(v float64) float64 { return math.Round(v*10) / 10 }

// Compute returns one entry per provider family that has at least
// one contributing account, in a stable display order.
func Compute(accounts []quotamonitor.AccountQuota) []Entry {
	type family struct {
		key, label, short string
		value             func(quotamonitor.AccountQuota) (float64, bool)
	}
	provider := func(name string, fn func(quotamonitor.AccountQuota) (float64, bool)) func(quotamonitor.AccountQuota) (float64, bool) {
		return func(a quotamonitor.AccountQuota) (float64, bool) {
			if strings.ToLower(strings.TrimSpace(a.Provider)) != name {
				return 0, false
			}
			return fn(a)
		}
	}
	families := []family{
		{"claude", "Claude", "CC", provider("claude", claudeAvailability)},
		{"codex", "GPT", "GPT", provider("codex", codexAvailability)},
		{"antigravity_gemini", "안티그래비티 · Gemini", "AG", provider("antigravity", func(a quotamonitor.AccountQuota) (float64, bool) {
			return antigravityAvailability(a, antigravityGeminiPattern)
		})},
		{"antigravity_claude", "안티그래비티 · Claude/GPT", "AG", provider("antigravity", func(a quotamonitor.AccountQuota) (float64, bool) {
			return antigravityAvailability(a, antigravityClaudePattern)
		})},
	}
	entries := make([]Entry, 0, len(families))
	for _, f := range families {
		sum, n := 0.0, 0
		for _, a := range accounts {
			if v, ok := f.value(a); ok {
				sum += v
				n++
			}
		}
		if n > 0 {
			entries = append(entries, Entry{Key: f.key, Label: f.label, Short: f.short, RemainingPercent: roundTenth(sum / float64(n)), Accounts: n})
		}
	}
	return entries
}
