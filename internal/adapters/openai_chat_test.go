package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// fixtureChatPath and testSessionHeader are the generic endpoint and session header of the client
// tests; the OpenCode Go path and its session header are asserted against the provider that declares
// them (SPEC §10.2, DR-3).
const (
	fixtureChatPath   = "/v1/chat/completions"
	testSessionHeader = "x-test-session"
)

// usageFull is a complete usage object of one reply: prompt, cached, completion and reasoning tokens
// (SPEC §10.2).
const usageFull = `{"prompt_tokens":1200,"prompt_tokens_details":{"cached_tokens":800},` +
	`"completion_tokens":300,"completion_tokens_details":{"reasoning_tokens":120}}`

// usagePromptOnly names the prompt count only; the other three counts are absent, never 0 (DR-6).
const usagePromptOnly = `{"prompt_tokens":1200}`

// usageEmpty names no count at all (DR-6).
const usageEmpty = `{}`

// turnCost is the cost of usageFull on DefaultModel under fixturePrices: 400 fresh input tokens at
// 0.28 per million, 800 cached at 0.028 and 300 completion at 0.42 (SPEC §8.1).
const turnCost = 0.0002604

// replyBody returns the recorded reply shape with the model and the usage a test names: the wire
// format the live probe of 2026-10-12 read, with one tool call (SPEC §17).
func replyBody(model, usage string) string {
	return fmt.Sprintf(`{"id":"chatcmpl-9f2c","object":"chat.completion","created":1760275200,`+
		`"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"done",`+
		`"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read",`+
		`"arguments":"{\"path\":\"env.go\"}"}}]},"finish_reason":"tool_calls"}],`+
		`"usage":%s}`, model, usage)
}

// fixturePrices is the price table of the tests: realistic per-million-token values of the model the
// skeleton runs, so the cost arithmetic is checked against a pinned row (SPEC §8.1).
func fixturePrices() PriceTable {
	return PriceTable{DefaultModel: {InputPerM: 0.28, CachedInputPerM: 0.028, OutputPerM: 0.42}}
}

// measured returns a token count the reply named.
func measured(tokens int64) core.Measured[int64] {
	return core.Measured[int64]{Value: tokens, Known: true}
}

// costOf returns a cost that the pinned price table measured, in US dollars.
func costOf(usd float64) core.Measured[float64] {
	return core.Measured[float64]{Value: usd, Known: true}
}

// usageOf returns the roll of fresh input, cached, completion and reasoning tokens of one reply.
func usageOf(fresh, cached, completion, reasoning int64) Usage {
	usage := Usage{Prompt: measured(fresh + cached), Cached: measured(cached)}
	usage.Completion = measured(completion)
	usage.Reasoning = measured(reasoning)
	return usage
}

// fakeMessage, fakeTool and fakePayload are the parts of a request body a test reads back: the model,
// the messages and the tools (SPEC §10.2).
type fakeMessage struct {
	Role    string
	Content string
}

// fakeTool is one tool of a request body, in the OpenAI wire format.
type fakeTool struct {
	Type     string
	Function struct {
		Name string
	}
}

// fakePayload is the body of one chat turn.
type fakePayload struct {
	Model    string
	Messages []fakeMessage
	Tools    []fakeTool
}

// toolNames returns the names of the tools a payload offers, sorted, so a test compares the set.
func (p fakePayload) toolNames() []string {
	names := make([]string, 0, len(p.Tools))
	for _, tool := range p.Tools {
		names = append(names, tool.Function.Name)
	}
	slices.Sort(names)
	return names
}

// content returns the role and content of every message of a payload, so a test reads the stable
// prefix a request sends and the task it carries (SPEC §10.2).
func (p fakePayload) content() string {
	var b strings.Builder
	for _, m := range p.Messages {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// fakeTurn is one request a fake host took.
type fakeTurn struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// fakeHost is a fake OpenAI-compatible host: it records every request and answers the endpoints a
// test registers, so no test reaches the network (SPEC §5.13).
type fakeHost struct {
	srv     *httptest.Server
	mu      sync.Mutex
	taken   []fakeTurn
	answers map[string]func(w http.ResponseWriter)
}

// newFakeHost starts the fake host; it answers no endpoint until a test registers one.
func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	f := &fakeHost{answers: map[string]func(http.ResponseWriter){}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the body of %s %s: %v", r.Method, r.URL.Path, err)
		}
		f.mu.Lock()
		taken := fakeTurn{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body}
		f.taken = append(f.taken, taken)
		answer := f.answers[r.URL.Path]
		f.mu.Unlock()
		if answer == nil {
			http.Error(w, "no answer registered for "+r.URL.Path, http.StatusNotFound)
			return
		}
		answer(w)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// serve registers the answer of one endpoint path.
func (f *fakeHost) serve(path string, answer func(w http.ResponseWriter)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[path] = answer
}

// serveURL registers the answer of a full endpoint URL a provider itself declares, so a test names no
// endpoint of its own (SPEC §10.2).
func (f *fakeHost) serveURL(t *testing.T, endpoint string, answer func(w http.ResponseWriter)) {
	t.Helper()
	f.serve(endpointPath(t, endpoint), answer)
}

// requests returns the requests the fake host has taken, in order.
func (f *fakeHost) requests() []fakeTurn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.taken)
}

