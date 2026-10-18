package engine

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// defaultContractCap is the SPEC §9.2 prompt cap. The spec writes "32 KB"; this
// repo counts the sibling 64 KiB settings cap as 64 << 10, so the default is
// 32 << 10. A zero CapBytes selects it. CapBytes only limits the result: the
// same card and unit produce the same text under every cap that accepts them.
const defaultContractCap = 32 << 10

// contractHeadings returns the fixed sections of every prompt contract, in order (SPEC §9.2).
func contractHeadings() []string {
	return []string{"ROLE", "UNIT", "SCOPE", "RULES", "VERIFY", "EVIDENCE", "RETURN"}
}

const (
	engineerCard = `id: engineer
purpose: implement the unit inside its file set
method: red before only from a recorded result, and three reds with no new cause stop
limits: never touch the test-author files, leave the file set or weaken a test`

	testAuthorCard = `id: test-author
purpose: write the unit tests before code, in one commit
method: tests check the done criteria through the production entry point and fail on base for the missing behaviour
limits: never write production code or add a skipped, focused or empty test`

	auditorCard = `id: auditor
purpose: judge work this role did not write
method: each done criterion gets PASS or FAIL with one evidence line, and this role never fixes
limits: never share a run or context with the writer, and never write VERDICT into a branch commit`

	unitHead = `## Problem
Severity: high · Affected: system
No role card and no prompt contract exist, so a run has no method, limits or model.

## Scope
In:
- internal/roles/schema.go
- internal/roles/cards.go
- internal/roles/roles.go
- internal/engine/contract.go
Out:
- architect card

## To do
- The skeleton cards state each role method and limits — verify: go test ./internal/roles/ → the schema test passes

## Verification
`
	unitTail = `
## Done criteria
- Each skeleton role has a valid card and a bounded prompt contract

## Links
- Merged: #38
`
)

// unitText is one unit body in the SPEC §8.4 shape. The verify line is concatenated
// so the fixture can contain the backticks the standard requires.
func unitText() string {
	return unitHead + "`go test ./internal/roles/... ./internal/engine/...` → exit 0" + unitTail
}

// withRunScope fills the repository, branch and file set SCOPE must bind for this run.
func withRunScope(in ContractInput) ContractInput {
	in.Repository = "sungurerdim/" + core.Product
	in.Branch = "unit/37-role-cards"
	in.FileSet = []string{
		"internal/roles/schema.go",
		"internal/roles/cards.go",
		"internal/roles/roles.go",
		"internal/engine/contract.go",
	}
	return in
}

// TestContractCapRefusalNamesSize proves the prompt contract is built from the card
// and the unit, stays within the default cap, and that an over-cap contract is refused
// with prompt_too_large naming the size it measured (SPEC §9.2, Done criteria).
func TestContractCapRefusalNamesSize(t *testing.T) {
	t.Parallel()
	unit := unitText()
	cards := []struct{ role, card string }{
		{"engineer", engineerCard},
		{"test-author", testAuthorCard},
		{"auditor", auditorCard},
	}
	for _, tc := range cards {
		t.Run(tc.role, func(t *testing.T) {
			t.Parallel()
			in := withRunScope(ContractInput{RunID: "01J8Z3K7Q5W0H8N4P2R6T1V9YA", Role: tc.role, Card: tc.card, Unit: unit})
			got := mustContract(t, in)
			if got.Bytes > defaultContractCap {
				t.Fatalf("contract is %d bytes, over the default cap %d", got.Bytes, defaultContractCap)
			}
		})
	}

	t.Run("cap boundary", func(t *testing.T) {
		t.Parallel()
		under := withRunScope(ContractInput{
			RunID: "01J8Z3K7Q5W0H8N4P2R6T1V9YA",
			Role:  "engineer",
			Card:  engineerCard,
			Unit:  unit,
		})
		var last, next Contract
		var over ContractInput
		for i := 0; i < 10000; i++ {
			got := measureContract(t, under)
			if got.Bytes > defaultContractCap {
				t.Fatalf("contract of %d bytes exceeded the cap before the boundary step", got.Bytes)
			}
			over = under
			over.Unit += "\nThe contract includes this unit line.\n"
			grown := measureContract(t, over)
			if grown.Bytes <= got.Bytes {
				t.Fatal("adding unit text did not grow the contract, so it is not built from the unit")
			}
			if grown.Bytes > defaultContractCap {
				last = got
				next = grown
				break
			}
			under = over
		}
		if last.Bytes == 0 {
			t.Fatal("contract never reached the default cap")
		}
		if accepted := mustContract(t, under); accepted.Bytes != last.Bytes {
			t.Fatalf("default cap refused a contract of %d bytes", last.Bytes)
		}
		over.CapBytes = defaultContractCap
		assertTooLarge(t, over, next.Bytes)
		over.CapBytes = 0
		assertTooLarge(t, over, next.Bytes)
		over.CapBytes = next.Bytes - 1
		assertTooLarge(t, over, next.Bytes)
		over.CapBytes = next.Bytes
		if accepted := mustContract(t, over); accepted.Bytes != next.Bytes {
			t.Fatalf("cap equal to the measured size %d refused the contract", next.Bytes)
		}
	})
}

