package repocheck_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// The rule names check repo/trace reports: a rule row whose enforcement does not resolve (an id
// without a live enforcement), and a source marker that names no rule of the registry (an
// enforcement without an id).
const (
	ruleNoLiveEnforcement = "no_live_enforcement"
	ruleUnknownRule       = "unknown_rule"
)

// traceRegistry is the shape of the one registry, docs/standards.md: one row per rule with its
// enforcement in the third cell. DR-1 is enforced by a test, DR-2 by check repo/size and DR-3 by
// the gate step lint; DR-4, DR-5 and DR-6 are planned forms, DR-7 is a judgement of a role the
// cards file ships and DR-8 is decision-only. It holds one row of every grammar the registry writes.
func traceRegistry() []string {
	return []string{
		"# Standards registry",
		"",
		"| id | statement | enforcement | fix | milestone | rev |",
		"|---|---|---|---|---|---|",
		"| DR-1 | The engine runs unattended. | test `gates/runner_test.go` TestLive | Restore the wait limit. | M-Skeleton | ff78469a |",
		"| DR-2 | A managed project needs no setup file. | check `repo/size` | Remove the setting. | M-Skeleton | 40c19761 |",
		"| DR-3 | Source passes the repository's static rules. | check `lint` | Handle the finding. | M-Skeleton | 065cbbd6 |",
		"| DR-4 | Work whose answer is its input is code. | planned #56 | Derive it mechanically. | M-Trust | 664103ce |",
		"| DR-5 | OpenSSF Scorecard reports the posture. | planned: CI job `scorecard` (§12.2) | Close the scored item. | M-Hardening | ab7dccc0 |",
		"| DR-6 | The release notes use Keep a Changelog. | planned: release step `svu` notes (§12.3) | Regroup the notes. | M-Usable | d491121a |",
		"| DR-7 | Tests are never weakened. | judgement: auditor | Restore the assertion. | M-Skeleton | f5905f54 |",
		"| DR-8 | The owner decides the release date. | decision-only | Record the decision. | M-Usable | 0badc0de |",
		"",
	}
}

// traceLive is the one thing the pure check cannot know and its caller supplies: the ids of the
// checks the gate really runs, written as the registry writes them. A check of gate step repo is
// `repo/<check>`; any other gate step is its id.
func traceLive() []string { return []string{"repo/size", "lint"} }

// traceRegistryText is traceRegistry as the file body.
func traceRegistryText() string { return strings.Join(traceRegistry(), "\n") }

// traceRow returns the registry with the row of id replaced by row.
func traceRow(id, row string) string {
	lines := traceRegistry()
	for i, line := range lines {
		if strings.HasPrefix(line, "| "+id+" |") {
			lines[i] = row
		}
	}
	return strings.Join(lines, "\n")
}

// traceLine returns the 1-based line of the row of id in registry.
func traceLine(t *testing.T, registry, id string) int {
	t.Helper()
	for i, line := range strings.Split(registry, "\n") {
		if strings.HasPrefix(line, "| "+id+" |") {
			return i + 1
		}
	}
	t.Fatalf("registry has no row %q", id)
	return 0
}

// traceCards is the Go file of the shipped role cards with one card, auditor, whose one limit
// names gate, a Go expression, on line 5.
func traceCards(gate string) string {
	return "package roles\n\n// Cards returns the shipped cards.\nfunc Cards() []Card {\n" +
		"\treturn []Card{{ID: \"auditor\", MayNot: []Limit{{Text: \"share a run\", Gate: " + gate + "}}}}\n}\n"
}

