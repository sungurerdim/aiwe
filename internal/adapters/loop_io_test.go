package adapters

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// shellBigMarker is the first bytes of the file whose dump is over the read tool cap; a tool result
// that holds it returned output the run must never read (SPEC §10.2).
const shellBigMarker = "shell-big-output"

// shellEnvDumpCommand returns the command that prints the whole environment of the child, so a test
// reads exactly the environment the shell tool of a run handed its command (SPEC §6.5, DR-10).
func shellEnvDumpCommand() string {
	if runtime.GOOS == "windows" {
		return "cmd /c set"
	}
	return "env"
}

// shellGitEnv returns the test's own environment without the repository pointers the fixture arms
// for the run, so a test's own git calls never follow the run's GIT_DIR into the outside repository.
func shellGitEnv() []string {
	env := os.Environ()
	for _, name := range shellRepoEnvNames() {
		env = shellDropEnv(env, name)
	}
	return env
}

// shellRepoEnvNames are the parent variables that point a git call at another repository, the ones
// the run environment drops (SPEC §6.5, DR-10).
func shellRepoEnvNames() []string {
	return []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"}
}

// shellDropEnv returns env without every entry named name, whatever its case.
func shellDropEnv(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if n, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(n, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// shellSetEnv returns env with name set to value, so the run sees one value.
func shellSetEnv(env []string, name, value string) []string {
	return append(shellDropEnv(env, name), name+"="+value)
}

// shellRunEnv returns an environment in the shape internal/engine's runEnv builds for a run: the
// parent environment without its repository pointers and with the home, the profile and the temp
// variables inside the run's own scratch directory (SPEC §6.5, DR-10). It is the value a RunRequest
// carries, so the shell tool of a run must see this and only this.
func shellRunEnv(t *testing.T, scratch string) []string {
	t.Helper()
	env := core.CleanEnv(core.Environ(), nil)
	for _, name := range shellRepoEnvNames() {
		env = shellDropEnv(env, name)
	}
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "TMPDIR"} {
		env = shellSetEnv(env, name, scratch)
	}
	return env
}

// shellGitInit makes dir a git repository of its own, the way a run clone is one (SPEC §6.5).
func shellGitInit(t *testing.T, dir string) {
	t.Helper()
	shellGit(t, dir, false, "init", "-q")
}

// shellGit runs git in dir with the repository pointers of the fixture removed; allowFail passes a
// command the test only probes with.
func shellGit(t *testing.T, dir string, allowFail bool, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	cmd.Env = shellGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil && !allowFail {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// shellLocalIdentity returns the local user.name of the repository in dir, or "" when it names none.
func shellLocalIdentity(t *testing.T, dir string) string {
	t.Helper()
	return shellGit(t, dir, true, "config", "--local", "--get", "user.name")
}

// shellEnvOf reads an environment dump into its name/value pairs, so a test asserts on one variable.
func shellEnvOf(dump string) map[string]string {
	pairs := make(map[string]string)
	for _, line := range strings.Split(dump, "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			pairs[name] = value
		}
	}
	return pairs
}

// TestLoopShellGetsOnlyTheRunEnv guards SPEC §6.5 and DR-10: the shell tool of a run runs its child
// with the run environment the request carries and nothing of the parent's, so a command of the
// clone cannot change a repository outside the run through the parent's GIT_DIR, cannot reach the
// parent's profile from HOME or USERPROFILE, and sees APPDATA under the run scratch. It catches a
// shell tool that rebuilds the environment from the parent process, which keeps GIT_DIR and the
// user's profile — the gh login a parent-profile command would then print.
func TestLoopShellGetsOnlyTheRunEnv(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)

	base := t.TempDir()
	root := filepath.Join(base, "clone")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("make the run clone: %v", err)
	}
	shellGitInit(t, root)

	outside := filepath.Join(base, "outside-repo")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatalf("make the outside repository: %v", err)
	}
	shellGitInit(t, outside)

	parentProfile := filepath.Join(base, "parent-profile")
	if err := os.Mkdir(parentProfile, 0o700); err != nil {
		t.Fatalf("make the parent profile: %v", err)
	}
	// The parent arms exactly what a run must never inherit: a git directory that points at another
	// repository and the user's own profile (SPEC §6.5, DR-10).
	t.Setenv("GIT_DIR", filepath.Join(outside, ".git"))
	t.Setenv("GIT_WORK_TREE", outside)
	t.Setenv("HOME", parentProfile)
	t.Setenv("USERPROFILE", parentProfile)
	t.Setenv("APPDATA", parentProfile)

	scratch := filepath.Join(base, "run-scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatalf("make the run scratch: %v", err)
	}

	f := newFakeHost(t)
	f.serve(openCodeChatPath, scriptedAnswers(
		replyTurn(DefaultModel, assistantToolCall("call_1", ToolShell,
			fmt.Sprintf(`{"command":%q}`, shellEnvDumpCommand())), usageFull),
		replyTurn(DefaultModel, assistantToolCall("call_2", ToolShell, `{"command":"git config user.name x"}`), usageFull),
		replyTurn(DefaultModel, assistantText("done"), usageFull),
	))
	endpoint := newGoFixture(f, ProcessEnv(), log).Endpoints().Chat
	loop := loopTestLoop(endpoint, f.srv.Client(), log, 5, time.Minute)
	req := loopTestRequest(t, root, ToolPermissions{Shell: true})
	req.Env = shellRunEnv(t, scratch)
	if _, err := loop.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := shellLocalIdentity(t, root); got != "x" {
		t.Errorf("the shell command did not run in the run clone: its local user.name is %q, want %q (SPEC §10.2)", got, "x")
	}
	if got := shellLocalIdentity(t, outside); got != "" {
		t.Errorf("a shell command of the run changed the repository outside it: its local user.name is %q; the parent's GIT_DIR reached the child (SPEC §6.5, DR-10)", got)
	}

	bodies := loopTurns(t, f)
	if len(bodies) != 3 {
		t.Fatalf("the loop made %d turns, want the two shell turns and the final answer (SPEC §10.2)", len(bodies))
	}
	dump, ok := loopToolResult(bodies[1])
	if !ok || dump == "" {
		t.Fatalf("the loop fed back no environment, want the output of %q (SPEC §10.2)", shellEnvDumpCommand())
	}
	child := shellEnvOf(dump)
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA"} {
		if got := child[name]; got != scratch {
			t.Errorf("the shell child sees %s=%q, want the run scratch %q: the parent's profile reached the child (SPEC §6.5, DR-10)",
				name, got, scratch)
		}
	}
	for _, name := range shellRepoEnvNames() {
		if got, ok := child[name]; ok {
			t.Errorf("the shell child sees %s=%q, want the run environment without it: the parent's repository pointer reached the child (SPEC §6.5, DR-10)",
				name, got)
		}
	}
}

