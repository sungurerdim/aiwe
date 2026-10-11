package repocheck_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// rulesSpec is a spec in the shape of docs/SPEC.md: a §3 table that names tests relative to
// internal/, the §12.1 repository files, and a §14 phase table whose Files cells list sources.
// It uses the stand-in product name of the other fixtures.
func rulesSpec() string {
	return strings.Join([]string{
		"# zentra — specification",
		"",
		"## 3. Design rules",
		"",
		"| # | Rule | Serves | Enforcement |",
		"|---|---|---|---|",
		"| DR-1 | The engine runs unattended. | M2 | test `engine/lifecycle_test.go` (every exit path returns a named outcome) |",
		"| DR-2 | Records are append-only. | M1 | test `ledger/record_test.go` |",
		"| DR-3 | Text stays byte-exact. | S1 | test `core/text_test.go`; check `repo/source-rules` |",
		"",
		"## 12. Repository",
		"",
		"### 12.1 Repository files (industry standard only)",
		"",
		"`LICENSE` (MIT) · `README.md` · `CONTRIBUTING.md` · `SECURITY.md` (private vulnerability",
		"reporting) · `CODE_OF_CONDUCT.md` · `go.mod`, `go.sum` ·",
		"`gate.json` (§12.2) · `.golangci.yml`, `.custom-gcl.yml` (§12.2) · `.goreleaser.yaml` (§12.3) ·",
		"`.githooks/commit-msg` · `.gitattributes` (LF in every checkout, DR-27) · `.gitignore` (`/dist/`,",
		"the release output, §12.3) · `.github/dependabot.yml` (ecosystems `gomod` and `github-actions`) ·",
		"`.github/workflows/ci.yml` (the only workflow).",
		"No issue form and no pull-request template: `CONTRIBUTING.md` names `zentra unit template`",
		"and `git config core.hooksPath .githooks`.",
		"No `CHANGELOG.md`: release notes live on each GitHub Release (§12.3).",
		"",
		"### 12.2 Gate, tool chain and CI",
		"",
		"**Gate** — `gate.json`, run by `zentra gates gate --repo .`.",
		"",
		"| Step | Command | Catches | Why |",
		"|---|---|---|---|",
		"| `format` | `gofmt -l .` (empty output) | unformatted files | one style |",
		"| `test` | `go test -json ./...` | behaviour | DR-38 limits apply |",
		"",
		"## 14. Delivery plan",
		"",
		"### Phase 0 — Go skeleton",
		"",
		"| Unit | Goal | Files | Verify |",
		"|---|---|---|---|",
		"| 0.5 | Text stays byte-exact. | `core/text.go` | `go test -run Text ./internal/core/` |",
		"",
		"### Phase 1 — One sandbox repository end to end",
		"",
		"| Unit | Goal | Files | Verify |",
		"|---|---|---|---|",
		"| 1.24 | Records append and rotate. | `ledger/record.go`, `ledger/rotate.go` | `go test -run Record ./internal/ledger/` |",
		"",
	}, "\n")
}

// rulesGate is a gate.json that holds every §12.2 step of rulesSpec.
const rulesGate = `{"formatVersion": 1, "steps": [
  {"id": "format", "command": ["gofmt", "-l", "."], "required": true, "expect": {"exitCode": 0}},
  {"id": "test", "command": ["go", "test", "./..."], "required": true, "expect": {"exitCode": 0}}
]}
`

// rulesRepoFiles are the §12.1 files of rulesSpec.
func rulesRepoFiles() []string {
	return []string{
		"LICENSE", "README.md", "CONTRIBUTING.md", "SECURITY.md", "CODE_OF_CONDUCT.md",
		"go.mod", "go.sum", "gate.json", ".golangci.yml", ".custom-gcl.yml", ".goreleaser.yaml",
		".githooks/commit-msg", ".gitattributes", ".gitignore", ".github/dependabot.yml",
		".github/workflows/ci.yml",
	}
}

// rulesRepo writes a repository with rulesSpec as docs/SPEC.md, every §12.1 file except those in
// skip, and the given extra files, each with a one-line body; gate.json is rulesGate.
func rulesRepo(t *testing.T, skip map[string]bool, extra ...string) string {
	t.Helper()
	return rulesRepoSpec(t, rulesSpec(), skip, extra...)
}

// rulesSpecWith returns rulesSpec with the text from the line starting with from up to the line
// starting with to (or the end, when to is empty) replaced by body.
func rulesSpecWith(t *testing.T, from, to string, body ...string) string {
	t.Helper()
	spec := rulesSpec()
	i := strings.Index(spec, from)
	j := len(spec)
	if to != "" {
		j = strings.Index(spec, to)
	}
	if i < 0 || j < i {
		t.Fatalf("rulesSpec has no %q before %q", from, to)
	}
	repl := ""
	if len(body) > 0 {
		repl = strings.Join(body, "\n") + "\n\n"
	}
	return spec[:i] + repl + spec[j:]
}

