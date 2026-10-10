package repocheck_test

import (
	"os"
	"path/filepath"
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
// skip, and the given extra files, each with a one-line body.
func rulesRepo(t *testing.T, skip map[string]bool, extra ...string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("docs/SPEC.md", rulesSpec())
	for _, f := range rulesRepoFiles() {
		if !skip[f] {
			write(f, "fixture\n")
		}
	}
	for _, f := range extra {
		write(f, "package fixture\n")
	}
	return dir
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

	t.Run("a repository without docs/SPEC.md is an error", func(t *testing.T) {
		// Guards against absence read as evidence (DR-13): no spec means no rule was checked.
		got, err := repocheck.Rules(t.TempDir())
		if err == nil {
			t.Fatalf("Rules(no spec) = %+v, nil error; want an error", got)
		}
	})
}
