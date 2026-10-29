package store

import "strings"

// RewardProviderID marks earnings rows that pay the account rather than one Provider.
// Upstream sends these rows with an empty `provider_id`, so they can never match
// device_provider_ids and would silently disappear from device-scoped analysis.
// The value is ours, never upstream's, and is stored only when the Provider is blank.
const RewardProviderID = "bloomer_reward"

// rewardModels mirrors the dashboard whitelist in apps/dashboard/public/models.js.
// Upstream identifies rewards only through `model`, so both lists must be extended
// together when a new payout name is observed.
var rewardModels = map[string]struct{}{
	"basereward": {},
	"basebonus":  {},
	"bonus":      {},
	"reward":     {},
	"subsidy":    {},
	"incentive":  {},
}

// IsRewardModel reports whether a pseudo model id denotes a reward payout. The
// comparison ignores case and the separators upstream has used so far.
func IsRewardModel(model string) bool {
	var key strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(model)) {
		switch r {
		case '-', '_', '.', ' ':
			continue
		}
		key.WriteRune(r)
	}
	_, ok := rewardModels[key.String()]
	return ok
}

// rewardProvider keeps a blank Provider on reward rows identifiable without
// rewriting rows that already carry a real Provider ID.
func rewardProvider(providerID, model string) string {
	if strings.TrimSpace(providerID) != "" || !IsRewardModel(model) {
		return providerID
	}
	return RewardProviderID
}
