package repocheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// gateSelector is the command CI runs to pick the gate steps of a change: it calls
// gates.StepsForChange on gate.json and writes the build output, the one output a job reads (#316).
const gateSelector = "go run ./internal/cmd/gatesteps"

// gateStep is the part of a gate.json step this check reads.
type gateStep struct {
	ID string
}

// appendGateDrift adds gate_ci_drift when the workflow can pick gate steps apart from gate.json (K4,
// #316). The two agree only when one job's step runs gateSelector and is the source of that job's one
// output, build; every gate run passes no --step and pastes no expression into its script; no job
// that runs the gate needs that job, directly or through another job, or reads it as needs.<id> or
// needs['<id>'] in any key, value or script of the job (its if, env and steps); and no condition
// names a step id or reads a change class. A missing or unreadable gate.json is drift, never a
// pass (DR-6).
func appendGateDrift(fsys fs.FS, roots []*yamlNode, rep *CIShapeReport) error {
	data, err := readGateFile(fsys)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("ci-shape: %w", err)
	}
	var gate struct{ Steps []gateStep }
	jobs := child(roots, "jobs")
	agree := err == nil && json.Unmarshal(data, &gate) == nil && len(gate.Steps) > 0 && jobs != nil
	if agree {
		sel, ok := selectionJob(jobs)
		agree = ok && gateRunsEvery(jobs, sel) && !namesAStep(roots, gate.Steps) && !decidesAClass(roots)
	}
	if !agree {
		rep.Findings = append(rep.Findings, Finding{Rule: ruleGateCIDrift, Path: ciWorkflowPath})
	}
	return nil
}

// selectionJob returns the one job with a build output. build must be its only output, since
// gateSelector writes no other and an output no job reads is dead configuration; it must come from
// one step, and that step's script must write it only through gateSelector: an output written out,
// or an `echo build=...` beside the selector or in its place, is a second source.
func selectionJob(jobs *yamlNode) (string, bool) {
	found := ""
	for _, job := range jobs.children {
		outputs := child(job.children, "outputs")
		if outputs == nil || child(outputs.children, "build") == nil {
			continue
		}
		id, ok := outputStep(outputs)
		if found != "" || len(outputs.children) != 1 || !ok || !selectorScript(stepWithID(job, id)) {
			return "", false
		}
		found = job.key
	}
	return found, found != ""
}

// outputStep returns the step id every output reads as `${{ steps.<id>.outputs.<key> }}`; an output
// with another value, or outputs of two steps, have no one source.
func outputStep(outputs *yamlNode) (string, bool) {
	id := ""
	for _, o := range outputs.children {
		rest, ok := strings.CutPrefix(strings.ReplaceAll(unquote(o.value), " ", ""), "${{steps.")
		step, tail, cut := strings.Cut(rest, ".")
		if !ok || !cut || tail != "outputs."+o.key+"}}" || (id != "" && step != id) {
			return "", false
		}
		id = step
	}
	return id, id != ""
}

// stepWithID returns the step of job whose id is id, or nil.
func stepWithID(job *yamlNode, id string) *yamlNode {
	steps := child(job.children, "steps")
	if steps == nil {
		return nil
	}
	for _, s := range steps.children {
		if n := child(s.children, "id"); n != nil && unquote(n.value) == id {
			return s
		}
	}
	return nil
}

// script returns the lines of the run of step: the block scalar, or the inline command.
func script(step *yamlNode) []string {
	run := child(step.children, "run")
	switch {
	case run == nil:
		return nil
	case isBlockScalar(run.value):
		return run.block
	}
	return []string{unquote(run.value)}
}

// selectorScript reports whether step writes $GITHUB_OUTPUT on one line only, the line that runs
// gateSelector, with no expression pasted into the script. Every `git diff` names both paths of a
// rename (--no-renames), and no failure is swallowed with `|| true`: a move out of a step's inputs
// would otherwise skip that step (fail open).
func selectorScript(step *yamlNode) bool {
	if step == nil {
		return false
	}
	writes, selector := 0, 0
	for _, l := range script(step) {
		if strings.Contains(l, "GITHUB_OUTPUT") {
			writes++
			if strings.HasPrefix(l, gateSelector+" ") {
				selector++
			}
		}
		if strings.Contains(l, "${{") || strings.Contains(l, "|| true") ||
			(strings.Contains(l, "git diff") && !strings.Contains(l, "--no-renames")) {
			return false
		}
	}
	return writes == 1 && selector == 1
}

