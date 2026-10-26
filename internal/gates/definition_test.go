package gates

import (
	"bytes"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestParseDefinitionStepRefusalText catches a refusal of one step that prints its status word
// twice on the CLI text line, or that drops which step was refused (#160).
func TestParseDefinitionStepRefusalText(t *testing.T) {
	cases := []struct {
		name, body, step string
	}{
		{"empty id in step 1", `{"formatVersion":1,"steps":[{"id":"","command":["go","test","./..."]}]}`, "step 1"},
		{"no program in step 2", `{"formatVersion":1,"steps":[` +
			`{"id":"vet","command":["go","vet","./..."]},{"id":"test","command":[]}]}`, "step 2"},
		{"bad pattern in step 1", `{"formatVersion":1,"steps":[` +
			`{"id":"test","command":["go","test"],"expect":{"exitCode":0,"outputMatches":"("}}]}`, "step 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDefinition([]byte(tc.body))
			if err == nil {
				t.Fatal("accepted a gate with an invalid step")
			}
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Outcome != core.OutcomeError {
				t.Fatalf("refusal is not a core.Error with outcome %q: %v", core.OutcomeError, err)
			}
			var out bytes.Buffer
			if err := core.WriteResult(&out, core.FormatText, core.ErrorResult(err)); err != nil {
				t.Fatalf("write text result: %v", err)
			}
			line := out.String()
			if n := strings.Count(line, "error:"); n != 1 {
				t.Errorf("text line names the status %d times, want 1: %q", n, line)
			}
			if !strings.HasPrefix(line, "error: ") {
				t.Errorf("text line does not start with the status word: %q", line)
			}
			if !strings.Contains(line, tc.step) {
				t.Errorf("text line drops %q: %q", tc.step, line)
			}
		})
	}
}

// docsOnlyPaths returns a docs-only change: document extensions and docs/** from SPEC §8.2.
// One unknown path among them would make the change unknown. No test reads them, so no step
// other than repo declares them; a doc a test reads is an input of the test step.
func docsOnlyPaths() []string {
	return []string{
		"README.md", "CONTRIBUTING.md", "docs/guide.md", "docs/guide.mdx", "notes.txt", "docs/figure.png",
	}
}

// repositoryCodeSteps returns the SPEC §12.2 run for a code change: the static steps plus test.
// A docs-only change is not in this list; it runs repo alone.
func repositoryCodeSteps() []string {
	return []string{"format", "lint", "vuln", "tidy", "deadcode", "repo", "test"}
}

// repositoryGate reads this repository's gate.json through the production parser.
func repositoryGate(t *testing.T) Definition {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "gate.json"))
	if err != nil {
		t.Fatalf("read gate.json: %v", err)
	}
	def, err := ParseDefinition(data)
	if err != nil {
		t.Fatalf("parse gate.json: %v", err)
	}
	return def
}

// stepIDs returns the step ids of def in declared order.
func stepIDs(def Definition) []string {
	ids := make([]string, len(def.Steps))
	for i, s := range def.Steps {
		ids[i] = s.ID
	}
	return ids
}

// gateStep builds a step that declares when and inputs. command is the argv the step runs.
func gateStep(id string, when, inputs []string, command ...string) GateStep {
	return GateStep{
		ID:       id,
		Command:  command,
		Required: true,
		When:     when,
		Inputs:   inputs,
	}
}