// TestLoopRefusesShellOutputOverTheCap guards SPEC §10.2: the shell tool refuses a command whose
// output is over the read tool cap instead of returning it, and the refusal names the cap, so a
// runaway command cannot fill every later turn of the run. It catches a shell tool that returns the
// whole output or a cut prefix of it.
func TestLoopRefusesShellOutputOverTheCap(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(openCodeKeyEnv, envSecret)

	base := t.TempDir()
	root := filepath.Join(base, "clone")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("make the run clone: %v", err)
	}
	shellGitInit(t, root)
	// A blob one byte over the cap, so one git command prints more than the cap.
	over := shellBigMarker + strings.Repeat("a", maxToolReadBytes+1-len(shellBigMarker))
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(over), 0o600); err != nil {
		t.Fatalf("seed the file over the cap: %v", err)
	}
	sha := shellGit(t, root, false, "hash-object", "-w", "big.txt")
	command := "git cat-file blob " + sha

	f := newFakeHost(t)
	f.serve(openCodeChatPath, scriptedAnswers(
		replyTurn(DefaultModel, assistantToolCall("call_1", ToolShell,
			fmt.Sprintf(`{"command":%q}`, command)), usageFull),
		replyTurn(DefaultModel, assistantText("done"), usageFull),
	))
	endpoint := newGoFixture(f, ProcessEnv(), log).Endpoints().Chat
	loop := loopTestLoop(endpoint, f.srv.Client(), log, 5, time.Minute)
	req := loopTestRequest(t, root, ToolPermissions{Shell: true})
	req.Env = shellRunEnv(t, t.TempDir())
	if _, err := loop.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	bodies := loopTurns(t, f)
	if len(bodies) != 2 {
		t.Fatalf("the loop made %d turns, want the shell turn and the final answer (SPEC §10.2)", len(bodies))
	}
	got, ok := loopToolResult(bodies[1])
	if !ok {
		t.Fatalf("the second turn holds no tool result, want the refusal of the over-cap command (SPEC §10.2)")
	}
	if !strings.Contains(got, strconv.Itoa(maxToolReadBytes)) {
		t.Errorf("the refusal of a command over the cap does not name the cap of %d bytes: the tool result is %q (SPEC §10.2)",
			maxToolReadBytes, got)
	}
	if strings.Contains(got, shellBigMarker) || len(got) >= maxToolReadBytes {
		t.Errorf("the shell tool returned %d bytes of output over the cap of %d, want a refusal and never a cut: %q (SPEC §10.2)",
			len(got), maxToolReadBytes, got)
	}
}
