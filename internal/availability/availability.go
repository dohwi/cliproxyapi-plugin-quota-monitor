package availability

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	quotamonitor "quota-monitor/internal/quota"
)

// TierEntry summarizes one subscription tier inside a provider family so
// tooltips and popovers can show the per-tier breakdown alongside the
// capacity-weighted fleet average.
type TierEntry struct {
	Tier                   string   `json:"tier"`
	Label                  string   `json:"label"`
	RemainingPercent       float64  `json:"remaining_percent"`
	ShortRemainingPercent  *float64 `json:"short_remaining_percent,omitempty"`
	WeeklyRemainingPercent *float64 `json:"weekly_remaining_percent,omitempty"`
	Accounts               int      `json:"accounts"`
	Weight                 float64  `json:"weight"`
}

// Entry is the fleet-wide availability of one provider family:
// the tier-capacity-weighted remaining percentage over every account that
// reports it. ShortRemainingPercent and WeeklyRemainingPercent expose the 5h
// and 7d windows separately; RemainingPercent is min(5h, 7d) when both windows
// are measured so summary gauges always match the tighter displayed window.
type Entry struct {
	Key                    string      `json:"key"`
	Label                  string      `json:"label"`
	Short                  string      `json:"short"`
	RemainingPercent       float64     `json:"remaining_percent"`
	ShortRemainingPercent  *float64    `json:"short_remaining_percent,omitempty"`
	WeeklyRemainingPercent *float64    `json:"weekly_remaining_percent,omitempty"`
	Accounts               int         `json:"accounts"`
	Tiers                  []TierEntry `json:"tiers,omitempty"`
}

type availabilitySample struct {
	effective float64
	short     float64
	hasShort  bool
	weekly    float64
	hasWeekly bool
}

