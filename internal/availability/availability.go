package availability

import (
	"math"
	"regexp"
	"sort"
	"strings"

	quotamonitor "quota-monitor/internal/quota"
)

// TierEntry summarizes one subscription tier inside a provider family so
// tooltips and popovers can show the per-tier breakdown alongside the
// capacity-weighted fleet average.
type TierEntry struct {
	Tier             string  `json:"tier"`
	Label            string  `json:"label"`
	RemainingPercent float64 `json:"remaining_percent"`
	Accounts         int     `json:"accounts"`
	Weight           float64 `json:"weight"`
}

// Entry is the fleet-wide availability of one provider family:
// the tier-capacity-weighted remaining percentage over every account that
// reports it. It is computed once here so the dashboard header and external
// status surfaces (e.g. pipaya gauges) always show the same number.
type Entry struct {
	Key              string      `json:"key"`
	Label            string      `json:"label"`
	Short            string      `json:"short"`
	RemainingPercent float64     `json:"remaining_percent"`
	Accounts         int         `json:"accounts"`
	Tiers            []TierEntry `json:"tiers,omitempty"`
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

type tierMeta struct {
	label  string
	weight float64
	rank   int
}

func tierInfo(provider, plan string) tierMeta {
	p := strings.ToLower(strings.TrimSpace(provider))
	t := quotamonitor.NormalizePlanType(p, plan)
	switch p {
	case "claude":
		switch t {
		case "ultra":
			return tierMeta{"Ultra", 10, 0}
		case "max":
			return tierMeta{"Max", 5, 1}
		case "enterprise":
			return tierMeta{"Enterprise", 2, 2}
		case "team":
			return tierMeta{"Team", 1.5, 3}
		case "pro":
			return tierMeta{"Pro", 1, 4}
		case "free":
			return tierMeta{"Free", 0.2, 5}
		default:
			return tierMeta{"", 1, 99}
		}
	case "codex":
		switch t {
		case "pro_500":
			return tierMeta{"Pro 500", 50, 0}
		case "pro_200":
			return tierMeta{"Pro 200", 20, 1}
		case "pro":
			return tierMeta{"Pro", 20, 2}
		case "pro_100":
			return tierMeta{"Pro 100", 5, 3}
		case "enterprise":
			return tierMeta{"Enterprise", 2, 4}
		case "team":
			return tierMeta{"Team", 1, 5}
		case "edu":
			return tierMeta{"Edu", 1, 6}
		case "plus":
			return tierMeta{"Plus", 1, 7}
		case "k12":
			return tierMeta{"K12", 1, 8}
		case "go":
			return tierMeta{"Go", 0.5, 9}
		case "free":
			return tierMeta{"Free", 0.2, 10}
		default:
			return tierMeta{"", 1, 99}
		}
	case "antigravity":
		switch t {
		case "ultra":
			return tierMeta{"AI Ultra", 5, 0}
		case "pro":
			return tierMeta{"AI Pro", 1, 1}
		case "standard":
			return tierMeta{"Standard", 1, 2}
		case "free":
			return tierMeta{"Free", 0.2, 3}
		default:
			return tierMeta{"", 1, 99}
		}
	default:
		return tierMeta{"", 1, 99}
	}
}

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
	// Only split Antigravity into Full (AGY) vs Lite (AGY (Lite)) when
	// accounts actually span both model pools. When every account shares the
	// same model pool, keep a single unified AGY pool.
	hasAGFull, hasAGLite := false, false
	for _, a := range accounts {
		if !strings.EqualFold(strings.TrimSpace(a.Provider), "antigravity") {
			continue
		}
		if !accountCountsTowardAvailability(a, a.Windows) {
			continue
		}
		switch quotamonitor.NormalizeAntigravityTier(a.ModelTier) {
		case quotamonitor.AntigravityTierFull:
			hasAGFull = true
		case quotamonitor.AntigravityTierLite:
			hasAGLite = true
		}
	}
	splitAGByPool := hasAGFull && hasAGLite

	agPool := func(wantLite bool, familyRe *regexp.Regexp) func(quotamonitor.AccountQuota) (float64, bool) {
		return provider("antigravity", func(a quotamonitor.AccountQuota) (float64, bool) {
			if splitAGByPool {
				isLite := quotamonitor.NormalizeAntigravityTier(a.ModelTier) == quotamonitor.AntigravityTierLite
				if isLite != wantLite {
					return 0, false
				}
			} else if wantLite {
				return 0, false
			}
			return antigravityAvailability(a, familyRe)
		})
	}

	families := []family{
		{"claude", "Claude", "CC", provider("claude", claudeAvailability)},
		{"codex", "GPT", "GPT", provider("codex", codexAvailability)},
		{"antigravity_gemini", "AGY · Gemini", "AG", agPool(false, antigravityGeminiPattern)},
		{"antigravity_claude", "AGY · Claude/GPT", "AG", agPool(false, antigravityClaudePattern)},
		{"antigravity_lite_gemini", "AGY (Trial/Free) · Gemini", "AGT", agPool(true, antigravityGeminiPattern)},
		{"antigravity_lite_claude", "AGY (Trial/Free) · Claude/GPT", "AGT", agPool(true, antigravityClaudePattern)},
	}
	entries := make([]Entry, 0, len(families))
	for _, f := range families {
		weightedSum, weightTotal, n := 0.0, 0.0, 0
		isLitePool := strings.HasPrefix(f.key, "antigravity_lite_")
		type tierAgg struct {
			tier   string
			meta   tierMeta
			sum    float64
			count  int
		}
		byTier := map[string]*tierAgg{}
		hasKnownTier := false
		for _, a := range accounts {
			if v, ok := f.value(a); ok {
				plan := quotamonitor.NormalizePlanType(a.Provider, a.PlanType)
				meta := tierInfo(a.Provider, plan)
				if isLitePool && plan == "pro" {
					meta.label = "AI Pro Trial"
				}
				weightedSum += v * meta.weight
				weightTotal += meta.weight
				n++
				if plan != "" {
					hasKnownTier = true
				}
				agg := byTier[plan]
				if agg == nil {
					agg = &tierAgg{tier: plan, meta: meta}
					byTier[plan] = agg
				}
				agg.sum += v
				agg.count++
			}
		}
		if n > 0 && weightTotal > 0 {
			entry := Entry{
				Key:              f.key,
				Label:            f.label,
				Short:            f.short,
				RemainingPercent: roundTenth(weightedSum / weightTotal),
				Accounts:         n,
			}
			if hasKnownTier {
				tiers := make([]TierEntry, 0, len(byTier))
				for _, agg := range byTier {
					label := agg.meta.label
					if label == "" {
						label = "Other"
					}
					tiers = append(tiers, TierEntry{
						Tier:             agg.tier,
						Label:            label,
						RemainingPercent: roundTenth(agg.sum / float64(agg.count)),
						Accounts:         agg.count,
						Weight:           agg.meta.weight,
					})
				}
				sort.Slice(tiers, func(i, j int) bool {
					ri := tierInfo(strings.Split(f.key, "_")[0], tiers[i].Tier).rank
					rj := tierInfo(strings.Split(f.key, "_")[0], tiers[j].Tier).rank
					if ri != rj {
						return ri < rj
					}
					return tiers[i].Tier < tiers[j].Tier
				})
				entry.Tiers = tiers
				if isLitePool && len(tiers) == 1 && tiers[0].Label != "" && tiers[0].Label != "Other" {
					suffix := "Gemini"
					if strings.HasSuffix(f.key, "_claude") {
						suffix = "Claude/GPT"
					}
					entry.Label = "AGY (" + tiers[0].Label + ") · " + suffix
				}
			}
			entries = append(entries, entry)
		}
	}
	return entries
}
