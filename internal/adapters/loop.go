package adapters

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The named outcomes a loop run ends with: the model's final answer, the turn limit or the time
// limit of the run (SPEC §10.2).
const (
	OutcomeFinalAnswer core.Outcome = "final_answer"
	OutcomeTurnLimit   core.Outcome = "turn_limit"
	OutcomeTimeLimit   core.Outcome = "time_limit"
)

// The roles of the messages one turn sends: the stable system prefix, the task, the assistant
// message of a reply and the result of a tool call (SPEC §10.2).
const (
	roleSystem    = "system"
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
)

// LoopConfig wires one agent loop over a model API: the endpoint, the credential it reads by name,
// the HTTP client and the limits of the run (SPEC §10.2, O14).
type LoopConfig struct {
	Env           Env
	Endpoint      string
	SessionHeader string
	KeyEnv        string
	Version       string
	Prices        PriceTable
	Log           *slog.Logger
	HTTP          *http.Client
	MaxTurns      int
	TimeLimit     time.Duration
}

// Loop is aiwe's own lean agent loop: it sends the stable prefix and the task to a model API, runs
// the tool calls of a reply inside the run clone and repeats until a named outcome (SPEC §10.2,
// O14).
type Loop struct {
	cfg LoopConfig
	// chat sends the turns of a run, so the headers, the reply bound and the price table of the
	// wire format have one home (SPEC §10.2).
	chat *ChatClient
}

// NewLoop returns the loop of cfg.
func NewLoop(cfg LoopConfig) *Loop {
	chat := NewChatClient(ChatConfig{
		Env: cfg.Env, Endpoint: cfg.Endpoint, SessionHeader: cfg.SessionHeader, KeyEnv: cfg.KeyEnv,
		Version: cfg.Version, Prices: cfg.Prices, Log: cfg.Log, HTTP: cfg.HTTP,
	})
	return &Loop{cfg: cfg, chat: chat}
}

// Run runs one role to a named outcome: the model's final answer, the turn limit or the time limit
// of the run. The key is read by name at run time, and a run without a key or a run id sends
// nothing (SPEC §10.2, DR-19).
func (l *Loop) Run(ctx context.Context, req RunRequest) (core.Result[RunReport], error) {
	key, err := Key(l.cfg.Env, l.cfg.KeyEnv)
	if err != nil {
		return core.Result[RunReport]{}, err
	}
	if req.RunID == "" {
		return core.Result[RunReport]{}, ErrRunIDMissing
	}
	return l.run(ctx, req, key)
}

// run runs one role with the key its caller read, so a provider that reads the key before its own
// requests reads it once (SPEC §10.2, DR-19).
func (l *Loop) run(ctx context.Context, req RunRequest, key Secret) (core.Result[RunReport], error) {
	if req.Model == "" {
		req.Model = DefaultModel
	}
	ctx, cancel := l.limited(ctx)
	defer cancel()
	return l.turns(ctx, newLoopRun(l.chat.cfg.Prices, req, key))
}

// limited bounds a run by its time limit; a run without one keeps the context of its caller (SPEC
// §5.18, §10.2).
func (l *Loop) limited(ctx context.Context) (context.Context, context.CancelFunc) {
	if l.cfg.TimeLimit <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, l.cfg.TimeLimit)
}

// turns runs the turns of one role until a named outcome: a reply that answers with text and no
// tool call is the final answer, and the turn or the time limit of the run ends it too (SPEC §10.2,
// O14).
func (l *Loop) turns(ctx context.Context, run *loopRun) (core.Result[RunReport], error) {
	for taken := 0; ; {
		if l.cfg.MaxTurns > 0 && taken >= l.cfg.MaxTurns {
			return run.report(OutcomeTurnLimit), nil
		}
		taken++
		reply, err := l.turn(ctx, run)
		if err != nil {
			if expired(ctx, err) {
				return run.report(OutcomeTimeLimit), nil
			}
			return core.Result[RunReport]{}, err
		}
		if run.answer(ctx, reply) {
			return run.report(OutcomeFinalAnswer), nil
		}
	}
}