func floatPtr(v float64) *float64 {
	r := roundTenth(v)
	return &r
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

func claudeAvailability(a quotamonitor.AccountQuota) (availabilitySample, bool) {
	primary := findWindow(a.Windows, windowFiveHourMinutes, "five-hour", "5h")
	return primarySecondaryAvailability(a, primary, findWindow(a.Windows, windowWeeklyMinutes, "weekly", "7d"))
}

func codexAvailability(a quotamonitor.AccountQuota) (availabilitySample, bool) {
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

// primarySecondaryAvailability combines an account's 5h (primary) and 7d
// (secondary) windows into a conservative bottleneck effective remaining
// percent (min(5h, 7d)) while also preserving each measured window separately.
// A depleted weekly window (0%) always blocks immediate 5h usage as well.
func primarySecondaryAvailability(a quotamonitor.AccountQuota, primary, secondary *quotamonitor.QuotaWindow) (availabilitySample, bool) {
	var considered []quotamonitor.QuotaWindow
	for _, w := range []*quotamonitor.QuotaWindow{primary, secondary} {
		if w != nil {
			considered = append(considered, *w)
		}
	}
	if !accountCountsTowardAvailability(a, considered) {
		return availabilitySample{}, false
	}
	var shortVal, weeklyVal float64
	var shortKnown, weeklyKnown bool
	if primary != nil {
		shortVal, shortKnown = windowRemaining(*primary)
	}
	if secondary != nil {
		weeklyVal, weeklyKnown = windowRemaining(*secondary)
	}
	if !shortKnown && !weeklyKnown {
		return availabilitySample{}, false
	}
	sample := availabilitySample{
		hasShort:  shortKnown,
		hasWeekly: weeklyKnown,
	}
	if weeklyKnown {
		sample.weekly = weeklyVal
	}
	if shortKnown {
		// Weekly exhaustion blocks immediate usage even if the 5h bucket is full.
		if weeklyKnown && weeklyVal == 0 {
			sample.short = 0
		} else {
			sample.short = shortVal
		}
	}
	switch {
	case shortKnown && weeklyKnown:
		sample.effective = math.Min(sample.short, sample.weekly)
	case shortKnown:
		sample.effective = sample.short
	default:
		sample.effective = sample.weekly
	}
	return sample, true
}

func antigravityWindowKind(w quotamonitor.QuotaWindow) int {
	if mins := windowMinutes(w); mins == windowFiveHourMinutes || mins == windowWeeklyMinutes {
		return mins
	}
	name := strings.ToLower(strings.TrimSpace(w.Name))
	switch {
	case strings.Contains(name, "weekly") || strings.Contains(name, "7d") || strings.Contains(name, "7-day"):
		return windowWeeklyMinutes
	case strings.Contains(name, "five hour") || strings.Contains(name, "five-hour") || strings.Contains(name, "5h"):
		return windowFiveHourMinutes
	default:
		return 0
	}
}

func antigravityAvailability(a quotamonitor.AccountQuota, family *regexp.Regexp) (availabilitySample, bool) {
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
		switch antigravityWindowKind(*w) {
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

type modelPool struct {
	sig        string
	modelCount int
	isLite     bool
	bestRank   int
	accounts   []quotamonitor.AccountQuota
}

func accountModelPoolMeta(provName string, a quotamonitor.AccountQuota) (sig string, modelCount int, isLite bool) {
	norm := quotamonitor.NormalizeModelList(a.Models)
	if provName == "antigravity" {
		if len(norm) > 0 {
			isLite = quotamonitor.ClassifyAntigravityModels(norm) == quotamonitor.AntigravityTierLite
		} else {
			isLite = quotamonitor.NormalizeAntigravityTier(a.ModelTier) == quotamonitor.AntigravityTierLite
		}
	}
	if len(norm) > 0 {
		return quotamonitor.ModelSetSignature(norm), len(norm), isLite
	}
	if provName == "antigravity" {
		switch quotamonitor.NormalizeAntigravityTier(a.ModelTier) {
		case quotamonitor.AntigravityTierFull:
			return "ag:full", 1000, false
		case quotamonitor.AntigravityTierLite:
			return "ag:lite", 1, true
		}
	}
	return "", 0, isLite
}

// partitionProviderPools groups contributing accounts of one provider by their
// normalized offered model list. When every account offers the same model set
// (or model catalogs are unknown), a single unified pool is returned. When
// accounts span 2+ distinct model sets, each distinct set becomes its own pool,
// ordered richest/highest-tier first.
func partitionProviderPools(accounts []quotamonitor.AccountQuota, provName string, contributes func(quotamonitor.AccountQuota) bool) ([]modelPool, bool) {
	bySig := make(map[string]*modelPool)
	var unknown []quotamonitor.AccountQuota
	var all []quotamonitor.AccountQuota
	for _, a := range accounts {
		if !strings.EqualFold(strings.TrimSpace(a.Provider), provName) {
			continue
		}
		if !contributes(a) {
			continue
		}
		all = append(all, a)
		sig, cnt, isLite := accountModelPoolMeta(provName, a)
		if sig == "" {
			unknown = append(unknown, a)
			continue
		}
		p := bySig[sig]
		rank := tierInfo(provName, quotamonitor.NormalizePlanType(a.Provider, a.PlanType)).rank
		if p == nil {
			p = &modelPool{sig: sig, modelCount: cnt, isLite: isLite, bestRank: rank}
			bySig[sig] = p
		} else {
			if cnt > p.modelCount {
				p.modelCount = cnt
			}
			if rank < p.bestRank {
				p.bestRank = rank
			}
		}
		p.accounts = append(p.accounts, a)
	}
	if len(all) == 0 {
		return nil, false
	}
	if len(bySig) <= 1 {
		isLite := false
		modelCount := 0
		sig := ""
		for _, p := range bySig {
			isLite = p.isLite
			modelCount = p.modelCount
			sig = p.sig
		}
		return []modelPool{{sig: sig, modelCount: modelCount, isLite: isLite, accounts: all}}, false
	}
	pools := make([]modelPool, 0, len(bySig))
	for _, p := range bySig {
		pools = append(pools, *p)
	}
	sort.Slice(pools, func(i, j int) bool {
		if provName == "antigravity" && pools[i].isLite != pools[j].isLite {
			return !pools[i].isLite
		}
		if pools[i].modelCount != pools[j].modelCount {
			return pools[i].modelCount > pools[j].modelCount
		}
		if pools[i].bestRank != pools[j].bestRank {
			return pools[i].bestRank < pools[j].bestRank
		}
		if len(pools[i].accounts) != len(pools[j].accounts) {
			return len(pools[i].accounts) > len(pools[j].accounts)
		}
		return pools[i].sig < pools[j].sig
	})
	if len(unknown) > 0 {
		pools[0].accounts = append(pools[0].accounts, unknown...)
	}
	return pools, true
}

func poolTierTag(provName string, accounts []quotamonitor.AccountQuota, isLitePool bool) string {
	type item struct {
		label string
		rank  int
	}
	seen := make(map[string]item)
	for _, a := range accounts {
		plan := quotamonitor.NormalizePlanType(a.Provider, a.PlanType)
		meta := tierInfo(provName, plan)
		isLiteAccount := isLitePool || (provName == "antigravity" && quotamonitor.NormalizeAntigravityTier(a.ModelTier) == quotamonitor.AntigravityTierLite)
		if isLiteAccount && plan == "pro" {
			meta.label = "AI Pro Trial"
		}
		if meta.label == "" {
			continue
		}
		if prev, ok := seen[meta.label]; !ok || meta.rank < prev.rank {
			seen[meta.label] = item{label: meta.label, rank: meta.rank}
		}
	}
	if len(seen) == 0 {
		return ""
	}
	items := make([]item, 0, len(seen))
	for _, it := range seen {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].rank != items[j].rank {
			return items[i].rank < items[j].rank
		}
		return items[i].label < items[j].label
	})
	if len(items) == 1 {
		return items[0].label
	}
	if len(items) == 2 {
		return items[0].label + "/" + items[1].label
	}
	return items[0].label + "+"
}

func poolHashSuffix(sig string) string {
	s := strings.TrimPrefix(sig, "ag:")
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

func disambiguatePoolTags(provName string, pools []modelPool) []string {
	tags := make([]string, len(pools))
	tagCounts := make(map[string]int, len(pools))
	for i, p := range pools {
		tags[i] = poolTierTag(provName, p.accounts, p.isLite)
		tagCounts[tags[i]]++
	}
	for i, p := range pools {
		t := tags[i]
		if t != "" && tagCounts[t] == 1 {
			continue
		}
		// Check whether modelCount distinguishes colliding pools with the same tag.
		uniqueCount := p.modelCount > 0
		if uniqueCount {
			for j, other := range pools {
				if i != j && tags[j] == t && other.modelCount == p.modelCount {
					uniqueCount = false
					break
				}
			}
		}
		if uniqueCount {
			if t != "" {
				tags[i] = t + " (" + strconv.Itoa(p.modelCount) + ")"
			} else {
				tags[i] = strconv.Itoa(p.modelCount) + " models"
			}
		} else {
			if t != "" {
				tags[i] = t + " #" + strconv.Itoa(i+1)
			} else {
				tags[i] = "Pool " + strconv.Itoa(i+1)
			}
		}
	}
	return tags
}

// Compute returns one entry per provider family (auto-split by offered model
// set when accounts within a provider expose different model catalogs, and
// auto-merged when they match), in a stable display order.
func Compute(accounts []quotamonitor.AccountQuota) []Entry {
	type family struct {
		key, label, short string
		provName          string
		isLitePool        bool
		poolAccounts      []quotamonitor.AccountQuota
		value             func(quotamonitor.AccountQuota) (availabilitySample, bool)
	}

	var families []family

	appendSingleFamilyPools := func(provName, baseKey, baseLabel, baseShort string, sampleFn func(quotamonitor.AccountQuota) (availabilitySample, bool)) {
		pools, split := partitionProviderPools(accounts, provName, func(a quotamonitor.AccountQuota) bool {
			_, ok := sampleFn(a)
			return ok
		})
		if len(pools) == 0 {
			return
		}
		if !split {
			families = append(families, family{
				key: baseKey, label: baseLabel, short: baseShort,
				provName: provName, poolAccounts: pools[0].accounts, value: sampleFn,
			})
			return
		}
		tags := disambiguatePoolTags(provName, pools)
		for i, p := range pools {
			key := baseKey
			if i > 0 {
				key = baseKey + "_pool_" + poolHashSuffix(p.sig)
			}
			label := baseLabel + " · " + tags[i]
			families = append(families, family{
				key: key, label: label, short: baseShort,
				provName: provName, poolAccounts: p.accounts, value: sampleFn,
			})
		}
	}

	appendSingleFamilyPools("claude", "claude", "Claude", "CC", claudeAvailability)
	appendSingleFamilyPools("codex", "codex", "GPT", "GPT", codexAvailability)

	agPools, agSplit := partitionProviderPools(accounts, "antigravity", func(a quotamonitor.AccountQuota) bool {
		return accountCountsTowardAvailability(a, a.Windows)
	})
	if len(agPools) > 0 {
		if !agSplit {
			families = append(families,
				family{key: "antigravity_gemini", label: "AGY · Gemini", short: "AG", provName: "antigravity", poolAccounts: agPools[0].accounts, value: func(a quotamonitor.AccountQuota) (availabilitySample, bool) { return antigravityAvailability(a, antigravityGeminiPattern) }},
				family{key: "antigravity_claude", label: "AGY · Claude/GPT", short: "AG", provName: "antigravity", poolAccounts: agPools[0].accounts, value: func(a quotamonitor.AccountQuota) (availabilitySample, bool) { return antigravityAvailability(a, antigravityClaudePattern) }},
			)
		} else {
			liteCount := 0
			for _, p := range agPools {
				if p.isLite {
					liteCount++
				}
			}
			tags := disambiguatePoolTags("antigravity", agPools)
			liteKeyUsed := false
			for i, p := range agPools {
				var gemKey, claudeKey, gemLabel, claudeLabel, shortCode string
				isLite := p.isLite
				if i == 0 {
					gemKey, claudeKey, shortCode = "antigravity_gemini", "antigravity_claude", "AG"
					gemLabel, claudeLabel = "AGY · Gemini", "AGY · Claude/GPT"
				} else if isLite && !liteKeyUsed && (liteCount == 1 || i == len(agPools)-1) {
					liteKeyUsed = true
					gemKey, claudeKey, shortCode = "antigravity_lite_gemini", "antigravity_lite_claude", "AGT"
					gemLabel, claudeLabel = "AGY (Trial/Free) · Gemini", "AGY (Trial/Free) · Claude/GPT"
				} else {
					suffix := poolHashSuffix(p.sig)
					gemKey = "antigravity_pool_" + suffix + "_gemini"
					claudeKey = "antigravity_pool_" + suffix + "_claude"
					shortCode = "AG"
					if isLite {
						shortCode = "AGT"
					}
					gemLabel = "AGY (" + tags[i] + ") · Gemini"
					claudeLabel = "AGY (" + tags[i] + ") · Claude/GPT"
				}
				poolAccs := p.accounts
				families = append(families,
					family{key: gemKey, label: gemLabel, short: shortCode, provName: "antigravity", isLitePool: isLite, poolAccounts: poolAccs, value: func(a quotamonitor.AccountQuota) (availabilitySample, bool) { return antigravityAvailability(a, antigravityGeminiPattern) }},
					family{key: claudeKey, label: claudeLabel, short: shortCode, provName: "antigravity", isLitePool: isLite, poolAccounts: poolAccs, value: func(a quotamonitor.AccountQuota) (availabilitySample, bool) { return antigravityAvailability(a, antigravityClaudePattern) }},
				)
			}
		}
	}

	entries := make([]Entry, 0, len(families))
	for _, f := range families {
		weightedSum, weightTotal, n := 0.0, 0.0, 0
		shortWeightedSum, shortWeightTotal := 0.0, 0.0
		weeklyWeightedSum, weeklyWeightTotal := 0.0, 0.0
		isLitePool := f.isLitePool || strings.HasPrefix(f.key, "antigravity_lite_")
		type tierAgg struct {
			tier        string
			meta        tierMeta
			sum         float64
			count       int
			shortSum    float64
			shortCount  int
			weeklySum   float64
			weeklyCount int
		}
		byTier := map[string]*tierAgg{}
		hasKnownTier := false
		for _, a := range f.poolAccounts {
			if s, ok := f.value(a); ok {
				plan := quotamonitor.NormalizePlanType(a.Provider, a.PlanType)
				meta := tierInfo(a.Provider, plan)
				isLiteAccount := isLitePool || (f.provName == "antigravity" && quotamonitor.NormalizeAntigravityTier(a.ModelTier) == quotamonitor.AntigravityTierLite)
				if isLiteAccount && plan == "pro" {
					meta.label = "AI Pro Trial"
				}
				weightedSum += s.effective * meta.weight
				weightTotal += meta.weight
				if s.hasShort {
					shortWeightedSum += s.short * meta.weight
					shortWeightTotal += meta.weight
				}
				if s.hasWeekly {
					weeklyWeightedSum += s.weekly * meta.weight
					weeklyWeightTotal += meta.weight
				}
				n++
				if plan != "" {
					hasKnownTier = true
				}
				agg := byTier[plan]
				if agg == nil {
					agg = &tierAgg{tier: plan, meta: meta}
					byTier[plan] = agg
				} else if meta.label == "AI Pro Trial" {
					agg.meta.label = "AI Pro Trial"
				}
				agg.sum += s.effective
				agg.count++
				if s.hasShort {
					agg.shortSum += s.short
					agg.shortCount++
				}
				if s.hasWeekly {
					agg.weeklySum += s.weekly
					agg.weeklyCount++
				}
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
			if shortWeightTotal > 0 {
				entry.ShortRemainingPercent = floatPtr(shortWeightedSum / shortWeightTotal)
			}
			if weeklyWeightTotal > 0 {
				entry.WeeklyRemainingPercent = floatPtr(weeklyWeightedSum / weeklyWeightTotal)
			}
			if entry.ShortRemainingPercent != nil && entry.WeeklyRemainingPercent != nil {
				entry.RemainingPercent = math.Min(*entry.ShortRemainingPercent, *entry.WeeklyRemainingPercent)
			}
			if hasKnownTier {
				tiers := make([]TierEntry, 0, len(byTier))
				for _, agg := range byTier {
					label := agg.meta.label
					if label == "" {
						label = "Other"
					}
					te := TierEntry{
						Tier:             agg.tier,
						Label:            label,
						RemainingPercent: roundTenth(agg.sum / float64(agg.count)),
						Accounts:         agg.count,
						Weight:           agg.meta.weight,
					}
					if agg.shortCount > 0 {
						te.ShortRemainingPercent = floatPtr(agg.shortSum / float64(agg.shortCount))
					}
					if agg.weeklyCount > 0 {
						te.WeeklyRemainingPercent = floatPtr(agg.weeklySum / float64(agg.weeklyCount))
					}
					if te.ShortRemainingPercent != nil && te.WeeklyRemainingPercent != nil {
						te.RemainingPercent = math.Min(*te.ShortRemainingPercent, *te.WeeklyRemainingPercent)
					}
					tiers = append(tiers, te)
				}
				sort.Slice(tiers, func(i, j int) bool {
					ri := tierInfo(f.provName, tiers[i].Tier).rank
					rj := tierInfo(f.provName, tiers[j].Tier).rank
					if ri != rj {
						return ri < rj
					}
					return tiers[i].Tier < tiers[j].Tier
				})
				entry.Tiers = tiers
				if strings.HasPrefix(f.key, "antigravity_lite_") && len(tiers) == 1 && tiers[0].Label != "" && tiers[0].Label != "Other" {
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
