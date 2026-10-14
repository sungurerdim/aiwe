package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// loopUsageFirst and loopUsageSecond are the two usage objects the usage test scripts: each names
// every count, so their sum is measured, never 0 (SPEC §10.2, DR-6).
const (
	loopUsageFirst  = `{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens":10,"completion_tokens_details":{"reasoning_tokens":3}}`
	loopUsageSecond = `{"prompt_tokens":200,"prompt_tokens_details":{"cached_tokens":50},"completion_tokens":20,"completion_tokens_details":{"reasoning_tokens":7}}`
)

// loopTurnBody is one loop request as a test reads it back: the model, the messages, the tools and
// the reasoning effort the loop sent (SPEC §10.2).
type loopTurnBody struct {
	Model    string        `json:"model"`
	Messages []loopMessage `json:"messages"`
	Tools    []loopTool    `json:"tools"`
	Effort   string        `json:"reasoning_effort"`
}

// loopMessage is one message of a loop turn; an assistant message may carry tool calls and a tool
// message names the call it answers.
type loopMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCallID string         `json:"tool_call_id"`
	ToolCalls  []loopToolCall `json:"tool_calls"`
}

// loopTool is one tool definition a turn offers.
type loopTool struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

// loopToolCall is one tool call of an assistant message.
type loopToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// loopTestLoop returns the loop of the tests, wired to the endpoint and HTTP client of a fake host
// and to the OpenCode Go wire values, so the loop runs the same model API a role does
// (SPEC §10.2, O14).
func loopTestLoop(endpoint string, client *http.Client, log *slog.Logger, maxTurns int, limit time.Duration) *Loop {
	return NewLoop(LoopConfig{
		Env: ProcessEnv(), Endpoint: endpoint, SessionHeader: openCodeSessionKey,
		KeyEnv: openCodeKeyEnv, Version: chatVersion, Prices: fixturePrices(), Log: log, HTTP: client,
		MaxTurns: maxTurns, TimeLimit: limit,
	})
}

// loopTestRequest returns one run of the fixture role with the clone root and the tools it allows.
func loopTestRequest(root string, perms ToolPermissions) RunRequest {
	return RunRequest{RunID: chatRunID, Role: chatRole, Task: chatTask, Tools: perms, Root: root}
}