// rulesRepoSpec is rulesRepo with spec as docs/SPEC.md.
func rulesRepoSpec(t *testing.T, spec string, skip map[string]bool, extra ...string) string {
	t.Helper()
	dir := t.TempDir()
	writeRulesFile(t, dir, "docs/SPEC.md", spec)
	for _, f := range rulesRepoFiles() {
		switch {
		case skip[f]:
		case f == "gate.json":
			writeRulesFile(t, dir, f, rulesGate)
		default:
			writeRulesFile(t, dir, f, "fixture\n")
		}
	}
	for _, f := range extra {
		writeRulesFile(t, dir, f, "package fixture\n")
	}
	return dir
}

// writeRulesFile writes body to the slash-separated, repository-relative file rel of dir.
func writeRulesFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// specLine returns the 1-based line of spec that starts with prefix.
func specLine(t *testing.T, spec, prefix string) int {
	t.Helper()
	for i, line := range strings.Split(spec, "\n") {
		if strings.HasPrefix(line, prefix) {
			return i + 1
		}
	}
	t.Fatalf("spec has no line starting with %q", prefix)
	return 0
}

// presentRepo is a repository with spec as docs/SPEC.md in which every named test and its
// source exist.
func presentRepo(t *testing.T, spec string, skip map[string]bool) string {
	t.Helper()
	return rulesRepoSpec(t, spec, skip,
		"internal/engine/lifecycle.go", "internal/engine/lifecycle_test.go",
		"internal/core/text.go", "internal/core/text_test.go")
}

// rulesFindings runs Rules on repo and keeps the findings of one rule.
func rulesFindings(t *testing.T, repo, rule string) []repocheck.Finding {
	t.Helper()
	got, err := repocheck.Rules(repo)
	if err != nil {
		t.Fatalf("Rules error = %v, want nil", err)
	}
	var kept []repocheck.Finding
	for _, f := range got {
		if f.Rule == rule {
			kept = append(kept, f)
		}
	}
	return kept
}

