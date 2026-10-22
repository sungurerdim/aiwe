package roles

import (
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Severity derives one severity from the affected parties and the class (K8).
//
// The plan names the two axes and no cell that differs by party. The mapping an issue
// record already uses is the table: must is high and nice is low for every non-empty set
// of known parties (customer, owner, admin, developer, system). An empty set is unknown,
// because absence is not a severity (DR-6). A party outside that set, a repeated party,
// or a class other than must or nice is unit_invalid. Party order does not change the result.
func Severity(affected []string, class string) (string, error) {
	if class != "must" && class != "nice" {
		return "", unitInvalid("class must or nice", "class "+class, "name the class must or nice")
	}
	if len(affected) == 0 {
		return "unknown", nil
	}
	if err := validateParties(affected); err != nil {
		return "", err
	}
	if class == "nice" {
		return "low", nil
	}
	return "high", nil
}

func validateParties(affected []string) error {
	seen := make(map[string]bool, len(affected))
	for _, raw := range affected {
		name := strings.TrimSpace(raw)
		if !knownParty(name) {
			return unitInvalid("an affected party the severity table lists", "party "+maskSecrets(name),
				"name one or more of customer, owner, admin, developer, system")
		}
		if seen[name] {
			return unitInvalid("each affected party once", "a repeated "+name,
				"list each affected party once")
		}
		seen[name] = true
	}
	return nil
}

func knownParty(name string) bool {
	switch name {
	case "customer", "owner", "admin", "developer", "system":
		return true
	default:
		return false
	}
}

func unitInvalid(expected, found, next string) error {
	return &core.Error{Outcome: "unit_invalid", Expected: expected, Found: found, Next: next}
}
