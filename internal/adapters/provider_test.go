package adapters

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The fixtures the tests of this package share: the run id of one turn in the shape a run uses, its
// role and task, and the build version the User-Agent names (SPEC §5.13).
const (
	chatRunID   = "01JQ4W8Q3F0V2T6M9B5K7ZP1RH"
	chatRole    = "test-author"
	chatTask    = "write the failing test of the provider port"
	chatVersion = "0.2.3"
)

// testLog returns the one logger of SPEC §5.3 over a buffer a test reads for a leaked credential.
func testLog(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	lines := &bytes.Buffer{}
	return core.NewLog(lines, slog.LevelDebug), lines
}

// contractFixture is one provider under test: the factory, the key value that must never leave the
// environment, the environment with and without that key, a logger whose buffer a test reads, and
// the request the provider must serve (SPEC §5.13, DR-19).
type contractFixture struct {
	New    func(env Env, log *slog.Logger) Provider
	Key    string
	Env    Env
	Absent Env
	Log    *slog.Logger
	Lines  *bytes.Buffer
	Run    RunRequest
}

// providerContract runs the provider port contract (SPEC §5.13, §10.2) against every implementation
// and fake: one stable id, one capability vocabulary, detection with and without a credential, one
// run request with one result vocabulary, and the rule that a credential never leaves the
// environment — not into a log line, an error or a report (DR-19).
func providerContract(t *testing.T, f contractFixture) {
	t.Helper()
	p := f.New(f.Env, f.Log)
	if p.ID() == "" {
		t.Error("provider contract: ID() is empty; every provider needs a stable id (SPEC §5.1)")
	}
	caps := p.Capabilities()
	if caps.Kind != KindModelAPI && caps.Kind != KindAgentRuntime {
		t.Errorf("provider contract: Capabilities().Kind is %q, want %q or %q (O14)",
			caps.Kind, KindModelAPI, KindAgentRuntime)
	}
	if caps.Kind == KindModelAPI {
		if len(caps.Models) == 0 {
			t.Error("provider contract: a model API offers no model, so no run can choose one (SPEC §8.1)")
		}
		if !caps.ReportsModel {
			t.Error("provider contract: a model API reports the model its reply names (SPEC §10.2)")
		}
		if caps.Resume {
			t.Error("provider contract: a model API has no session to resume (SPEC §10.2)")
		}
	}
	if got := p.Detect(context.Background(), f.Env); !got.Present {
		t.Errorf("provider contract: Detect with the credential present reports present=false: %+v", got)
	}
	if got := p.Detect(context.Background(), f.Absent); got.Present || got.Reason == "" {
		t.Errorf("provider contract: Detect without the credential reported present=%v reason=%q, "+
			"want absent with a reason (DR-6)", got.Present, got.Reason)
	}
	res, err := p.Run(context.Background(), f.Run)
	if err != nil {
		t.Fatalf("provider contract: Run: %v", err)
	}
	if res.Status != core.StatusOK || res.Outcome != core.OutcomeOK {
		t.Errorf("provider contract: Run returned %s/%s, want %s/%s (SPEC §10.2)",
			res.Status, res.Outcome, core.StatusOK, core.OutcomeOK)
	}
	if res.Data.Served == "" {
		t.Error("provider contract: the report names no served model (SPEC §10.2)")
	}
	if f.Lines != nil && strings.Contains(f.Lines.String(), f.Key) {
		t.Errorf("provider contract: the credential reached a log line: %s (DR-19)", f.Lines)
	}
	if text := fmt.Sprintf("%+v", res); strings.Contains(text, f.Key) {
		t.Errorf("provider contract: the credential reached the result: %s (DR-19)", text)
	}
}

// fakeProvider is the port's fake: a model API that answers from its own model list, so the contract
// suite has a second implementation and a defect in it shows without a network (SPEC §5.13).
type fakeProvider struct {
	id     string
	keyEnv string
	models []string
}

// ID returns the id of the fake.
func (f fakeProvider) ID() string { return f.id }

// Capabilities returns one model API that offers the models of the fixture.
func (f fakeProvider) Capabilities() Capabilities {
	return Capabilities{Kind: KindModelAPI, Models: f.models, ReportsModel: true}
}

// Detect reads the credential of the fake from env, as a model API does.
func (f fakeProvider) Detect(_ context.Context, env Env) Detection {
	if _, err := Key(env, f.keyEnv); err != nil {
		return Detection{Reason: "harness_missing"}
	}
	return Detection{Present: true, Version: "1"}
}

// Run refuses a request without a role or a task, so a vacuous fake cannot pass the contract.
func (f fakeProvider) Run(_ context.Context, req RunRequest) (core.Result[RunReport], error) {
	if req.Role == "" || req.Task == "" {
		return core.Result[RunReport]{}, fmt.Errorf("fake provider: expected a role and a task; "+
			"found role %q and task %q", req.Role, req.Task)
	}
	model := req.Model
	if model == "" {
		model = f.models[0]
	}
	usage := Usage{Prompt: core.Measured[int64]{Value: 12, Known: true}}
	report := RunReport{Served: model, Usage: usage}
	return core.Result[RunReport]{Status: core.StatusOK, Outcome: core.OutcomeOK, Data: report}, nil
}

// TestProviderContractFake catches a port whose vocabulary a fake cannot satisfy: the contract suite
// runs against the fake first, before any client does (SPEC §5.13).
func TestProviderContractFake(t *testing.T) {
	log, lines := testLog(t)
	fake := func(Env, *slog.Logger) Provider {
		return fakeProvider{id: "fake-model-api", keyEnv: envKeyVar, models: []string{DefaultModel}}
	}
	env := fixtureEnv{envKeyVar + "=" + envSecret}
	tools := ToolPermissions{Read: true, Edit: true}
	providerContract(t, contractFixture{
		New:    fake,
		Key:    envSecret,
		Env:    env,
		Absent: fixtureEnv{"HOME=/tmp"},
		Log:    log,
		Lines:  lines,
		Run:    RunRequest{RunID: chatRunID, Role: chatRole, Task: chatTask, Tools: tools},
	})
}
