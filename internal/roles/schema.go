// Package roles holds the skeleton role cards and the pinned model list each role runs on
// (SPEC §9.1, §8.1).
package roles

import (
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Card is one role card: the SPEC §9.1 fields, with the method in Body.
type Card struct {
	ID      string
	Purpose string
	Trigger string
	Needs   string
	Output  string
	Tier    string
	May     []string
	MayNot  []Limit
	Body    string
}

// Limit is one gated boundary of a card.
type Limit struct {
	Text string
	Gate string
}

// Validate refuses a card that is missing a SPEC §9.1 field, has a blank entry, or names a tier
// other than base, raised or top.
func (c Card) Validate() error {
	fields := []struct{ name, value string }{
		{"id", c.ID},
		{"purpose", c.Purpose},
		{"trigger", c.Trigger},
		{"needs", c.Needs},
		{"output", c.Output},
		{"tier", c.Tier},
		{"body", c.Body},
	}
	for _, f := range fields {
		if blank(f.value) {
			return cardInvalid("a non-blank "+f.name, "a blank "+f.name)
		}
	}
	if !validTier(c.Tier) {
		return cardInvalid("tier base, raised or top", c.Tier)
	}
	if err := validateMay(c.May); err != nil {
		return err
	}
	return validateLimits(c.MayNot)
}

func validTier(tier string) bool {
	switch tier {
	case "base", "raised", "top":
		return true
	default:
		return false
	}
}

func blank(s string) bool {
	return strings.TrimSpace(s) == ""
}

func validateMay(may []string) error {
	if len(may) == 0 {
		return cardInvalid("at least one may entry", "no may entry")
	}
	for _, m := range may {
		if blank(m) {
			return cardInvalid("a non-blank may entry", "a blank may entry")
		}
	}
	return nil
}

func validateLimits(limits []Limit) error {
	if len(limits) == 0 {
		return cardInvalid("at least one limit", "no limit")
	}
	for _, m := range limits {
		if blank(m.Text) {
			return cardInvalid("a limit text", "a limit without text")
		}
		if blank(m.Gate) {
			return cardInvalid("a limit gate", "a limit without a gate")
		}
	}
	return nil
}

func cardInvalid(expected, found string) error {
	return &core.Error{Outcome: core.OutcomeError, Expected: expected, Found: found,
		Next: "correct the card to the format of SPEC §9.1"}
}