func TestRules(t *testing.T) {
	t.Run("a named test missing while its source exists", func(t *testing.T) {
		repo := rulesRepo(t, nil,
			"internal/engine/lifecycle.go",
			"internal/core/text.go", "internal/core/text_test.go")
		got := rulesFindings(t, repo, "missing_test")
		if !hasFinding(got, "missing_test", "internal/engine/lifecycle_test.go") {
			t.Fatalf("Rules missing_test = %+v, want a finding for internal/engine/lifecycle_test.go", got)
		}
		if hasFinding(got, "missing_test", "internal/core/text_test.go") {
			t.Fatalf("Rules missing_test = %+v, want none for internal/core/text_test.go, which exists", got)
		}
	})

	t.Run("a named test missing while its source is neither in the tree nor planned", func(t *testing.T) {
		// Guards against reading every absent source as planned: no §14 Files cell lists
		// engine/lifecycle.go, so SPEC §3 makes its missing test a finding.
		repo := rulesRepo(t, nil, "internal/core/text.go", "internal/core/text_test.go")
		got := rulesFindings(t, repo, "missing_test")
		if !hasFinding(got, "missing_test", "internal/engine/lifecycle_test.go") {
			t.Fatalf("Rules missing_test = %+v, want a finding for internal/engine/lifecycle_test.go", got)
		}
	})

	t.Run("a planned test of a later phase is not reported", func(t *testing.T) {
		// Guards against a phase-blind check: ledger/record.go is not in the tree yet and a §14
		// Files cell of phase 1 lists it, so ledger/record_test.go may be absent.
		repo := rulesRepo(t, nil,
			"internal/engine/lifecycle.go", "internal/engine/lifecycle_test.go",
			"internal/core/text.go", "internal/core/text_test.go")
		if got := rulesFindings(t, repo, "missing_test"); len(got) != 0 {
			t.Fatalf("Rules missing_test = %+v, want no finding", got)
		}
	})

	t.Run("a repository file of §12.1 missing", func(t *testing.T) {
		repo := rulesRepo(t, map[string]bool{"SECURITY.md": true, ".github/workflows/ci.yml": true})
		got := rulesFindings(t, repo, "missing_repo_file")
		for _, want := range []string{"SECURITY.md", ".github/workflows/ci.yml"} {
			if !hasFinding(got, "missing_repo_file", want) {
				t.Errorf("Rules missing_repo_file = %+v, want a finding for %s", got, want)
			}
		}
	})

	t.Run("every repository file of §12.1 present", func(t *testing.T) {
		// Guards against reading every backticked name as a file: CHANGELOG.md is named only to
		// say it does not exist, /dist/ is an ignore pattern, gomod is an ecosystem name, and the
		// template and git config commands are not files.
		repo := rulesRepo(t, nil)
		if got := rulesFindings(t, repo, "missing_repo_file"); len(got) != 0 {
			t.Fatalf("Rules missing_repo_file = %+v, want no finding", got)
		}
	})

	t.Run("an empty Serves cell", func(t *testing.T) {
		// Guards against a rule that serves nothing (DR-33): a blank Serves cell is a finding at
		// its spec line, and a filled one is not.
		spec := strings.Replace(rulesSpec(), "| Records are append-only. | M1 |", "| Records are append-only. |   |", 1)
		got := rulesFindings(t, presentRepo(t, spec, nil), "empty_serves")
		want := repocheck.Finding{Rule: "empty_serves", Path: "docs/SPEC.md", Line: specLine(t, spec, "| DR-2 |")}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("Rules empty_serves = %+v, want only %+v", got, want)
		}
	})

	t.Run("every Serves cell filled", func(t *testing.T) {
		// Guards against reading the header or separator row as a rule row.
		if got := rulesFindings(t, presentRepo(t, rulesSpec(), nil), "empty_serves"); len(got) != 0 {
			t.Fatalf("Rules empty_serves = %+v, want no finding", got)
		}
	})

	t.Run("a §12.2 gate step absent from gate.json", func(t *testing.T) {
		// Guards against a gate that silently drops a step the spec requires (§12.2): the finding
		// sits at the step's spec row, and a step gate.json holds is not reported.
		spec := rulesSpec()
		repo := presentRepo(t, spec, nil)
		writeRulesFile(t, repo, "gate.json", `{"formatVersion": 1, "steps": [
  {"id": "format", "command": ["gofmt", "-l", "."], "required": true, "expect": {"exitCode": 0}}]}`)
		got := rulesFindings(t, repo, "missing_gate_step")
		want := repocheck.Finding{Rule: "missing_gate_step", Path: "docs/SPEC.md", Line: specLine(t, spec, "| `test` |")}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("Rules missing_gate_step = %+v, want only %+v", got, want)
		}
	})

	t.Run("every §12.2 gate step in gate.json", func(t *testing.T) {
		// Guards against reading the Command column, whose backticked commands are not steps.
		if got := rulesFindings(t, presentRepo(t, rulesSpec(), nil), "missing_gate_step"); len(got) != 0 {
			t.Fatalf("Rules missing_gate_step = %+v, want no finding", got)
		}
	})

	t.Run("no gate.json lacks every §12.2 step", func(t *testing.T) {
		// Guards against absence read as a pass (DR-13): without gate.json no step is declared.
		spec := rulesSpec()
		got := rulesFindings(t, presentRepo(t, spec, map[string]bool{"gate.json": true}), "missing_gate_step")
		for _, step := range []string{"| `format` |", "| `test` |"} {
			want := repocheck.Finding{Rule: "missing_gate_step", Path: "docs/SPEC.md", Line: specLine(t, spec, step)}
			if !slices.Contains(got, want) {
				t.Errorf("Rules missing_gate_step = %+v, want %+v", got, want)
			}
		}
	})

	t.Run("an unreadable gate.json is an error, not a pass", func(t *testing.T) {
		// Guards against absence read as evidence (DR-13): steps that cannot be read were not checked.
		repo := presentRepo(t, rulesSpec(), nil)
		writeRulesFile(t, repo, "gate.json", "fixture\n")
		if got, err := repocheck.Rules(repo); err == nil {
			t.Fatalf("Rules(unreadable gate.json) = %+v, nil error; want an error", got)
		}
	})

	t.Run("a spec section that is missing or names nothing is an error, not a pass", func(t *testing.T) {
		// Guards against absence read as evidence (DR-13): a section Rules cannot find, or one that
		// yields no test or file name, means that rule was not checked.
		broken := []struct{ name, spec string }{
			{"no section at all", "# spec\n"},
			{"no §3", rulesSpecWith(t, "## 3. Design rules", "## 12. Repository")},
			{"no §12.1", rulesSpecWith(t, "### 12.1", "### 12.2")},
			{"no §14", rulesSpecWith(t, "## 14. Delivery plan", "")},
			{"§3 names no test", rulesSpecWith(t, "## 3. Design rules", "## 12. Repository",
				"## 3. Design rules", "",
				"| # | Rule | Serves | Enforcement |", "|---|---|---|---|",
				"| DR-1 | The engine runs unattended. | M2 | review |")},
			{"§12.1 names no file", rulesSpecWith(t, "### 12.1", "### 12.2",
				"### 12.1 Repository files (industry standard only)", "", "Industry standard files only.")},
			{"§14 lists no file", rulesSpecWith(t, "## 14. Delivery plan", "",
				"## 14. Delivery plan", "", "No unit is planned yet.")},
			{"no §12.2", rulesSpecWith(t, "### 12.2", "## 14. Delivery plan")},
			{"§12.2 names no step", rulesSpecWith(t, "### 12.2", "## 14. Delivery plan",
				"### 12.2 Gate, tool chain and CI", "", "The gate is gate.json.")},
		}
		for _, tc := range broken {
			t.Run(tc.name, func(t *testing.T) {
				got, err := repocheck.Rules(presentRepo(t, tc.spec, nil))
				if err == nil {
					t.Fatalf("Rules(%s) = %+v, nil error; want an error", tc.name, got)
				}
			})
		}
	})

	t.Run("a repository without docs/SPEC.md is an error", func(t *testing.T) {
		// Guards against absence read as evidence (DR-13): no spec means no rule was checked.
		got, err := repocheck.Rules(t.TempDir())
		if err == nil {
			t.Fatalf("Rules(no spec) = %+v, nil error; want an error", got)
		}
	})
}