// endpointPath returns the path of a full endpoint URL.
func endpointPath(t *testing.T, endpoint string) string {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil || u.Path == "" {
		t.Fatalf("endpoint %q is not a URL with a path: %v", endpoint, err)
	}
	return u.Path
}

// answerJSON answers a request with body as JSON.
func answerJSON(body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// answerStatus answers a request with status and body, the way a refused turn comes back.
func answerStatus(status int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// newTurnClient returns the client of the tests: it talks to f, reads the fixture credential by name
// at run time and logs through log (SPEC §10.2, §5.3).
func newTurnClient(t *testing.T, f *fakeHost, log *slog.Logger) *ChatClient {
	t.Helper()
	cfg := ChatConfig{
		Env:           ProcessEnv(),
		Endpoint:      f.srv.URL + fixtureChatPath,
		SessionHeader: testSessionHeader,
		KeyEnv:        envKeyVar,
		Version:       chatVersion,
		Prices:        fixturePrices(),
		Log:           log,
		HTTP:          f.srv.Client(),
	}
	return NewChatClient(cfg)
}

// fixtureRequest returns one turn of the fixture role and task on model.
func fixtureRequest(model string) RunRequest {
	tools := ToolPermissions{Read: true, Edit: true, Shell: true}
	return RunRequest{RunID: chatRunID, Role: chatRole, Task: chatTask, Model: model, Tools: tools}
}

// checkHeader fails when one header of a request is not the value the wire format requires.
func checkHeader(t *testing.T, turn fakeTurn, name, want string) {
	t.Helper()
	if got := turn.Header.Get(name); got != want {
		t.Errorf("header %s is %q, want %q; a missing header fails the turn (SPEC §10.2)", name, got, want)
	}
}

// checkMeasured fails when a token count or a cost is not the measured value a row names: an absent
// field stays unknown, never 0 (DR-6).
func checkMeasured[T int64 | float64](t *testing.T, name string, got, want core.Measured[T], catch string) {
	t.Helper()
	if got.Known != want.Known {
		t.Fatalf("%s: known = %v (value %v), want known = %v; %s", name, got.Known, got.Value, want.Known, catch)
	}
	if want.Known && math.Abs(float64(got.Value)-float64(want.Value)) > 1e-9 {
		t.Errorf("%s = %v, want %v; %s", name, got.Value, want.Value, catch)
	}
}

// TestTurnSendsTheRequiredHeadersAndBody catches a turn that misses a required header, sends no tool
// or sends more than one request: one POST to the endpoint carries Bearer auth, the session header
// with the run id and the product User-Agent, and its body names the model, the role and task and the
// allowed tools (SPEC §10.2, O14).
func TestTurnSendsTheRequiredHeadersAndBody(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(envKeyVar, envSecret)
	f := newFakeHost(t)
	f.serve(fixtureChatPath, answerJSON(replyBody(DefaultModel, usageFull)))
	client := newTurnClient(t, f, log)
	req := fixtureRequest(DefaultModel)
	req.Tools = ToolPermissions{Read: true, Edit: true}
	res, err := client.Turn(context.Background(), req)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Status != core.StatusOK || res.Outcome != core.OutcomeOK {
		t.Fatalf("Turn returned %s/%s, want %s/%s (SPEC §10.2)", res.Status, res.Outcome, core.StatusOK, core.OutcomeOK)
	}
	got := f.requests()
	if len(got) != 1 {
		t.Fatalf("the client made %d requests, want one turn (SPEC §10.2)", len(got))
	}
	turn := got[0]
	if turn.Method != http.MethodPost || turn.Path != fixtureChatPath {
		t.Errorf("the turn is %s %s, want POST %s (SPEC §10.2)", turn.Method, turn.Path, fixtureChatPath)
	}
	checkHeader(t, turn, "Authorization", "Bearer "+envSecret)
	checkHeader(t, turn, testSessionHeader, chatRunID)
	checkHeader(t, turn, "User-Agent", core.Product+"/"+chatVersion)
	checkHeader(t, turn, "Content-Type", "application/json")
	var payload fakePayload
	if err := json.Unmarshal(turn.Body, &payload); err != nil {
		t.Fatalf("the turn body is not JSON: %v; body %s", err, turn.Body)
	}
	if payload.Model != DefaultModel {
		t.Errorf("the body names model %q, want %q (SPEC §10.2)", payload.Model, DefaultModel)
	}
	if len(payload.Messages) < 2 {
		t.Fatalf("the body holds %d messages, want a role message and the task (SPEC §10.2)", len(payload.Messages))
	}
	if payload.Messages[0].Role != "system" {
		t.Errorf("the first message is %q, want the stable system prefix first (SPEC §10.2)", payload.Messages[0].Role)
	}
	if text := payload.content(); !strings.Contains(text, chatRole) || !strings.Contains(text, chatTask) {
		t.Errorf("the messages carry %q, want the role %q and the task %q (SPEC §10.2)", text, chatRole, chatTask)
	}
	if names := payload.toolNames(); !slices.Equal(names, []string{"edit", "read"}) {
		t.Errorf("the body offers tools %v, want the allowed two and no shell (SPEC §10.2)", names)
	}
	for _, tool := range payload.Tools {
		if tool.Type != "function" {
			t.Errorf("a tool of type %q, want function on the OpenAI wire format", tool.Type)
		}
	}
}

// TestTurnRecordsTheServedModelUsageAndCost catches a report that keeps the requested model instead
// of the served one, drops a token count or prices a count the reply never named: the report names
// the model the reply names, records prompt, cached, completion and reasoning tokens and prices the
// served model, while an absent field stays unknown, never 0 (SPEC §10.2, DR-6).
func TestTurnRecordsTheServedModelUsageAndCost(t *testing.T) {
	tests := []struct {
		name       string
		request    string
		reply      string
		usage      string
		wantServed string
		wantUsage  Usage
		wantCost   core.Measured[float64]
		catch      string
	}{
		{"the reply names the requested model", DefaultModel, DefaultModel, usageFull, DefaultModel, usageOf(400, 800, 300, 120), costOf(turnCost), "the report drops the served model, a token count or the cost of a priced reply"},
		{"the reply names another model", "glm-4.6", DefaultModel, usageFull, DefaultModel, usageOf(400, 800, 300, 120), costOf(turnCost), "the report keeps the requested model instead of the served one"},
		{"the served model has no price row", DefaultModel, "kimi-k2-instruct", usageFull, "kimi-k2-instruct", usageOf(400, 800, 300, 120), core.Measured[float64]{}, "a served model without a price row is unknown, never a measured 0 (no_price, DR-6)"},
		{"the reply names no token count", DefaultModel, DefaultModel, usageEmpty, DefaultModel, Usage{}, core.Measured[float64]{}, "an absent usage field must be unknown, never 0 (DR-6)"},
		{"the reply names the prompt count only", DefaultModel, DefaultModel, usagePromptOnly, DefaultModel, Usage{Prompt: measured(1200)}, core.Measured[float64]{}, "a partial usage must not price the missing counts as 0 (DR-6)"},
	}
	log, _ := testLog(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envKeyVar, envSecret)
			f := newFakeHost(t)
			f.serve(fixtureChatPath, answerJSON(replyBody(tc.reply, tc.usage)))
			client := newTurnClient(t, f, log)
			req := fixtureRequest(tc.request)
			req.Tools = ToolPermissions{Read: true}
			res, err := client.Turn(context.Background(), req)
			if err != nil {
				t.Fatalf("Turn: %v", err)
			}
			if res.Data.Served != tc.wantServed {
				t.Errorf("Served = %q, want %q; %s", res.Data.Served, tc.wantServed, tc.catch)
			}
			checkMeasured(t, "prompt tokens", res.Data.Usage.Prompt, tc.wantUsage.Prompt, tc.catch)
			checkMeasured(t, "cached tokens", res.Data.Usage.Cached, tc.wantUsage.Cached, tc.catch)
			checkMeasured(t, "completion tokens", res.Data.Usage.Completion, tc.wantUsage.Completion, tc.catch)
			checkMeasured(t, "reasoning tokens", res.Data.Usage.Reasoning, tc.wantUsage.Reasoning, tc.catch)
			checkMeasured(t, "cost", res.Data.Cost, tc.wantCost, tc.catch)
		})
	}
}