// TestAuditorContractBindsVerdictToRunID proves the auditor contract binds its VERDICT
// line to the run's RunID (DR-5, SPEC §9.1, §9.3).
func TestAuditorContractBindsVerdictToRunID(t *testing.T) {
	t.Parallel()
	const runID = "01J8Z3K7Q5W0H8N4P2R6T1V9YB"
	in := withRunScope(ContractInput{
		RunID: runID, Role: "auditor", Card: auditorCard, Unit: unitText(),
	})
	c := mustContract(t, in)
	for _, line := range strings.Split(c.Text, "\n") {
		if strings.Contains(line, "VERDICT:") && strings.Contains(line, runID) {
			return
		}
	}
	t.Fatalf("auditor contract has no VERDICT: line bound to RunID %s:\n%s", runID, c.Text)
}

// TestAuditorContractWithoutRunIDIsRefused proves an auditor contract that binds no run is refused
// with run_id_missing: without it the RETURN text ends "bound to RunID " and a VERDICT can name no
// run at all (SPEC §9.1, §9.3, DR-5). The refusal is a *core.Error, so the caller reads one outcome
// and its next action (§5.3).
func TestAuditorContractWithoutRunIDIsRefused(t *testing.T) {
	t.Parallel()
	in := withRunScope(ContractInput{Role: "auditor", Card: auditorCard, Unit: unitText()})
	if in.RunID != "" {
		t.Fatalf("fixture binds RunID %q, want an unbound run", in.RunID)
	}
	_, err := BuildContract(in)
	if err == nil {
		t.Fatal("accepted an auditor contract that binds no run")
	}
	var e *core.Error
	if !errors.As(err, &e) {
		t.Fatalf("refusal is %T (%v), want *core.Error", err, err)
	}
	if e.Outcome != "run_id_missing" {
		t.Fatalf("outcome = %q, want run_id_missing", e.Outcome)
	}
}

// TestNextOfFailsClosedOnAMissingRow proves a refusal whose outcome has no row takes the next action
// of the error row, never an empty string: internal/core holds the one copy of every next action,
// and a missing row is read as the error row, exactly as core reads it itself (SPEC §5.3, DR-17).
func TestNextOfFailsClosedOnAMissingRow(t *testing.T) {
	t.Parallel()
	want := ""
	for _, row := range core.Outcomes() {
		if row.Outcome == core.OutcomeError {
			want = row.Next
		}
	}
	if want == "" {
		t.Fatal("the outcome table has no error row")
	}
	if got := nextOf(core.Outcome("no_such_outcome")); got != want {
		t.Fatalf("nextOf of a row-less outcome = %q, want the error row's next action %q", got, want)
	}
}

