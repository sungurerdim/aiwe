package gates

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// rootsHelperEnv makes this test binary a helper check when the variable is "1". Its first
// argument is a file it writes its environment to, one entry per line. A second argument names a
// root override variable; the helper first writes one file into the directory it names. It exits
// 0 and opens no window.
const rootsHelperEnv = "AIWE_GATES_TEST_ROOTS"

func init() {
	if os.Getenv(rootsHelperEnv) != "1" || len(os.Args) < 2 {
		return
	}
	if len(os.Args) == 3 {
		if dir := os.Getenv(os.Args[2]); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, "left.txt"), []byte("left\n"), 0o600); err != nil {
				os.Exit(3)
			}
		}
	}
	if err := os.WriteFile(os.Args[1], []byte(strings.Join(os.Environ(), "\n")), 0o600); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

// rootsTestVars are the root override variables of SPEC §6.4, derived from the product constant.
func rootsTestVars() []string {
	p := strings.ToUpper(core.Product)
	return []string{p + "_HOME", p + "_CACHE", p + "_TMP"}
}

// rootsTestStep returns a helper check that records its environment to a file in dir and, when
// leave is a root variable, leaves a file in that root.
func rootsTestStep(dir, id, leave string) (GateStep, string) {
	record := filepath.Join(dir, id+".env")
	argv := []string{os.Args[0], record}
	if leave != "" {
		argv = append(argv, leave)
	}
	return GateStep{ID: id, Command: argv, Required: true}, record
}

// rootsTestEnv reads the environment a helper check recorded, keyed by upper-case name.
func rootsTestEnv(t *testing.T, record string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the helper check left no environment record: %v", err)
	}
	env := map[string]string{}
	for _, kv := range strings.Split(string(data), "\n") {
		if name, value, ok := strings.Cut(kv, "="); ok {
			env[strings.ToUpper(name)] = value
		}
	}
	return env
}

// rootsTestOwner opens an owner over fresh roots, so a check's roots are owned directories.
func rootsTestOwner(t *testing.T) *core.Owner {
	t.Helper()
	base := t.TempDir()
	r := core.OwnedRoots{State: filepath.Join(base, "state"), Temp: filepath.Join(base, "temp"),
		Cache: filepath.Join(base, "cache"), Home: filepath.Join(base, "home"), Canonical: []string{filepath.Join(base, "repo")}}
	for _, d := range []string{r.Home, r.Canonical[0]} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	o, err := core.OpenOwner(r, self)
	if err != nil {
		t.Fatalf("OpenOwner: %v", err)
	}
	return o
}

// rootsTestSetup points the parent's roots and temp directory at test directories, so the parent's
// own roots are known and every fallback directory lands under the test's temp directory.
func rootsTestSetup(t *testing.T) map[string]string {
	t.Helper()
	parent := map[string]string{}
	for _, v := range rootsTestVars() {
		parent[v] = t.TempDir()
		t.Setenv(v, parent[v])
	}
	tmp := t.TempDir()
	for _, v := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(v, tmp)
	}
	t.Setenv(rootsHelperEnv, "1")
	return parent
}

// TestRoots catches a check that leaves an entry in its redirected state, cache or temp root and
// stays green (SPEC §7.8), a check that runs in the parent's roots, two checks that share roots,
// and fresh roots left on disk after a clean check. It runs with an owner, whose roots are owned
// directories, and without one.
func TestRoots(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner bool
	}{{"owner", true}, {"no owner", false}} {
		t.Run(tc.name, func(t *testing.T) {
			parent := rootsTestSetup(t)
			r := &Runner{}
			if tc.owner {
				r.Owner = rootsTestOwner(t)
			}
			dir := t.TempDir()
			clean1, rec1 := rootsTestStep(dir, "clean1", "")
			clean2, rec2 := rootsTestStep(dir, "clean2", "")
			report, err := r.Run(context.Background(), Definition{FormatVersion: 1, Steps: []GateStep{clean1, clean2}}, dir, nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if report.Result != ResultGreen {
				t.Fatalf("catches a clean check turned red by its roots: %+v", report)
			}
			env1, env2 := rootsTestEnv(t, rec1), rootsTestEnv(t, rec2)
			var seen []string
			for _, v := range rootsTestVars() {
				for _, got := range []string{env1[v], env2[v]} {
					if got == "" || got == parent[v] || slices.Contains(seen, got) {
						t.Errorf("catches a check whose %s is not one fresh root of its own: %q (parent %q, seen %v)", v, got, parent[v], seen)
					}
					seen = append(seen, got)
					if _, err := os.Stat(got); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("catches a clean check's fresh root %s left on disk: stat %q: %v", v, got, err)
					}
					if _, err := os.Stat(filepath.Dir(got)); got != "" && !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("catches a clean check's fresh roots directory left on disk: stat %q: %v", filepath.Dir(got), err)
					}
				}
			}

			for _, v := range rootsTestVars() {
				leave, rec := rootsTestStep(dir, "leave-"+strings.ToLower(v), v)
				report, err := r.Run(context.Background(), Definition{FormatVersion: 1, Steps: []GateStep{leave}}, dir, nil)
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
				step := report.Steps[0]
				if step.Result != ResultRed || !strings.Contains(step.Detail, "left.txt") {
					t.Errorf("catches a check that leaves an entry in its %s root and stays green: %+v", v, step)
				}
				if _, err := os.Stat(filepath.Join(parent[v], "left.txt")); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("catches a check that writes into the parent's %s root: %v", v, err)
				}
				if got := rootsTestEnv(t, rec)[v]; tc.owner {
					if _, err := os.Stat(got); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("catches owned fresh roots left on disk after a red check: stat %q: %v", got, err)
					}
				}
			}
		})
	}
}

// TestRootsEnv catches a check process that inherits a secret-shaped variable (DR-35, SPEC §6.5):
// the gate step gets the secret-free environment of core.CleanEnv, and a plain variable still
// reaches it.
func TestRootsEnv(t *testing.T) {
	rootsTestSetup(t)
	secrets := map[string]string{
		"GITHUB_TOKEN":          "ghp_exampleexampleexample",
		"AIWE_DEPLOY_PASSWORD":  "hunter2-example",
		"STRIPE_SECRET_KEY":     "sk_test_example",
		"DATABASE_URL":          "postgres://app:s3cret@db.example.com:5432/app",
		"AIWE_GATES_TEST_PLAIN": "visible",
	}
	for k, v := range secrets {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	step, rec := rootsTestStep(dir, "env", "")
	report, err := (&Runner{}).Run(context.Background(), Definition{FormatVersion: 1, Steps: []GateStep{step}}, dir, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Result != ResultGreen {
		t.Fatalf("the helper check did not run green: %+v", report)
	}
	env := rootsTestEnv(t, rec)
	for k := range secrets {
		if _, ok := env[k]; ok && k != "AIWE_GATES_TEST_PLAIN" {
			t.Errorf("catches a check process that inherits the secret-shaped variable %s", k)
		}
	}
	if env["AIWE_GATES_TEST_PLAIN"] != "visible" {
		t.Errorf("catches a check environment that drops a plain variable: got %q", env["AIWE_GATES_TEST_PLAIN"])
	}
	if env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Errorf("catches a check environment without core.CleanEnv's git settings: GIT_TERMINAL_PROMPT=%q", env["GIT_TERMINAL_PROMPT"])
	}
}
