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
	ModelCount             int         `json:"model_count,omitempty"`
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

func accountIsDisabled(a quotamonitor.AccountQuota) bool {
	op := a.Operational
	return op != nil && ((op.Disabled != nil && *op.Disabled) || strings.EqualFold(strings.TrimSpace(op.Status), "disabled"))
}

// accountCountsTowardAvailability keeps exhausted-but-unavailable accounts in the
// denominator as 0% instead of silently dropping them; disabled accounts never count.
func accountCountsTowardAvailability(a quotamonitor.AccountQuota, windows []quotamonitor.QuotaWindow) bool {
	if accountIsDisabled(a) {
		return false
	}
	op := a.Operational
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
	sig             string
	sortModelCount  int
	exactModelCount int
	isLite          bool
	bestRank        int
	accounts        []quotamonitor.AccountQuota // contributing accounts for availability math
	allAccounts     []quotamonitor.AccountQuota // all non-disabled accounts in this pool (for stable topology/labels)
}

func accountModelPoolMeta(provName string, a quotamonitor.AccountQuota) (sig string, sortCount, exactCount int, isLite bool, exact bool) {
	norm := quotamonitor.NormalizeModelList(a.Models)
	if provName == "antigravity" {
		if len(norm) > 0 {
			isLite = quotamonitor.ClassifyAntigravityModels(norm) == quotamonitor.AntigravityTierLite
		} else {
			isLite = quotamonitor.NormalizeAntigravityTier(a.ModelTier) == quotamonitor.AntigravityTierLite
		}
	}
	if len(norm) > 0 {
		return quotamonitor.ModelSetSignature(norm), len(norm), len(norm), isLite, true
	}
	if provName == "antigravity" {
		switch quotamonitor.NormalizeAntigravityTier(a.ModelTier) {
		case quotamonitor.AntigravityTierFull:
			return "ag:full", 1000, 0, false, false
		case quotamonitor.AntigravityTierLite:
			return "ag:lite", 1, 0, true, false
		}
	}
	return "", 0, 0, isLite, false
}

// partitionProviderPools derives stable model-set pool topology across all
// non-disabled accounts of a provider, then populates each pool's contributing
// subset via contributes(a). Using the non-disabled fleet for topology prevents
// a single pool's transient probe failure from collapsing split keys/labels back
// into a merged pool, and avoids creating synthetic "ag:full"/"ag:lite" pools
// alongside exact model-set pools.
func partitionProviderPools(accounts []quotamonitor.AccountQuota, provName string, contributes func(quotamonitor.AccountQuota) bool) ([]modelPool, bool) {
	type taggedAccount struct {
		acc      quotamonitor.AccountQuota
		contrib  bool
		sig      string
		sortCnt  int
		exactCnt int
		isLite   bool
		exact    bool
	}
	var fleet []taggedAccount
	hasExactFull := false
	hasExactLite := false
	hasAnyContrib := false
	for _, a := range accounts {
		if !strings.EqualFold(strings.TrimSpace(a.Provider), provName) {
			continue
		}
		if accountIsDisabled(a) {
			continue
		}
		c := contributes(a)
		if c {
			hasAnyContrib = true
		}
		sig, sortCnt, exactCnt, isLite, exact := accountModelPoolMeta(provName, a)
		if exact {
			if isLite {
				hasExactLite = true
			} else {
				hasExactFull = true
			}
		}
		fleet = append(fleet, taggedAccount{
			acc: a, contrib: c, sig: sig, sortCnt: sortCnt, exactCnt: exactCnt, isLite: isLite, exact: exact,
		})
	}
	if !hasAnyContrib {
		return nil, false
	}

	bySig := make(map[string]*modelPool)
	var tierAttach []taggedAccount
	var unknown []taggedAccount
	var allContrib []quotamonitor.AccountQuota
	var allFleet []quotamonitor.AccountQuota

	for _, item := range fleet {
		allFleet = append(allFleet, item.acc)
		if item.contrib {
			allContrib = append(allContrib, item.acc)
		}
		if item.sig == "" {
			unknown = append(unknown, item)
			continue
		}
		// Never create a phantom synthetic "ag:full"/"ag:lite" pool when an exact
		// model-set pool of the same lite/full class already exists in the fleet.
		if !item.exact && provName == "antigravity" && ((item.isLite && hasExactLite) || (!item.isLite && hasExactFull)) {
			tierAttach = append(tierAttach, item)
			continue
		}
		p := bySig[item.sig]
		rank := tierInfo(provName, quotamonitor.NormalizePlanType(item.acc.Provider, item.acc.PlanType)).rank
		if p == nil {
			p = &modelPool{
				sig: item.sig, sortModelCount: item.sortCnt, exactModelCount: item.exactCnt,
				isLite: item.isLite, bestRank: rank,
			}
			bySig[item.sig] = p
		} else {
			if item.sortCnt > p.sortModelCount {
				p.sortModelCount = item.sortCnt
			}
			if item.exactCnt > p.exactModelCount {
				p.exactModelCount = item.exactCnt
			}
			if rank < p.bestRank {
				p.bestRank = rank
			}
		}
		p.allAccounts = append(p.allAccounts, item.acc)
		if item.contrib {
			p.accounts = append(p.accounts, item.acc)
		}
	}

	if len(bySig) <= 1 {
		isLite := false
		sortModelCount := 0
		exactModelCount := 0
		sig := ""
		for _, p := range bySig {
			isLite = p.isLite
			sortModelCount = p.sortModelCount
			exactModelCount = p.exactModelCount
			sig = p.sig
		}
		return []modelPool{{
			sig: sig, sortModelCount: sortModelCount, exactModelCount: exactModelCount,
			isLite: isLite, accounts: allContrib, allAccounts: allFleet,
		}}, false
	}

	pools := make([]modelPool, 0, len(bySig))
	for _, p := range bySig {
		pools = append(pools, *p)
	}
	sort.Slice(pools, func(i, j int) bool {
		if provName == "antigravity" && pools[i].isLite != pools[j].isLite {
			return !pools[i].isLite
		}
		if pools[i].sortModelCount != pools[j].sortModelCount {
			return pools[i].sortModelCount > pools[j].sortModelCount
		}
		if pools[i].bestRank != pools[j].bestRank {
			return pools[i].bestRank < pools[j].bestRank
		}
		if len(pools[i].allAccounts) != len(pools[j].allAccounts) {
			return len(pools[i].allAccounts) > len(pools[j].allAccounts)
		}
		return pools[i].sig < pools[j].sig
	})
	for _, item := range tierAttach {
		target := 0
		for idx := range pools {
			if pools[idx].isLite == item.isLite {
				target = idx
				break
			}
		}
		pools[target].allAccounts = append(pools[target].allAccounts, item.acc)
		if item.contrib {
			pools[target].accounts = append(pools[target].accounts, item.acc)
		}
	}
	for _, item := range unknown {
		pools[0].allAccounts = append(pools[0].allAccounts, item.acc)
		if item.contrib {
			pools[0].accounts = append(pools[0].accounts, item.acc)
		}
	}
	return pools, true
}

