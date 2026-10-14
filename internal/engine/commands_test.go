package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// unitBodyFor renders a standard unit body whose Scope In list, To do list and Verification line
// are the pieces under test; the Problem line and the closing lines stay constant, so one malformed
// piece isolates one fault (SPEC §8.4).
func unitBodyFor(scopeIn, toDo, verify string) string {
	return "## Problem\nSeverity: high · Affected: owner, system\nA unit body is checked.\n\n" +
		"## Scope\nIn:\n" + scopeIn + "\nOut:\n- none\n\n" +
		"## To do\n" + toDo + "\n\n" +
		"## Verification\n" + verify + "\n"
}

// TestParseUnitRefusesAToDoItemWithoutVerifyAndSignal covers the To do rule of the standard: every
// item is `task — verify: <check> → <signal>`, so a body whose To do list holds an item without a
// verify or without its expected signal is refused and the item is named (SPEC §8.4). A sibling
// well-formed item never lets a malformed one pass.
func TestParseUnitRefusesAToDoItemWithoutVerifyAndSignal(t *testing.T) {
	const good = "- Write the parser tests — verify: `go test ./internal/engine/...` → pass"
	cases := []struct {
		name    string
		catches string
		item    string
	}{
		{
			name:    "no verify and no signal",
			catches: "a To do item that carries neither verify: nor a signal, accepted on the strength of a sibling item",
			item:    "- Polish the wording",
		},
		{
			name:    "verify without a signal",
			catches: "a To do item whose verify names no expected signal, accepted as if the plan were complete",
			item:    "- Review the diff — verify: go vet ./internal/engine/...",
		},
		{
			name:    "signal without a verify",
			catches: "a To do item that names a signal but no verify, accepted as if its check were written",
			item:    "- Review the diff → PASS",
		},
		{
			name:    "a verify with an empty check",
			catches: "a To do item whose check is empty, accepted as though any command would do",
			item:    "- Polish the wording — verify: → done",
		},
		{
			name:    "no task text before the verify",
			catches: "a To do item that names no task, accepted as if the plan were complete",
			item:    "- verify: go vet ./internal/engine/... → pass",
		},
		{
			name:    "no plain list marker",
			catches: "a To do line under another marker, passed over as if the plan held no such item",
			item:    "* Polish the wording",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := unitBodyFor("- internal/engine/unit.go", good+"\n"+tc.item,
				"`go test ./internal/engine/...` → exit 0")
			_, err := ParseUnit(body)
			if err == nil {
				t.Fatalf("%s: ParseUnit accepted a malformed To do item", tc.catches)
			}
			fault := strings.TrimSpace(strings.TrimPrefix(tc.item, "- "))
			if !strings.Contains(err.Error(), fault) {
				t.Errorf("%s: error %q does not name the fault %q", tc.catches, err, fault)
			}
		})
	}
}

// TestParseUnitAcceptsAWellFormedToDoList is the other side of the Done criteria: a body whose
// every To do item carries its verify and its expected signal passes, so the stricter check never
// refuses a unit that follows the standard (SPEC §8.4).
func TestParseUnitAcceptsAWellFormedToDoList(t *testing.T) {
	toDo := "- Write the parser tests — verify: `go test ./internal/engine/...` → new tests red before the code commit\n" +
		"- Review the diff — verify: review verdict → PASS"
	got, err := ParseUnit(unitBodyFor("- internal/engine/unit.go", toDo,
		"`go test ./internal/engine/...` → exit 0"))
	if err != nil {
		t.Fatalf("ParseUnit(well-formed To do list) error = %v, want nil", err)
	}
	if len(got.Tests) == 0 {
		t.Errorf("ParseUnit(well-formed To do list) Tests = %q, want the test item", got.Tests)
	}
}

