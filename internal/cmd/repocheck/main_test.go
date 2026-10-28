package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestRepocheckDoctor catches a `repocheck doctor` run that exits 0 while a prerequisite is
// missing: an unresolved go tool entry, a Go older than go.mod, or no go.mod at all (§5.7, §12.2).
func TestRepocheckDoctor(t *testing.T) {
	tests := []struct {
		name     string
		goMod    string
		toolsMod string
		wantZero bool
		wantText []string
	}{
		{
			name:     "exits 0 when git, Go and every tool are present",
			goMod:    "module example.com/fixture\n\ngo 1.24\n",
			toolsMod: "module example.com/fixture/tools\n\ngo 1.24\n",
			wantZero: true,
			wantText: []string{"git", "go"},
		},
		{
			name:  "exits non-zero when a go tool entry does not resolve",
			goMod: "module example.com/fixture\n\ngo 1.24\n",
			toolsMod: "module example.com/fixture/tools\n\ngo 1.24\n\n" +
				"tool example.invalid/absent/cmd/absenttool\n",
			wantZero: false,
			wantText: []string{"example.invalid/absent/cmd/absenttool", "go mod download -modfile=tools/go.mod"},
		},
		{
			name:     "exits non-zero when Go is below the go.mod version",
			goMod:    "module example.com/fixture\n\ngo 1.999\n",
			toolsMod: "module example.com/fixture/tools\n\ngo 1.24\n",
			wantZero: false,
			wantText: []string{"go"},
		},
	}
	for _, tc := range tests {
		t.Run("doctor "+tc.name, func(t *testing.T) {
			// No module download and no toolchain switch: the doctor must judge the local machine.
			t.Setenv("GOPROXY", "off")
			t.Setenv("GOTOOLCHAIN", "local")
			t.Setenv("GOFLAGS", "-mod=readonly")
			repo := t.TempDir()
			if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(tc.goMod), 0o600); err != nil {
				t.Fatalf("write go.mod: %v", err)
			}
			if err := os.Mkdir(filepath.Join(repo, "tools"), 0o700); err != nil {
				t.Fatalf("create tools: %v", err)
			}
			if err := os.WriteFile(filepath.Join(repo, "tools", "go.mod"), []byte(tc.toolsMod), 0o600); err != nil {
				t.Fatalf("write tools/go.mod: %v", err)
			}
			var out bytes.Buffer
			code := core.RunCLI(context.Background(), commands(), []string{"doctor", "--repo", repo}, &out, io.Discard)
			if tc.wantZero && code != 0 {
				t.Fatalf("repocheck doctor exit code = %d, want 0; output %q", code, out.String())
			}
			if !tc.wantZero && code == 0 {
				t.Fatalf("repocheck doctor exit code = 0 with a missing prerequisite; output %q", out.String())
			}
			for _, want := range tc.wantText {
				if !strings.Contains(out.String(), want) {
					t.Errorf("repocheck doctor output %q misses %q", out.String(), want)
				}
			}
		})
	}

	t.Run("doctor refuses a repository without go.mod", func(t *testing.T) {
		var out bytes.Buffer
		code := core.RunCLI(context.Background(), commands(), []string{"doctor", "--repo", t.TempDir()}, &out, io.Discard)
		if code == 0 {
			t.Fatalf("repocheck doctor exit code = 0 without go.mod; output %q", out.String())
		}
	})
}

// allChecks are the checks `repocheck all` runs today; ci-shape is reported, never run (§12.2).
func allChecks() []string {
	return []string{"rules", "size", "boundaries", "source-rules", "deps"}
}

// allReport is the data of one `repocheck all --format json` envelope: one entry per check.
type allReport struct {
	Status string `json:"status"`
	Data   struct {
		Checks []struct {
			Check  string `json:"check"`
			Result string `json:"result"`
		} `json:"checks"`
	} `json:"data"`
}