// declaredGate is a gate whose when is the declaration under test. It is not gate.json:
// TestDocsOnlyChangeRunsOnlyTheRepoStep pins this repository's gate to SPEC §12.2.
// Classes of a path, one class or ambiguous: docs is an extension .md .mdx .rst .adoc .txt
// or a path under docs/ (SPEC §8.2); test-only is a name ending in _test.go; source is any
// other .go file; ci is a path under .github/; deps is go.mod, go.sum, tools/go.mod or
// tools/go.sum; config is gate.json, .golangci.yml, .goreleaser.yaml, .gitattributes,
// .gitignore or a path under .githooks/. Two of those classes on one path are ambiguous.
func declaredGate() Definition {
	return Definition{FormatVersion: formatVersion, Steps: []GateStep{
		gateStep("format", []string{"source", "test-only"}, []string{"**/*.go"},
			"gofmt", "-l", "."),
		gateStep("lint", []string{"source", "test-only", "config"}, []string{"**/*.go", ".golangci.yml"},
			"go", "tool", "-modfile=tools/go.mod", "golangci-lint", "run", "--allow-parallel-runners", "./..."),
		gateStep("vuln", []string{"source", "deps"}, []string{"**/*.go", "go.mod", "go.sum"},
			"go", "tool", "-modfile=tools/go.mod", "govulncheck", "./..."),
		gateStep("tidy", []string{"deps"}, []string{"go.mod", "go.sum", "tools/go.mod", "tools/go.sum"},
			"go", "mod", "tidy", "-diff"),
		gateStep("deadcode", []string{"source"}, []string{"**/*.go"},
			"go", "tool", "-modfile=tools/go.mod", "deadcode", "./cmd/...", "./internal/cmd/..."),
		gateStep("repo", []string{"docs", "test-only", "source", "config", "ci", "deps"}, []string{"**"},
			"go", "run", "./internal/cmd/repocheck", "all", "--repo", "."),
		gateStep("test", []string{"source", "test-only", "deps"},
			[]string{"**/*.go", "go.mod", "go.sum", "tools/go.mod", "tools/go.sum"},
			"go", "test", "-json", "./..."),
	}}
}

// TestDocsOnlyChangeRunsOnlyTheRepoStep catches a docs-only change that still runs lint,
// vuln or test, and a source or test-only change that does not run the SPEC §12.2 code
// steps. An unknown path, an ambiguous path and a change with no paths run every step.
func TestDocsOnlyChangeRunsOnlyTheRepoStep(t *testing.T) {
	def := repositoryGate(t)
	all := stepIDs(def)

	t.Run("each step declares when and inputs", func(t *testing.T) {
		for _, s := range def.Steps {
			if len(s.Inputs) == 0 || len(s.When) == 0 {
				t.Errorf("step %q inputs %v when %v; each step declares both", s.ID, s.Inputs, s.When)
			}
			if s.ID != "repo" && slices.Contains(s.When, "docs") && !namesEachDoc(s) {
				t.Errorf("step %q when includes docs and inputs %v match docs by a glob, so a docs-only change runs it",
					s.ID, s.Inputs)
			}
		}
		repo, ok := stepByID(def, "repo")
		if !ok || !slices.Contains(repo.When, "docs") {
			t.Errorf("repo when = %v, want docs", repo.When)
		}
	})

	t.Run("docs", func(t *testing.T) {
		if got := StepsForChange(def, docsOnlyPaths()); !slices.Equal(got, []string{"repo"}) {
			t.Fatalf("StepsForChange(%v) = %v, want [repo]", docsOnlyPaths(), got)
		}
	})

	t.Run("source", func(t *testing.T) {
		got := StepsForChange(def, []string{"internal/gates/definition.go", "cmd/" + core.Product + "/main.go"})
		if !slices.Equal(got, repositoryCodeSteps()) {
			t.Fatalf("StepsForChange(source) = %v, want %v", got, repositoryCodeSteps())
		}
	})

	t.Run("test-only", func(t *testing.T) {
		got := StepsForChange(def, []string{"internal/gates/definition_test.go"})
		if !slices.Equal(got, repositoryCodeSteps()) {
			t.Fatalf("StepsForChange(test-only) = %v, want %v", got, repositoryCodeSteps())
		}
	})

	t.Run("unknown", func(t *testing.T) {
		if got := StepsForChange(def, []string{"assets/logo.png"}); !slices.Equal(got, all) {
			t.Fatalf("StepsForChange(assets/logo.png) = %v, want every step %v", got, all)
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		if got := StepsForChange(def, []string{"docs/example_test.go"}); !slices.Equal(got, all) {
			t.Fatalf("StepsForChange(docs/example_test.go) = %v, want every step %v", got, all)
		}
	})

	t.Run("no paths", func(t *testing.T) {
		if got := StepsForChange(def, nil); !slices.Equal(got, all) {
			t.Fatalf("StepsForChange(no paths) = %v, want every step %v", got, all)
		}
	})

	for _, p := range []string{"gate.json", ".github/workflows/ci.yml"} {
		t.Run("a change of "+p, func(t *testing.T) {
			if got := StepsForChange(def, []string{p}); !slices.Equal(got, all) {
				t.Fatalf("StepsForChange(%s) = %v, want every step %v", p, got, all)
			}
		})
	}
}

// TestParseDefinitionChangeClass catches a when value that is not a change class
// (docs, test-only, source, config, ci, deps) accepted as a step, including a refusal
// that only happens because when is still an unknown field.
func TestParseDefinitionChangeClass(t *testing.T) {
	const body = `{"formatVersion":1,"steps":[{"id":"repo","command":["go","run","./internal/cmd/repocheck","all","--repo","."],"required":true,"inputs":["**"],"when":["published"],"expect":{"exitCode":0}}]}`
	_, err := ParseDefinition([]byte(body))
	if err == nil || !strings.Contains(err.Error(), "published") || strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ParseDefinition = %v; want a refusal of change class published", err)
	}
}

