package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// usageAnswer is the answer of the usage read: rolling, weekly and monthly percent with the reset
// time of each window (SPEC §10.2, §7.6). The endpoint is undocumented, so this is the recorded shape
// of the live probe of 2026-10-12 (SPEC §17).
const usageAnswer = `{"rolling":{"percent":42.5,"resets_at":"2026-10-12T13:00:00Z"},` +
	`"weekly":{"percent":7.25,"resets_at":"2026-10-13T00:00:00Z"},` +
	`"monthly":{"percent":3.5,"resets_at":"2026-11-01T00:00:00Z"}}`

// outcomeName matches the snake_case name of an outcome (SPEC §5.3).
var outcomeName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

// usageResets returns the reset times of the three windows of usageAnswer.
func usageResets() (rolling, weekly, monthly time.Time) {
	rolling = time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	weekly = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	monthly = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	return rolling, weekly, monthly
}

// noDial is a transport that fails the test when anything dials the provider: detecting a model API
// and refusing a run read the environment, never the network (SPEC §10.2, DR-19).
type noDial struct{ t *testing.T }

// RoundTrip reports the request that must not have left.
func (d noDial) RoundTrip(r *http.Request) (*http.Response, error) {
	d.t.Errorf("a request left for %s, want none before the turn (SPEC §10.2)", r.URL)
	return nil, errors.New("no dial")
}

// newGoFixture returns the OpenCode Go provider of the tests: it talks to the fake host of f, reads
// its key from env at run time and logs through log. Its turn and time limits are small, so a
// fixture that never answers with a final answer ends the run instead of holding the test
// (SPEC §10.2, DR-1).
func newGoFixture(f *fakeHost, env Env, log *slog.Logger) *OpenCodeGo {
	cfg := OpenCodeGoConfig{Env: env, Base: f.srv.URL, Version: chatVersion, Log: log, HTTP: f.srv.Client(),
		MaxTurns: 5, TimeLimit: time.Minute}
	return NewOpenCodeGo(cfg)
}

// goFinalReply is one chat reply that ends a role run: the served model, the final answer with no
// tool call and every token count (SPEC §10.2).
func goFinalReply() string {
	return replyTurn(DefaultModel, assistantText("done"), usageFull)
}

// registerGoAnswers registers the answer of both endpoints a provider declares: the usage read and
// the chat turn (SPEC §10.2).
func registerGoAnswers(t *testing.T, f *fakeHost, p *OpenCodeGo, usage, reply func(w http.ResponseWriter)) {
	t.Helper()
	f.serveURL(t, p.Endpoints().Usage, usage)
	f.serveURL(t, p.Endpoints().Chat, reply)
}

// checkEndpoint fails when an endpoint is not the documented URL of the Go quota: only /zen/go/v1 is
// used, never the credit path /zen/v1 (SPEC §10.2).
func checkEndpoint(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("Endpoints().%s = %q, want %q; only the Go quota path is used (SPEC §10.2)", name, got, want)
	}
	if strings.Contains(got, "/zen/v1") {
		t.Errorf("Endpoints().%s = %q names the credit path /zen/v1, which a Go subscription never uses (SPEC §10.2)", name, got)
	}
}

// checkQuota compares one quota with the windows a row names, percent and reset time each, so an
// absent window stays unknown (SPEC §7.6, DR-6).
func checkQuota(t *testing.T, got, want Quota, catch string) {
	t.Helper()
	names := []string{"rolling", "weekly", "monthly"}
	windows := []Window{got.Rolling, want.Rolling, got.Weekly, want.Weekly, got.Monthly, want.Monthly}
	for i, name := range names {
		seen, expected := windows[2*i], windows[2*i+1]
		checkMeasured(t, name+" percent", seen.Percent, expected.Percent, catch)
		if seen.Reset.Known != expected.Reset.Known {
			t.Fatalf("%s reset: known = %v (at %v), want known = %v; %s", name, seen.Reset.Known, seen.Reset.At, expected.Reset.Known, catch)
		}
		if expected.Reset.Known && !seen.Reset.At.Equal(expected.Reset.At) {
			t.Errorf("%s reset at %v, want %v; %s", name, seen.Reset.At, expected.Reset.At, catch)
		}
	}
}