// measureContract builds in under a cap above the default, so the measured size is
// known even when the default cap would refuse it.
func measureContract(t *testing.T, in ContractInput) Contract {
	t.Helper()
	in.CapBytes = 1 << 20
	return mustContract(t, in)
}

// mustContract calls the production builder and requires a contract that embeds the
// card and the unit under the caller's cap.
func mustContract(t *testing.T, in ContractInput) Contract {
	t.Helper()
	got, err := BuildContract(in)
	if err != nil {
		t.Fatalf("contract under its cap: %v", err)
	}
	if got.Bytes != len(got.Text) {
		t.Fatalf("Bytes = %d, len(Text) = %d", got.Bytes, len(got.Text))
	}
	limit := in.CapBytes
	if limit == 0 {
		limit = defaultContractCap
	}
	if got.Bytes == 0 || got.Bytes > limit {
		t.Fatalf("contract is %d bytes, want 1..%d", got.Bytes, limit)
	}
	if !strings.Contains(got.Text, in.Card) || !strings.Contains(got.Text, in.Unit) {
		t.Fatal("contract does not contain the card and the unit it was built from")
	}
	if !headingsInOrder(got.Text) {
		t.Fatalf("contract sections are missing or out of order:\n%s", got.Text)
	}
	assertScopeBindsRun(t, got.Text, in)
	return got
}

func headingsInOrder(text string) bool {
	prev := -1
	for _, heading := range contractHeadings() {
		found := -1
		for i, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == heading {
				found = i
				break
			}
		}
		if found <= prev {
			return false
		}
		prev = found
	}
	return true
}

// assertScopeBindsRun requires the SCOPE section to carry this run's repository, branch and
// each file-set path on its own line.
func assertScopeBindsRun(t *testing.T, text string, in ContractInput) {
	t.Helper()
	body := sectionBody(t, text, "SCOPE")
	if in.Repository == "" || in.Branch == "" || len(in.FileSet) == 0 {
		t.Fatal("contract input is missing repository, branch or file set")
	}
	if !strings.Contains(body, "Repository: "+in.Repository) {
		t.Fatalf("SCOPE does not bind repository %q:\n%s", in.Repository, body)
	}
	if !strings.Contains(body, "Branch: "+in.Branch) {
		t.Fatalf("SCOPE does not bind branch %q:\n%s", in.Branch, body)
	}
	for _, path := range in.FileSet {
		found := false
		for _, line := range strings.Split(body, "\n") {
			if line == path {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("SCOPE does not bind file %q:\n%s", path, body)
		}
	}
}

// sectionBody returns the lines between heading and the next contract heading.
func sectionBody(t *testing.T, text, heading string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if line == heading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("contract has no %s section:\n%s", heading, text)
	}
	known := map[string]bool{}
	for _, h := range contractHeadings() {
		known[h] = true
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if known[lines[i]] {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// assertTooLarge requires a prompt_too_large refusal that names size as its own integer.
func assertTooLarge(t *testing.T, in ContractInput, size int) {
	t.Helper()
	_, err := BuildContract(in)
	if err == nil {
		t.Fatalf("contract of %d bytes was accepted", size)
	}
	var e *core.Error
	if !errors.As(err, &e) {
		t.Fatalf("over-cap refusal is %T, want *core.Error", err)
	}
	if e.Outcome != "prompt_too_large" {
		t.Fatalf("outcome = %q, want prompt_too_large", e.Outcome)
	}
	if !namesInt(err.Error(), size) {
		t.Fatalf("refusal %q does not name the size %d", err.Error(), size)
	}
}

// namesInt reports whether s contains n as a whole integer, not as part of a longer one.
func namesInt(s string, n int) bool {
	want := strconv.Itoa(n)
	rest := s
	for {
		i := strings.Index(rest, want)
		if i < 0 {
			return false
		}
		before := i == 0 || rest[i-1] < '0' || rest[i-1] > '9'
		end := i + len(want)
		after := end == len(rest) || rest[end] < '0' || rest[end] > '9'
		if before && after {
			return true
		}
		rest = rest[i+1:]
	}
}