// TestUnitCheckCommandRegistered proves the engine serves `aiwe unit check` through the one command
// registry: the command is named unit check, carries only declared parameters, has a purpose and a
// run function, so a session can run the unit standard check itself (SPEC §5.7, §7.2).
func TestUnitCheckCommandRegistered(t *testing.T) {
	cmds := Commands()
	if err := core.CheckParams(core.Dictionary(), cmds); err != nil {
		t.Fatalf("the engine commands do not pass the dictionary check: %v", err)
	}
	for _, cmd := range cmds {
		if cmd.Object != "unit" || cmd.Verb != "check" {
			continue
		}
		if cmd.Module != "engine" {
			t.Errorf("unit check module = %q, want %q", cmd.Module, "engine")
		}
		if cmd.Run == nil {
			t.Errorf("unit check has no run function")
		}
		if cmd.Purpose == "" {
			t.Errorf("unit check has no purpose")
		}
		return
	}
	t.Fatalf("Commands() registers no unit check command")
}

// TestUnitCheckRefusesAMalformedBody covers the refusals of the check itself: a body that misses a
// section, whose To do list holds a line without its verify and signal, or whose file set is over
// the ceiling of five source files is refused through the command entry point with the fault named,
// so a malformed unit never passes (SPEC §8.4).
func TestUnitCheckRefusesAMalformedBody(t *testing.T) {
	const (
		scope  = "- internal/engine/unit.go"
		verify = "`go test ./internal/engine/...` → exit 0"
		good   = "- Write the parser tests — verify: `go test ./internal/engine/...` → pass"
	)
	cases := []struct {
		name     string
		body     string
		sentinel error
		names    string
	}{
		{
			name:     "a section the standard needs is missing",
			body:     unitBodyFor(scope, "", verify),
			sentinel: ErrUnitSectionMissing,
			names:    `"To do"`,
		},
		{
			name:  "a To do line carries neither verify nor signal",
			body:  unitBodyFor(scope, good+"\n- Polish the wording", verify),
			names: "Polish the wording",
		},
		{
			name: "the file set is over the five-source ceiling",
			body: unitBodyFor(scope+"\n- internal/engine/a.go\n- internal/engine/b.go"+
				"\n- internal/engine/c.go\n- internal/engine/d.go\n- internal/engine/e.go", good, verify),
			sentinel: ErrUnitScopeTooWide,
			names:    "6 source files",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnitCheck(UnitCheckInput{Body: tc.body})
			if err == nil {
				t.Fatalf("UnitCheck accepted a body that breaks the standard")
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Errorf("UnitCheck error = %v, want errors.Is %v", err, tc.sentinel)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("UnitCheck error %q does not name the fault %q", err, tc.names)
			}
		})
	}
}

// TestUnitCheckAcceptsAWellFormedBody is the other side of the Done criteria: a body that follows
// the standard passes the check, and the result reports the file set, the verify and the test items
// the engine will hold the unit to (SPEC §8.4).
func TestUnitCheckAcceptsAWellFormedBody(t *testing.T) {
	toDo := "- Write the parser tests — verify: `go test ./internal/engine/...` → new tests red before the code commit\n" +
		"- Review the diff — verify: review verdict → PASS"
	res, err := UnitCheck(UnitCheckInput{Body: unitBodyFor(
		"- internal/engine/unit.go", toDo, "`go test ./internal/engine/...` → exit 0")})
	if err != nil {
		t.Fatalf("UnitCheck(well-formed body) error = %v, want nil", err)
	}
	if res.Status != core.StatusOK || res.Outcome != core.OutcomeOK {
		t.Fatalf("UnitCheck(well-formed body) = %s/%s, want %s/%s",
			res.Status, res.Outcome, core.StatusOK, core.OutcomeOK)
	}
	unit, ok := res.Data.(Unit)
	if !ok {
		t.Fatalf("UnitCheck(well-formed body) data = %T, want an engine Unit", res.Data)
	}
	if want := []string{"internal/engine/unit.go"}; !reflect.DeepEqual(unit.Files, want) {
		t.Errorf("UnitCheck files = %q, want %q", unit.Files, want)
	}
	if want := []string{"go test ./internal/engine/..."}; !reflect.DeepEqual(unit.Verify, want) {
		t.Errorf("UnitCheck verify = %q, want %q", unit.Verify, want)
	}
	if want := []string{"Write the parser tests — verify: `go test ./internal/engine/...` → new tests red before the code commit"}; !reflect.DeepEqual(unit.Tests, want) {
		t.Errorf("UnitCheck test items = %q, want %q", unit.Tests, want)
	}
	if !strings.Contains(res.Detail, "unit check: 1 files") {
		t.Errorf("UnitCheck detail = %q, want the file count the check reports", res.Detail)
	}
}

