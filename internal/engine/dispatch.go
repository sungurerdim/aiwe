// Package engine dispatches one role run: it resolves the unit's roles (SPEC §9.1), picks an
// assignment entry for each (SPEC §8.1) and hands the prompt built by BuildContract to the provider
// that entry names (SPEC §10.2, §9.2). A judging run is chosen so it differs from the writing run
// wherever the assignment allows (DR-5, SPEC §8.1 point 5).
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sungurerdim/aiwe/internal/adapters"
	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/roles"
)

// The skeleton roles of SPEC §9.1 that dispatch names.
const (
	roleEngineer   = "engineer"
	roleTestAuthor = "test-author"
)

// errNoCapacity is the §7.4 no_capacity refusal: no assignment entry or provider can take the run.
var errNoCapacity = errors.New("no_capacity")

// Independence is the level at which a judging run differs from the writing run (SPEC §8.1 point 5).
type Independence string

const (
	IndependenceProvider Independence = "provider"
	IndependenceModel    Independence = "model"
	IndependenceInstance Independence = "instance"
)

// Assignment is the session's ordered candidate list per role (SPEC §8.1).
type Assignment map[string][]roles.Pin

// DispatchRequest is one role run: the run id, the role, the prompt contract it receives, the
// assignment entry it takes and the run clone its tools stay inside (SPEC §9.2, §10.2, DR-15).
type DispatchRequest struct {
	RunID  string
	Role   string
	Prompt string
	Entry  roles.Pin
	Root   string
}

// DispatchReport is what one provider returned: the run's named outcome and its last message.
type DispatchReport struct {
	Outcome core.Outcome
	Return  string
}

// Runner is the one port a role run reaches a provider through (SPEC §10.2, §5.1): an adapter
// implements it and a fake stands in for it in the tests (SPEC §5.13).
type Runner interface {
	core.Identified
	Run(ctx context.Context, req DispatchRequest) (DispatchReport, error)
}

// Dispatcher delivers a run to the provider its assignment entry names, and to no other.
type Dispatcher struct {
	runners []Runner
}

// NewDispatcher returns the dispatcher over the compiled adapters.
func NewDispatcher(runners ...Runner) *Dispatcher {
	return &Dispatcher{runners: runners}
}

// Dispatch hands req to the runner whose id is the entry's source; an unknown source is no_capacity,
// never a run on another provider (SPEC §7.4, DR-15).
func (d *Dispatcher) Dispatch(ctx context.Context, req DispatchRequest) (DispatchReport, error) {
	if d == nil {
		return DispatchReport{}, fmt.Errorf("%w: no dispatcher", errNoCapacity)
	}
	if req.RunID == "" {
		return DispatchReport{}, errors.New("dispatch: a run without a run id")
	}
	for _, runner := range d.runners {
		if runner.ID() == req.Entry.Source {
			return runner.Run(ctx, req)
		}
	}
	return DispatchReport{}, fmt.Errorf("%w: no provider names source %q", errNoCapacity, req.Entry.Source)
}

// FirstEntry returns the first assignment entry of a role: the skeleton runs on pins and takes the
// first available entry of the session list (SPEC §8.1 point 11). No entry is no_capacity.
func FirstEntry(a Assignment, role string) (roles.Pin, error) {
	for _, entry := range a[role] {
		if entry.Source != "" {
			return entry, nil
		}
	}
	return roles.Pin{}, fmt.Errorf("%w: role %q has no assignment entry", errNoCapacity, role)
}

// PickAuditor returns the auditor's entry and its independence from the writing run (SPEC §8.1 point
// 5, DR-5): another provider's candidate first, then another model of the writer's source, then a
// new instance of the same entry, so one source still works and a different model is not required.
func PickAuditor(candidates []roles.Pin, writer roles.Pin) (roles.Pin, Independence, error) {
	if entry, ok := firstOtherSource(candidates, writer.Source); ok {
		return entry, IndependenceProvider, nil
	}
	if entry, ok := firstOtherModel(candidates, writer); ok {
		return entry, IndependenceModel, nil
	}
	if entry, ok := firstNamed(candidates); ok {
		return entry, IndependenceInstance, nil
	}
	return roles.Pin{}, "", fmt.Errorf("%w: role %q has no assignment entry", errNoCapacity, roleAuditor)
}