// TestSelectsCommand catches a CI build matrix that is not derived from gate.json: a docs-only
// selection that still builds and tests every OS, a code selection that does not, or a match on
// the step id instead of the command the step runs.
func TestSelectsCommand(t *testing.T) {
	def := repositoryGate(t)
	if SelectsCommand(def, StepsForChange(def, docsOnlyPaths()), "go", "test") {
		t.Errorf("SelectsCommand(docs-only selection, go test) = true, want false")
	}
	if !SelectsCommand(def, StepsForChange(def, []string{"internal/gates/definition.go"}), "go", "test") {
		t.Errorf("SelectsCommand(source selection, go test) = false, want true")
	}
	renamed := declaredGate()
	for i := range renamed.Steps {
		if renamed.Steps[i].ID == "test" {
			renamed.Steps[i].ID = "unit"
		}
	}
	if !SelectsCommand(renamed, []string{"unit"}, "go", "test") {
		t.Errorf("SelectsCommand([unit], go test) = false, want true: the command decides, not the id")
	}
	if SelectsCommand(renamed, []string{"test"}, "go", "test") {
		t.Errorf("SelectsCommand([test], go test) = true, want false: no step of that id exists")
	}
	if SelectsCommand(renamed, []string{"format"}, "gofmt", "-l", ".", "-w") {
		t.Errorf("SelectsCommand(argv longer than the command) = true, want false")
	}
}

// namesEachDoc reports whether every input of s that can match a doc names one path, never a glob: a
// step that reads a doc runs on that doc alone, not on every docs-only change. It fails closed: a
// glob can match a doc unless it ends in a literal .go outside docs/, since a Go file is source or
// test-only and so never in a docs-only change; `**`, `**/*`, `README*` and `**/*.png` all can, and
// no inputs is the whole tree.
func namesEachDoc(s GateStep) bool {
	if len(s.Inputs) == 0 {
		return false
	}
	for _, in := range s.Inputs {
		if !strings.ContainsAny(in, "*?[") {
			continue
		}
		if slices.Contains(classesOf(in), "docs") || !strings.HasSuffix(path.Base(in), ".go") {
			return false
		}
	}
	return true
}