// traceTree writes a repository tree and returns its root: registry as docs/standards.md, the
// test file DR-1 names, one Go file that carries a rule marker, and the cards file whose limit
// names DR-7. A path in replace is written over its default, and an empty body leaves the path out
// of the tree.
func traceTree(t *testing.T, registry string, replace map[string]string) string {
	t.Helper()
	files := map[string]string{
		"docs/standards.md": registry,
		"internal/gates/runner_test.go": "package gates\n\nimport \"testing\"\n\n" +
			"// TestLive proves the rule of a wait that has a limit.\nfunc TestLive(t *testing.T) {}\n",
		"internal/fixture/fixture.go": "package fixture\n\n// rule: DR-1\n\n" +
			"// Answer returns a fixed number.\nfunc Answer() int { return 42 }\n",
		"internal/roles/cards.go": traceCards(`"DR-7"`),
	}
	for path, body := range replace {
		if body == "" {
			delete(files, path)
			continue
		}
		files[path] = body
	}
	repo := t.TempDir()
	for path, body := range files {
		p := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return repo
}

// TestTrace catches a check repo/trace that passes a rule row whose enforcement no live check or
// test carries, or a source marker that names no rule of the registry: a rule can lose its only
// test today without a single check turning red (the-gate-fails-a-rule-id-without-a-live-enforcement).
func TestTrace(t *testing.T) {
	t.Run("a registry whose every enforcement resolves passes and counts each kind", func(t *testing.T) {
		rep, err := repocheck.Trace(traceTree(t, traceRegistryText(), nil), traceLive())
		if err != nil || len(rep.Findings) != 0 {
			t.Fatalf("Trace = %+v, %v; want no finding and no error", rep, err)
		}
		if rep.Total != 8 || rep.Live != 3 || rep.Planned != 3 || rep.Judgement != 1 || rep.DecisionOnly != 1 {
			t.Fatalf("counts = %d total, %d live, %d planned, %d judgement, %d decision-only; want 8, 3, 3, 1, 1",
				rep.Total, rep.Live, rep.Planned, rep.Judgement, rep.DecisionOnly)
		}
	})

	// Each row of the table drops one enforcement: a test whose function or file is absent, a
	// check id no gate step runs, a judgement role no card ships, and a cell that names no kind or
	// no test function.
	broken := []struct {
		name     string
		registry string
		replace  map[string]string
		row      string
	}{
		{"a test function no file declares", traceRegistryText(),
			map[string]string{
				"internal/gates/runner_test.go": "package gates\n\nimport \"testing\"\n\n" +
					"// TestOther is not the test DR-1 names.\nfunc TestOther(t *testing.T) {}\n",
			}, "DR-1"},
		{"a method that bears the name of the test", traceRegistryText(),
			map[string]string{
				"internal/gates/runner_test.go": "package gates\n\nimport \"testing\"\n\ntype suite struct{}\n\n" +
					"// TestLive is a method, not a test function.\nfunc (suite) TestLive(t *testing.T) {}\n",
			}, "DR-1"},
		{"a test file that is absent", traceRegistryText(),
			map[string]string{"internal/gates/runner_test.go": ""}, "DR-1"},
		{"a check id no check runs",
			traceRow("DR-2", "| DR-2 | A managed project needs no setup file. | check `repo/absent` | Remove the setting. | M-Skeleton | 40c19761 |"),
			nil, "DR-2"},
		{"a judgement role no card names",
			traceRow("DR-7", "| DR-7 | Tests are never weakened. | judgement: nobody | Restore the assertion. | M-Skeleton | f5905f54 |"),
			nil, "DR-7"},
		{"a judgement role in a tree without the cards file", traceRegistryText(),
			map[string]string{"internal/roles/cards.go": ""}, "DR-7"},
		{"a test cell that names no test function",
			traceRow("DR-2", "| DR-2 | A managed project needs no setup file. | test `gates/runner_test.go` | Remove the setting. | M-Skeleton | 40c19761 |"),
			nil, "DR-2"},
		{"an empty enforcement cell",
			traceRow("DR-2", "| DR-2 | A managed project needs no setup file. | | Remove the setting. | M-Skeleton | 40c19761 |"),
			nil, "DR-2"},
		{"an enforcement kind no grammar names",
			traceRow("DR-2", "| DR-2 | A managed project needs no setup file. | validate it by hand | Remove the setting. | M-Skeleton | 40c19761 |"),
			nil, "DR-2"},
	}
	for _, tc := range broken {
		t.Run("a rule id without a live enforcement: "+tc.name, func(t *testing.T) {
			rep, err := repocheck.Trace(traceTree(t, tc.registry, tc.replace), traceLive())
			if err != nil {
				t.Fatalf("Trace error = %v, want the finding %q", err, ruleNoLiveEnforcement)
			}
			if len(rep.Findings) != 1 {
				t.Fatalf("findings = %+v, want exactly one", rep.Findings)
			}
			f := rep.Findings[0]
			if want := traceLine(t, tc.registry, tc.row); f.Rule != ruleNoLiveEnforcement ||
				f.Path != "docs/standards.md" || f.Line != want {
				t.Fatalf("finding = %+v; want %s at docs/standards.md:%d",
					f, ruleNoLiveEnforcement, want)
			}
		})
	}

	// Each row names an id that no rule row holds: a `// rule:` marker, and a Gate value of a card
	// limit, which cites the rule that bounds the role.
	markers := []struct {
		name string
		path string
		body string
		line int
	}{
		{"a marker that names an unknown id", "internal/fixture/fixture.go", "package fixture\n\n// rule: DR-99\n\n" +
			"// Answer returns a fixed number.\nfunc Answer() int { return 42 }\n", 3},
		{"a marker that names no id", "internal/fixture/fixture.go", "package fixture\n\n// rule:\n\n" +
			"// Answer returns a fixed number.\nfunc Answer() int { return 42 }\n", 3},
		{"a card Gate value that names an unknown id", "internal/roles/cards.go", traceCards(`"DR-99"`), 5},
	}
	for _, tc := range markers {
		t.Run("an enforcement without an id: "+tc.name, func(t *testing.T) {
			tree := traceTree(t, traceRegistryText(), map[string]string{tc.path: tc.body})
			rep, err := repocheck.Trace(tree, traceLive())
			if err != nil {
				t.Fatalf("Trace error = %v, want the finding %q", err, ruleUnknownRule)
			}
			if len(rep.Findings) != 1 {
				t.Fatalf("findings = %+v, want exactly one", rep.Findings)
			}
			f := rep.Findings[0]
			if f.Rule != ruleUnknownRule || f.Path != tc.path || f.Line != tc.line {
				t.Fatalf("finding = %+v; want %s at %s:%d", f, ruleUnknownRule, tc.path, tc.line)
			}
		})
	}

	t.Run("a marker inside a string literal is not a marker", func(t *testing.T) {
		// The marker is a comment key: a Go file that quotes one in a string literal names no rule
		// and is not red.
		body := "package fixture\n\n// Doc quotes a rule marker that is not a comment.\n" +
			"const Doc = `\n// rule: DR-99\n`\n"
		tree := traceTree(t, traceRegistryText(), map[string]string{"internal/fixture/fixture.go": body})
		rep, err := repocheck.Trace(tree, traceLive())
		if err != nil || len(rep.Findings) != 0 {
			t.Fatalf("Trace = %+v, %v; want no finding and no error", rep, err)
		}
	})

	t.Run("a tree the check cannot read is an error, never a pass", func(t *testing.T) {
		// DR-6: a registry whose rule rows, a Go file or a card the check cannot read measured
		// nothing; a Go file over the read cap is refused unread (#199).
		cases := []struct {
			name     string
			registry string
			replace  map[string]string
		}{
			{"no docs/standards.md", "", map[string]string{"docs/standards.md": ""}},
			{"a registry with no rule row",
				"# Standards registry\n\n| id | statement | enforcement | fix | milestone | rev |\n|---|---|---|---|---|---|\n",
				nil},
			{"a card Gate value that is no string literal", traceRegistryText(),
				map[string]string{"internal/roles/cards.go": traceCards("gateID")}},
			{"a Go file over the read cap", traceRegistryText(),
				map[string]string{"internal/fixture/big.go": "package fixture\n" + strings.Repeat("/", 4<<20)}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rep, err := repocheck.Trace(traceTree(t, tc.registry, tc.replace), traceLive())
				if err == nil {
					t.Fatalf("Trace = %+v, nil error; want an error", rep)
				}
			})
		}
	})

	t.Run("a tree without a Go file cannot measure the markers", func(t *testing.T) {
		// DR-6: the marker rule read no source file, so it is not green. The registry holds a
		// planned row only, so no rule row of it is red.
		planned := "# Standards registry\n\n| id | statement | enforcement | fix | milestone | rev |\n" +
			"|---|---|---|---|---|---|\n" +
			"| DR-1 | The engine runs unattended. | planned #40 | Restore the wait limit. | M-Skeleton | ff78469a |\n"
		tree := traceTree(t, planned, map[string]string{
			"internal/gates/runner_test.go": "",
			"internal/fixture/fixture.go":   "",
			"internal/roles/cards.go":       "",
		})
		if rep, err := repocheck.Trace(tree, traceLive()); err == nil {
			t.Fatalf("Trace = %+v, nil error; want an error on a tree without a Go file", rep)
		}
	})

	t.Run("a FIFO x.go is an error, never a blocked read", func(t *testing.T) {
		// #199: a plain open of a FIFO waits for a writer, so `repocheck all` would hang.
		if runtime.GOOS == "windows" {
			t.Skip("a FIFO is a Unix file type")
		}
		repo := traceTree(t, traceRegistryText(), nil)
		path := filepath.Join(repo, "internal", "fixture", "x.go")
		if out, err := exec.CommandContext(t.Context(), "mkfifo", "-m", "600", path).CombinedOutput(); err != nil {
			t.Fatalf("mkfifo: %v: %s", err, out)
		}
		done := make(chan error, 1)
		go func() {
			_, err := repocheck.Trace(repo, traceLive())
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "x.go") {
				t.Fatalf("Trace = %v; want an error that names x.go", err)
			}
		case <-time.After(5 * time.Second):
			if w, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
				_ = w.Close()
			}
			<-done
			t.Fatal("Trace still blocked after 5 s; want a named error")
		}
	})
}