// firstOtherSource returns the first candidate of a provider other than source.
func firstOtherSource(candidates []roles.Pin, source string) (roles.Pin, bool) {
	for _, entry := range candidates {
		if entry.Source != "" && entry.Source != source {
			return entry, true
		}
	}
	return roles.Pin{}, false
}

// firstOtherModel returns the first candidate of the writer's provider with another model.
func firstOtherModel(candidates []roles.Pin, writer roles.Pin) (roles.Pin, bool) {
	for _, entry := range candidates {
		if entry.Source == writer.Source && entry.Model != writer.Model {
			return entry, true
		}
	}
	return roles.Pin{}, false
}

// firstNamed returns the first candidate that names a source.
func firstNamed(candidates []roles.Pin) (roles.Pin, bool) {
	for _, entry := range candidates {
		if entry.Source != "" {
			return entry, true
		}
	}
	return roles.Pin{}, false
}

// ResolveRoles returns the roles of one unit in dispatch order (SPEC §9.1): the engineer writes and
// the auditor judges it in a separate run (DR-5); a risky unit runs the test-author first (L17).
func ResolveRoles(files []string, severity string) []string {
	if RiskyUnit(files, severity) {
		return []string{roleTestAuthor, roleEngineer, roleAuditor}
	}
	return []string{roleEngineer, roleAuditor}
}

// RiskyUnit reports whether L17 makes a unit risky: severity critical or high, or a file-set path of
// the §8.2 security table or of the risky segment list.
func RiskyUnit(files []string, severity string) bool {
	if strings.EqualFold(severity, "critical") || strings.EqualFold(severity, "high") {
		return true
	}
	if pathSegmentHolds(files, securitySegments()) {
		return true
	}
	return pathSegmentHolds(files, riskySegments())
}

// securitySegments are the §8.2 path segments that select the security reviewer's table.
func securitySegments() []string {
	return []string{"auth", "login", "session", "token", "secret", "credential", "password",
		"crypto", "permission", "oauth", "webhook"}
}

// riskySegments are the L17 path segments that make a unit risky.
func riskySegments() []string {
	return []string{"identity", "authz", "delete", "purge", "migrat", "routing", "router", "matching"}
}

// pathSegmentHolds reports whether one slash-separated segment of any path holds one of words,
// whatever its case, as §8.2 reads the security and risky tables.
func pathSegmentHolds(files, words []string) bool {
	for _, path := range files {
		for _, segment := range strings.Split(path, "/") {
			segment = strings.ToLower(segment)
			for _, word := range words {
				if strings.Contains(segment, word) {
					return true
				}
			}
		}
	}
	return false
}

// CardText renders one card for the ROLE section of a prompt (SPEC §9.2): its id, purpose, the
// boundaries it may not cross and its method, which is the card body (SPEC §9.1).
func CardText(card roles.Card) string {
	var b strings.Builder
	b.WriteString("id: " + card.ID + "\npurpose: " + card.Purpose + "\n")
	for _, may := range card.May {
		b.WriteString("may: " + may + "\n")
	}
	for _, limit := range card.MayNot {
		b.WriteString("may not: " + limit.Text + " (" + limit.Gate + ")\n")
	}
	b.WriteString("method: " + card.Body)
	return b.String()
}