// scriptedAnswers answers the chat path with the replies in order; a turn past the script is refused,
// so a test catches a loop that sends more turns than the run allows (SPEC §10.2).
func scriptedAnswers(replies ...string) func(http.ResponseWriter) {
	next := 0
	return func(w http.ResponseWriter) {
		if next >= len(replies) {
			http.Error(w, "the loop sent more turns than the script answers", http.StatusInternalServerError)
			return
		}
		body := replies[next]
		next++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// assistantToolCall returns an assistant message that asks for one tool call.
func assistantToolCall(id, name, arguments string) string {
	return fmt.Sprintf(`{"role":"assistant","content":null,"tool_calls":[{"id":%q,"type":"function",`+
		`"function":{"name":%q,"arguments":%q}}]}`, id, name, arguments)
}

// assistantText returns an assistant message with a final answer and no tool call.
func assistantText(text string) string {
	encoded, _ := json.Marshal(text)
	return fmt.Sprintf(`{"role":"assistant","content":%s}`, encoded)
}

// replyTurn returns one chat reply of a served model, an assistant message and a usage object.
func replyTurn(model, assistant, usage string) string {
	return fmt.Sprintf(`{"model":%q,"choices":[{"index":0,"message":%s,"finish_reason":"stop"}],"usage":%s}`,
		model, assistant, usage)
}

// loopTurns returns the parsed bodies of the turns the fake host took, in order.
func loopTurns(t *testing.T, f *fakeHost) []loopTurnBody {
	t.Helper()
	taken := f.requests()
	bodies := make([]loopTurnBody, 0, len(taken))
	for _, turn := range taken {
		var body loopTurnBody
		if err := json.Unmarshal(turn.Body, &body); err != nil {
			t.Fatalf("a loop turn body is not JSON: %v; body %s", err, turn.Body)
		}
		bodies = append(bodies, body)
	}
	return bodies
}

// loopToolNames returns the names of the tools a turn offers, sorted, so a test compares the set.
func loopToolNames(body loopTurnBody) []string {
	names := make([]string, 0, len(body.Tools))
	for _, tool := range body.Tools {
		names = append(names, tool.Function.Name)
	}
	slices.Sort(names)
	return names
}

// loopToolResult returns the content of the tool message of a turn, and whether the turn holds one.
func loopToolResult(body loopTurnBody) (string, bool) {
	for _, message := range body.Messages {
		if message.Role == "tool" {
			return message.Content, true
		}
	}
	return "", false
}

// TestLoopRunsARoleThroughToolsToAFinalAnswer is the done criterion: a role runs end to end through
// the loop on OpenCode Go, the loop runs every tool the run allows inside the clone, and the run
// leaves one measured result (SPEC §10.2, O14).
func TestLoopRunsARoleThroughToolsToAFinalAnswer(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("seed the clone: %v", err)
	}
	f := newFakeHost(t)
	f.serve(openCodeChatPath, scriptedAnswers(
		replyTurn(DefaultModel, assistantToolCall("call_1", ToolWrite, `{"path":"out.txt","content":"hello"}`), usageFull),
		replyTurn(DefaultModel, assistantToolCall("call_2", ToolRead, `{"path":"out.txt"}`), usageFull),
		replyTurn(DefaultModel, assistantToolCall("call_3", ToolEdit, `{"path":"out.txt","old":"hello","new":"world"}`), usageFull),
		replyTurn(DefaultModel, assistantToolCall("call_4", ToolList, `{"path":"."}`), usageFull),
		replyTurn(DefaultModel, assistantToolCall("call_5", ToolShell, `{"command":"git --version"}`), usageFull),
		replyTurn(DefaultModel, assistantText("done"), usageFull),
	))
	endpoint := newGoFixture(f, ProcessEnv(), log).Endpoints().Chat
	loop := loopTestLoop(endpoint, f.srv.Client(), log, 10, time.Minute)
	res, err := loop.Run(context.Background(), loopTestRequest(root, allTools()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Data.Outcome != OutcomeFinalAnswer {
		t.Errorf("the run ended with outcome %q, want %q: a final answer ends the run (SPEC §10.2)",
			res.Data.Outcome, OutcomeFinalAnswer)
	}
	if res.Data.Turns != 6 {
		t.Errorf("the run took %d turns, want the five tool turns and the final answer (SPEC §10.2)", res.Data.Turns)
	}
	if res.Data.Served != DefaultModel {
		t.Errorf("the report names served model %q, want %q (SPEC §10.2)", res.Data.Served, DefaultModel)
	}
	if !res.Data.Usage.Prompt.Known || res.Data.Usage.Prompt.Value <= 0 {
		t.Errorf("the report holds usage %+v, want the measured tokens of the turns (SPEC §10.2)", res.Data.Usage)
	}
	if data, err := os.ReadFile(filepath.Join(root, "out.txt")); err != nil || string(data) != "world" {
		t.Errorf("the clone holds %q, %v, want the edited %q (SPEC §10.2)", data, err, "world")
	}
	taken := f.requests()
	if len(taken) != 6 {
		t.Fatalf("the loop made %d requests, want one per turn (SPEC §10.2)", len(taken))
	}
	checkHeader(t, taken[0], "Authorization", "Bearer "+envSecret)
	checkHeader(t, taken[0], openCodeSessionKey, chatRunID)
	bodies := loopTurns(t, f)
	if got, ok := loopToolResult(bodies[1]); !ok || got != "hello" {
		t.Errorf("the second turn fed back %q, %v, want the file %q the first turn wrote (SPEC §10.2)", got, ok, "hello")
	}
}

// TestLoopKeepsTheStablePrefixAcrossTurns catches a prefix that changes mid-run: the role prompt is
// the first message of every turn and the tool definitions are the same in every turn, so the
// provider's prompt cache hits (SPEC §10.2).
func TestLoopKeepsTheStablePrefixAcrossTurns(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("seed the clone: %v", err)
	}
	f := newFakeHost(t)
	f.serve(openCodeChatPath, scriptedAnswers(
		replyTurn(DefaultModel, assistantToolCall("call_1", ToolRead, `{"path":"note.txt"}`), usageFull),
		replyTurn(DefaultModel, assistantText("done"), usageFull),
	))
	endpoint := newGoFixture(f, ProcessEnv(), log).Endpoints().Chat
	loop := loopTestLoop(endpoint, f.srv.Client(), log, 5, time.Minute)
	if _, err := loop.Run(context.Background(), loopTestRequest(root, ToolPermissions{Read: true})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	turns := loopTurns(t, f)
	if len(turns) != 2 {
		t.Fatalf("the loop made %d turns, want two (SPEC §10.2)", len(turns))
	}
	first, second := turns[0], turns[1]
	if len(first.Messages) == 0 || first.Messages[0].Role != "system" {
		t.Fatalf("the first message of the first turn is %+v, want the stable system prefix first (SPEC §10.2)", first.Messages)
	}
	if !strings.Contains(first.Messages[0].Content, chatRole) {
		t.Errorf("the prefix %q names no role, want the role %q (SPEC §10.2)", first.Messages[0].Content, chatRole)
	}
	if second.Messages[0].Role != first.Messages[0].Role || second.Messages[0].Content != first.Messages[0].Content {
		t.Errorf("the stable prefix changed between turns: %q then %q (SPEC §10.2)",
			first.Messages[0].Content, second.Messages[0].Content)
	}
	firstTools, _ := json.Marshal(first.Tools)
	secondTools, _ := json.Marshal(second.Tools)
	if string(firstTools) != string(secondTools) {
		t.Errorf("the tool definitions changed between turns: %s then %s (SPEC §10.2)", firstTools, secondTools)
	}
	if names := loopToolNames(first); !slices.Equal(names, []string{ToolRead}) {
		t.Errorf("the first turn offers tools %v, want the allowed read only (SPEC §10.2)", names)
	}
}

// TestLoopRecordsEveryTurnsUsage catches a run that records only the last turn or drops a token
// count: the report rolls the prompt, cached, completion and reasoning tokens of every turn and
// prices the served model over that roll (SPEC §10.2, DR-6).
func TestLoopRecordsEveryTurnsUsage(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("seed the clone: %v", err)
	}
	f := newFakeHost(t)
	f.serve(openCodeChatPath, scriptedAnswers(
		replyTurn(DefaultModel, assistantToolCall("call_1", ToolRead, `{"path":"note.txt"}`), loopUsageFirst),
		replyTurn(DefaultModel, assistantText("done"), loopUsageSecond),
	))
	endpoint := newGoFixture(f, ProcessEnv(), log).Endpoints().Chat
	loop := loopTestLoop(endpoint, f.srv.Client(), log, 5, time.Minute)
	res, err := loop.Run(context.Background(), loopTestRequest(root, ToolPermissions{Read: true}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := usageOf(210, 90, 30, 10)
	checkMeasured(t, "prompt tokens", res.Data.Usage.Prompt, want.Prompt, "the report sums the prompt tokens of every turn")
	checkMeasured(t, "cached tokens", res.Data.Usage.Cached, want.Cached, "the report sums the cached tokens of every turn")
	checkMeasured(t, "completion tokens", res.Data.Usage.Completion, want.Completion, "the report sums the completion tokens of every turn")
	checkMeasured(t, "reasoning tokens", res.Data.Usage.Reasoning, want.Reasoning, "the report sums the reasoning tokens of every turn")
	checkMeasured(t, "cost", res.Data.Cost, fixturePrices().Cost(DefaultModel, want), "cost prices the served model over every turn")
	if res.Data.Turns != 2 {
		t.Errorf("the run took %d turns, want two (SPEC §10.2)", res.Data.Turns)
	}
}

// TestLoopEndsOnTheTurnLimit catches a loop that runs past its turn limit or reports the limit as
// an untyped error: the run stops at the limit with the named turn_limit outcome (SPEC §10.2).
func TestLoopEndsOnTheTurnLimit(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)
	root := t.TempDir()
	f := newFakeHost(t)
	f.serve(openCodeChatPath, answerJSON(replyTurn(DefaultModel,
		assistantToolCall("call", ToolList, `{"path":"."}`), usageFull)))
	endpoint := newGoFixture(f, ProcessEnv(), log).Endpoints().Chat
	loop := loopTestLoop(endpoint, f.srv.Client(), log, 2, time.Minute)
	res, err := loop.Run(context.Background(), loopTestRequest(root, ToolPermissions{List: true}))
	if err != nil {
		t.Fatalf("Run: %v; a turn limit is a named outcome, not an error (SPEC §10.2)", err)
	}
	if res.Data.Outcome != OutcomeTurnLimit {
		t.Errorf("the run ended with outcome %q, want %q (SPEC §10.2)", res.Data.Outcome, OutcomeTurnLimit)
	}
	if res.Data.Turns != 2 {
		t.Errorf("the run took %d turns, want the limit of two (SPEC §10.2)", res.Data.Turns)
	}
	if got := len(f.requests()); got != 2 {
		t.Errorf("the loop made %d requests, want the two turns of the limit (SPEC §10.2)", got)
	}
}

// TestLoopEndsOnTheTimeLimit catches a loop that hangs past its time limit or reports the limit as
// an untyped error: the run ends with the named time_limit outcome (SPEC §10.2).
func TestLoopEndsOnTheTimeLimit(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)
	// stopped releases the host of a turn the loop never cancels, so the fixture cannot outlive
	// the test that owns it (SPEC §10.2).
	stopped := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// The host holds the reply of the turn back until the loop cancels it at the run's time
		// limit. Reading the request body to its end first makes the server watch the connection,
		// so the canceled turn, which closes it, ends this request context too: a server that
		// never reads the body never notices the disconnect (SPEC §10.2).
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-stopped:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stopped) })
	loop := loopTestLoop(srv.URL+openCodeChatPath, srv.Client(), log, 10, 60*time.Millisecond)
	res, err := loop.Run(context.Background(), loopTestRequest(t.TempDir(), ToolPermissions{Read: true}))
	if err != nil {
		t.Fatalf("Run: %v; a time limit is a named outcome, not an error (SPEC §10.2)", err)
	}
	if res.Data.Outcome != OutcomeTimeLimit {
		t.Errorf("the run ended with outcome %q, want %q (SPEC §10.2)", res.Data.Outcome, OutcomeTimeLimit)
	}
}

