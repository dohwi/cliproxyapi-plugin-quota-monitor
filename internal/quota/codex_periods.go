package quotamonitor

import "strings"

const (
	CodexFiveHour = "five-hour"
	CodexWeekly   = "weekly"
)

var codexWindowSeparators = strings.NewReplacer("_", "-", " ", "-")

func codexWindowName(name string) string {
	return codexWindowSeparators.Replace(strings.ToLower(strings.TrimSpace(name)))
}

func codexDurationLabel(name string) string {
	switch name {
	case "5h", "5-hour", "5-hours", "five-hour", "five-hours":
		return CodexFiveHour
	case "7d", "7-day", "7-days", "seven-day", "seven-days", "week", "weekly":
		return CodexWeekly
	}
	return ""
}

func codexSlotKind(name string) string {
	switch name {
	case "primary", "primary-window", "primary-limit":
		return CodexFiveHour
	case "secondary", "secondary-window", "secondary-limit":
		return CodexWeekly
	}
	return ""
}

// SameCodexWindowSlot reports whether two observations use the same positional
// account-wide slot, independently of its metering period.
func SameCodexWindowSlot(a, b QuotaWindow) bool {
	slot := codexSlotKind(codexWindowName(a.Name))
	return slot != "" && slot == codexSlotKind(codexWindowName(b.Name))
}

// IsCodexAccountWindow distinguishes the two account-wide slots from scoped
// limits such as code review or a specific model's primary quota.
func IsCodexAccountWindow(w QuotaWindow) bool {
	name := codexWindowName(w.Name)
	return codexSlotKind(name) != "" || codexDurationLabel(name) != ""
}

// CodexWindowKind uses the full metering period, never the time until reset.
// WHAM can put the weekly limit in primary on accounts without a 5h window.
// Slot names are a legacy fallback when the period was not reported.
func CodexWindowKind(w QuotaWindow) string {
	if w.WindowMinutes != nil {
		switch *w.WindowMinutes {
		case 300:
			return CodexFiveHour
		case 10080:
			return CodexWeekly
		default:
			return ""
		}
	}
	name := codexWindowName(w.Name)
	if kind := codexDurationLabel(name); kind != "" {
		return kind
	}
	if kind := codexDurationLabel(codexWindowName(w.LimitName)); kind != "" {
		return kind
	}
	if kind := codexSlotKind(name); kind != "" {
		return kind
	}
	if strings.HasSuffix(name, "-primary") {
		return CodexFiveHour
	}
	if strings.HasSuffix(name, "-secondary") {
		return CodexWeekly
	}
	return ""
}

// DistinctCodexWindows preserves wire names and scoped limits. For each
// account-wide period, prefer a measured duration over a positional fallback.
// A zero-length account slot is a placeholder, not a metered quota.
func DistinctCodexWindows(windows []QuotaWindow) []QuotaWindow {
	chosen := make(map[string]int, 2)
	for i, w := range windows {
		if kind := CodexWindowKind(w); kind != "" && IsCodexAccountWindow(w) {
			if j, ok := chosen[kind]; !ok || (windows[j].WindowMinutes == nil && w.WindowMinutes != nil) {
				chosen[kind] = i
			}
		}
	}
	var out []QuotaWindow
	for i, w := range windows {
		if IsCodexAccountWindow(w) {
			if w.WindowMinutes != nil && *w.WindowMinutes == 0 {
				continue
			}
			if j, ok := chosen[CodexWindowKind(w)]; ok && i != j {
				continue
			}
		}
		out = append(out, w)
	}
	return out
}
