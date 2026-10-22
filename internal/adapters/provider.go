// Package adapters holds the provider and harness adapters, the compiled adapter list and the
// model API clients; every adapter is reached through the Provider port (SPEC §10.2, §6.1).
package adapters

import (
	"context"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Kind is the kind of a provider: a model API aiwe's own loop calls, or an agent runtime the
// vendor runs (O14).
type Kind string

// The two kinds behind the one Provider interface (O14).
const (
	KindModelAPI     Kind = "model_api"
	KindAgentRuntime Kind = "agent_runtime"
)

// Capabilities is what a provider offers before it runs: its kind, the models it serves and the
// behaviours a caller may rely on (SPEC §10.2).
type Capabilities struct {
	Kind         Kind
	Models       []string
	Resume       bool
	ReportsModel bool
}

// Detection is what Detect found: whether the provider is present, its version and, when it is
// absent, a snake_case reason (DR-6, SPEC §10.2).
type Detection struct {
	Present bool
	Version string
	Reason  string
}

// ToolPermissions is the tool set one run may use; a tool a run does not allow is never offered to
// the model (SPEC §10.2).
type ToolPermissions struct {
	Read  bool
	Write bool
	Edit  bool
	List  bool
	Shell bool
}

// RunRequest is the one request vocabulary of every provider: the run id, the role and task, the
// model, the tool permissions, the clone the tools stay inside, the run environment the shell tool
// hands its child and the reasoning effort of the candidate (SPEC §10.2, DR-10, DR-15). Env is the
// environment the engine builds for the run. The shell child gets only that copy, never the parent
// process, so a command of the clone cannot see the parent's repository pointers or profile.
type RunRequest struct {
	RunID  string
	Role   string
	Task   string
	Model  string
	Tools  ToolPermissions
	Root   string
	Env    []string
	Effort string
}

// Usage is the token roll of one reply: prompt, cached, completion and reasoning tokens; a count
// the reply never named is unknown, never 0 (DR-6).
type Usage struct {
	Prompt     core.Measured[int64]
	Cached     core.Measured[int64]
	Completion core.Measured[int64]
	Reasoning  core.Measured[int64]
}

// RollUsage sums the usage of every turn of one run: each count is the sum of the counts the turns
// named, and a count any turn left unnamed is unknown, never 0 (DR-6, SPEC §10.2). A run without an
// answered turn has unknown counts.
func RollUsage(turns []Usage) Usage {
	if len(turns) == 0 {
		return Usage{}
	}
	roll := turns[0]
	for _, turn := range turns[1:] {
		roll = roll.add(turn)
	}
	return roll
}

// add returns the sum of the two usages; a count either left unnamed is unknown, so a total is
// never a measured 0 over a turn that named none (DR-6).
func (u Usage) add(next Usage) Usage {
	return Usage{
		Prompt:     addCount(u.Prompt, next.Prompt),
		Cached:     addCount(u.Cached, next.Cached),
		Completion: addCount(u.Completion, next.Completion),
		Reasoning:  addCount(u.Reasoning, next.Reasoning),
	}
}

// addCount returns the sum of two measured token counts; a count either left unnamed stays unknown
// (DR-6).
func addCount(sum, next core.Measured[int64]) core.Measured[int64] {
	if !sum.Known || !next.Known {
		return core.Measured[int64]{}
	}
	return core.Measured[int64]{Value: sum.Value + next.Value, Known: true}
}

// RunReport is the one result vocabulary of every provider: the served model, the usage, the cost,
// the quota, the named outcome and the number of turns the run took (SPEC §10.2).
type RunReport struct {
	Served  string
	Usage   Usage
	Cost    core.Measured[float64]
	Quota   Quota
	Outcome core.Outcome
	Turns   int
}

// Reset is the time a quota window resets; an unreadable answer leaves it unknown (DR-6).
type Reset struct {
	At    time.Time
	Known bool
}

// Window is one quota window: its percent used and its reset time (SPEC §7.6).
type Window struct {
	Percent core.Measured[float64]
	Reset   Reset
}

// Quota is the rolling, weekly and monthly quota of one provider account (SPEC §7.6, §10.2).
type Quota struct {
	Rolling Window
	Weekly  Window
	Monthly Window
}

// Provider is the one port an engine reaches a provider through, whatever its kind (SPEC §10.2).
type Provider interface {
	ID() string
	Detect(ctx context.Context, env Env) Detection
	Capabilities() Capabilities
	Run(ctx context.Context, req RunRequest) (core.Result[RunReport], error)
}