// TestPriceTableCostIsPerMillion catches a cost that mixes up input, cached and output tokens or
// prices a count the reply never named: the cost is the pinned row's arithmetic over fresh input,
// cached input and completion tokens, and a model without a row or a usage with an unknown count is
// unknown, never 0 (SPEC §8.1, DR-6).
func TestPriceTableCostIsPerMillion(t *testing.T) {
	tests := []struct {
		name  string
		model string
		usage Usage
		want  core.Measured[float64]
		catch string
	}{
		{"the pinned row prices the served model", DefaultModel, usageOf(400, 800, 300, 120), costOf(turnCost), "400 fresh input at 0.28, 800 cached at 0.028 and 300 completion at 0.42 per million tokens"},
		{"a cached token is not charged as fresh input", DefaultModel, usageOf(0, 1000, 0, 0), costOf(1000 * 0.028 / 1e6), "a cached token costs the cached price, not the input price"},
		{"a model with no row has no price", "kimi-k2-instruct", usageOf(400, 800, 300, 120), core.Measured[float64]{}, "an unpriced model is unknown, never 0 (no_price, DR-6)"},
		{"a usage with an unknown count has no price", DefaultModel, Usage{Prompt: measured(1200)}, core.Measured[float64]{}, "a count the reply never named must not be priced as 0 (DR-6)"},
	}
	prices := fixturePrices()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkMeasured(t, "cost", prices.Cost(tc.model, tc.usage), tc.want, tc.catch)
		})
	}
}

