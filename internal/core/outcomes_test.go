package core_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// outcomeTable indexes core.Outcomes() by name and fails on a name listed twice.
func outcomeTable(t *testing.T) map[core.Outcome]core.OutcomeRow {
	t.Helper()
	rows := core.Outcomes()
	if len(rows) == 0 {
		t.Fatal("core.Outcomes() is empty: no error maps to a named outcome")
	}
	table := make(map[core.Outcome]core.OutcomeRow, len(rows))
	for _, r := range rows {
		if _, dup := table[r.Outcome]; dup {
			t.Fatalf("outcome %q is listed twice in the outcome table", r.Outcome)
		}
		table[r.Outcome] = r
	}
	return table
}

func TestOutcomes(t *testing.T) {
	table := outcomeTable(t)

	t.Run("status word mapped to the wrong exit code", func(t *testing.T) {
		tests := []struct {
			name   string
			status core.Status
			want   int
		}{
			{name: "ok exits other than 0", status: core.StatusOK, want: 0},
			{name: "error exits other than 1", status: core.StatusError, want: 1},
			{name: "usage exits other than 2", status: core.StatusUsage, want: 2},
			{name: "red exits other than 3", status: core.StatusRed, want: 3},
			{name: "no_change exits other than 4", status: core.StatusNoChange, want: 4},
			{name: "unknown status exits other than 1 (not fail-closed)", status: core.Status("pending"), want: 1},
			{name: "empty status exits other than 1 (not fail-closed)", status: core.Status(""), want: 1},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if got := tc.status.ExitCode(); got != tc.want {
					t.Errorf("Status(%q).ExitCode() = %d, want %d", tc.status, got, tc.want)
				}
			})
		}
	})

	t.Run("outcome row without a class or a next action", func(t *testing.T) {
		known := map[core.Status]bool{core.StatusOK: true, core.StatusError: true, core.StatusUsage: true,
			core.StatusRed: true, core.StatusNoChange: true}
		for _, r := range core.Outcomes() {
			if r.Outcome == "" {
				t.Errorf("outcome table has a row with an empty outcome name: %+v", r)
			}
			if !known[r.Status] {
				t.Errorf("outcome %q has status %q, want one of ok, error, usage, red, no_change", r.Outcome, r.Status)
			}
			if strings.TrimSpace(r.Next) == "" {
				t.Errorf("outcome %q has no next action", r.Outcome)
			}
		}
	})

	t.Run("named outcome missing or carrying the wrong status", func(t *testing.T) {
		tests := []struct {
			name    string
			outcome core.Outcome
			want    core.Status
		}{
			{name: "ok missing", outcome: "ok", want: core.StatusOK},
			{name: "error missing", outcome: "error", want: core.StatusError},
			{name: "usage missing", outcome: "usage", want: core.StatusUsage},
			{name: "red missing", outcome: "red", want: core.StatusRed},
			{name: "param_duplicate missing", outcome: "param_duplicate", want: core.StatusError},
			{name: "param_undeclared missing", outcome: "param_undeclared", want: core.StatusError},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				r, ok := table[tc.outcome]
				if !ok {
					t.Fatalf("outcome %q is not in the outcome table", tc.outcome)
				}
				if r.Status != tc.want {
					t.Errorf("outcome %q has status %q, want %q", tc.outcome, r.Status, tc.want)
				}
			})
		}
	})

	t.Run("error mapped to no named outcome or to the wrong exit code", func(t *testing.T) {
		tests := []struct {
			name     string
			err      error
			outcome  core.Outcome
			status   core.Status
			exitCode int
		}{
			{
				name:    "wrapped sentinel loses its outcome",
				err:     fmt.Errorf("check parameters of engine unit-run: %w", core.ErrParamDuplicate),
				outcome: "param_duplicate", status: core.StatusError, exitCode: 1,
			},
			{
				name:    "sentinel twice wrapped loses its outcome",
				err:     fmt.Errorf("start: %w", fmt.Errorf("parameter %q: %w", "unit", core.ErrParamUndeclared)),
				outcome: "param_undeclared", status: core.StatusError, exitCode: 1,
			},
			{
				name:    "core.Error with a usage outcome exits other than 2",
				err:     &core.Error{Outcome: "usage", Expected: "a command name", Found: "nothing", Next: "run " + core.Product + " --help"},
				outcome: "usage", status: core.StatusUsage, exitCode: 2,
			},
			{
				name: "wrapped core.Error loses its outcome",
				err: fmt.Errorf("parse arguments: %w",
					&core.Error{Outcome: "usage", Expected: "--format text or json", Found: "yaml", Next: "pass --format json"}),
				outcome: "usage", status: core.StatusUsage, exitCode: 2,
			},
			{
				name:    "plain error of an unknown condition is not fail-closed",
				err:     errors.New("open .git/HEAD: access is denied"),
				outcome: "error", status: core.StatusError, exitCode: 1,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				r := core.ErrorResult(tc.err)
				if r.Outcome != tc.outcome {
					t.Errorf("ErrorResult(%v).Outcome = %q, want %q", tc.err, r.Outcome, tc.outcome)
				}
				if _, ok := table[r.Outcome]; !ok {
					t.Errorf("ErrorResult(%v).Outcome = %q is not in the outcome table", tc.err, r.Outcome)
				}
				if r.Status != tc.status {
					t.Errorf("ErrorResult(%v).Status = %q, want %q", tc.err, r.Status, tc.status)
				}
				if got := r.Status.ExitCode(); got != tc.exitCode {
					t.Errorf("ErrorResult(%v) exits %d, want %d", tc.err, got, tc.exitCode)
				}
				if r.Detail == "" {
					t.Errorf("ErrorResult(%v).Detail is empty: the reader gets no message", tc.err)
				}
			})
		}
	})

	t.Run("core.Error with an outcome missing from the table is not fail-closed", func(t *testing.T) {
		r := core.ErrorResult(&core.Error{Outcome: "outcome_nobody_declared", Expected: "x", Found: "y", Next: "z"})
		if r.Status != core.StatusError {
			t.Errorf("ErrorResult of an undeclared outcome has status %q, want %q", r.Status, core.StatusError)
		}
		if _, ok := table[r.Outcome]; !ok {
			t.Errorf("ErrorResult of an undeclared outcome returns outcome %q, which is not in the table", r.Outcome)
		}
	})

	// BC-1 (SPEC §13.1): an outcome string used in code is missing from the outcome table.
	t.Run("BC-1 outcome string used in code missing from the outcome table", func(t *testing.T) {
		used := outcomesUsedInSource(t, filepath.Join("..", ".."))
		for _, sentinel := range []string{"param_duplicate", "root_unresolved"} {
			if !used[sentinel] {
				t.Fatalf("source scan did not find the sentinel %s in internal/core; found %v", sentinel, used)
			}
		}
		for name := range used {
			if _, ok := table[core.Outcome(name)]; !ok {
				t.Errorf("outcome %q is used in code but missing from the outcome table", name)
			}
		}
	})
}

