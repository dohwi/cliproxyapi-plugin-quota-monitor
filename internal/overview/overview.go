package overview

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	quotamonitor "quota-monitor/internal/quota"
)

// Summary contains only aggregate quota data, never account identity or
// upstream labels. Percentages describe the worst known window, not capacity
// summed across accounts. Unknown counts must accompany any displayed value.
type Summary struct {
	Provider string `json:"provider"`
	Family   string `json:"family"`
	Label    string `json:"label"`
	Stale    bool   `json:"stale"`
	quotaRemainingSummary
	Limits []quotaLimitSummary `json:"windows"`
}

type quotaRemainingSummary struct {
	AccountCount     int        `json:"account_count"`
	KnownCount       int        `json:"known_account_count"`
	UnknownCount     int        `json:"unknown_account_count"`
	StaleCount       int        `json:"stale_account_count"`
	UsedPercent      *float64   `json:"used_percent"`
	RemainingPercent *float64   `json:"remaining_percent"`
	ResetAt          *time.Time `json:"reset_at"`
}

type quotaLimitSummary struct {
	Name string `json:"name"`
	quotaRemainingSummary
}

// Build intentionally does not use currentAccountSelection: every
// non-disabled account contributes, including unavailable and never-used ones.
// Known/unknown partition accounts by presence of a valid numeric observation;
// stale overlaps those counts, retaining useful last-known values explicitly.
// Missing windows count as unknown in per-limit rows. Empty families are emitted
// with null percentages so consumers need not infer absence as full availability.
func Build(accounts []quotamonitor.AccountQuota, snapshotStale bool) []Summary {
	out := make([]Summary, 0, 5)
	for _, spec := range [][2]string{{"claude", "claude"}, {"codex", "gpt"}, {"gemini", "gemini"}, {"antigravity", "gemini"}, {"antigravity", "claude"}} {
		summary := Summary{Provider: spec[0], Family: spec[1], Label: overviewLabel(spec[0], spec[1]), Stale: snapshotStale, Limits: []quotaLimitSummary{}}
		var perAccount []map[string]quotaRemainingSummary
		var staleAccounts []bool
		names := map[string]bool{}
		for _, account := range accounts {
			if overviewProvider(account.Provider) != spec[0] || overviewDisabled(account) {
				continue
			}
			stale := snapshotStale || account.Stale || account.Status == quotamonitor.StatusStale
			limits := map[string]quotaRemainingSummary{}
			var worst quotaRemainingSummary
			for _, window := range account.Windows {
				name, family, ok := overviewWindow(window, spec[0])
				if !ok || family != spec[1] {
					continue
				}
				names[name] = true
				value := overviewWindowRemaining(window)
				row := limits[name]
				row.observe(value, window.ResetAt)
				limits[name] = row
				worst.observe(value, window.ResetAt)
			}
			summary.addAccount(worst, stale)
			summary.Stale = summary.Stale || stale
			perAccount = append(perAccount, limits)
			staleAccounts = append(staleAccounts, stale)
		}
		var sortedNames []string
		for name := range names {
			sortedNames = append(sortedNames, name)
		}
		sort.Strings(sortedNames)
		for _, name := range sortedNames {
			row := quotaLimitSummary{Name: name}
			for i, limits := range perAccount {
				row.addAccount(limits[name], staleAccounts[i])
			}
			summary.Limits = append(summary.Limits, row)
		}
		out = append(out, summary)
	}
	return out
}

func (s *quotaRemainingSummary) addAccount(value quotaRemainingSummary, stale bool) {
	s.AccountCount++
	if value.RemainingPercent == nil {
		s.UnknownCount++
	} else {
		s.KnownCount++
	}
	if stale {
		s.StaleCount++
	}
	s.observe(value.RemainingPercent, value.ResetAt)
}