// TestTurnReadsTheKeyAtRunTime catches a key read once at construction: the client reads the variable
// at run time, so a turn after that variable changed carries the new value (DR-19).
func TestTurnReadsTheKeyAtRunTime(t *testing.T) {
	const first = "sk-live-first-4c1d2e"
	const second = "sk-live-second-9a2f3b"
	log, _ := testLog(t)
	t.Setenv(envKeyVar, first)
	f := newFakeHost(t)
	f.serve(fixtureChatPath, answerJSON(replyBody(DefaultModel, usageFull)))
	client := newTurnClient(t, f, log)
	t.Setenv(envKeyVar, second)
	if _, err := client.Turn(context.Background(), fixtureRequest(DefaultModel)); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	got := f.requests()
	if len(got) != 1 {
		t.Fatalf("the client made %d requests, want one turn (SPEC §10.2)", len(got))
	}
	checkHeader(t, got[0], "Authorization", "Bearer "+second)
}

// TestTurnWithoutAKeySendsNothing catches a turn that runs without a credential or invents an empty
// one: the client fails with key_missing before it opens a request (DR-6, DR-19).
func TestTurnWithoutAKeySendsNothing(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(envKeyVar, "")
	f := newFakeHost(t)
	f.serve(fixtureChatPath, answerJSON(replyBody(DefaultModel, usageFull)))
	client := newTurnClient(t, f, log)
	_, err := client.Turn(context.Background(), fixtureRequest(DefaultModel))
	if !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("Turn without a key: got %v, want %v (DR-6, DR-19)", err, ErrKeyMissing)
	}
	if got := f.requests(); len(got) != 0 {
		t.Errorf("the client opened %d requests without a key, want none (DR-19)", len(got))
	}
}

// TestTurnRefusedTurnNeverLogsTheKey catches a credential that leaks into an error or a log line: a
// refused turn fails with text that never carries the key, while the request did carry it (DR-19).
func TestTurnRefusedTurnNeverLogsTheKey(t *testing.T) {
	log, lines := testLog(t)
	t.Setenv(envKeyVar, envSecret)
	f := newFakeHost(t)
	f.serve(fixtureChatPath, answerStatus(http.StatusUnauthorized, `{"error":{"message":"invalid credential"}}`))
	client := newTurnClient(t, f, log)
	res, err := client.Turn(context.Background(), fixtureRequest(DefaultModel))
	if err == nil {
		t.Fatalf("Turn on HTTP 401 returned %s and no error, want a failure (SPEC §7.6)", res.Status)
	}
	if strings.Contains(err.Error(), envSecret) || strings.Contains(lines.String(), envSecret) {
		t.Errorf("the credential left the environment: error %q, log lines %s (DR-19)", err, lines)
	}
	got := f.requests()
	if len(got) != 1 {
		t.Fatalf("the client made %d requests, want one refused turn (SPEC §10.2)", len(got))
	}
	checkHeader(t, got[0], "Authorization", "Bearer "+envSecret)
}

// TestTurnUnreadableReplyIsAnError catches a cut or unreadable reply reported as a finished turn: a
// 200 whose body is not a reply is an error, never an ok report of unknown fields (SPEC §10.2).
func TestTurnUnreadableReplyIsAnError(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(envKeyVar, envSecret)
	f := newFakeHost(t)
	f.serve(fixtureChatPath, answerStatus(http.StatusOK, "not a reply"))
	client := newTurnClient(t, f, log)
	res, err := client.Turn(context.Background(), fixtureRequest(DefaultModel))
	if err == nil {
		t.Fatalf("Turn with an unreadable body returned %s and no error, want an error", res.Status)
	}
}