// TestRunCLIServesUnitCheck proves the command is served through the one registry the binary runs:
// the check is bound and its refusal asks for the unit body, so a call never passes a unit the
// engine did not read (SPEC §5.7, DR-6).
func TestRunCLIServesUnitCheck(t *testing.T) {
	var out, stream bytes.Buffer
	code := core.RunCLI(context.Background(), Commands(), []string{"unit", "check", "--format", "json"}, &out, &stream)
	if code != core.StatusUsage.ExitCode() {
		t.Fatalf("unit check without a body exit code = %d, want %d; stdout %q",
			code, core.StatusUsage.ExitCode(), out.String())
	}
	var env struct {
		Outcome core.Outcome `json:"outcome"`
		Detail  string       `json:"detail"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("unit check printed no JSON envelope: %v; stdout %q", err, out.String())
	}
	if env.Outcome != core.OutcomeUsage {
		t.Errorf("unit check outcome = %q, want %q", env.Outcome, core.OutcomeUsage)
	}
	if !strings.Contains(env.Detail, "pass the unit body") {
		t.Errorf("unit check detail = %q, want the next action that asks for the unit body", env.Detail)
	}
}

// TestRunCLIUnitCheckReadsTheBody covers the body parameter on the binary's one command line: a call
// whose --body holds a well-formed unit body passes with the unit the engine will hold it to, and a
// call whose --body breaks the standard is refused with its fault named. A body no front can fill
// would leave the check unreachable, so no well-formed unit would pass it (SPEC §5.7, §8.4).
func TestRunCLIUnitCheckReadsTheBody(t *testing.T) {
	const (
		scope  = "- internal/engine/unit.go"
		verify = "`go test ./internal/engine/...` → exit 0"
		good   = "- Write the parser tests — verify: `go test ./internal/engine/...` → pass"
	)
	cases := []struct {
		name    string
		catches string
		body    string
		code    int
		outcome core.Outcome
		names   string
	}{
		{
			name:    "a well-formed body",
			catches: "a body the front refuses as an undeclared --body although it follows the standard",
			body:    unitBodyFor(scope, good, verify),
			code:    0,
			outcome: core.OutcomeOK,
			names:   "unit check: 1 files",
		},
		{
			name:    "a To do line without its verify and signal",
			catches: "a malformed body accepted on the command line as if the unit followed the standard",
			body:    unitBodyFor(scope, good+"\n- Polish the wording", verify),
			code:    core.StatusError.ExitCode(),
			outcome: core.OutcomeError,
			names:   "Polish the wording",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, stream bytes.Buffer
			code := core.RunCLI(context.Background(), Commands(),
				[]string{"unit", "check", "--body", tc.body, "--format", "json"}, &out, &stream)
			if code != tc.code {
				t.Fatalf("%s: exit code = %d, want %d; stdout %q", tc.catches, code, tc.code, out.String())
			}
			var env struct {
				Outcome core.Outcome `json:"outcome"`
				Detail  string       `json:"detail"`
			}
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatalf("unit check --body printed no JSON envelope: %v; stdout %q", err, out.String())
			}
			if env.Outcome != tc.outcome {
				t.Errorf("%s: outcome = %q, want %q; stdout %q", tc.catches, env.Outcome, tc.outcome, out.String())
			}
			if !strings.Contains(env.Detail, tc.names) {
				t.Errorf("%s: detail %q does not name %q", tc.catches, env.Detail, tc.names)
			}
		})
	}
}
