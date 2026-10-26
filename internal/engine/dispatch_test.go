package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/roles"
	"github.com/sungurerdim/aiwe/internal/tracker"
)

// TestRiskyUnit covers L17: a unit is risky when its severity is critical or high, or when its file
// set carries a path of the security table or of the risky segment list.
func TestRiskyUnit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		severity string
		files    []string
		want     bool
	}{
		{"a critical unit is risky", "critical", lifecycleTestFileSet(), true},
		{"a high unit is risky", "high", lifecycleTestFileSet(), true},
		{"a medium unit on engine code is not risky", "medium", lifecycleTestFileSet(), false},
		{"a medium unit on a security path is risky", "medium", []string{"internal/engine/token_io.go"}, true},
		{"a path segment that holds auth is in the security table", "low", []string{"internal/engine/oauth_login.go"}, true},
		{"a session path is in the security table", "low", []string{"internal/engine/session.go"}, true},
		{"an identity path is in the risky list", "low", []string{"internal/engine/identity.go"}, true},
		{"a migration path is in the risky list", "low", []string{"internal/engine/migration.go"}, true},
		{"a matching path is in the risky list", "low", []string{"internal/engine/matching.go"}, true},
		{"a segment that merely holds a security word still matches", "low", []string{"internal/engine/author.go"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := RiskyUnit(tc.files, tc.severity); got != tc.want {
				t.Fatalf("RiskyUnit(%v, %q) = %v, want %v", tc.files, tc.severity, got, tc.want)
			}
		})
	}
}