// liveGate is a gate.json whose steps are the given JSON step objects.
func liveGate(steps ...string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`{"formatVersion":1,"steps":[` + strings.Join(steps, ",") + `]}`)}
}

// liveStep is one gate.json step with id and the required flag.
func liveStep(id string, required bool) string {
	return fmt.Sprintf(`{"id":%q,"command":["go","version"],"required":%t}`, id, required)
}

// TestLiveChecks catches a live set that holds a check the gate never runs, or drops one it runs:
// each row names the broken read it catches. A check id the set holds wrongly turns a rule row
// with no live enforcement green (the-gate-fails-a-rule-id-without-a-live-enforcement).
func TestLiveChecks(t *testing.T) {
	hook := &fstest.MapFile{Data: []byte("#!/bin/sh\n")}
	checks := []string{"size", "trace"}
	tests := []struct {
		name string
		tree fstest.MapFS
		want []string
	}{
		{"a required step repo makes each repocheck check live, and each required step is live",
			fstest.MapFS{"gate.json": liveGate(liveStep("repo", true), liveStep("lint", true))},
			[]string{"lint", "repo", "repo/size", "repo/trace"}},
		{"a step that is not required is no live check",
			fstest.MapFS{"gate.json": liveGate(liveStep("repo", true), liveStep("deadcode", false))},
			[]string{"repo", "repo/size", "repo/trace"}},
		{"a gate without step repo runs no repocheck check",
			fstest.MapFS{"gate.json": liveGate(liveStep("lint", true))},
			[]string{"lint"}},
		{"a step repo that is not required runs no repocheck check",
			fstest.MapFS{"gate.json": liveGate(liveStep("repo", false), liveStep("lint", true))},
			[]string{"lint"}},
		{"each hook of .githooks is live and a directory there is not",
			fstest.MapFS{"gate.json": liveGate(liveStep("lint", true)),
				".githooks/commit-msg": hook, ".githooks/lib/common.sh": hook},
			[]string{"commit-msg", "lint"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repocheck.LiveChecks(tc.tree, checks)
			if err != nil {
				t.Fatalf("LiveChecks error = %v, want %v", err, tc.want)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("LiveChecks = %v, want %v", got, tc.want)
			}
		})
	}

	// DR-6: a gate.json the check cannot read is an error, never an empty live set; a gate.json
	// that is not a regular file is refused unopened, since an open of a FIFO blocks (#199).
	unreadable := []struct {
		name string
		tree fstest.MapFS
	}{
		{"no gate.json", fstest.MapFS{".githooks/commit-msg": hook}},
		{"a gate.json that is not JSON", fstest.MapFS{"gate.json": {Data: []byte("steps: [repo]")}}},
		{"a gate.json that is a FIFO", fstest.MapFS{"gate.json": {Data: []byte(`{"steps":[]}`), Mode: fs.ModeNamedPipe}}},
	}
	for _, tc := range unreadable {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := repocheck.LiveChecks(tc.tree, checks); err == nil {
				t.Fatalf("LiveChecks = %v, nil error; want an error", got)
			}
		})
	}
}

