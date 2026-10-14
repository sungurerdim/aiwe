package engine

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

// engineTestBaseSHA and engineTestTipSHA are commit ids of a realistic shape, for the check rows
// that compare paths without a repository.
const (
	engineTestBaseSHA = "3f9a1c0b7d2e4f5a6b8c9d0e1f2a3b4c5d6e7f80"
	engineTestTipSHA  = "8c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c90"
)

// TestObserveDiff guards DR-9: the observed diff is Git's own list of the paths between the run's
// base commit and its tip. It catches a diff that misses a changed path, reports one twice or names
// the wrong range.
func TestObserveDiff(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	r := engineTestRepoNew(t, roots.Canonical[0])

	engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(engineTestSource)), "package engine\n\n// the claim\n")
	engineTestWrite(t, filepath.Join(r.dir, "docs", "run.md"), "# the run\n")
	engineTestWrite(t, filepath.Join(r.dir, "internal", "engine", "lock_test.go"), "package engine\n")
	engineTestGit(t, r.dir, "add", ".")
	engineTestGit(t, r.dir, "commit", "-q", "-m", "feat(engine): run change")
	tip := engineTestGit(t, r.dir, "rev-parse", "HEAD")

	diff, err := ObserveDiff(ctx, r.dir, r.base, tip)
	if err != nil {
		t.Fatalf("ObserveDiff: %v", err)
	}
	want := []string{"docs/run.md", engineTestSource, "internal/engine/lock_test.go"}
	if !slices.Equal(diff.Paths, want) {
		t.Errorf("observed paths = %v, want %v", diff.Paths, want)
	}
	if diff.Base != r.base || diff.Tip != tip {
		t.Errorf("observed range = %s...%s, want %s...%s", diff.Base, diff.Tip, r.base, tip)
	}
}

// TestCheckScope guards DR-9, Q25 and the done criterion: the observed diff is compared with the
// declared file set, and a file outside it ends the run. It catches a scope check that ignores the
// set or that flags a test, lock or module file.
func TestCheckScope(t *testing.T) {
	declared := []string{engineTestSource, "internal/engine/clone.go"}
	for _, tc := range []struct {
		name    string
		paths   []string
		outside []string
	}{
		{"the declared files only", []string{engineTestSource, "internal/engine/clone.go"}, nil},
		{"a declared file and the test of another declared file",
			[]string{engineTestSource, "internal/engine/clone_test.go"}, nil},
		{"the test of a declared file alone", []string{"internal/engine/lock_test.go"}, nil},
		{"the module files", []string{"go.mod", "go.sum"}, nil},
		{"a source file outside the set", []string{engineTestSource, "internal/engine/coverage.go"},
			[]string{"internal/engine/coverage.go"}},
		{"two files outside the set", []string{"docs/SPEC.md", engineTestSource, "internal/engine/coverage.go"},
			[]string{"docs/SPEC.md", "internal/engine/coverage.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckScope(declared, Diff{Base: engineTestBaseSHA, Tip: engineTestTipSHA, Paths: tc.paths})
			if len(tc.outside) == 0 {
				if err != nil {
					t.Fatalf("CheckScope(%v) = %v, want no violation", tc.paths, err)
				}
				return
			}
			var violation *ScopeViolation
			if !errors.As(err, &violation) {
				t.Fatalf("CheckScope(%v) = %v, want a *ScopeViolation", tc.paths, err)
			}
			if !slices.Equal(violation.Outside, tc.outside) {
				t.Errorf("the violation names %v, want %v", violation.Outside, tc.outside)
			}
		})
	}
}

// TestObserveDiffDivergedBase guards DR-9: every engine diff is `git diff <base>...<tip>`, so a tip
// that does not descend from the base is measured from the two commits' merge base. It catches a
// diff that compares the two trees directly, which would report the base's own change as a path the
// run changed.
func TestObserveDiffDivergedBase(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	r := engineTestRepoNew(t, roots.Canonical[0])

	root := engineTestGit(t, r.dir, "rev-parse", r.base+"^")
	engineTestGit(t, r.dir, "checkout", "-q", "-b", "side", root)
	engineTestWrite(t, filepath.Join(r.dir, "docs", "run.md"), "# the run\n")
	engineTestGit(t, r.dir, "add", "docs/run.md")
	engineTestGit(t, r.dir, "commit", "-q", "-m", "feat(engine): a run on another line")
	tip := engineTestGit(t, r.dir, "rev-parse", "HEAD")

	diff, err := ObserveDiff(ctx, r.dir, r.base, tip)
	if err != nil {
		t.Fatalf("ObserveDiff: %v", err)
	}
	want := []string{"docs/run.md"}
	if !slices.Equal(diff.Paths, want) {
		t.Errorf("observed paths = %v, want %v: the diff runs from the run's base commit, not between the two trees",
			diff.Paths, want)
	}
}

// TestPathLinesQuoted guards DR-9 and SPEC §5.21: Git quotes a path that holds a byte it cannot
// print plainly, and the quoted path is compared just as it came, so it never equals a declared
// plain path and the scope check fails closed. It catches a check that unquotes a reported path.
func TestPathLinesQuoted(t *testing.T) {
	quoted := `"docs/caf\303\251.md"`
	paths := pathLines("plain.md\n" + quoted + "\n")
	if want := []string{"plain.md", quoted}; !slices.Equal(paths, want) {
		t.Errorf("pathLines = %v, want %v", paths, want)
	}
	err := CheckScope([]string{"plain.md", "docs/café.md"},
		Diff{Base: engineTestBaseSHA, Tip: engineTestTipSHA, Paths: paths})
	var violation *ScopeViolation
	if !errors.As(err, &violation) {
		t.Fatalf("the scope check of a quoted path = %v, want a *ScopeViolation", err)
	}
	if want := []string{quoted}; !slices.Equal(violation.Outside, want) {
		t.Errorf("the violation names %v, want %v", violation.Outside, want)
	}
}

// TestRunScopeViolation guards the done criterion and DR-9: the diff of a run that changed a file
// outside its declared set fails with a scope violation naming that file, and the same run passes
// while every observed path is declared.
func TestRunScopeViolation(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	r := engineTestRepoNew(t, roots.Canonical[0])

	engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(engineTestSource)), "package engine\n\n// the claim\n")
	engineTestWrite(t, filepath.Join(r.dir, "docs", "SPEC.md"), "a file outside the declared set\n")
	engineTestGit(t, r.dir, "add", ".")
	engineTestGit(t, r.dir, "commit", "-q", "-m", "feat(engine): a run outside its set")
	tip := engineTestGit(t, r.dir, "rev-parse", "HEAD")

	diff, err := ObserveDiff(ctx, r.dir, r.base, tip)
	if err != nil {
		t.Fatalf("ObserveDiff: %v", err)
	}
	err = CheckScope([]string{engineTestSource}, diff)
	var violation *ScopeViolation
	if !errors.As(err, &violation) {
		t.Fatalf("the scope check of a run that changed docs/SPEC.md = %v, want a *ScopeViolation", err)
	}
	if !slices.Equal(violation.Outside, []string{"docs/SPEC.md"}) {
		t.Errorf("the violation names %v, want [docs/SPEC.md]", violation.Outside)
	}
	if err := CheckScope([]string{engineTestSource, "docs/SPEC.md"}, diff); err != nil {
		t.Errorf("the scope check of the same run with both paths declared = %v, want no violation", err)
	}
}