// runRole builds the role's prompt contract with its card and the stable system prefix, checks the
// §9.2 cap before dispatch, then hands it to the provider its assignment entry names (DR-15).
func (l *lifecycleRun) runRole(ctx context.Context, step, role string,
	entry *roles.Pin, unit, fix string) (roleRun, error) {
	card, ok := roles.Lookup(role)
	if !ok {
		return roleRun{}, l.stop(step, OutcomeRoleUnresolved, fmt.Errorf("no card is shipped for role %q", role))
	}
	pin, err := l.entry(role, entry)
	if err != nil {
		return roleRun{}, l.stop(step, OutcomeNoCapacity, err)
	}
	runID := l.req.NextRunID()
	contract, err := BuildContract(ContractInput{
		RunID: runID, Role: role, Card: CardText(card), Unit: unit, Fix: fix,
		Repository: l.req.Repo.Owner + "/" + l.req.Repo.Name,
		Branch:     l.req.Branch, FileSet: l.unit.Files,
	})
	if err != nil {
		return roleRun{}, l.stop(step, stepOutcomeOf(err, OutcomeEnvironment), err)
	}
	report, err := l.req.Runs.Dispatch(ctx, DispatchRequest{
		RunID: runID, Role: role, Prompt: contract.Text, Entry: pin, Root: l.clone.Dir,
	})
	if err != nil {
		return roleRun{}, l.stop(step, dispatchOutcome(err), err)
	}
	run := roleRun{record: RunRecord{RunID: runID, Role: role, Prompt: contract.Text,
		Bytes: contract.Bytes, Entry: pin}, report: report}
	l.rep.Runs = append(l.rep.Runs, run.record)
	if report.Outcome == adapters.OutcomeFinalAnswer || report.Outcome == core.OutcomeOK {
		return run, nil
	}
	outcome, known := providerStop(report.Outcome)
	if !known {
		return roleRun{}, l.stop(step, OutcomeEnvironment,
			fmt.Errorf("the %s run returned the outcome %q, which is no outcome of SPEC §7.4", role, report.Outcome))
	}
	return roleRun{}, l.stop(step, outcome, fmt.Errorf("the %s run ended %q", role, outcome))
}

// providerStop returns the §7.4 outcome a provider run that did not end well may name: `environment`,
// `timed_out`, `stalled`, `model_mismatch`, `account_restricted`, `no_capacity`, `lost`,
// `permission_blocked`, `answer_truncated` and `token_limit` (SPEC §7.4). It adds the two limits of
// the agent loop, `turn_limit` and `time_limit`, which a run really returns; their names are the
// constants of adapters/loop.go and rows of core/outcomes.go, not copies. An empty word or a word
// outside these is refused by the caller as `environment` (DR-1).
func providerStop(outcome core.Outcome) (StepOutcome, bool) {
	switch outcome {
	case core.Outcome(OutcomeEnvironment), "timed_out", "stalled", "model_mismatch", "account_restricted",
		core.Outcome(OutcomeNoCapacity), "lost", "permission_blocked", "answer_truncated", "token_limit",
		adapters.OutcomeTurnLimit, adapters.OutcomeTimeLimit:
		return StepOutcome(outcome), true
	case core.OutcomeOK, core.OutcomeError, core.OutcomeUsage:
		return "", false
	}
	return "", false
}

// entry returns the assignment entry of a role: the caller's pick, else its first entry (§8.1).
func (l *lifecycleRun) entry(role string, picked *roles.Pin) (roles.Pin, error) {
	if picked != nil {
		return *picked, nil
	}
	return FirstEntry(l.req.Assignment, role)
}

// dispatchOutcome names a dispatch error: a refusal to route is no_capacity, the unit waits; any
// other error is a fault of the tool or the network, `environment` (SPEC §7.4).
func dispatchOutcome(err error) StepOutcome {
	if errors.Is(err, errNoCapacity) {
		return OutcomeNoCapacity
	}
	return OutcomeEnvironment
}

// stepOutcomeOf returns the outcome a contract refusal names, else fallback.
func stepOutcomeOf(err error, fallback StepOutcome) StepOutcome {
	var e *core.Error
	if errors.As(err, &e) && e.Outcome != "" {
		return StepOutcome(e.Outcome)
	}
	return fallback
}

// roleRun is one dispatched role run: the record the report keeps and the provider's report.
type roleRun struct {
	record RunRecord
	report DispatchReport
}