// TestLoopPassesTheReasoningEffort catches a loop that drops the candidate's effort or invents one
// for a run without it: the request carries the effort the run names in the API's reasoning field
// (SPEC §10.2, DR-15).
func TestLoopPassesTheReasoningEffort(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)
	tests := []struct {
		name   string
		effort string
	}{
		{"a high effort", "high"},
		{"a low effort", "low"},
		{"the default effort", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHost(t)
			f.serve(openCodeChatPath, answerJSON(replyTurn(DefaultModel, assistantText("done"), usageFull)))
			endpoint := newGoFixture(f, ProcessEnv(), log).Endpoints().Chat
			loop := loopTestLoop(endpoint, f.srv.Client(), log, 2, time.Minute)
			req := loopTestRequest(t.TempDir(), ToolPermissions{})
			req.Effort = tc.effort
			if _, err := loop.Run(context.Background(), req); err != nil {
				t.Fatalf("Run: %v", err)
			}
			bodies := loopTurns(t, f)
			if len(bodies) != 1 {
				t.Fatalf("the loop made %d turns, want one (SPEC §10.2)", len(bodies))
			}
			if bodies[0].Effort != tc.effort {
				t.Errorf("the request carries reasoning effort %q, want %q (SPEC §10.2, DR-15)", bodies[0].Effort, tc.effort)
			}
		})
	}
}

