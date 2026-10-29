// Package engine reads one judging return: the verdict counts only from its own run bound to its
// RunID (DR-5), a RED that cites a done criterion or a rule opens a fix round (K8), and the task of
// that round is the FIX delta only (SPEC §7.3 step 10).
package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/sungurerdim/aiwe/internal/roles"
)

// Verdict is the one word a judging run's return binds to its own run (SPEC §9.1, §9.3).
type Verdict string

const (
	VerdictGreen Verdict = "GREEN"
	VerdictRed   Verdict = "RED"
)

const (
	// maxFixRounds is the §7.4 ceiling: two fix rounds at most, then the unit is quarantined.
	maxFixRounds = 2
	// fixReturnLines is the §9.3 limit on the previous return a fix round carries.
	fixReturnLines = 12
)

// UnboundVerdictError is a verdict that names another run or no run: DR-5 lets a verdict count only
// from its own result bound to its RunID.
type UnboundVerdictError struct {
	RunID   string
	BoundTo string
}

// Error names both runs, so a reader sees which run wrote the verdict.
func (e *UnboundVerdictError) Error() string {
	if e.BoundTo == "" {
		return fmt.Sprintf("verdict: the return binds no run; it counts only bound to RunID %s", e.RunID)
	}
	return fmt.Sprintf("verdict: the return binds RunID %s; it counts only from its own run %s", e.BoundTo, e.RunID)
}

// ReadVerdict reads the auditor's return bound to runID and the judgement it carries. The VERDICT
// line must name runID itself, so a verdict of another run is refused (DR-5).
func ReadVerdict(runID, text string, criteria []string) (Verdict, roles.Judgement, error) {
	verdict, bound, err := parseVerdict(text)
	if err != nil {
		return "", roles.Judgement{}, err
	}
	if bound != runID {
		return "", roles.Judgement{}, &UnboundVerdictError{RunID: runID, BoundTo: bound}
	}
	judgement, err := roles.ParseJudgement(normalisedReturn(text, verdict), criteria)
	if err != nil {
		return "", roles.Judgement{}, fmt.Errorf("verdict: %w", err)
	}
	return verdict, judgement, nil
}

// parseVerdict reads the one VERDICT line of a return: its word and the run id it binds.
func parseVerdict(text string) (Verdict, string, error) {
	verdict, bound, seen := Verdict(""), "", false
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(strings.TrimRight(line, "\r")), "VERDICT:")
		if !ok {
			continue
		}
		if seen {
			return "", "", errors.New("verdict: the return carries two VERDICT lines, want one")
		}
		seen = true
		var err error
		if verdict, bound, err = parseVerdictLine(rest); err != nil {
			return "", "", err
		}
	}
	if !seen {
		return "", "", errors.New("verdict: the return carries no VERDICT line")
	}
	return verdict, bound, nil
}

// parseVerdictLine reads what follows `VERDICT:`: the GREEN or RED word and, when given, the run id
// after `bound to RunID`.
func parseVerdictLine(rest string) (Verdict, string, error) {
	word, tail, _ := strings.Cut(strings.TrimSpace(rest), " ")
	verdict := Verdict(word)
	if verdict != VerdictGreen && verdict != VerdictRed {
		return "", "", fmt.Errorf("verdict: %q is no GREEN or RED word", word)
	}
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return verdict, "", nil
	}
	id, ok := strings.CutPrefix(tail, "bound to RunID ")
	if !ok || strings.TrimSpace(id) == "" {
		return "", "", fmt.Errorf("verdict: %q is not `bound to RunID <id>`", tail)
	}
	return verdict, strings.TrimSpace(id), nil
}

// normalisedReturn returns text for roles.ParseJudgement: the verdict line becomes the exact
// `VERDICT: GREEN` or `VERDICT: RED`, so a one-line GREEN return never reaches it as empty text.
func normalisedReturn(text string, verdict Verdict) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "VERDICT:") {
			lines[i] = "VERDICT: " + string(verdict)
		}
	}
	return strings.Join(lines, "\n")
}

// FixDelta is the task of one fix round (SPEC §7.3 step 10): the failing items of the red record,
// the diff stat, the RED findings with path:line and the previous return, at most §9.3's 12 lines.
type FixDelta struct {
	Failing  []string
	DiffStat string
	Findings []roles.Finding
	Previous string
	// Output is the output tail of a red verify, which is no earlier return.
	Output string
}

// Text renders the FIX section: the delta only, never the unit text.
func (d FixDelta) Text() string {
	var b strings.Builder
	for _, item := range d.Failing {
		b.WriteString("Failing: " + item + "\n")
	}
	for _, finding := range d.Findings {
		b.WriteString("- " + finding.Location + " [" + finding.Rule + "] " +
			finding.Evidence + " — " + finding.Impact + "\n")
	}
	if d.DiffStat != "" {
		b.WriteString("Diff stat:\n" + d.DiffStat + "\n")
	}
	if output := cutLines(strings.TrimSpace(d.Output), fixReturnLines); output != "" {
		b.WriteString("Verify output:\n" + output + "\n")
	}
	if previous := cutLines(strings.TrimSpace(d.Previous), fixReturnLines); previous != "" {
		b.WriteString("Previous return:\n" + previous + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// FixRoundAllowed reports whether another fix round may start: at most maxFixRounds, then the unit
// is quarantined (SPEC §7.4).
func FixRoundAllowed(done int) bool {
	return done < maxFixRounds
}

// cutLines returns the last at most n lines of text, so a long return fits the §9.3 limit.
func cutLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// auditDelta returns the FIX task of a red audit: the RED findings with path:line, the diff stat
// and the auditor's own return (SPEC §7.3 step 10).
func auditDelta(judgement roles.Judgement, previous, stat string) FixDelta {
	return FixDelta{Findings: judgement.Findings, DiffStat: stat, Previous: previous}
}

// verifyDelta returns the FIX task of a red verify: the failing verify, its output tail and the
// diff stat (SPEC §7.3 step 10).
func verifyDelta(report VerifyReport, stat string) FixDelta {
	return FixDelta{
		Failing:  []string{"verify " + report.Step + " exited " + strconv.Itoa(report.ExitCode)},
		DiffStat: stat,
		Output:   report.Tail,
	}
}