// writeFixture writes a small module whose size check is red: one source file over 400 lines.
func writeFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	var big strings.Builder
	big.WriteString("package main\n\nfunc main() {}\n")
	for range 420 {
		big.WriteString("\n// filler line that pushes the file over the source size limit\n")
	}
	files := map[string]string{
		"go.mod":                      "module example.com/fixture\n\ngo 1.24\n",
		"cmd/fixture/main.go":         big.String(),
		"internal/fixture/fixture.go": "package fixture\n\n// Answer returns a fixed number.\nfunc Answer() int { return 42 }\n",
	}
	for rel, body := range files {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return repo
}

// TestRepocheckAll catches a gate `repo` step (`repocheck all`, §12.2) that skips a check of #14,
// reports the missing ci-shape as green, or exits 0 while a check is red.
func TestRepocheckAll(t *testing.T) {
	t.Run("registers all", func(t *testing.T) {
		cmds := commands()
		i := slices.IndexFunc(cmds, func(c core.Command) bool { return c.Object == "all" && c.Verb == "" })
		if i < 0 {
			t.Fatal("commands() has no \"all\" command; the gate step `repocheck all` would be refused")
		}
		if cmds[i].Run == nil || !slices.Contains(cmds[i].Params, "repo") || !slices.Contains(cmds[i].Params, "format") {
			t.Fatalf("all command = %+v, want a Run function and params repo and format", cmds[i])
		}
	})

	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "-mod=readonly")
	repo := writeFixture(t)
	var out bytes.Buffer
	code := core.RunCLI(context.Background(), commands(), []string{"all", "--repo", repo, "--format", "json"}, &out, io.Discard)
	var rep allReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("repocheck all --format json is not one result envelope: %v; output %q", err, out.String())
	}
	results := map[string]string{}
	for _, c := range rep.Data.Checks {
		if _, dup := results[c.Check]; dup {
			t.Errorf("check %q reported twice; output %q", c.Check, out.String())
		}
		results[c.Check] = c.Result
	}

	t.Run("runs every check of #14 and skips none", func(t *testing.T) {
		for _, name := range allChecks() {
			res, ok := results[name]
			if !ok {
				t.Errorf("repocheck all did not report check %q; output %q", name, out.String())
				continue
			}
			if res == "" || res == "not_measured" || res == "skipped" {
				t.Errorf("check %q result = %q, want it run (green, red or error)", name, res)
			}
		}
	})

	t.Run("reports ci-shape as not_measured, never green", func(t *testing.T) {
		if got, ok := results["ci-shape"]; !ok || got != "not_measured" {
			t.Fatalf("ci-shape result = %q (reported %v), want not_measured; output %q", got, ok, out.String())
		}
	})

	t.Run("exits non-zero when a check is red", func(t *testing.T) {
		if results["size"] != "red" {
			t.Fatalf("size result = %q on a source file over 400 lines, want red; output %q",
				results["size"], out.String())
		}
		if code == 0 {
			t.Fatalf("repocheck all exit code = 0 with check size red; output %q", out.String())
		}
		if rep.Status == "ok" {
			t.Fatalf("envelope status = ok with check size red; output %q", out.String())
		}
	})

	// Each check main.go serves as its own command answers with exactly that check; a refused
	// command answers with a usage error and no check at all. The names come from checks(), the
	// one source commands() builds the per-check commands from.
	for _, c := range checks() {
		t.Run(c.name+" is served as its own command", func(t *testing.T) {
			var buf bytes.Buffer
			core.RunCLI(context.Background(), commands(),
				[]string{c.name, "--repo", repo, "--format", "json"}, &buf, io.Discard)
			var one allReport
			if err := json.Unmarshal(buf.Bytes(), &one); err != nil {
				t.Fatalf("repocheck %s --format json is not one result envelope: %v; output %q", c.name, err, buf.String())
			}
			if len(one.Data.Checks) != 1 || one.Data.Checks[0].Check != c.name {
				t.Fatalf("repocheck %s reported checks %+v, want exactly one check named %q; output %q",
					c.name, one.Data.Checks, c.name, buf.String())
			}
		})
	}

	// A check that read zero files measured nothing; green would be a fail-open (DR-6).
	for _, name := range []string{"size", "source-rules"} {
		t.Run(name+" is not green when it reads zero Go files", func(t *testing.T) {
			empty := t.TempDir()
			if err := os.WriteFile(filepath.Join(empty, "go.mod"),
				[]byte("module example.com/empty\n\ngo 1.24\n"), 0o600); err != nil {
				t.Fatalf("write go.mod: %v", err)
			}
			t.Chdir(empty)
			var buf bytes.Buffer
			code := core.RunCLI(context.Background(), commands(),
				[]string{name, "--repo", ".", "--format", "json"}, &buf, io.Discard)
			if code == 0 {
				t.Fatalf("repocheck %s exit code = 0 on a module without Go files; output %q", name, buf.String())
			}
			var one allReport
			if err := json.Unmarshal(buf.Bytes(), &one); err != nil {
				t.Fatalf("repocheck %s --format json is not one result envelope: %v; output %q", name, err, buf.String())
			}
			for _, c := range one.Data.Checks {
				if c.Result == "green" {
					t.Fatalf("check %s result = green after reading zero Go files; output %q", c.Check, buf.String())
				}
			}
		})
	}
}