// TestOpenCodeGoPinnedDefaults catches a client pointed at the credit endpoint, another host or
// another default model: the Go quota URLs and the model the skeleton runs are the pinned ones
// (SPEC §10.2, O14).
func TestOpenCodeGoPinnedDefaults(t *testing.T) {
	log, _ := testLog(t)
	p := NewOpenCodeGo(OpenCodeGoConfig{Env: ProcessEnv(), Log: log})
	ep := p.Endpoints()
	checkEndpoint(t, "Chat", ep.Chat, "https://opencode.ai/zen/go/v1/chat/completions")
	checkEndpoint(t, "Usage", ep.Usage, "https://opencode.ai/zen/go/v1/usage")
	if DefaultModel != "deepseek-v4.1-flash" {
		t.Errorf("DefaultModel = %q, want the model the skeleton runs (SPEC §10.2)", DefaultModel)
	}
	if p.cfg.TimeLimit != 60*time.Minute {
		t.Errorf("a provider built without a time limit runs %v, want the 60 min run limit default, so a role run "+
			"never waits without a limit (SPEC §7.4, DR-1)", p.cfg.TimeLimit)
	}
}

// TestOpenCodeGoRunsARoleThroughTheLoop is the done criterion of the loop: a role runs end to end
// through the provider. It reads the Go quota once, runs the tool call of the first turn inside the
// clone, feeds its result back and ends on the final answer, and the result names the turns, the
// usage of every turn and the quota (SPEC §10.2, O14).
func TestOpenCodeGoRunsARoleThroughTheLoop(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(envKeyVar, envSecret)
	root := t.TempDir()
	f := newFakeHost(t)
	p := newGoFixture(f, ProcessEnv(), log)
	registerGoAnswers(t, f, p, answerJSON(usageAnswer), scriptedAnswers(
		replyTurn(DefaultModel, assistantToolCall("call_1", ToolWrite, `{"path":"out.txt","content":"hello"}`), usageFull),
		goFinalReply(),
	))
	req := RunRequest{RunID: chatRunID, Role: chatRole, Task: chatTask, Tools: ToolPermissions{Write: true}, Root: root}
	res, err := p.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != core.StatusOK || res.Outcome != core.OutcomeOK || res.Data.Outcome != OutcomeFinalAnswer {
		t.Fatalf("Run returned %s/%s with outcome %q, want %s/%s with %q (SPEC §10.2)",
			res.Status, res.Outcome, res.Data.Outcome, core.StatusOK, core.OutcomeOK, OutcomeFinalAnswer)
	}
	if res.Data.Turns != 2 {
		t.Errorf("the run took %d turns, want the tool turn and the final answer (SPEC §10.2)", res.Data.Turns)
	}
	if data, err := os.ReadFile(filepath.Join(root, "out.txt")); err != nil || string(data) != "hello" {
		t.Errorf("the clone holds %q, %v, want the file %q the tool turn wrote: the provider ran no loop (SPEC §10.2, O14)",
			data, err, "hello")
	}
	got := f.requests()
	if len(got) != 3 {
		t.Fatalf("the provider made %d requests, want the usage read and the two turns (SPEC §10.2)", len(got))
	}
	if got[0].Path != endpointPath(t, p.Endpoints().Usage) {
		t.Errorf("the first request is %s, want the usage read before the first turn (SPEC §10.2)", got[0].Path)
	}
	checkHeader(t, got[2], openCodeSessionKey, chatRunID)
	checkMeasured(t, "prompt tokens", res.Data.Usage.Prompt, measured(2400), "the report sums the prompt tokens of both turns")
	if !res.Data.Quota.Rolling.Percent.Known {
		t.Errorf("the report holds quota %+v, want the rolling window the usage read named (SPEC §10.2)", res.Data.Quota)
	}
}

// TestOpenCodeGoEndsOnTheTurnLimit catches a provider that reports a role it stopped at the turn
// limit as done: the result carries the turn_limit outcome and its error status, and the provider
// sends no turn past the limit (SPEC §5.3, §10.2).
func TestOpenCodeGoEndsOnTheTurnLimit(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(envKeyVar, envSecret)
	f := newFakeHost(t)
	cfg := OpenCodeGoConfig{Env: ProcessEnv(), Base: f.srv.URL, Version: chatVersion, Log: log, HTTP: f.srv.Client(),
		MaxTurns: 2, TimeLimit: time.Minute}
	p := NewOpenCodeGo(cfg)
	registerGoAnswers(t, f, p, answerJSON(usageAnswer), answerJSON(replyTurn(DefaultModel,
		assistantToolCall("call", ToolList, `{"path":"."}`), usageFull)))
	req := RunRequest{RunID: chatRunID, Role: chatRole, Task: chatTask, Tools: ToolPermissions{List: true}, Root: t.TempDir()}
	res, err := p.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v; a turn limit is a named outcome, not an error (SPEC §10.2)", err)
	}
	checkStoppedShort(t, res, OutcomeTurnLimit)
	if got := len(f.requests()); got != 3 {
		t.Errorf("the provider made %d requests, want the usage read and the two turns of the limit (SPEC §10.2)", got)
	}
}