// TestNamesEachDoc catches a docs rule that passes a step whose inputs can match a doc by a glob, so
// a docs-only change runs it. Each row names the input that would slip past the rule.
func TestNamesEachDoc(t *testing.T) {
	cases := []struct {
		name   string // the failure the row catches
		inputs []string
		want   bool
	}{
		{"every path (**) read as no doc", []string{"go.mod", "**"}, false},
		{"every file (**/*) read as no doc", []string{"go.mod", "**/*"}, false},
		{"a name prefix (README*) read as no doc", []string{"go.mod", "README*"}, false},
		{"a non-doc extension docs/ can hold read as no doc", []string{"go.mod", "**/*.png"}, false},
		{"no inputs, the whole tree, read as no doc", nil, false},
		{"a doc extension glob read as no doc", []string{"go.mod", "**/*.md"}, false},
		{"a glob under docs/ read as no doc", []string{"go.mod", "docs/**"}, false},
		{"a Go glob under docs/ read as no doc", []string{"go.mod", "docs/*.go"}, false},
		{"a Go glob, which no docs-only path matches, refused", []string{"go.mod", "**/*.go"}, true},
		{"a named doc refused", []string{"go.mod", "docs/SPEC.md"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := GateStep{ID: "test", When: []string{"docs"}, Inputs: tc.inputs}
			if got := namesEachDoc(s); got != tc.want {
				t.Fatalf("namesEachDoc(inputs %v) = %v, want %v", tc.inputs, got, tc.want)
			}
		})
	}
}

// stepByID returns the step with id.
func stepByID(def Definition, id string) (GateStep, bool) {
	for _, s := range def.Steps {
		if s.ID == id {
			return s, true
		}
	}
	return GateStep{}, false
}