// TestRepocheckAllWorkflowLink catches a ci-shape check that follows a .github/workflows link out
// of the repository and measures the outside ci.yml instead of refusing it (#214).
func TestRepocheckAllWorkflowLink(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "-mod=readonly")
	repo := writeFixture(t)
	outside := t.TempDir()
	ci := "name: ci\non: [push]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
	if err := os.WriteFile(filepath.Join(outside, "ci.yml"), []byte(ci), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".github"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, ".github", "workflows")
	if runtime.GOOS == "windows" {
		// A junction needs no privilege, so the case runs on Windows too.
		if out, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J %s %s: %v: %s", link, outside, err, out)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := core.RunCLI(t.Context(), commands(), []string{"all", "--repo", repo, "--format", "json"}, &out, io.Discard)
	var rep allReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("repocheck all --format json is not one result envelope: %v; output %q", err, out.String())
	}
	for _, c := range rep.Data.Checks {
		if c.Check == "ci-shape" && c.Result != "error" {
			t.Fatalf("ci-shape result = %q through a workflows link out of the repository, want error; output %q",
				c.Result, out.String())
		}
	}
	if code == 0 {
		t.Fatalf("repocheck all exit code = 0 with ci-shape reading outside the repository; output %q", out.String())
	}
}

// TestRepocheckCommitMsg catches a commit-msg run that passes a Closes line without its Effect
// line, a Closes line to another repository than origin, a Closes line when origin cannot be read,
// a Refs line, or a unit commit without its Closes line (SPEC §12.3, R5).
func TestRepocheckCommitMsg(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is not on PATH: %v", err)
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:octo-org/widget.git"}} {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	noOrigin := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "-C", noOrigin, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	const closes = "fix: the gate step reports its exit code\n\nEffect: 3 → 0\nCloses octo-org/widget#7\n"
	tests := []struct {
		name string
		msg  string
		args []string
		ok   bool
		rule string
	}{
		{"Closes to origin with an Effect line", closes, []string{"--repo", repo}, true, ""},
		{"Closes without an Effect line", "fix: the gate step\n\nCloses octo-org/widget#7\n",
			[]string{"--repo", repo}, false, "missing_effect"},
		{"Closes to another repository", "fix: the gate step\n\nEffect: 3 → 0\nCloses octo-org/gadget#7\n",
			[]string{"--repo", repo}, false, "closes_other_repo"},
		{"Closes with no origin remote", closes, []string{"--repo", noOrigin}, false, "closes_repo_unknown"},
		{"Closes with no --repo", closes, nil, false, "closes_repo_unknown"},
		{"a non-unit commit with no issue trailer", "fix: the gate step\n\nThe body explains it.\n", nil, true, ""},
		{"a Refs line", "fix: the gate step\n\nRefs octo-org/widget#7\n", nil, false, "refs_on_commit"},
		{"a unit commit with no Closes line",
			"fix: the gate step\n\nRun-Id: 01JBCDEFGHJKMNPQRSTVWXYZ01\n", nil, false, "missing_closes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "COMMIT_EDITMSG"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(tc.msg); err != nil {
				t.Fatal(err)
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			stdin := os.Stdin
			os.Stdin = f
			t.Cleanup(func() { os.Stdin = stdin; _ = f.Close() })
			var out bytes.Buffer
			code := core.RunCLI(t.Context(), commands(), append([]string{"commit-msg"}, tc.args...), &out, io.Discard)
			if tc.ok != (code == 0) || !strings.Contains(out.String(), tc.rule) {
				t.Fatalf("commit-msg %v = exit %d, output %q; want ok %v and rule %q", tc.args, code, out.String(), tc.ok, tc.rule)
			}
		})
	}
}