// TestResolveRoles covers §9.1: every skeleton unit runs an engineer and an auditor, and a risky
// unit runs the test-author first.
func TestResolveRoles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		severity string
		files    []string
		want     []string
	}{
		{"a risky unit runs test-author before engineer", "high", lifecycleTestFileSet(),
			[]string{roleTestAuthor, roleEngineer, roleAuditor}},
		{"a plain unit runs engineer then auditor", "medium", lifecycleTestFileSet(),
			[]string{roleEngineer, roleAuditor}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveRoles(tc.files, tc.severity)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("ResolveRoles = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPickAuditor covers §8.1 point 5 and DR-5: another provider's candidate comes first, then
// another model of the writer's source, then a new instance of the same entry, so one source works.
func TestPickAuditor(t *testing.T) {
	t.Parallel()
	writer := roles.Pin{Source: "sub-a", Model: "model-a", Effort: "high"}
	tests := []struct {
		name         string
		candidates   []roles.Pin
		wantSource   string
		wantModel    string
		independence Independence
		wantErr      bool
	}{
		{
			name: "another provider is preferred over the writer's own source",
			candidates: []roles.Pin{
				{Source: "sub-a", Model: "model-a", Effort: "high"},
				{Source: "api-b", Model: "model-b", Effort: "high"},
			},
			wantSource: "api-b", wantModel: "model-b", independence: IndependenceProvider,
		},
		{
			name: "another model of the writer's source comes before a new instance",
			candidates: []roles.Pin{
				{Source: "sub-a", Model: "model-b", Effort: "high"},
				{Source: "sub-a", Model: "model-c", Effort: "high"},
			},
			wantSource: "sub-a", wantModel: "model-b", independence: IndependenceModel,
		},
		{
			name:         "one entry is a new instance",
			candidates:   []roles.Pin{writer},
			wantSource:   "sub-a",
			wantModel:    "model-a",
			independence: IndependenceInstance,
		},
		{
			name:       "no candidate is no_capacity",
			candidates: nil,
			wantErr:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, independence, err := PickAuditor(tc.candidates, writer)
			if tc.wantErr {
				if err == nil {
					t.Fatal("accepted an empty auditor assignment")
				}
				return
			}
			if err != nil {
				t.Fatalf("PickAuditor: %v", err)
			}
			if got.Source != tc.wantSource || got.Model != tc.wantModel || independence != tc.independence {
				t.Fatalf("PickAuditor = %q/%q/%q, want %q/%q/%q",
					got.Source, got.Model, independence, tc.wantSource, tc.wantModel, tc.independence)
			}
		})
	}
}

// TestPromptOverTheCapIsRefusedBeforeDispatch proves every run's prompt is built by BuildContract and
// checked against the §9.2 cap before a provider sees it: over the cap names prompt_too_large and no
// run leaves (SPEC §9.2, SPEC §7.3 step 5).
func TestPromptOverTheCapIsRefusedBeforeDispatch(t *testing.T) {
	t.Parallel()
	f := newLifecycleFixture(t, "medium")
	f.body += strings.Repeat("The engine has no lifecycle. ", 1400)
	rep, err := RunUnit(t.Context(), f.request())
	if err != nil {
		t.Fatalf("RunUnit: %v", err)
	}
	last := rep.Steps[len(rep.Steps)-1]
	if last.Step != StepWrite || last.Outcome != OutcomePromptTooLarge {
		t.Fatalf("last step = %s/%s, want write/prompt_too_large", last.Step, last.Outcome)
	}
	if len(rep.Runs) != 0 || len(f.runner.runs) != 0 {
		t.Fatalf("the run left %d/%d role runs over the cap, want none", len(rep.Runs), len(f.runner.runs))
	}
}

// TestDispatcherRoutesTheRunToItsProvider proves the dispatcher hands a run only to the provider its
// assignment entry names, so a role never runs on a source it was not assigned (DR-15, SPEC §8.1).
func TestDispatcherRoutesTheRunToItsProvider(t *testing.T) {
	t.Parallel()
	first := &fakeRunner{id: "sub-a"}
	second := &fakeRunner{id: "api-b"}
	d := NewDispatcher(first, second)
	req := DispatchRequest{RunID: "run-1", Role: roleEngineer, Prompt: "ROLE\n...",
		Entry: roles.Pin{Source: "api-b", Model: "model-b", Effort: "high"}}
	if _, err := d.Dispatch(t.Context(), req); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(first.runs) != 0 || len(second.runs) != 1 {
		t.Fatalf("dispatcher reached sub-a %d times and api-b %d times, want 0 and 1", len(first.runs), len(second.runs))
	}
}

// TestDispatcherRefusesAnEntryNoRunnerNames covers the fail-closed dispatch: a candidate whose
// source has no runner is refused, never routed to another provider (SPEC §7.4 no_capacity).
func TestDispatcherRefusesAnEntryNoRunnerNames(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(&fakeRunner{id: "sub-a"})
	req := DispatchRequest{RunID: "run-1", Role: roleEngineer, Prompt: "ROLE\n...",
		Entry: roles.Pin{Source: "api-b", Model: "model-b", Effort: "high"}}
	_, err := d.Dispatch(context.Background(), req)
	if err == nil {
		t.Fatal("dispatched a run to a source no provider names")
	}
	if !errors.Is(err, errNoCapacity) {
		t.Fatalf("dispatch refusal = %v, want no_capacity", err)
	}
}

// TestRoleRunStopsWithANamedOutcome covers the DR-1 stop of a role run that did not end well: a
// provider that returns nothing, and a transport error, each name the §7.4 outcome of the fault
// (`environment`), never an empty word and never the `no_capacity` wait.
//
// rule: DR-1
func TestRoleRunStopsWithANamedOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prepare func(*lifecycleFixture)
	}{
		{"a provider that returns an empty report", func(f *lifecycleFixture) {
			f.runner.report = func(DispatchRequest) DispatchReport { return DispatchReport{} }
		}},
		{"a transport error of the provider", func(f *lifecycleFixture) {
			f.runner.err = errors.New("read tcp: connection reset by peer")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newLifecycleFixture(t, "medium")
			tc.prepare(f)
			rep, err := RunUnit(t.Context(), f.request())
			if err != nil {
				t.Fatalf("RunUnit: %v", err)
			}
			last := rep.Steps[len(rep.Steps)-1]
			if last.Step != StepWrite || last.Outcome != "environment" || rep.Outcome != "environment" {
				t.Fatalf("last step = %s/%q, report = %q, want write/environment", last.Step, last.Outcome, rep.Outcome)
			}
			if last.Detail == "" {
				t.Fatal("the stop names no cause")
			}
		})
	}
}

// TestRoleWithoutACardIsRoleUnresolved covers SPEC §7.4: a role that ships no card names
// `role_unresolved`, not the `no_capacity` wait, and no run leaves.
func TestRoleWithoutACardIsRoleUnresolved(t *testing.T) {
	t.Parallel()
	f := newLifecycleFixture(t, "medium")
	l := &lifecycleRun{req: f.request()}
	if _, err := l.runRole(t.Context(), StepWrite, "no-such-role", nil, "", ""); err == nil {
		t.Fatal("ran a role that ships no card")
	}
	last := l.rep.Steps[len(l.rep.Steps)-1]
	if last.Outcome != "role_unresolved" || l.rep.Outcome != "role_unresolved" {
		t.Fatalf("outcome = %q, report = %q, want role_unresolved", last.Outcome, l.rep.Outcome)
	}
	if len(f.runner.runs) != 0 {
		t.Fatalf("%d runs left for a role with no card, want none", len(f.runner.runs))
	}
}

// failingTracker is a tracker whose read fails, as a lost network does.
type failingTracker struct{ tracker.Tracker }

// ReadUnits fails with the transport error.
func (failingTracker) ReadUnits(context.Context, tracker.RepoID) ([]tracker.Unit, error) {
	return nil, errors.New("read tcp: connection reset by peer")
}

