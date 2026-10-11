// Package gates runs a declared gate step by step and reports each step (DR-10).
package gates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/sungurerdim/aiwe/internal/core"
)

// formatVersion is the only gate.json format this engine reads (SPEC §8.3).
const formatVersion = 1

// Definition is a gate: the stored gate of SPEC §8.3, or a project gate.json in the same format.
// A project gate.json omits Origin, InputHash and Proof.
type Definition struct {
	FormatVersion int        `json:"formatVersion"`
	Steps         []GateStep `json:"steps"`
	Origin        string     `json:"origin,omitempty"`
	InputHash     string     `json:"inputHash,omitempty"`
	Proof         *Proof     `json:"proof,omitempty"`
}

// GateStep is one step of a gate. Command is an argv array, never a shell string.
type GateStep struct {
	ID       string   `json:"id"`
	Command  []string `json:"command"`
	Required bool     `json:"required"`
	// Inputs are the step's declared inputs as globs; empty means the whole tracked tree (§7.8).
	Inputs []string `json:"inputs,omitempty"`
	// TimeoutSeconds is the step's time limit; 0 means none beyond the gate's context.
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	Expect         Expect `json:"expect"`
}

// Expect is what makes a step green. OutputMatches and OutputRejects are RE2 patterns over the
// step's output.
type Expect struct {
	ExitCode      int    `json:"exitCode"`
	OutputMatches string `json:"outputMatches,omitempty"`
	OutputRejects string `json:"outputRejects,omitempty"`
}

// Proof is the base proof a derived gate carries (SPEC §8.3, §7.8).
type Proof struct {
	BaseCommit string      `json:"baseCommit"`
	Steps      []ProofStep `json:"steps"`
}

// ProofStep is the measured result of one step at the base commit.
type ProofStep struct {
	ID         string `json:"id"`
	Result     Result `json:"result"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
}

// ParseDefinition reads a gate.json. It refuses invalid UTF-8, an unknown field, a format other
// than version 1, an empty gate, and a step without an id, with a duplicate id or without an argv
// command (fail-closed, SPEC §4).
func ParseDefinition(data []byte) (Definition, error) {
	if _, err := core.ReadText(data); err != nil {
		return Definition{}, fmt.Errorf("parse gate definition: %w", err)
	}
	var def Definition
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&def); err != nil {
		return Definition{}, invalidGate("a gate.json object of SPEC §8.3", err.Error(), err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Definition{}, invalidGate("one JSON object", "data after the object", nil)
	}
	if err := checkDefinition(def); err != nil {
		return Definition{}, err
	}
	return def, nil
}

// checkDefinition refuses a decoded gate that a runner could not report step by step.
func checkDefinition(def Definition) error {
	if def.FormatVersion != formatVersion {
		return invalidGate(fmt.Sprintf("formatVersion %d", formatVersion),
			fmt.Sprintf("formatVersion %d", def.FormatVersion), nil)
	}
	if len(def.Steps) == 0 {
		return invalidGate("at least one step", "no step", nil)
	}
	if def.Origin != "" && !validOrigin(def.Origin) {
		return invalidGate("origin project, derived or synthesized", fmt.Sprintf("origin %q", def.Origin), nil)
	}
	seen := make(map[string]bool, len(def.Steps))
	for i, s := range def.Steps {
		if err := checkStep(s); err != nil {
			err.Found = fmt.Sprintf("step %d: %s", i+1, err.Found)
			return err
		}
		if seen[s.ID] {
			return invalidGate("a unique step id", fmt.Sprintf("id %q twice", s.ID), nil)
		}
		seen[s.ID] = true
	}
	return nil
}

// checkStep refuses a step without an id, without a program to run, with a negative time limit
// or with an output pattern that is not valid RE2. The refusal is a *core.Error, so the caller can
// name the step inside it and the text line names the status once (#160).
func checkStep(s GateStep) *core.Error {
	if s.ID == "" {
		return invalidGate("a step id", "an empty id", nil)
	}
	if len(s.Command) == 0 || s.Command[0] == "" {
		return invalidGate("an argv command", fmt.Sprintf("step %q without a program", s.ID), nil)
	}
	if s.TimeoutSeconds < 0 {
		return invalidGate("timeoutSeconds 0 or more", fmt.Sprintf("step %q timeoutSeconds %d", s.ID, s.TimeoutSeconds), nil)
	}
	for _, p := range []string{s.Expect.OutputMatches, s.Expect.OutputRejects} {
		if _, err := regexp.Compile(p); err != nil {
			return invalidGate("an RE2 output pattern", fmt.Sprintf("step %q pattern %q", s.ID, p), err)
		}
	}
	return nil
}

// validOrigin reports whether origin is one of the origins of SPEC §8.3.
func validOrigin(origin string) bool {
	switch origin {
	case "project", "derived", "synthesized":
		return true
	default:
		return false
	}
}

// invalidGate returns the error of a gate.json that cannot be read as a gate.
func invalidGate(expected, found string, cause error) *core.Error {
	return &core.Error{Outcome: core.OutcomeError, Expected: expected, Found: found,
		Next: "correct gate.json to the format of SPEC §8.3", Err: cause}
}
