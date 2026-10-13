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

// windowRemaining returns the remaining percent of a window. A reached or
// denied limit is 0; a window with no numbers is treated as fully available.
func windowRemaining(w quotamonitor.QuotaWindow) float64 {
	if (w.LimitReached != nil && *w.LimitReached) || (w.Allowed != nil && !*w.Allowed) {
		return 0
	}
	if w.RemainingPercent != nil && !math.IsNaN(*w.RemainingPercent) && !math.IsInf(*w.RemainingPercent, 0) {
		return clampPercentValue(*w.RemainingPercent)
	}
	if w.UsedPercent != nil && !math.IsNaN(*w.UsedPercent) && !math.IsInf(*w.UsedPercent, 0) {
		return 100 - clampPercentValue(*w.UsedPercent)
	}
	return 100
}

// accountCountsTowardAvailability keeps exhausted-but-unavailable accounts in the
// denominator as 0% instead of silently dropping them; disabled accounts never count.
func accountCountsTowardAvailability(a quotamonitor.AccountQuota, windows []quotamonitor.QuotaWindow) bool {
	op := a.Operational
	if op != nil && op.Disabled != nil && *op.Disabled {
		return false
	}
	if (op != nil && op.Unavailable != nil && *op.Unavailable) || a.Status == quotamonitor.StatusError {
		for _, w := range windows {
			if windowRemaining(w) == 0 {
				return true
			}
		}
		return false
	}
	return true
}

func findWindow(windows []quotamonitor.QuotaWindow, minutes int, names ...string) *quotamonitor.QuotaWindow {
	for i := range windows {
		if windowMinutes(windows[i]) == minutes {
			return &windows[i]
		}
	}
	for i := range windows {
		name := strings.ToLower(strings.TrimSpace(windows[i].Name))
		for _, n := range names {
			if name == n {
				return &windows[i]
			}
		}
	}
	return nil
}

// primarySecondaryAvailability serves Claude and Codex: the 5h window decides,
// but a depleted weekly window blocks the account regardless of 5h.
func primarySecondaryAvailability(a quotamonitor.AccountQuota, primaryNames, secondaryNames []string) (float64, bool) {
	primary := findWindow(a.Windows, windowFiveHourMinutes, primaryNames...)
	if primary == nil {
		return 0, false
	}
	secondary := findWindow(a.Windows, windowWeeklyMinutes, secondaryNames...)
	considered := []quotamonitor.QuotaWindow{*primary}
	if secondary != nil {
		considered = append(considered, *secondary)
	}
	if !accountCountsTowardAvailability(a, considered) {
		return 0, false
	}
	if secondary != nil && windowRemaining(*secondary) <= 0 {
		return 0, true
	}
	return windowRemaining(*primary), true
}

func antigravityAvailability(a quotamonitor.AccountQuota, family *regexp.Regexp) (float64, bool) {
	var familyWindows []quotamonitor.QuotaWindow
	for _, w := range a.Windows {
		if family.MatchString(w.Name) {
			familyWindows = append(familyWindows, w)
		}
	}
	lowest, found := 100.0, false
	for _, w := range familyWindows {
		if windowMinutes(w) == windowFiveHourMinutes {
			found = true
			lowest = math.Min(lowest, windowRemaining(w))
		}
	}
	if !found || !accountCountsTowardAvailability(a, familyWindows) {
		return 0, false
	}
	for _, w := range familyWindows {
		if windowMinutes(w) == windowWeeklyMinutes && windowRemaining(w) == 0 {
			return 0, true
		}
	}
	return lowest, true
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
		{"claude", "Claude", "CC", provider("claude", func(a quotamonitor.AccountQuota) (float64, bool) {
			return primarySecondaryAvailability(a, []string{"five-hour", "5h"}, []string{"weekly", "7d"})
		})},
		{"codex", "GPT", "GPT", provider("codex", func(a quotamonitor.AccountQuota) (float64, bool) {
			return primarySecondaryAvailability(a, []string{"primary"}, []string{"secondary"})
		})},
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