// TestStepsForChange catches a selector that ignores when or inputs, treats an unknown or
// ambiguous path as a skip, or hard-codes a docs-only change to the repo step.
func TestStepsForChange(t *testing.T) {
	def := declaredGate()
	all := stepIDs(def)
	sourceSteps := []string{"format", "lint", "vuln", "deadcode", "repo", "test"}
	cases := []struct {
		name  string
		paths []string
		want  []string
	}{
		{"docs", docsOnlyPaths(), []string{"repo"}},
		{"test-only", []string{"internal/gates/definition_test.go"}, []string{"format", "lint", "repo", "test"}},
		{"source", []string{"internal/gates/definition.go", "cmd/" + core.Product + "/main.go"}, sourceSteps},
		{"config", []string{".golangci.yml"}, []string{"lint", "repo"}},
		{"config the lint inputs miss", []string{".gitignore"}, []string{"repo"}},
		{"ci outside the workflows", []string{".github/CODEOWNERS"}, []string{"repo"}},
		// gate.json and a workflow decide what runs and how, so a change of them can break any step.
		{"gate.json runs every step", []string{"gate.json"}, all},
		{"a workflow runs every step", []string{".github/workflows/ci.yml"}, all},
		{"a workflow beside docs runs every step", []string{"README.md", ".github/workflows/ci.yml"}, all},
		// git diff --no-renames names both sides of a move, so a moved source still runs its steps.
		{"a source moved to testdata", []string{"internal/gates/commands.go", "internal/gates/testdata/commands.txt"}, sourceSteps},
		{"deps", []string{"go.mod", "tools/go.sum"}, []string{"vuln", "tidy", "repo", "test"}},
		{"docs and source", []string{"README.md", "internal/gates/definition.go"}, sourceSteps},
		{"unknown", []string{"assets/logo.png"}, all},
		{"ambiguous docs and test", []string{"docs/example_test.go"}, all},
		{"ambiguous ci and docs", []string{".github/README.md"}, all},
		{"no paths", nil, all},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StepsForChange(def, tc.paths); !slices.Equal(got, tc.want) {
				t.Fatalf("StepsForChange(%v) = %v, want %v", tc.paths, got, tc.want)
			}
		})
	}

	t.Run("a step with no when runs", func(t *testing.T) {
		// Impact is unknown, so the whole step runs beside the steps the class names.
		open := declaredGate()
		open.Steps[0].When = nil
		got := StepsForChange(open, []string{"README.md"})
		if !slices.Equal(got, []string{"format", "repo"}) {
			t.Fatalf("StepsForChange(README.md) = %v, want [format repo]", got)
		}
	})

	t.Run("docs is not hard-coded to repo", func(t *testing.T) {
		open := declaredGate()
		open.Steps[1].When = []string{"docs", "source", "test-only", "config"}
		open.Steps[1].Inputs = []string{"**/*.md", "**/*.txt", "**/*.go", "docs/**"}
		got := StepsForChange(open, []string{"README.md"})
		if !slices.Equal(got, []string{"lint", "repo"}) {
			t.Fatalf("StepsForChange(README.md) = %v, want [lint repo]", got)
		}
	})

	t.Run("inputs exclude a step the class names", func(t *testing.T) {
		open := declaredGate()
		open.Steps[1].When = []string{"docs", "source", "test-only", "config"}
		got := StepsForChange(open, []string{"README.md"})
		if !slices.Equal(got, []string{"repo"}) {
			t.Fatalf("StepsForChange(README.md) = %v, want [repo]", got)
		}
	})

	t.Run("a literal input path with a glob character selects its step", func(t *testing.T) {
		// The cache key counts docs/a[1].md as an input (matchesInputs); selection must agree.
		open := Definition{FormatVersion: formatVersion, Steps: []GateStep{
			gateStep("docs", []string{"docs"}, []string{"docs/a[1].md"}, "true"),
		}}
		got := StepsForChange(open, []string{"docs/a[1].md"})
		if !slices.Equal(got, []string{"docs"}) {
			t.Fatalf("StepsForChange(docs/a[1].md) = %v, want [docs]", got)
		}
	})

	t.Run("when excludes a step the inputs name", func(t *testing.T) {
		open := declaredGate()
		open.Steps[0].Inputs = []string{"**/*.md", "**/*.go"}
		got := StepsForChange(open, []string{"README.md"})
		if !slices.Equal(got, []string{"repo"}) {
			t.Fatalf("StepsForChange(README.md) = %v, want [repo]", got)
		}
	})

	t.Run("classes come from the same selection", func(t *testing.T) {
		classes, steps := ChangeSelection(def, []string{"README.md"})
		if !slices.Equal(classes, []string{"docs"}) || !slices.Equal(steps, []string{"repo"}) {
			t.Fatalf("ChangeSelection(README.md) = %v %v, want [docs] [repo]", classes, steps)
		}
		classes, steps = ChangeSelection(def, nil)
		if !slices.Equal(classes, []string{"all"}) || !slices.Equal(steps, all) {
			t.Fatalf("ChangeSelection(no paths) = %v %v, want [all] and every step", classes, steps)
		}
	})

	t.Run("parsed when and inputs are kept", func(t *testing.T) {
		const body = `{"formatVersion":1,"steps":[` +
			`{"id":"lint","command":["go","tool","golangci-lint","run","./..."],"required":true,` +
			`"inputs":["**/*.go"],"when":["source"],"expect":{"exitCode":0}},` +
			`{"id":"repo","command":["go","run","./internal/cmd/repocheck","all","--repo","."],` +
			`"required":true,"inputs":["**"],"when":["docs","source"],"expect":{"exitCode":0}}]}`
		parsed, err := ParseDefinition([]byte(body))
		if err != nil {
			t.Fatalf("parse a step that declares when and inputs: %v", err)
		}
		if !slices.Equal(parsed.Steps[1].When, []string{"docs", "source"}) ||
			!slices.Equal(parsed.Steps[1].Inputs, []string{"**"}) {
			t.Fatalf("parsed repo step = %+v, want when [docs source] and inputs [**]", parsed.Steps[1])
		}
		if got := StepsForChange(parsed, []string{"README.md"}); !slices.Equal(got, []string{"repo"}) {
			t.Fatalf("StepsForChange(README.md) = %v, want [repo]", got)
		}
	})
}