// expired reports whether a failed turn ran out of the time limit of its run, which is a named
// outcome of the loop, never an error (SPEC §10.2).
func expired(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// loopRun is the state of one run: the conversation it has built, the tool set confined to the
// clone, the price table of the run, the served model and the usage of every answered turn
// (SPEC §10.2, O2, O14).
type loopRun struct {
	prices   PriceTable
	req      RunRequest
	key      Secret
	tools    *Tools
	messages []wireMessage
	defs     []chatTool
	usages   []Usage
	served   string
}

// newLoopRun returns the run of req: the role prompt and the task come first and the tool
// definitions follow once, so the stable prefix never changes within the run (SPEC §10.2). The
// shell child is bound to the environment the request carries before any turn, including when that
// environment is empty, so a command of the clone never inherits the parent process (SPEC §6.5,
// DR-10).
func newLoopRun(prices PriceTable, req RunRequest, key Secret) *loopRun {
	tools := NewTools(req.Root, req.Tools)
	tools.bindRunEnv(req.Env)
	return &loopRun{
		prices: prices,
		req:    req,
		key:    key,
		tools:  tools,
		messages: []wireMessage{
			{Role: roleSystem, Content: rolePrompt(req.Role)},
			{Role: roleUser, Content: req.Task},
		},
		defs: toolDefinitions(req.Tools),
	}
}

// rolePrompt returns the stable prefix of every turn of one run: the role the model plays (SPEC
// §10.2).
func rolePrompt(role string) string {
	return "role: " + role
}

// answer records one reply and runs the tool calls it asks for; it reports whether the reply ended
// the run with a final answer. A reply the turn refuses never reaches here, so a message with no
// tool call carries the text the model answered with (SPEC §10.2). A refused call is recorded too,
// so the model reads what the clone allows and takes the next turn (SPEC §10.2, O2, O14).
func (r *loopRun) answer(ctx context.Context, reply wireReply) bool {
	r.served = reply.Model
	r.usages = append(r.usages, reply.Usage.usage())
	message := reply.Choices[0].Message
	message.Role = roleAssistant
	r.messages = append(r.messages, message)
	if len(message.ToolCalls) == 0 {
		return true
	}
	for _, call := range message.ToolCalls {
		r.messages = append(r.messages, wireMessage{
			Role: roleTool, ToolCallID: call.ID, Content: r.toolResult(ctx, call),
		})
	}
	return false
}

// toolResult returns the content of one tool call: its output, or the refusal a call outside the
// clone or outside the allowed set gets (SPEC §10.2, O2).
func (r *loopRun) toolResult(ctx context.Context, call wireToolCall) string {
	args, err := toolArgs(call.Function.Arguments)
	if err != nil {
		return err.Error()
	}
	out, err := r.tools.Do(ctx, call.Function.Name, args)
	if err != nil {
		if out == "" {
			return err.Error()
		}
		return out + "\n" + err.Error()
	}
	return out
}

// report returns the result of one run: the served model, the usage and the cost rolled over every
// answered turn — a count no turn named stays unknown, never 0 (DR-6) — the named outcome and the
// turns that answered it (SPEC §10.2). The status is the one the outcome table gives the named
// outcome, so a run stopped at a limit is never reported as done; a run that ended well is ok/ok,
// as every provider's success is (SPEC §5.3).
func (r *loopRun) report(outcome core.Outcome) core.Result[RunReport] {
	usage := RollUsage(r.usages)
	data := RunReport{
		Served:  r.served,
		Usage:   usage,
		Outcome: outcome,
		Turns:   len(r.usages),
		Cost:    r.prices.Cost(r.served, usage),
	}
	status := outcomeStatus(outcome)
	if status == core.StatusOK {
		return core.Result[RunReport]{Status: status, Outcome: core.OutcomeOK, Data: data}
	}
	return core.Result[RunReport]{Status: status, Outcome: outcome, Data: data}
}

// outcomeStatus returns the status the outcome table gives o; an outcome with no row is an error,
// never a success (fail-closed, SPEC §5.3).
func outcomeStatus(o core.Outcome) core.Status {
	for _, row := range core.Outcomes() {
		if row.Outcome == o {
			return row.Status
		}
	}
	return core.StatusError
}
