package repocheck_test

import (
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// The fixture rows of the two tables: the design-rule table carries three cells, the standards table
// four (its third is the source, which the registry does not carry), and the registry carries six.
const (
	drSpecRow = "| DR-1 | The engine runs unattended. | test `engine/lifecycle_test.go` |\n"
	drRegRow  = "| DR-1 | The engine runs unattended. | test `engine/lifecycle_test.go` | Run it. | M-Skeleton | ff78469a |\n"

	stdSpecRow = "| STD-1 | The repository is MIT licensed. | `LICENSE` | planned: repocheck rules content |\n"
	stdRegRow  = "| STD-1 | The repository is MIT licensed. | planned: repocheck rules content | Restore it. | M-Skeleton | 10bda667 |\n"
)

// registrySpec is a spec whose §3.1 comes before §4 and whose tables hold dr and std.
func registrySpec(dr, std string) string {
	return "# spec\n\n## 3. Design rules\n\n| id | statement | enforcement |\n|---|---|---|\n" + dr +
		"\n### 3.1 Standards\n\n| id | statement | source | enforcement |\n|---|---|---|---|\n" + std +
		"\n## 4. Principles\n"
}

// registryDoc is the registry of the same rows, with the three columns the spec does not carry.
func registryDoc(dr, std string) string {
	return "# registry\n\n| id | statement | enforcement | fix | milestone | rev |\n|---|---|---|---|---|---|\n" +
		dr + std
}

// registryRepo writes a repository whose docs/SPEC.md and docs/standards.md hold the given rows.
func registryRepo(t *testing.T, drSpec, stdSpec, drReg, stdReg string) string {
	t.Helper()
	dir := t.TempDir()
	writeRulesFile(t, dir, "docs/SPEC.md", registrySpec(drSpec, stdSpec))
	writeRulesFile(t, dir, "docs/standards.md", registryDoc(drReg, stdReg))
	return dir
}

// TestRegistry catches a §3 or §3.1 row that the registry does not carry, or carries differently, and
// a live pair that drifts (K41).
func TestRegistry(t *testing.T) {
	t.Run("the same rows pass", func(t *testing.T) {
		got, err := repocheck.Registry(registryRepo(t, drSpecRow, stdSpecRow, drRegRow, stdRegRow))
		if err != nil || len(got) != 0 {
			t.Fatalf("Registry = %+v, %v; want no finding and no error", got, err)
		}
	})

	t.Run("a changed statement is a finding at the spec row", func(t *testing.T) {
		got, err := repocheck.Registry(registryRepo(t, drSpecRow, stdSpecRow,
			strings.Replace(drRegRow, "The engine runs unattended.", "The engine runs alone.", 1), stdRegRow))
		if err != nil {
			t.Fatalf("Registry error = %v, want nil", err)
		}
		if len(got) != 1 || got[0].Rule != "registry_drift" || got[0].Path != "docs/SPEC.md" || got[0].Line != 7 {
			t.Fatalf("Registry = %+v, want one registry_drift finding at docs/SPEC.md:7", got)
		}
	})

	t.Run("a changed enforcement is a finding at the spec row", func(t *testing.T) {
		got, err := repocheck.Registry(registryRepo(t, drSpecRow, stdSpecRow,
			strings.Replace(drRegRow, "test `engine/lifecycle_test.go`", "test `engine/run_test.go`", 1), stdRegRow))
		if err != nil {
			t.Fatalf("Registry error = %v, want nil", err)
		}
		if len(got) != 1 || got[0].Line != 7 {
			t.Fatalf("Registry = %+v, want one finding at docs/SPEC.md:7", got)
		}
	})

	t.Run("the standards enforcement is its fourth cell", func(t *testing.T) {
		// Guards against reading the source cell as the enforcement: a changed source passes, and a
		// changed enforcement is the finding.
		source := strings.Replace(stdSpecRow, "`LICENSE`", "`LICENSE`, Q1", 1)
		if got, err := repocheck.Registry(registryRepo(t, drSpecRow, source, drRegRow, stdRegRow)); err != nil || len(got) != 0 {
			t.Fatalf("Registry = %+v, %v; want no finding and no error for a changed source cell", got, err)
		}
		changed := strings.Replace(stdRegRow, "planned: repocheck rules content", "check `repo/licence`", 1)
		got, err := repocheck.Registry(registryRepo(t, drSpecRow, stdSpecRow, drRegRow, changed))
		if err != nil {
			t.Fatalf("Registry error = %v, want nil", err)
		}
		if len(got) != 1 || got[0].Line != 13 {
			t.Fatalf("Registry = %+v, want one finding at docs/SPEC.md:13", got)
		}
	})

	t.Run("a row the spec lacks names the spec", func(t *testing.T) {
		extra := "| DR-2 | Work decided by its input is code. | test `engine/code_test.go` | Write it. | M-Trust | 664103ce |\n"
		got, err := repocheck.Registry(registryRepo(t, drSpecRow, stdSpecRow, drRegRow+extra, stdRegRow))
		if err != nil {
			t.Fatalf("Registry error = %v, want nil", err)
		}
		if len(got) != 1 || got[0].Path != "docs/SPEC.md" || got[0].Line != 0 {
			t.Fatalf("Registry = %+v, want one finding at docs/SPEC.md", got)
		}
	})

	t.Run("a row the registry lacks names the registry", func(t *testing.T) {
		extra := "| DR-2 | Work decided by its input is code. | test `engine/code_test.go` |\n"
		got, err := repocheck.Registry(registryRepo(t, drSpecRow+extra, stdSpecRow, drRegRow, stdRegRow))
		if err != nil {
			t.Fatalf("Registry error = %v, want nil", err)
		}
		if len(got) != 1 || got[0].Path != "docs/standards.md" {
			t.Fatalf("Registry = %+v, want one finding at docs/standards.md", got)
		}
	})

	t.Run("a repository without a registry is an error", func(t *testing.T) {
		dir := t.TempDir()
		writeRulesFile(t, dir, "docs/SPEC.md", registrySpec(drSpecRow, stdSpecRow))
		if got, err := repocheck.Registry(dir); err == nil {
			t.Fatalf("Registry(no registry) = %+v, nil error; want an error", got)
		}
	})

	t.Run("a registry without a rule row is an error", func(t *testing.T) {
		// Guards against absence read as evidence (DR-6): no row means no rule was checked.
		if got, err := repocheck.Registry(registryRepo(t, drSpecRow, stdSpecRow, "", "")); err == nil {
			t.Fatalf("Registry(empty registry) = %+v, nil error; want an error", got)
		}
	})

	t.Run("the live pair passes", func(t *testing.T) {
		// The tables this repository ships must equal its registry (K41); a hand-edited copy is red.
		got, err := repocheck.Registry("../..")
		if err != nil {
			t.Fatalf("Registry(../..) error = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Fatalf("Registry(../..) = %+v, want no finding", got)
		}
	})
}