// TestOutcomesUsed catches an outcome table row that no code path produces (SPEC §5.5): the table
// itself does not count as a producer.
func TestOutcomesUsed(t *testing.T) {
	used := outcomesUsedInSource(t, filepath.Join("..", ".."))
	for _, r := range core.Outcomes() {
		if !used[string(r.Outcome)] {
			t.Errorf("outcome table row %q is produced by no code path under internal or cmd", r.Outcome)
		}
	}
}

// outcomesUsedInSource returns every outcome string literal in the non-test Go files under
// root/internal and root/cmd: Outcome("x") conversions, Outcome: "x" fields, constants of type
// Outcome, and package-level sentinels var ErrX = errors.New("x"). The body of a function named
// Outcomes, the table itself, is skipped.
func outcomesUsedInSource(t *testing.T, root string) map[string]bool {
	t.Helper()
	used := map[string]bool{}
	lit := func(e ast.Expr) (string, bool) {
		b, ok := e.(*ast.BasicLit)
		if !ok || b.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(b.Value)
		return s, err == nil
	}
	isOutcomeIdent := func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name == "Outcome"
		case *ast.SelectorExpr:
			return x.Sel.Name == "Outcome"
		}
		return false
	}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || (gd.Tok != token.VAR && gd.Tok != token.CONST) {
					continue
				}
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						if vs.Type != nil && isOutcomeIdent(vs.Type) {
							if s, ok := lit(vs.Values[i]); ok {
								used[s] = true
							}
						}
						call, ok := vs.Values[i].(*ast.CallExpr)
						if !ok || !strings.HasPrefix(name.Name, "Err") || len(call.Args) != 1 {
							continue
						}
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "New" {
							if s, ok := lit(call.Args[0]); ok {
								used[s] = true
							}
						}
					}
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.FuncDecl:
					return x.Name.Name != "Outcomes"
				case *ast.CallExpr:
					if isOutcomeIdent(x.Fun) && len(x.Args) == 1 {
						if s, ok := lit(x.Args[0]); ok {
							used[s] = true
						}
					}
				case *ast.KeyValueExpr:
					if k, ok := x.Key.(*ast.Ident); ok && k.Name == "Outcome" {
						if s, ok := lit(x.Value); ok {
							used[s] = true
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
	return used
}
