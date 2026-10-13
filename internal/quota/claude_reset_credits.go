package quotamonitor

import (
	"encoding/json"
	"time"
)

// ParseClaudeResetCredits extracts display-safe owned reset counts from Claude
// usage. Unknown inventory is nonfatal; a known empty inventory returns an
// explicit zero so callers can clear stale counts. No redemption handles or raw
// grants are retained, and usable_now does not affect ownership.
func ParseClaudeResetCredits(data []byte, clock time.Time) (*ResetCredits, error) {
	var root *struct {
		Inventory json.RawMessage `json:"cedar_ember"`
	}
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		return nil, &APICallError{Kind: "parse"}
	}
	var inventory struct {
		Eligible *bool             `json:"eligible"`
		Grants   []json.RawMessage `json:"grants"`
	}
	if err := json.Unmarshal(root.Inventory, &inventory); err != nil ||
		inventory.Eligible == nil || !*inventory.Eligible || inventory.Grants == nil || len(inventory.Grants) > 200 {
		return nil, nil
	}

	count := 0
	var nearest *time.Time
	for _, raw := range inventory.Grants {
		// Decode only allowlisted fields; private IDs and metadata are skipped.
		var grant struct {
			Left   *int    `json:"resets_left"`
			Total  *int    `json:"resets_total"`
			Paused *bool   `json:"paused"`
			Starts *string `json:"starts_at"`
			Ends   *string `json:"ends_at"`
		}
		if err := json.Unmarshal(raw, &grant); err != nil || grant.Left == nil || grant.Paused == nil ||
			*grant.Left < 0 || (grant.Total != nil && (*grant.Total < 0 || *grant.Total < *grant.Left)) {
			continue
		}
		starts, validStart := claudeResetCreditsBound(grant.Starts)
		ends, validEnd := claudeResetCreditsBound(grant.Ends)
		if !validStart || !validEnd || *grant.Paused || *grant.Left == 0 ||
			(starts != nil && starts.After(clock)) || (ends != nil && !ends.After(clock)) {
			continue
		}
		// Subtraction avoids overflow even for a syntactically valid large int.
		if *grant.Left > 50-count {
			return nil, nil
		}
		count += *grant.Left
		if ends != nil && (nearest == nil || ends.Before(*nearest)) {
			nearest = ends
		}
	}
	return &ResetCredits{AvailableCount: &count, NearestExpiry: nearest}, nil
}

func claudeResetCreditsBound(raw *string) (*time.Time, bool) {
	if raw == nil {
		return nil, true
	}
	bound, err := time.Parse(time.RFC3339Nano, *raw)
	if err != nil {
		return nil, false
	}
	bound = bound.UTC()
	return &bound, true
}