// TestOpenCodeGoRunsOneTurn is the done criterion: one turn through the provider reads the Go quota
// first, sends one chat turn to the Go quota endpoint with the required headers, and returns the
// served model, its four token counts, the cost of the pinned price table and the quota (SPEC §10.2,
// O14).
func TestOpenCodeGoRunsOneTurn(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(envKeyVar, envSecret)
	f := newFakeHost(t)
	p := newGoFixture(f, ProcessEnv(), log)
	registerGoAnswers(t, f, p, answerJSON(usageAnswer), answerJSON(goFinalReply()))
	res, err := p.Run(context.Background(), fixtureRequest(""))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != core.StatusOK || res.Outcome != core.OutcomeOK {
		t.Fatalf("Run returned %s/%s, want %s/%s (SPEC §10.2)", res.Status, res.Outcome, core.StatusOK, core.OutcomeOK)
	}
	ep := p.Endpoints()
	got := f.requests()
	if len(got) != 2 {
		t.Fatalf("the provider made %d requests, want the usage read and one chat turn (SPEC §10.2)", len(got))
	}
	if got[0].Method != http.MethodGet || got[0].Path != endpointPath(t, ep.Usage) {
		t.Errorf("the first request is %s %s, want the usage read at %s before the turn (SPEC §10.2)", got[0].Method, got[0].Path, ep.Usage)
	}
	turn := got[1]
	if turn.Method != http.MethodPost || turn.Path != endpointPath(t, ep.Chat) {
		t.Errorf("the turn is %s %s, want POST %s (SPEC §10.2)", turn.Method, turn.Path, ep.Chat)
	}
	checkHeader(t, turn, "Authorization", "Bearer "+envSecret)
	checkHeader(t, turn, "x-opencode-session", chatRunID)
	checkHeader(t, turn, "User-Agent", core.Product+"/"+chatVersion)
	checkHeader(t, got[0], "Authorization", "Bearer "+envSecret)
	var payload fakePayload
	if err := json.Unmarshal(turn.Body, &payload); err != nil {
		t.Fatalf("the turn body is not JSON: %v; body %s", err, turn.Body)
	}
	if payload.Model != DefaultModel {
		t.Errorf("a request that names no model sent %q, want the default %q (SPEC §10.2)", payload.Model, DefaultModel)
	}
	if res.Data.Served != DefaultModel {
		t.Errorf("the report names %q, want the model the reply names, %q (SPEC §10.2)", res.Data.Served, DefaultModel)
	}
	checkMeasured(t, "prompt tokens", res.Data.Usage.Prompt, measured(1200), "the reply names 1200 prompt tokens")
	checkMeasured(t, "cached tokens", res.Data.Usage.Cached, measured(800), "the reply names 800 cached tokens")
	checkMeasured(t, "completion tokens", res.Data.Usage.Completion, measured(300), "the reply names 300 completion tokens")
	checkMeasured(t, "reasoning tokens", res.Data.Usage.Reasoning, measured(120), "the reply names 120 reasoning tokens")
	if !res.Data.Cost.Known || res.Data.Cost.Value <= 0 {
		t.Errorf("the cost is %+v, want the cost of the pinned price table of the served model (SPEC §10.2)", res.Data.Cost)
	}
	rolling, weekly, monthly := usageResets()
	quota := Quota{
		Rolling: Window{Percent: core.Measured[float64]{Value: 42.5, Known: true}, Reset: Reset{At: rolling, Known: true}},
		Weekly:  Window{Percent: core.Measured[float64]{Value: 7.25, Known: true}, Reset: Reset{At: weekly, Known: true}},
		Monthly: Window{Percent: core.Measured[float64]{Value: 3.5, Known: true}, Reset: Reset{At: monthly, Known: true}},
	}
	checkQuota(t, res.Data.Quota, quota, "the usage answer names rolling, weekly and monthly percent with their reset time (SPEC §10.2)")
}