// TestLoopRefusesAPathOutsideTheClone catches a loop that runs a tool call whose path leaves the
// clone: the tool result fed back to the model never holds the file outside, and the run ends on
// the final answer (SPEC §10.2, O2).
func TestLoopRefusesAPathOutsideTheClone(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)
	base := t.TempDir()
	root := filepath.Join(base, "clone")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("make the clone root: %v", err)
	}
	secretPath := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("outside-secret"), 0o600); err != nil {
		t.Fatalf("seed the file outside the clone: %v", err)
	}
	f := newFakeHost(t)
	f.serve(openCodeChatPath, scriptedAnswers(
		replyTurn(DefaultModel, assistantToolCall("call_1", ToolRead, `{"path":"../secret.txt"}`), usageFull),
		replyTurn(DefaultModel, assistantText("done"), usageFull),
	))
	endpoint := newGoFixture(f, ProcessEnv(), log).Endpoints().Chat
	loop := loopTestLoop(endpoint, f.srv.Client(), log, 5, time.Minute)
	res, err := loop.Run(context.Background(), loopTestRequest(root, ToolPermissions{Read: true}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Data.Outcome != OutcomeFinalAnswer {
		t.Errorf("the run ended with outcome %q, want %q (SPEC §10.2)", res.Data.Outcome, OutcomeFinalAnswer)
	}
	turns := loopTurns(t, f)
	if len(turns) != 2 {
		t.Fatalf("the loop made %d turns, want two (SPEC §10.2)", len(turns))
	}
	got, ok := loopToolResult(turns[1])
	if !ok {
		t.Fatal("the second turn holds no tool result, want the refusal fed back to the model (SPEC §10.2)")
	}
	if strings.Contains(got, "outside-secret") {
		t.Errorf("the loop read a path outside the clone: the tool result holds %q (SPEC §10.2, O2)", got)
	}
	if data, _ := os.ReadFile(secretPath); string(data) != "outside-secret" {
		t.Errorf("the loop changed the file outside the clone to %q (SPEC §10.2, O2)", data)
	}
}

// TestLoopRefusesAReplyThatAnsweredNothing catches a loop that ends on a reply the model never
// answered with: a message that holds neither content nor a tool call and one the endpoint cut at
// its token limit are both refused, so a run never ends with a final answer over nothing
// (SPEC §10.2, §7.4).
func TestLoopRefusesAReplyThatAnsweredNothing(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)
	const emptyAnswer = `{"model":"` + DefaultModel + `","choices":[{"index":0,` +
		`"message":{"role":"assistant","content":null},"finish_reason":"stop"}],"usage":` + usageFull + `}`
	const cutAnswer = `{"model":"` + DefaultModel + `","choices":[{"index":0,` +
		`"message":{"role":"assistant","content":"half an answer"},"finish_reason":"length"}],"usage":` + usageFull + `}`
	tests := []struct {
		name  string
		reply string
	}{
		{"a reply that holds no answer", emptyAnswer},
		{"a reply the endpoint cut at its token limit", cutAnswer},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHost(t)
			f.serve(openCodeChatPath, answerJSON(tc.reply))
			endpoint := newGoFixture(f, ProcessEnv(), log).Endpoints().Chat
			loop := loopTestLoop(endpoint, f.srv.Client(), log, 5, time.Minute)
			res, err := loop.Run(context.Background(), loopTestRequest(t.TempDir(), ToolPermissions{}))
			if err == nil {
				t.Fatalf("Run ended with outcome %q and no error, want a refusal: %s is not an answer (SPEC §10.2, §7.4)",
					res.Data.Outcome, tc.name)
			}
			if got := len(f.requests()); got != 1 {
				t.Errorf("the loop made %d requests, want the one refused turn (SPEC §10.2)", got)
			}
		})
	}
}