// traceSpecs are the two specification files a card Gate value resolves against: the diff-check
// table of SPEC-contracts.md, the outcome table and the headings of SPEC.md. Each prose line holds
// an id that no table owns.
func traceSpecs() map[string]string {
	return map[string]string{
		"docs/SPEC-contracts.md": strings.Join([]string{
			"# Contracts", "", "D50 is named in prose only.", "",
			"| Id | Check | Enforced by |", "|---|---|---|",
			"| D1 | Forbidden paths per role | planned #24 |",
			"| D14 | Separation of powers | planned #24 |", "", "Other tables follow the first.", "",
			"| Name | Note |", "|---|---|", "| D88 | not a diff check |", "",
			"| Id | Problem | Mechanism |", "|---|---|---|", "| D77 | not a diff check | none |", "",
			"## 9. Roles", ""}, "\n"),
		"docs/SPEC.md": strings.Join([]string{
			"# Spec", "", "## 15. Deferred", "", "### 5.15 Owned removal", "",
			"| Event | Outcome | Engine action |", "|---|---|---|",
			"| A `VERDICT:` in a commit | `unaudited_commits`, `verdict_in_branch` | Refused; see `in_action_only`. |",
			""}, "\n"),
	}
}

// TestTraceGateValues catches a card Gate value resolved against the wrong owner: a D id needs a
// row of the diff-check table, an outcome key a backticked name of the Outcome column, and a section
// sign a heading; an id no owner holds is red, and an owner that cannot be read is an error, never
// a pass (DR-6).
func TestTraceGateValues(t *testing.T) {
	const (
		pass    = "pass"
		unknown = "unknown"
		failed  = "error"
	)
	cases := []struct {
		name    string
		gate    string
		without string
		want    string
	}{
		{"a D id that the diff-check table holds resolves", `"D1"`, "", pass},
		{"a D id that the diff-check table lacks is red", `"D99"`, "", unknown},
		{"a D id named only in prose is red", `"D50"`, "", unknown},
		{"an outcome key of the Outcome column resolves", `"verdict_in_branch"`, "", pass},
		{"an outcome key that no table holds is red", `"verdict_nowhere"`, "", unknown},
		{"a name in another column is no outcome key", `"in_action_only"`, "", unknown},
		{"a D id of another table with an Id column is red", `"D77"`, "", unknown},
		{"a D id of a table that ended before is red", `"D88"`, "", unknown},
		{"a section of the contracts resolves", `"§9"`, "", pass},
		{"an existing section resolves", `"§15"`, "", pass},
		{"an existing numbered subsection resolves", `"§5.15"`, "", pass},
		{"a missing section is red", `"§99"`, "", unknown},
		{"a rule id of the registry still resolves", `"DR-1"`, "", pass},
		{"an id of no kind is red", `"2 + 2"`, "", unknown},
		{"a D id without SPEC-contracts.md is an error", `"D1"`, "docs/SPEC-contracts.md", failed},
		{"an outcome key without SPEC.md is an error", `"verdict_in_branch"`, "docs/SPEC.md", failed},
		{"a section without SPEC.md is an error", `"§15"`, "docs/SPEC.md", failed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replace := traceSpecs()
			replace["internal/roles/cards.go"] = traceCards(tc.gate)
			if tc.without != "" {
				replace[tc.without] = ""
			}
			rep, err := repocheck.Trace(traceTree(t, traceRegistryText(), replace), traceLive())
			switch tc.want {
			case failed:
				if err == nil {
					t.Fatalf("Trace = %+v, nil error; want an error", rep)
				}
			case pass:
				if err != nil || len(rep.Findings) != 0 {
					t.Fatalf("Trace = %+v, %v; want no finding and no error", rep, err)
				}
			default:
				if err != nil || len(rep.Findings) != 1 || rep.Findings[0].Rule != ruleUnknownRule ||
					rep.Findings[0].Path != "internal/roles/cards.go" || rep.Findings[0].Line != 5 {
					t.Fatalf("Trace = %+v, %v; want %s at internal/roles/cards.go:5", rep, err, ruleUnknownRule)
				}
			}
		})
	}
}