// TestAFaultOfATaskIsEnvironment covers SPEC §7.4: a tool, network or git fault in a step names
// `environment`, not a generic word that has no row of the §7.4 table.
//
// rule: DR-1
func TestAFaultOfATaskIsEnvironment(t *testing.T) {
	t.Parallel()
	boom := errors.New("the tool failed")
	tests := []struct {
		name     string
		prepare  func(*lifecycleFixture, *LifecycleRequest)
		wantStep string
	}{
		{"the tracker read fails", func(_ *lifecycleFixture, r *LifecycleRequest) {
			r.Tracker = failingTracker{r.Tracker}
		}, StepIntake},
		{"the claim table fails", func(f *lifecycleFixture, _ *LifecycleRequest) { f.claimer.err = boom }, StepClaim},
		{"the verify cannot run", func(f *lifecycleFixture, _ *LifecycleRequest) { f.verifier.err = boom }, StepVerify},
		{"the collect fails", func(f *lifecycleFixture, _ *LifecycleRequest) { f.collector.err = boom }, StepCollect},
		{"the run branch holds no commit", func(f *lifecycleFixture, _ *LifecycleRequest) { f.collector.tip = "" }, StepCollect},
		{"the diff cannot be read", func(f *lifecycleFixture, _ *LifecycleRequest) { f.differ.err = boom }, StepScope},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newLifecycleFixture(t, "medium")
			req := f.request()
			tc.prepare(f, &req)
			rep, err := RunUnit(t.Context(), req)
			if err != nil {
				t.Fatalf("RunUnit: %v", err)
			}
			last := rep.Steps[len(rep.Steps)-1]
			if last.Step != tc.wantStep || last.Outcome != "environment" || rep.Outcome != "environment" {
				t.Fatalf("last step = %s/%q, report = %q, want %s/environment", last.Step, last.Outcome, rep.Outcome, tc.wantStep)
			}
		})
	}
}

// TestAnUnknownProviderOutcomeIsRefused covers DR-1 and DR-6: a provider word that is no row of the
// outcome table is refused as `environment` and never reaches Accept as the run's outcome.
//
// rule: DR-1
func TestAnUnknownProviderOutcomeIsRefused(t *testing.T) {
	t.Parallel()
	f := newLifecycleFixture(t, "medium")
	f.runner.report = func(DispatchRequest) DispatchReport { return DispatchReport{Outcome: "bananaß日本"} }
	rep, err := RunUnit(t.Context(), f.request())
	if err != nil {
		t.Fatalf("RunUnit: %v", err)
	}
	last := rep.Steps[len(rep.Steps)-1]
	if last.Outcome != "environment" || rep.Outcome != "environment" {
		t.Fatalf("last outcome = %q, report = %q, want environment", last.Outcome, rep.Outcome)
	}
	if !strings.Contains(last.Detail, "bananaß日本") {
		t.Fatalf("the stop does not name the refused word: %q", last.Detail)
	}
	if acc, _ := Accept(rep); acc.Outcome == "bananaß日本" {
		t.Fatalf("Accept took the provider's own word %q as the outcome", acc.Outcome)
	}
}

// TestAProviderOutcomeOfTheTableIsPassedOn covers SPEC §7.4: every outcome the table names for a run
// that did not end well reaches the report unchanged, so a trimmed allow-list fails. The words are the
// ones SPEC §7.4 defines for a role run: `environment` (line 378), `timed_out` and `stalled` (380),
// `model_mismatch` and `account_restricted` (381), `no_capacity` (382), `lost` (388),
// `permission_blocked` (390), `answer_truncated` and `token_limit` (391). The agent loop adds
// `turn_limit` and `time_limit` (core/outcomes.go:116-117, adapters/loop.go:17-18), which a run
// really returns, so they pass too; any other word is refused as `environment`.
//
// rule: DR-1
func TestAProviderOutcomeOfTheTableIsPassedOn(t *testing.T) {
	t.Parallel()
	passed := []string{"environment", "timed_out", "stalled", "model_mismatch", "account_restricted",
		"no_capacity", "lost", "permission_blocked", "answer_truncated", "token_limit",
		"turn_limit", "time_limit"}
	for _, word := range passed {
		t.Run(word, func(t *testing.T) {
			t.Parallel()
			f := newLifecycleFixture(t, "medium")
			f.runner.report = func(DispatchRequest) DispatchReport {
				return DispatchReport{Outcome: core.Outcome(word)}
			}
			rep, err := RunUnit(t.Context(), f.request())
			if err != nil {
				t.Fatalf("RunUnit: %v", err)
			}
			want := StepOutcome(word)
			last := rep.Steps[len(rep.Steps)-1]
			if last.Outcome != want || rep.Outcome != want {
				t.Fatalf("provider word %q: last = %q, report = %q, want %q", word, last.Outcome, rep.Outcome, want)
			}
		})
	}
}