// Ties use the latest reset, since the earliest exhausted window resetting does
// not recover all tied constraints. A missing reset on a tie stays unknown.
func (s *quotaRemainingSummary) observe(remaining *float64, reset *time.Time) {
	if remaining == nil {
		return
	}
	if s.RemainingPercent == nil || *remaining < *s.RemainingPercent {
		r, u := *remaining, 100-*remaining
		s.RemainingPercent, s.UsedPercent = &r, &u
		s.ResetAt = overviewReset(reset)
	} else if *remaining == *s.RemainingPercent {
		if reset == nil || reset.IsZero() || s.ResetAt == nil {
			s.ResetAt = nil
		} else if reset.After(*s.ResetAt) {
			s.ResetAt = overviewReset(reset)
		}
	}
}

func overviewReset(reset *time.Time) *time.Time {
	if reset == nil || reset.IsZero() {
		return nil
	}
	copy := *reset
	return &copy
}

func overviewWindowRemaining(w quotamonitor.QuotaWindow) *float64 {
	if (w.Allowed != nil && !*w.Allowed) || (w.LimitReached != nil && *w.LimitReached) {
		zero := 0.0
		return &zero
	}
	var result *float64
	for i, candidate := range []*float64{w.RemainingPercent, w.UsedPercent} {
		if candidate == nil || math.IsNaN(*candidate) || math.IsInf(*candidate, 0) || *candidate < 0 || *candidate > 100 {
			continue
		}
		value := *candidate
		if i == 1 {
			value = 100 - value
		}
		if result == nil || value < *result {
			result = &value
		}
	}
	return result
}

func overviewProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude", "anthropic":
		return "claude"
	case "codex", "gpt", "openai":
		return "codex"
	case "gemini", "google":
		return "gemini"
	case "antigravity":
		return "antigravity"
	}
	return ""
}

func overviewDisabled(a quotamonitor.AccountQuota) bool {
	return a.Operational != nil && ((a.Operational.Disabled != nil && *a.Operational.Disabled) || strings.EqualFold(strings.TrimSpace(a.Operational.Status), "disabled"))
}

// Canonical labels prevent raw window/limit names (which may contain account
// identifiers) from entering the overview. Unrecognized windows share an
// "other" row, or a duration row when the provider supplies window_minutes.
func overviewWindow(w quotamonitor.QuotaWindow, provider string) (name, family string, ok bool) {
	label := strings.ToLower(strings.TrimSpace(w.Name + " " + w.LimitName))
	label = strings.NewReplacer("_", "-", " ", "-").Replace(label)
	if strings.Contains(label, "overage") || strings.Contains(label, "code-review") || strings.Contains(label, "codereview") {
		return "", "", false
	}
	family = provider
	if provider == "codex" {
		family = "gpt"
	}
	if provider == "antigravity" {
		switch {
		case strings.Contains(label, "gemini"):
			family = "gemini"
		case strings.Contains(label, "claude"), strings.Contains(label, "gpt"):
			family = "claude"
		default:
			return "", "", false
		}
	}
	minutes := 0
	if w.WindowMinutes != nil {
		minutes = *w.WindowMinutes
	}
	switch {
	case strings.Contains(label, "weekly"), strings.Contains(label, "seven-day"), strings.Contains(label, "7d"), minutes == 10080, provider == "codex" && strings.Contains(label, "secondary"):
		name = "weekly"
		if provider == "claude" {
			for _, suffix := range []string{"opus", "sonnet", "oauth-apps", "cowork", "sessions"} {
				if strings.Contains(label, suffix) {
					name += "-" + suffix
					break
				}
			}
		}
	case strings.Contains(label, "five-hour"), strings.Contains(label, "5-hour"), strings.Contains(label, "5h"), minutes == 300, provider == "codex" && strings.Contains(label, "primary"):
		name = "five-hour"
	case minutes > 0:
		name = "window-" + strconv.Itoa(minutes) + "m"
	default:
		name = "other"
	}
	return name, family, true
}

func overviewLabel(provider, family string) string {
	if provider == "antigravity" {
		if family == "gemini" {
			return "Antigravity / Gemini"
		}
		return "Antigravity / Claude"
	}
	switch provider {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex / GPT"
	default:
		return "Gemini"
	}
}
