package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// unitCheckBodyParam is the parameter a `unit check` call carries the unit body in. The shared
// dictionary names `body` with that one meaning, so every surface can fill it (SPEC §5.7).
const unitCheckBodyParam = "body"

// Commands returns the engine commands of the one command registry (SPEC §5.7): `unit check` reads
// a unit body against the issue standard, so a session writes a unit from the template and refuses
// a malformed one before the tracker holds it (SPEC §7.2, §8.4).
func Commands() []core.Command {
	return []core.Command{
		{Module: "engine", Object: "unit", Verb: "check", Params: []string{"format", unitCheckBodyParam},
			Purpose: "check a unit body against the issue standard and name every fault",
			Run:     runUnitCheck},
	}
}

// UnitCheckInput is one `unit check`: the body of the unit to check.
type UnitCheckInput struct {
	Body string
}

// UnitCheck reads in.Body as a unit issue body and returns the file set, the verify commands and
// the test items it holds. A body that breaks the standard is refused with the fault named — a
// missing section, a To do item without its verify and signal, or a file set over the ceiling of
// DR-7 — so only a well-formed unit body passes (SPEC §8.4).
func UnitCheck(in UnitCheckInput) (core.Result[any], error) {
	unit, err := ParseUnit(in.Body)
	if err != nil {
		return core.Result[any]{}, unitRefusal(err)
	}
	return core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK, Data: unit,
		Detail: unitDetail(unit)}, nil
}

// unitRefusal maps one ParseUnit refusal to the error `unit check` prints: the sentinel it wraps
// names the outcome row of the table, so the reader gets that row's status and next action, and the
// fault stays in found (SPEC §5.3, §8.4). A refusal with no row of its own stays an error (DR-6).
func unitRefusal(err error) error {
	for _, sentinel := range []error{ErrUnitSectionMissing, ErrUnitBodyTooLarge, ErrUnitPathUnsafe,
		ErrUnitVerifyUnsafe, ErrUnitScopeTooWide, ErrUnitVerifyUnresolved} {
		if errors.Is(err, sentinel) {
			return &core.Error{Outcome: core.Outcome(sentinel.Error()), Err: err,
				Expected: "a unit body that follows the issue standard", Found: err.Error()}
		}
	}
	return err
}

// unitDetail is the one line a passing check prints: what the engine will hold the unit to.
func unitDetail(unit Unit) string {
	return fmt.Sprintf("unit check: %d files, %d verify, %d test items: %s",
		len(unit.Files), len(unit.Verify), len(unit.Tests), strings.Join(unit.Files, ", "))
}

// runUnitCheck checks the unit body the call carries in unitCheckBodyParam, a declared parameter of
// the command and a name of the shared dictionary. A call without a body is refused with the next
// action: the check never passes a unit the engine did not read (DR-6).
func runUnitCheck(_ context.Context, in core.Input) (core.Result[any], error) {
	body := in.Params[unitCheckBodyParam]
	if strings.TrimSpace(body) == "" {
		return core.Result[any]{}, &core.Error{Outcome: core.OutcomeUsage,
			Expected: "the unit body to check",
			Found:    "no unit body",
			Next:     "pass the unit body the session wrote, then run the check again"}
	}
	return UnitCheck(UnitCheckInput{Body: body})
}