// AccountPoolKeys maps each account's AccountKey to its canonical provider+pool
// unit key using the exact same non-disabled pool topology as Compute. Two
// accounts placed in the same displayed availability pool always share the same
// unit key (even when one falls back via tierAttach or unknown), while distinct
// model pools get distinct unit keys so each pool can highlight its own active
// account.
func AccountPoolKeys(accounts []quotamonitor.AccountQuota) map[string]string {
	keys := make(map[string]string, len(accounts))
	seenProv := make(map[string]bool)
	for _, a := range accounts {
		prov := strings.ToLower(strings.TrimSpace(a.Provider))
		if prov == "" || seenProv[prov] {
			continue
		}
		seenProv[prov] = true
		pools, split := partitionProviderPools(accounts, prov, func(quotamonitor.AccountQuota) bool { return true })
		if !split {
			for _, p := range pools {
				for _, acc := range p.allAccounts {
					if acc.AccountKey != "" {
						keys[acc.AccountKey] = prov
					}
				}
			}
			continue
		}
		for _, p := range pools {
			unitKey := prov
			if p.sig != "" {
				unitKey = prov + ":" + p.sig
			}
			for _, acc := range p.allAccounts {
				if acc.AccountKey != "" {
					keys[acc.AccountKey] = unitKey
				}
			}
		}
	}
	return keys
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

func disambiguatePoolTags(provName string, pools []modelPool) ([]string, []bool) {
	baseTags := make([]string, len(pools))
	tags := make([]string, len(pools))
	disambiguated := make([]bool, len(pools))
	tagCounts := make(map[string]int, len(pools))
	for i, p := range pools {
		tagAccs := p.allAccounts
		if len(tagAccs) == 0 {
			tagAccs = p.accounts
		}
		baseTags[i] = poolTierTag(provName, tagAccs, p.isLite)
		tags[i] = baseTags[i]
		tagCounts[baseTags[i]]++
	}
	for i, p := range pools {
		t := baseTags[i]
		if t != "" && tagCounts[t] == 1 {
			continue
		}
		disambiguated[i] = true
		// Check whether exactModelCount distinguishes colliding pools with the same base tag.
		uniqueCount := p.exactModelCount > 0
		if uniqueCount {
			for j, other := range pools {
				if i != j && baseTags[j] == t && other.exactModelCount == p.exactModelCount {
					uniqueCount = false
					break
				}
			}
		}
		if uniqueCount {
			if t != "" {
				tags[i] = t + " (" + strconv.Itoa(p.exactModelCount) + ")"
			} else {
				tags[i] = strconv.Itoa(p.exactModelCount) + " models"
			}
		} else {
			if t != "" {
				tags[i] = t + " #" + strconv.Itoa(i+1)
			} else {
				tags[i] = "Pool " + strconv.Itoa(i+1)
			}
		}
	}
	return tags, disambiguated
}

// Compute returns one entry per provider family (auto-split by offered model
// set when accounts within a provider expose different model catalogs, and
// auto-merged when they match), in a stable display order.
func Compute(accounts []quotamonitor.AccountQuota) []Entry {
	type family struct {
		key, label, short string
		provName          string
		isLitePool        bool
		labelLocked       bool
		modelCount        int
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
				provName: provName, modelCount: pools[0].exactModelCount, poolAccounts: pools[0].accounts, value: sampleFn,
			})
			return
		}
		tags, _ := disambiguatePoolTags(provName, pools)
		for i, p := range pools {
			key := baseKey
			if i > 0 {
				key = baseKey + "_pool_" + poolHashSuffix(p.sig)
			}
			label := baseLabel + " · " + tags[i]
			families = append(families, family{
				key: key, label: label, short: baseShort,
				provName: provName, modelCount: p.exactModelCount, poolAccounts: p.accounts, value: sampleFn,
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
				family{key: "antigravity_gemini", label: "AGY · Gemini", short: "AG", provName: "antigravity", modelCount: agPools[0].exactModelCount, poolAccounts: agPools[0].accounts, value: func(a quotamonitor.AccountQuota) (availabilitySample, bool) { return antigravityAvailability(a, antigravityGeminiPattern) }},
				family{key: "antigravity_claude", label: "AGY · Claude/GPT", short: "AG", provName: "antigravity", modelCount: agPools[0].exactModelCount, poolAccounts: agPools[0].accounts, value: func(a quotamonitor.AccountQuota) (availabilitySample, bool) { return antigravityAvailability(a, antigravityClaudePattern) }},
			)
		} else {
			liteCount := 0
			for _, p := range agPools {
				if p.isLite {
					liteCount++
				}
			}
			tags, disambiguated := disambiguatePoolTags("antigravity", agPools)
			liteKeyUsed := false
			for i, p := range agPools {
				var gemKey, claudeKey, gemLabel, claudeLabel, shortCode string
				isLite := p.isLite
				labelLocked := false
				if i == 0 {
					gemKey, claudeKey, shortCode = "antigravity_gemini", "antigravity_claude", "AG"
					if isLite {
						shortCode = "AGT"
					}
					if (disambiguated[i] || isLite) && tags[i] != "" {
						gemLabel = "AGY (" + tags[i] + ") · Gemini"
						claudeLabel = "AGY (" + tags[i] + ") · Claude/GPT"
						labelLocked = true
					} else {
						gemLabel, claudeLabel = "AGY · Gemini", "AGY · Claude/GPT"
					}
				} else if isLite && !liteKeyUsed && (liteCount == 1 || i == len(agPools)-1) {
					liteKeyUsed = true
					gemKey, claudeKey, shortCode = "antigravity_lite_gemini", "antigravity_lite_claude", "AGT"
					if disambiguated[i] && tags[i] != "" {
						gemLabel = "AGY (" + tags[i] + ") · Gemini"
						claudeLabel = "AGY (" + tags[i] + ") · Claude/GPT"
						labelLocked = true
					} else {
						gemLabel, claudeLabel = "AGY (Trial/Free) · Gemini", "AGY (Trial/Free) · Claude/GPT"
					}
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
					labelLocked = true
				}
				poolAccs := p.accounts
				families = append(families,
					family{key: gemKey, label: gemLabel, short: shortCode, provName: "antigravity", isLitePool: isLite, labelLocked: labelLocked, modelCount: p.exactModelCount, poolAccounts: poolAccs, value: func(a quotamonitor.AccountQuota) (availabilitySample, bool) { return antigravityAvailability(a, antigravityGeminiPattern) }},
					family{key: claudeKey, label: claudeLabel, short: shortCode, provName: "antigravity", isLitePool: isLite, labelLocked: labelLocked, modelCount: p.exactModelCount, poolAccounts: poolAccs, value: func(a quotamonitor.AccountQuota) (availabilitySample, bool) { return antigravityAvailability(a, antigravityClaudePattern) }},
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
				ModelCount:       f.modelCount,
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
				if !f.labelLocked && strings.HasPrefix(f.key, "antigravity_lite_") && len(tiers) == 1 && tiers[0].Label != "" && tiers[0].Label != "Other" {
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