// TestOpenCodeGoUnreadableQuotaIsUnknown catches a quota read that fails a run or invents a measured
// 0: an unreadable usage answer leaves every window unknown, and the turn runs (SPEC §10.2, DR-6).
func TestOpenCodeGoUnreadableQuotaIsUnknown(t *testing.T) {
	tests := []struct {
		name   string
		answer func(w http.ResponseWriter)
		want   Quota
		catch  string
	}{
		{"an error status", answerStatus(http.StatusInternalServerError, `{"error":"unavailable"}`), Quota{}, "a 500 usage answer is unknown, not a measured 0 quota (DR-6)"},
		{"a body that is not JSON", answerStatus(http.StatusOK, "unavailable"), Quota{}, "an unreadable body is unknown, never a measured 0 (DR-6)"},
		{"a window the answer omits", answerJSON(`{}`), Quota{}, "a window the answer omits must be unknown, never 0 (DR-6)"},
		{"a percent without its reset", answerJSON(`{"rolling":{"percent":42.5}}`), Quota{Rolling: Window{Percent: core.Measured[float64]{Value: 42.5, Known: true}}}, "a reset time the answer omits must be unknown (DR-6)"},
	}
	log, _ := testLog(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envKeyVar, envSecret)
			f := newFakeHost(t)
			p := newGoFixture(f, ProcessEnv(), log)
			registerGoAnswers(t, f, p, tc.answer, answerJSON(goFinalReply()))
			res, err := p.Run(context.Background(), fixtureRequest(""))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Status != core.StatusOK || res.Outcome != core.OutcomeOK {
				t.Fatalf("Run returned %s/%s, want %s/%s: the turn runs whatever the quota says (SPEC §10.2)",
					res.Status, res.Outcome, core.StatusOK, core.OutcomeOK)
			}
			checkQuota(t, res.Data.Quota, tc.want, tc.catch)
		})
	}
}

// TestOpenCodeGoSendsNothingWithoutKeyOrRunID catches a provider that sends a turn without the
// credential or without the run id the session header names: it fails before any request (SPEC §10.2,
// DR-19).
func TestOpenCodeGoSendsNothingWithoutKeyOrRunID(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		runID string
		want  error
		catch string
	}{
		{"no key", "", chatRunID, ErrKeyMissing, "a turn without a credential must not be sent (DR-19)"},
		{"no run id", envSecret, "", nil, "the session header needs the run id, so a run without one is refused (SPEC §10.2)"},
	}
	log, _ := testLog(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envKeyVar, tc.key)
			f := newFakeHost(t)
			p := newGoFixture(f, ProcessEnv(), log)
			registerGoAnswers(t, f, p, answerJSON(usageAnswer), answerJSON(goFinalReply()))
			req := fixtureRequest("")
			req.RunID = tc.runID
			_, err := p.Run(context.Background(), req)
			if err == nil {
				t.Fatalf("Run(%s): got a result and no error, want a refusal; %s", tc.name, tc.catch)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Run(%s): got %v, want %v; %s", tc.name, err, tc.want, tc.catch)
			}
			if got := f.requests(); len(got) != 0 {
				t.Errorf("Run(%s) opened %d requests, want none; %s", tc.name, len(got), tc.catch)
			}
		})
	}
}

// TestOpenCodeGoDetectReadsOnlyTheEnvironment catches a Detect that dials the provider: a model API
// is present exactly when its key is in the environment, and an absent one names a reason (SPEC §10.2,
// DR-6).
func TestOpenCodeGoDetectReadsOnlyTheEnvironment(t *testing.T) {
	log, _ := testLog(t)
	client := &http.Client{Transport: noDial{t: t}}
	cfg := OpenCodeGoConfig{Env: ProcessEnv(), HTTP: client, Log: log}
	p := NewOpenCodeGo(cfg)
	if got := p.Detect(context.Background(), fixtureEnv{envKeyVar + "=" + envSecret}); !got.Present {
		t.Errorf("Detect with the key set reports present=false: %+v; a model API is present from its key (SPEC §10.2)", got)
	}
	got := p.Detect(context.Background(), fixtureEnv{"HOME=/tmp"})
	if got.Present || got.Reason == "" || !outcomeName.MatchString(got.Reason) {
		t.Errorf("Detect without the key reported present=%v reason=%q, want absent with a snake_case reason (DR-6)", got.Present, got.Reason)
	}
}

// TestOpenCodeGoProviderContract runs the port contract against the OpenCode Go client, the same
// suite the fake passes, so both share one request and result vocabulary (SPEC §5.13, DR-19).
func TestOpenCodeGoProviderContract(t *testing.T) {
	log, lines := testLog(t)
	t.Setenv(envKeyVar, envSecret)
	f := newFakeHost(t)
	build := func(env Env, log *slog.Logger) Provider {
		return newGoFixture(f, env, log)
	}
	p := newGoFixture(f, ProcessEnv(), log)
	registerGoAnswers(t, f, p, answerJSON(usageAnswer), answerJSON(goFinalReply()))
	tools := ToolPermissions{Read: true, Edit: true}
	providerContract(t, contractFixture{
		New:    build,
		Key:    envSecret,
		Env:    fixtureEnv{envKeyVar + "=" + envSecret},
		Absent: fixtureEnv{"HOME=/tmp"},
		Log:    log,
		Lines:  lines,
		Run:    RunRequest{RunID: chatRunID, Role: chatRole, Task: chatTask, Tools: tools},
	})
}
