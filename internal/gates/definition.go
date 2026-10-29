// Package gates runs a declared gate step by step and reports each step (DR-6).
package gates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// formatVersion is the only gate.json format this engine reads (SPEC §8.3).
const formatVersion = 1

// Definition is a gate: the stored gate of SPEC §8.3, or a project gate.json in the same format.
// A project gate.json omits Origin, InputHash and Proof.
type Definition struct {
	FormatVersion int        `json:"formatVersion"`
	Steps         []GateStep `json:"steps"`
	// DeclaredDeps are the non-code dependencies of the tests and the packages each selects (L70).
	DeclaredDeps []DeclaredDep `json:"declaredDeps,omitempty"`
	Origin       string        `json:"origin,omitempty"`
	InputHash    string        `json:"inputHash,omitempty"`
	Proof        *Proof        `json:"proof,omitempty"`
}

// GateStep is one step of a gate. Command is an argv array, never a shell string.
type GateStep struct {
	ID       string   `json:"id"`
	Command  []string `json:"command"`
	Required bool     `json:"required"`
	// Inputs are the step's declared inputs as globs; empty means the whole tracked tree (§7.8).
	Inputs []string `json:"inputs,omitempty"`
	// When names the change classes that run this step: docs, test-only, source, config, ci, deps.
	When []string `json:"when,omitempty"`
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
// than version 1, an empty gate, a step without an id, with a duplicate id, without an argv
// command or with a when that is not a change class, and a declaredDeps row without a path,
// with a tests entry that is not ./<dir>, or without a reason (fail-closed, SPEC §4).
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
	if err := checkDeclaredDeps(def.DeclaredDeps); err != nil {
		return err
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

// checkStep refuses a step without an id, without a program to run, with a negative time limit,
// with an output pattern that is not valid RE2 or with a when that is not a change class. The
// refusal is a *core.Error, so the caller can name the step inside it and the text line names the
// status once (#160).
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
	for _, w := range s.When {
		if !validChangeClass(w) {
			return invalidGate("a change class of docs, test-only, source, config, ci or deps",
				fmt.Sprintf("change class %q", w), nil)
		}
	}
	return nil
}

// checkDeclaredDeps refuses a declaredDeps row that declares nothing a selector could use: no path
// glob, a glob that can never match, a tests entry that is not ./<dir>, or no reason. Every row
// says why it exists, and a row that selects no test is a stated exemption (SPEC-contracts §8.3).
func checkDeclaredDeps(rows []DeclaredDep) *core.Error {
	for i, r := range rows {
		if fault := declaredDepFault(r); fault != "" {
			return invalidGate("a declaredDeps row with path globs, ./dir tests and a reason",
				fmt.Sprintf("declaredDeps row %d: %s", i+1, fault), nil)
		}
	}
	return nil
}

// declaredDepFault names what is wrong with a declaredDeps row, or returns "".
func declaredDepFault(r DeclaredDep) string {
	switch {
	case len(r.Paths) == 0:
		return "no path"
	case slices.ContainsFunc(r.Paths, badGlob):
		return "an empty or malformed path glob"
	case slices.ContainsFunc(r.Tests, func(e string) bool { return !validTestDir(e) }):
		return "a tests entry that is not ./<relative directory>"
	case strings.TrimSpace(r.Reason) == "":
		return fmt.Sprintf("%s has no reason", strings.Join(r.Paths, ", "))
	default:
		return ""
	}
}

// validTestDir reports whether a tests entry is `.` or `./<dir>`, a clean slash directory relative
// to the repository root, so one package has one spelling and joins GoSelection.Dir. A pattern
// (`./...`), an import path, a trailing slash, a dot or parent segment and a wildcard are not.
func validTestDir(entry string) bool {
	if entry == "." {
		return true
	}
	dir, ok := strings.CutPrefix(entry, "./")
	return ok && dir != "." && fs.ValidPath(dir) && !strings.Contains(dir, "...") &&
		!strings.ContainsAny(dir, `\*?[`)
}

// badGlob reports a glob that is empty or that path.Match refuses in one of its segments.
func badGlob(glob string) bool {
	if glob == "" {
		return true
	}
	for _, seg := range strings.Split(glob, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return true
		}
	}
	return false
}

// SelectorCoverage names the selector that picks tests from the coverage reverse index (SPEC L66).
const SelectorCoverage = "coverage"

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

