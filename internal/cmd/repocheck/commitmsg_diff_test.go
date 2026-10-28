package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// ruleJSON is the rule of one finding in the JSON report.
type ruleJSON struct {
	Rule string `json:"Rule"`
}

// commitMsgReport is the data of one `repocheck commit-msg --format json` envelope.
type commitMsgReport struct {
	Data struct {
		Checks []struct {
			Result   string     `json:"result"`
			Findings []ruleJSON `json:"findings"`
			Warnings []ruleJSON `json:"warnings"`
		} `json:"checks"`
	} `json:"data"`
}

// gitIn runs git in dir and fails the test on a non-zero exit.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)
	if out, err := exec.CommandContext(t.Context(), "git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// writeRepoFile writes body to rel under repo.
func writeRepoFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// diffRepo returns a git repository with one commit that holds a product file, a test with an
// assertion and a detail test; the staged changes of a test come on top of it.
func diffRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	writeRepoFile(t, repo, "run.go", "package run\n\nfunc Run() int {\n\treturn 1\n}\n")
	writeRepoFile(t, repo, "run_test.go", "package run\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {\n\tif Run() != 1 {\n\t\tt.Fatalf(\"Run() != 1\")\n\t}\n}\n")
	writeRepoFile(t, repo, "run_internal_test.go", "package run\n\nimport \"testing\"\n\nfunc TestRunDetail(t *testing.T) {\n\tif Run() != 1 {\n\t\tt.Fatalf(\"detail\")\n\t}\n}\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "chore: the base")
	return repo
}

// runCommitMsgOn feeds msg on stdin to `repocheck commit-msg --repo repo` and returns the exit code and report.
func runCommitMsgOn(t *testing.T, repo, msg string) (int, commitMsgReport) {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "COMMIT_EDITMSG"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(msg); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = stdin; _ = f.Close() })
	var out bytes.Buffer
	code := core.RunCLI(t.Context(), commands(), []string{"commit-msg", "--repo", repo, "--format", "json"}, &out, io.Discard)
	var rep commitMsgReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("commit-msg --format json is not one result envelope: %v; output %q", err, out.String())
	}
	return code, rep
}

// TestCommitMsgReadsTheStagedDiff catches a commit-msg run that ignores the staged diff: a staged
// code and assertion change with no Behavior change line must warn and still exit 0, and a staged
// deletion of a test without its Covered-by and Mutation lines must refuse (SPEC §5.13, L72).
func TestCommitMsgReadsTheStagedDiff(t *testing.T) {
	const (
		plain   = "fix: the run returns two\n\nThe run counted from zero.\n"
		line    = "fix: the run returns two\n\nBehavior change: Run returns 2, not 1.\n"
		dropped = "test: drop the detail test\n\nCovered-by: TestRun\nMutation: run.go:4 TestRun red, reverted\n"
	)
	changeBehavior := func(t *testing.T, repo string) {
		writeRepoFile(t, repo, "run.go", "package run\n\nfunc Run() int {\n\treturn 2\n}\n")
		writeRepoFile(t, repo, "run_test.go", "package run\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {\n\tif Run() != 2 {\n\t\tt.Fatalf(\"Run() != 2\")\n\t}\n}\n")
		gitIn(t, repo, "add", "-A")
	}
	deleteDetail := func(t *testing.T, repo string) { gitIn(t, repo, "rm", "-q", "run_internal_test.go") }
	tests := []struct {
		name     string
		stage    func(*testing.T, string)
		msg      string
		wantCode bool
		warning  string
		finding  string
	}{
		{"code and an assertion with no line warns and exits 0", changeBehavior, plain, true, "behavior_change_missing", ""},
		{"the Behavior change line clears the warning", changeBehavior, line, true, "", ""},
		{"nothing staged has nothing to warn on", func(*testing.T, string) {}, plain, true, "", ""},
		{"a deleted detail test with no lines is refused", deleteDetail, "test: drop the detail test\n", false, "", "detail_test_deletion_unproven"},
		{"a deleted detail test with both lines passes", deleteDetail, dropped, true, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := diffRepo(t)
			tc.stage(t, repo)
			code, rep := runCommitMsgOn(t, repo, tc.msg)
			if len(rep.Data.Checks) != 1 {
				t.Fatalf("commit-msg reported %d checks, want 1", len(rep.Data.Checks))
			}
			c := rep.Data.Checks[0]
			if (code == 0) != tc.wantCode {
				t.Errorf("commit-msg exit code = %d, want zero %v; the check result is %q", code, tc.wantCode, c.Result)
			}
			if got := firstRule(c.Warnings); got != tc.warning {
				t.Errorf("commit-msg warning = %q, want %q: the staged diff is not read", got, tc.warning)
			}
			if got := firstRule(c.Findings); got != tc.finding {
				t.Errorf("commit-msg finding = %q, want %q: the staged diff is not read", got, tc.finding)
			}
			if tc.warning != "" && strings.Contains(c.Result, "red") {
				t.Errorf("commit-msg result = %q on a warning, want green: a missing line never refuses", c.Result)
			}
		})
	}
}

// firstRule returns the first rule of items, or "" when there is none.
func firstRule(items []ruleJSON) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].Rule
}
