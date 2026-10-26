// Package engine builds a run's prompt contract from its card, unit and run (SPEC §9.2).
package engine

import (
	"fmt"
	"github.com/sungurerdim/aiwe/internal/core"
	"regexp"
	"strings"
)

// defaultPromptCap is 32 KiB (32 << 10), the SPEC §9.2 prompt cap. A zero CapBytes selects it.
const defaultPromptCap = 32 << 10

// The outcome rows the refusals of this file take, and the one role whose RETURN section binds the
// run (SPEC §5.3, §9.1, §9.3).
const (
	roleAuditor           = "auditor"
	outcomePromptTooLarge = core.Outcome("prompt_too_large")
	outcomeRunIDMissing   = core.Outcome("run_id_missing")
)

// ContractInput is the card text, the unit text and the run they are built for. Repository,
// Branch and FileSet are written into SCOPE. Fix, when non-empty, is the task of a fix round and
// replaces the UNIT section, so a fix round receives the delta only. CapBytes limits the result
// only; it is not part of the text. An empty repository, branch or file set is written as given.
type ContractInput struct {
	RunID      string
	Role       string
	Card       string
	Unit       string
	Fix        string
	Repository string
	Branch     string
	FileSet    []string
	CapBytes   int
}

// Contract is one prompt that fits its cap. Bytes is the length of Text in bytes.
type Contract struct {
	Text  string
	Bytes int
}

// BuildContract assembles the SPEC §9.2 sections from the card, the unit and the run's
// repository, branch and file set. The card, RULES and SCOPE come first as the stable prefix a fix
// round keeps, and then the task: UNIT, or FIX when a fix round supplies its delta (SPEC §7.3 step
// 10, K9). An auditor contract with no run id is refused before the text is built: its RETURN
// section writes the VERDICT line to that id (SPEC §9.1, §9.3, DR-5). A result over the cap is
// prompt_too_large and names the size it measured.
func BuildContract(in ContractInput) (Contract, error) {
	if err := checkBound(in); err != nil {
		return Contract{}, err
	}
	text := contractText(in)
	n := len(text)
	limit := in.CapBytes
	if limit == 0 {
		limit = defaultPromptCap
	}
	if n > limit {
		return Contract{}, tooLarge(n, limit)
	}
	return Contract{Text: text, Bytes: n}, nil
}

// checkBound refuses the input a contract cannot bind: an auditor contract whose RETURN text would
// end "bound to RunID " with no run to name, so its VERDICT could name no run at all (SPEC §9.1,
// §9.3, DR-5).
func checkBound(in ContractInput) error {
	if in.Role != roleAuditor || in.RunID != "" {
		return nil
	}
	return &core.Error{
		Outcome:  outcomeRunIDMissing,
		Expected: "an auditor contract bound to a run id",
		Found:    "an auditor contract with no run id",
		Next:     nextOf(outcomeRunIDMissing),
	}
}

func contractText(in ContractInput) string {
	var b strings.Builder
	writeSection(&b, "ROLE", in.Card)
	writeSection(&b, "RULES", rulesText())
	writeSection(&b, "SCOPE", scopeText(in))
	if in.Fix != "" {
		writeSection(&b, "FIX", in.Fix)
	} else {
		writeSection(&b, "UNIT", in.Unit)
	}
	writeSection(&b, "VERIFY", verifyText())
	writeSection(&b, "EVIDENCE", evidenceText())
	writeSection(&b, "RETURN", returnText(in))
	return b.String()
}

func writeSection(b *strings.Builder, heading, body string) {
	b.WriteString(heading)
	b.WriteByte('\n')
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
}

// scopeText binds this run's repository, branch and file set. The line cap is the SPEC §9.3 limit of 12 lines.
func scopeText(in ContractInput) string {
	var b strings.Builder
	b.WriteString("Repository: ")
	b.WriteString(in.Repository)
	b.WriteByte('\n')
	b.WriteString("Branch: ")
	b.WriteString(in.Branch)
	b.WriteByte('\n')
	b.WriteString("File set:\n")
	for _, path := range in.FileSet {
		b.WriteString(path)
		b.WriteByte('\n')
	}
	b.WriteString("Line cap: 12")
	return b.String()
}

func rulesText() string {
	return "Use Conventional Commits. A structural change removes no check. Contract limits override " +
		"project rules, which override card defaults. User-level harness rules are off."
}

func verifyText() string {
	return "The verify command is the Verification section of the unit. Ask the engine to run it and " +
		"keep the recorded result. Do not run the verify command yourself."
}

func evidenceText() string {
	return "A judging role and the debugger use the recorded results, the full diff, the diff stat, " +
		"and the D-check and mutation findings."
}

func returnText(in ContractInput) string {
	text := "Done names the verify command, the exit code and the last lines. Not done names why. " +
		"Scope deviation names a deliberate behaviour change. At most 12 lines."
	if in.Role != roleAuditor {
		return text
	}
	return text + "\nVERDICT: GREEN|RED bound to RunID " + in.RunID
}

// nextOf returns the next action of the outcome row o: the table in internal/core holds the one copy
// of that text, so a refusal reads it there instead of writing a second copy (SPEC §5.3, DR-17). It
// fails closed, as core itself reads the table: a name with no row takes the error row's next
// action, never an empty one.
func nextOf(o core.Outcome) string {
	var fallback core.OutcomeRow
	for _, row := range core.Outcomes() {
		if row.Outcome == o {
			return row.Next
		}
		if row.Outcome == core.OutcomeError {
			fallback = row
		}
	}
	core.Assert(fallback.Outcome == core.OutcomeError, "the outcome table has no %q row", core.OutcomeError)
	return fallback.Next
}

func tooLarge(size, limit int) error {
	return &core.Error{
		Outcome:  outcomePromptTooLarge,
		Expected: fmt.Sprintf("at most %d bytes", limit),
		Found:    fmt.Sprintf("%d bytes", size),
		Next:     nextOf(outcomePromptTooLarge),
	}
}

// Compiled patterns for one return line (SPEC §5.3). A path token is a slash or dot path, or a dotless file name, then :line.
var (
	returnPathPattern    = regexp.MustCompile(`(?:^|[^\p{L}\p{N}_./-])([\p{L}\p{N}_./-]*[/.][\p{L}\p{N}_./-]*:[0-9]+|[\p{L}_][\p{L}\p{N}_-]*:[0-9]+)`)
	returnIdentPattern   = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])([A-Z][A-Za-z0-9_]*)`)
	returnCommandPattern = regexp.MustCompile("`[^`]+`")
	// returnRefPattern matches a reference a return cites instead of a measurement: a SPEC section,
	// an issue number, a registry id such as DR-5, STD-32, K33 or L17, and the run id the VERDICT
	// line is bound to.
	returnRefPattern = regexp.MustCompile(`§[0-9]+(?:\.[0-9]+)*|#[0-9]+|\b[A-Z]{1,4}-[0-9]+\b|\b[A-Z]{1,2}[0-9]+\b|\bRunID[ \t]+[0-9A-Za-z]+`)
)

// CheckReturn refuses a return the tip does not support (#324). A path:line, including a dotless
// name, and an exported identifier must resolve on the tip. An all-caps label and only the word
// after VERDICT: do not. A number needs its command; "N of M" is a ratio and a percent is not.

// noteFields records the names of a struct's fields or an interface's methods.