// gateRunsEvery reports whether the workflow runs the gate, every gate run runs every step, and no
// job that runs the gate depends on job sel. The runner counts a required step left out of --step as
// not measured, so a gate run on the list of job sel is incomplete and red on every change that
// skips a step (DR-6, SPEC §7.8): that list decides the build matrix only. A gate job that needs job
// sel, directly or through another job, or that reads it anywhere in its body (a job or step if, a
// job or step env), can be skipped on its output, and a skipped job passes a required check.
func gateRunsEvery(jobs *yamlNode, sel string) bool {
	runs := 0
	for _, job := range jobs.children {
		n, every := jobGateRuns(job)
		if n > 0 && (!every || needsJob(jobs, job, sel, map[string]bool{}) || mentionsJob(job.children, sel)) {
			return false
		}
		runs += n
	}
	return runs > 0
}

// jobGateRuns returns how many steps of job run the gate and whether each of them runs every step.
func jobGateRuns(job *yamlNode) (int, bool) {
	steps := child(job.children, "steps")
	if steps == nil {
		return 0, true
	}
	runs := 0
	for _, s := range steps.children {
		lines := script(s)
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "gates gate") }) {
			continue
		}
		runs++
		if !runsEvery(lines) {
			return runs, false
		}
	}
	return runs, true
}

// runsEvery reports whether a gate run takes no step list: no --step names ids or a variable, and no
// expression is pasted into the script, where a list would run as shell.
func runsEvery(lines []string) bool {
	return !slices.ContainsFunc(lines, func(l string) bool {
		return strings.Contains(l, "${{") || strings.Contains(l, "--step")
	})
}

// needsJob reports whether job needs job sel, directly or through a job it needs; seen holds the
// jobs already followed, so a cycle ends.
func needsJob(jobs, job *yamlNode, sel string, seen map[string]bool) bool {
	for _, name := range needsOf(job) {
		if strings.EqualFold(name, sel) {
			return true
		}
		dep := child(jobs.children, name)
		if dep == nil || seen[name] {
			continue
		}
		seen[name] = true
		if needsJob(jobs, dep, sel, seen) {
			return true
		}
	}
	return false
}

// needsOf returns the job names of the needs of job, written as a scalar, a flow list on one or
// several lines, or a block list. The parser keeps each further line of a flow list as a child value
// such as `build,` or `]`, so a child value is trimmed and split like the inline one.
func needsOf(job *yamlNode) []string {
	n := child(job.children, "needs")
	if n == nil {
		return nil
	}
	parts := strings.Split(strings.Trim(n.value, "[] "), ",")
	for _, c := range n.children {
		parts = append(parts, strings.Split(strings.Trim(c.value, "[] "), ",")...)
	}
	var names []string
	for _, p := range parts {
		if name := unquote(strings.TrimSpace(p)); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// mentionsJob reports whether a key, a value or a block line of nodes or of a node below them reads
// job sel through the needs context.
func mentionsJob(nodes []*yamlNode, sel string) bool {
	found := false
	walk(nodes, func(n *yamlNode) {
		for _, l := range append([]string{n.key, n.value}, n.block...) {
			found = found || readsJob(l, sel)
		}
	})
	return found
}

// readsJob reports whether text reads job sel: needs.<sel> followed by no further name character,
// or the index form needs['<sel>'] or needs["<sel>"]. Spaces are removed and case is folded first,
// as an expression ignores both.
func readsJob(text, sel string) bool {
	t := strings.ToLower(strings.ReplaceAll(text, " ", ""))
	sel = strings.ToLower(sel)
	if strings.Contains(t, "needs['"+sel+"']") || strings.Contains(t, `needs["`+sel+`"]`) {
		return true
	}
	for rest := t; ; {
		_, after, ok := strings.Cut(rest, "needs."+sel)
		if !ok {
			return false
		}
		if after == "" || !nameChar(after[0]) {
			return true
		}
		rest = after
	}
}

// nameChar reports whether c can continue a job id: a letter, a digit, '_' or '-'.
func nameChar(c byte) bool {
	return c == '_' || c == '-' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// namesAStep reports a condition that names a gate.json step id, such as contains(..., 'test'): it is
// a second copy of the selection, which stays behind when gate.json moves or renames the step.
func namesAStep(roots []*yamlNode, steps []gateStep) bool {
	found := false
	walk(roots, func(n *yamlNode) {
		for _, s := range steps {
			found = found || (n.key == "if" &&
				(strings.Contains(n.value, "'"+s.ID+"'") || strings.Contains(n.value, `"`+s.ID+`"`)))
		}
	})
	return found
}

// decidesAClass reports a workflow that picks steps on a change class or a path itself: a `# class:`
// comment, an `outputs.classes` read, or a script that matches _test.go or .md paths. Each is a second
// classifier beside gates.StepsForChange.
func decidesAClass(roots []*yamlNode) bool {
	found := false
	walk(roots, func(n *yamlNode) {
		for _, c := range n.comments {
			found = found || strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(c, "#")), "class:")
		}
		for _, l := range append([]string{n.value}, n.block...) {
			found = found || strings.Contains(l, "outputs.classes") || strings.Contains(l, "_test.go") ||
				strings.Contains(l, ".md|.mdx")
		}
	})
	return found
}
