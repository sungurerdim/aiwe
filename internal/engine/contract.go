// Package engine builds a run's prompt contract from its card, unit and run (SPEC §9.2).
package engine

import (
	"fmt"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// defaultPromptCap is the SPEC §9.2 prompt cap. The spec writes "32 KB"; the sibling settings cap
// counts 64 KiB as 64 << 10, so this cap is 32 << 10. A zero CapBytes selects it.
const defaultPromptCap = 32 << 10

// ContractInput is the card text, the unit text and the run they are built for. Repository,
// Branch and FileSet are written into SCOPE. CapBytes limits the result only; it is not part of
// the text. An empty repository, branch or file set is written as given.
type ContractInput struct {
	RunID      string
	Role       string
	Card       string
	Unit       string
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
// repository, branch and file set. A result over the cap is prompt_too_large and names the size
// it measured.
func BuildContract(in ContractInput) (Contract, error) {
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

func contractText(in ContractInput) string {
	var b strings.Builder
	writeSection(&b, "ROLE", in.Card)
	writeSection(&b, "UNIT", in.Unit)
	writeSection(&b, "SCOPE", scopeText(in))
	writeSection(&b, "RULES", rulesText())
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

// scopeText binds this run's repository, branch and file set. The line cap is the SPEC §9.3
// limit of 12 lines, which is the same for every run.
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
	if in.Role != "auditor" {
		return text
	}
	return text + "\nVERDICT: GREEN|RED bound to RunID " + in.RunID
}

func tooLarge(size, limit int) error {
	return &core.Error{
		Outcome:  "prompt_too_large",
		Expected: fmt.Sprintf("at most %d bytes", limit),
		Found:    fmt.Sprintf("%d bytes", size),
		Next:     "shorten the card or the unit so the measured size fits the cap",
	}
}