const (
	classDocs     = "docs"
	classTestOnly = "test-only"
	classSource   = "source"
	classConfig   = "config"
	classCI       = "ci"
	classDeps     = "deps"
)

func validChangeClass(c string) bool {
	switch c {
	case classDocs, classTestOnly, classSource, classConfig, classCI, classDeps:
		return true
	default:
		return false
	}
}

// StepsForChange returns the step ids a change of paths runs, in declared order.
func StepsForChange(def Definition, paths []string) []string {
	_, steps := ChangeSelection(def, paths)
	return steps
}

// ChangeSelection returns the classes of paths and the steps they run. Empty, unknown or ambiguous is
// all, and so is a change of gate.json or a workflow: it can change what any step runs or how CI runs
// it, so no step may be skipped on it (fail closed).
func ChangeSelection(def Definition, paths []string) (classes, steps []string) {
	ids := declaredStepIDs(def)
	if len(paths) == 0 {
		return []string{"all"}, ids
	}
	set, clean, known := changeClasses(paths)
	if !known {
		return []string{"all"}, ids
	}
	var names []string
	for _, c := range []string{classDocs, classTestOnly, classSource, classConfig, classCI, classDeps} {
		if set[c] {
			names = append(names, c)
		}
	}
	if slices.ContainsFunc(clean, definesTheGate) {
		return names, ids
	}
	return names, stepsForClasses(def, set, clean)
}

// definesTheGate reports a path that decides what the gate runs or how CI runs it.
func definesTheGate(p string) bool { return p == "gate.json" || under(p, ".github/workflows") }

// SelectsCommand reports whether one of the steps ids of def runs a command that starts with argv.
// CI derives its build matrix from it, so the matrix follows the command, not a step id it names.
func SelectsCommand(def Definition, ids []string, argv ...string) bool {
	for _, s := range def.Steps {
		if slices.Contains(ids, s.ID) && len(s.Command) >= len(argv) && slices.Equal(s.Command[:len(argv)], argv) {
			return true
		}
	}
	return false
}

func declaredStepIDs(def Definition) []string {
	ids := make([]string, len(def.Steps))
	for i, s := range def.Steps {
		ids[i] = s.ID
	}
	return ids
}
func changeClasses(paths []string) (map[string]bool, []string, bool) {
	classes := make(map[string]bool, len(paths))
	clean := make([]string, len(paths))
	for i, p := range paths {
		clean[i] = path.Clean(strings.ReplaceAll(p, `\`, `/`))
		got := classesOf(clean[i])
		if len(got) != 1 {
			return nil, nil, false
		}
		classes[got[0]] = true
	}
	return classes, clean, true
}
func stepsForClasses(def Definition, classes map[string]bool, paths []string) []string {
	var ids []string
	for _, s := range def.Steps {
		classOK := len(s.When) == 0 || overlaps(s.When, classes)
		inputsOK := len(s.When) == 0 || inputsTouch(s.Inputs, paths)
		if classOK && inputsOK {
			ids = append(ids, s.ID)
		}
	}
	return ids
}
func overlaps(when []string, classes map[string]bool) bool {
	for _, w := range when {
		if classes[w] {
			return true
		}
	}
	return false
}
func inputsTouch(globs, paths []string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, p := range paths {
		if matchesInputs(p, globs) {
			return true
		}
	}
	return false
}
func classesOf(p string) []string {
	var classes []string
	if docExt(path.Ext(p)) || under(p, "docs") {
		classes = append(classes, classDocs)
	}
	switch {
	case strings.HasSuffix(path.Base(p), "_test.go"):
		classes = append(classes, classTestOnly)
	case path.Ext(p) == ".go":
		classes = append(classes, classSource)
	}
	if under(p, ".github") {
		classes = append(classes, classCI)
	}
	if depPath(p) {
		classes = append(classes, classDeps)
	}
	if configPath(p) {
		classes = append(classes, classConfig)
	}
	return classes
}
func docExt(ext string) bool {
	return ext == ".md" || ext == ".mdx" || ext == ".rst" || ext == ".adoc" || ext == ".txt"
}
func under(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }
func depPath(p string) bool {
	return p == "go.mod" || p == "go.sum" || p == "tools/go.mod" || p == "tools/go.sum"
}
func configPath(p string) bool {
	return p == "gate.json" || p == ".golangci.yml" || p == ".goreleaser.yaml" ||
		p == ".gitattributes" || p == ".gitignore" || under(p, ".githooks")
}