// traceFixture is writeFixture plus the two files a trace run reads: a gate.json that declares step
// repo, and a registry with one rule row whose test enforcement names registryTest. The fixture
// module declares declaredTest, so the two differ when the rule has no live test.
func traceFixture(t *testing.T, registryTest, declaredTest string) string {
	t.Helper()
	repo := writeFixture(t)
	files := map[string]string{
		"gate.json": `{"formatVersion":1,"steps":[{"id":"repo",` +
			`"command":["go","run","./internal/cmd/repocheck","all","--repo","."],` +
			`"required":true,"expect":{"exitCode":0}}]}`,
		"docs/standards.md": "| id | statement | enforcement | fix | milestone | rev |\n" +
			"|---|---|---|---|---|---|\n" +
			"| DR-30 | A rule id keeps a live enforcement. | test `fixture/fixture_test.go` " +
			registryTest + " | Restore the test. | M-Skeleton | 01234567 |\n",
		"internal/fixture/fixture_test.go": "package fixture\n\nimport \"testing\"\n\n// " +
			declaredTest + " proves the rule the registry names.\nfunc " + declaredTest +
			"(t *testing.T) {\n\tif Answer() != 42 {\n\t\tt.Fatalf(\"Answer() = %d, want 42\", Answer())\n\t}\n}\n",
	}
	for rel, body := range files {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return repo
}

// TestRepocheckTraceAll catches a gate step repo that never runs the trace check, and a trace check
// that passes a rule row whose test enforcement no file declares: a rule can lose its only test
// without the gate turning red (the-gate-fails-a-rule-id-without-a-live-enforcement).
func TestRepocheckTraceAll(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "-mod=readonly")
	if !slices.ContainsFunc(checks(), func(c check) bool { return c.name == "trace" }) {
		t.Fatal("checks() holds no trace check; `repocheck all` would never trace a rule id")
	}
	tests := []struct{ name, registryTest, declaredTest, want string }{
		{"a rule whose test exists is green", "TestAnswer", "TestAnswer", "green"},
		{"a rule whose test is absent is red", "TestGone", "TestAnswer", "red"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := traceFixture(t, tc.registryTest, tc.declaredTest)
			var out bytes.Buffer
			code := core.RunCLI(t.Context(), commands(), []string{"all", "--repo", repo, "--format", "json"}, &out, io.Discard)
			var rep allReport
			if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
				t.Fatalf("repocheck all --format json is not one result envelope: %v; output %q", err, out.String())
			}
			got := ""
			for _, c := range rep.Data.Checks {
				if c.Check == "trace" {
					got = c.Result
				}
			}
			if got != tc.want {
				t.Fatalf("trace result = %q, want %q; output %q", got, tc.want, out.String())
			}
			if tc.want == "red" && !strings.Contains(out.String(), "no_live_enforcement") {
				t.Fatalf("repocheck all output %q misses the finding no_live_enforcement", out.String())
			}
			if tc.want == "red" && code == 0 {
				t.Fatalf("repocheck all exit code = 0 with trace red; output %q", out.String())
			}
		})
	}
}
