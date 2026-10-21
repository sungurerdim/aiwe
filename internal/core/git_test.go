package core_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// gitTestRepo is a throwaway repository with one commit of README.md and a working-tree change on
// top of it. Marker is the file every trap appends to when git runs it.
type gitTestRepo struct {
	dir    string
	marker string
}

// gitTestNewRepo creates the repository with raw git, outside core.Git, so the fixture does not
// depend on the code under test.
func gitTestNewRepo(t *testing.T) gitTestRepo {
	t.Helper()
	base := t.TempDir()
	r := gitTestRepo{
		dir:    filepath.Join(base, "clone"),
		marker: filepath.ToSlash(filepath.Join(base, "ran.log")),
	}
	gitTestRaw(t, base, "init", "-q", "clone")
	gitTestRaw(t, r.dir, "config", "user.name", "Ada Lovelace")
	gitTestRaw(t, r.dir, "config", "user.email", "ada.lovelace@example.com")
	gitTestRaw(t, r.dir, "config", "commit.gpgsign", "false")
	gitTestRaw(t, r.dir, "config", "core.autocrlf", "false")
	gitTestWrite(t, filepath.Join(r.dir, "README.md"), "# "+core.Product+"\n\nSürüm 1.0\n")
	gitTestRaw(t, r.dir, "add", "README.md")
	gitTestRaw(t, r.dir, "commit", "-q", "-m", "docs: first readme")
	gitTestWrite(t, filepath.Join(r.dir, "README.md"), "# "+core.Product+"\n\nGüncel sürüm 2.0\n")
	return r
}

// gitTestRaw runs git directly, without the fixed options, and fails the test when git fails.
func gitTestRaw(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("raw git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// gitTestRawAllowFail runs git directly and ignores its exit code; a trap may make git fail.
func gitTestRawAllowFail(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	_, _ = cmd.CombinedOutput()
}

func gitTestWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// gitTestRan reports what the traps wrote to the marker; empty means no trap ran.
func gitTestRan(t *testing.T, marker string) string {
	t.Helper()
	b, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// gitTestHook is a hook script that appends its name to the marker.
func gitTestHook(marker, name string) string {
	return "#!/bin/sh\necho " + name + " >> '" + marker + "'\n"
}

// gitTestArm proves a trap is live: raw git must run it, or a passing core.Git call proves
// nothing. The marker is then cleared for the core.Git call.
func gitTestArm(t *testing.T, r gitTestRepo, args ...string) {
	t.Helper()
	gitTestRawAllowFail(t, r.dir, args...)
	if gitTestRan(t, r.marker) == "" {
		t.Fatalf("fixture not armed: raw git %v ran no trap, so the core.Git case would prove nothing", args)
	}
	if err := os.Remove(r.marker); err != nil {
		t.Fatal(err)
	}
}

// TestGitOptions guards DR-10 and SPEC §5.21: a run clone's .git/config, attributes or hooks must
// not make a core.Git call run a program, and every call carries the fixed options.
func TestGitOptions(t *testing.T) {
	home := gitTestHome(t)
	t.Run("fixed options win over the clone's config", func(t *testing.T) {
		// Catches a call without one of the four -c options: the clone's own value would show.
		r := gitTestNewRepo(t)
		repoHooks := filepath.ToSlash(filepath.Join(r.dir, ".githooks"))
		gitTestRaw(t, r.dir, "config", "core.fsmonitor", "echo fsmonitor >> '"+r.marker+"'")
		gitTestRaw(t, r.dir, "config", "core.hooksPath", repoHooks)
		gitTestRaw(t, r.dir, "config", "protocol.ext.allow", "always")
		gitTestRaw(t, r.dir, "config", "submodule.recurse", "true")
		for key, want := range map[string]string{
			"core.fsmonitor":     "false",
			"protocol.ext.allow": "never",
			"submodule.recurse":  "false",
		} {
			res, err := core.Git(t.Context(), r.dir, "config", "--get", key)
			if err != nil {
				t.Fatalf("core.Git config --get %s: %v", key, err)
			}
			if got := strings.TrimSpace(res.Stdout); got != want {
				t.Errorf("config %s = %q in a core.Git call, want the fixed %q over the clone's value", key, got, want)
			}
		}
		res, err := core.Git(t.Context(), r.dir, "config", "--get", "core.hooksPath")
		if err != nil {
			t.Fatalf("core.Git config --get core.hooksPath: %v", err)
		}
		hooks := strings.TrimSpace(res.Stdout)
		gitTestCheckHooksDir(t, r.dir, repoHooks, hooks)
		if want := filepath.ToSlash(filepath.Join(home, "hooks")); hooks != want {
			t.Errorf("core.hooksPath in a core.Git call = %q, want %q under the %s state root", hooks, want, platformOverride("HOME"))
		}
	})

	t.Run("diff.external is not run by diff, log or show", func(t *testing.T) {
		// Catches diff, log or show without --no-ext-diff.
		r := gitTestNewRepo(t)
		gitTestRaw(t, r.dir, "config", "diff.external", "echo external >> '"+r.marker+"'")
		gitTestArm(t, r, "diff")
		cases := [][]string{
			{"diff"},
			{"log", "-p", "-1"},
			{"show", "HEAD"},
		}
		gitTestRaw(t, r.dir, "commit", "-q", "-a", "-m", "docs: second readme")
		gitTestWrite(t, filepath.Join(r.dir, "README.md"), "# "+core.Product+"\n\nGüncel sürüm 3.0\n")
		for _, args := range cases {
			res, err := core.Git(t.Context(), r.dir, args...)
			if err != nil {
				t.Fatalf("core.Git %v: %v", args, err)
			}
			if ran := gitTestRan(t, r.marker); ran != "" {
				t.Fatalf("core.Git %v ran the clone's diff.external: %q", args, ran)
			}
			if !strings.Contains(res.Stdout, "diff --git a/README.md b/README.md") {
				t.Errorf("core.Git %v printed no internal diff of README.md:\n%s", args, res.Stdout)
			}
		}
	})

	t.Run("textconv driver is not run by diff", func(t *testing.T) {
		// Catches diff, log or show without --no-textconv.
		r := gitTestNewRepo(t)
		gitTestWrite(t, filepath.Join(r.dir, ".gitattributes"), "*.md diff=upper\n")
		gitTestRaw(t, r.dir, "config", "diff.upper.textconv", "echo textconv >> '"+r.marker+"' && cat")
		gitTestArm(t, r, "diff")
		for _, args := range [][]string{{"diff"}, {"log", "-p", "-1"}, {"show", "HEAD"}} {
			if _, err := core.Git(t.Context(), r.dir, args...); err != nil {
				t.Fatalf("core.Git %v: %v", args, err)
			}
			if ran := gitTestRan(t, r.marker); ran != "" {
				t.Fatalf("core.Git %v ran the clone's textconv driver: %q", args, ran)
			}
		}
	})

	t.Run("core.fsmonitor is not run by status", func(t *testing.T) {
		// Catches a call without -c core.fsmonitor=false.
		r := gitTestNewRepo(t)
		gitTestRaw(t, r.dir, "config", "core.fsmonitor", "echo fsmonitor >> '"+r.marker+"'")
		gitTestArm(t, r, "status", "--porcelain")
		res, err := core.Git(t.Context(), r.dir, "status", "--porcelain")
		if err != nil {
			t.Fatalf("core.Git status: %v", err)
		}
		if ran := gitTestRan(t, r.marker); ran != "" {
			t.Fatalf("core.Git status ran the clone's core.fsmonitor: %q", ran)
		}
		if !strings.Contains(res.Stdout, "README.md") {
			t.Errorf("core.Git status did not report the changed README.md:\n%s", res.Stdout)
		}
	})

	for _, tc := range []struct {
		name     string
		hooksDir func(r gitTestRepo) string
		setPath  bool
	}{
		{
			name:     "hooks in .git/hooks are not run by commit",
			hooksDir: func(r gitTestRepo) string { return filepath.Join(r.dir, ".git", "hooks") },
		},
		{
			name:     "hooks under the clone's core.hooksPath are not run by commit",
			hooksDir: func(r gitTestRepo) string { return filepath.Join(r.dir, ".githooks") },
			setPath:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Catches a call without -c core.hooksPath=<empty engine-owned directory>.
			r := gitTestNewRepo(t)
			dir := tc.hooksDir(r)
			for _, hook := range []string{"pre-commit", "commit-msg", "post-commit"} {
				gitTestWrite(t, filepath.Join(dir, hook), gitTestHook(r.marker, hook))
			}
			if tc.setPath {
				gitTestRaw(t, r.dir, "config", "core.hooksPath", filepath.ToSlash(dir))
			}
			gitTestRaw(t, r.dir, "add", "README.md")
			gitTestArm(t, r, "commit", "-q", "-m", "docs: armed hook check")
			gitTestWrite(t, filepath.Join(r.dir, "README.md"), "# "+core.Product+"\n\nGüncel sürüm 4.0\n")
			gitTestRaw(t, r.dir, "add", "README.md")
			if _, err := core.Git(t.Context(), r.dir, "commit", "-q", "-m", "docs: engine commit"); err != nil {
				t.Fatalf("core.Git commit: %v", err)
			}
			if ran := gitTestRan(t, r.marker); ran != "" {
				t.Fatalf("core.Git commit ran the clone's hooks: %q", ran)
			}
			if got := strings.TrimSpace(gitTestRaw(t, r.dir, "rev-list", "--count", "HEAD")); got != "3" {
				t.Errorf("commits after core.Git commit = %s, want 3: the commit did not land", got)
			}
		})
	}

	t.Run("ext transport is refused although the clone allows it", func(t *testing.T) {
		// Catches a call without -c protocol.ext.allow=never.
		r := gitTestNewRepo(t)
		script := filepath.ToSlash(filepath.Join(filepath.Dir(r.dir), "transport.sh"))
		gitTestWrite(t, script, gitTestHook(r.marker, "ext-transport"))
		gitTestRaw(t, r.dir, "config", "protocol.ext.allow", "always")
		remote := "ext::sh " + script
		gitTestArm(t, r, "ls-remote", remote)
		_, _ = core.Git(t.Context(), r.dir, "ls-remote", remote)
		if ran := gitTestRan(t, r.marker); ran != "" {
			t.Fatalf("core.Git ls-remote ran the ext transport command: %q", ran)
		}
	})

	t.Run("Git refuses a hooks directory that holds an entry", func(t *testing.T) {
		// Catches a call that takes a non-empty engine hooks directory as core.hooksPath, where
		// a planted file would run as a hook (fail-closed, DR-10).
		stray := filepath.Join(gitTestHome(t), "hooks", "stray")
		gitTestWrite(t, stray, gitTestHook("unused", "stray"))
		r := gitTestNewRepo(t)
		gitTestRaw(t, r.dir, "add", "README.md")
		res, err := core.Git(t.Context(), r.dir, "commit", "-q", "-m", "docs: commit with a stray hook")
		if err == nil {
			t.Fatalf("core.Git commit with %s in the hooks directory = nil error, want a refusal (stdout %q)", stray, res.Stdout)
		}
		if got := strings.TrimSpace(gitTestRaw(t, r.dir, "rev-list", "--count", "HEAD")); got != "1" {
			t.Errorf("commits after a refused core.Git commit = %s, want 1: git ran although the hooks directory was not empty", got)
		}
	})
}

// gitTestHome points the AIWE_HOME state root at a fresh temporary directory for the test, so
// no core.Git call creates or reads the user's real state root.
func gitTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(platformOverride("HOME"), home)
	return home
}

// TestGitRefusesLeadingOption guards SPEC §5.21: the first argument of a core.Git call is the
// subcommand. A leading option would skip --no-ext-diff or override a fixed -c option.
func TestGitRefusesLeadingOption(t *testing.T) {
	gitTestHome(t)

	t.Run("Git refuses --no-pager before diff", func(t *testing.T) {
		// Catches a leading option that moves diff out of args[0], so --no-ext-diff is dropped.
		r := gitTestNewRepo(t)
		gitTestRaw(t, r.dir, "config", "diff.external", "echo external >> '"+r.marker+"'")
		gitTestArm(t, r, "diff")
		_, err := core.Git(t.Context(), r.dir, "--no-pager", "diff")
		if ran := gitTestRan(t, r.marker); ran != "" {
			t.Errorf("core.Git --no-pager diff ran the clone's diff.external: %q", ran)
		}
		if err == nil {
			t.Errorf("core.Git --no-pager diff = nil error, want a refusal of the leading option")
		}
	})

	t.Run("Git refuses a caller -c that overrides core.hooksPath", func(t *testing.T) {
		// Catches a caller -c after the fixed options: git takes the last value, so the clone's
		// hooks would run.
		r := gitTestNewRepo(t)
		dir := filepath.Join(r.dir, ".githooks")
		for _, hook := range []string{"pre-commit", "commit-msg", "post-commit"} {
			gitTestWrite(t, filepath.Join(dir, hook), gitTestHook(r.marker, hook))
		}
		gitTestRaw(t, r.dir, "add", "README.md")
		gitTestArm(t, r, "-c", "core.hooksPath=.githooks", "commit", "-q", "-m", "docs: armed hook check")
		gitTestWrite(t, filepath.Join(r.dir, "README.md"), "# "+core.Product+"\n\nGüncel sürüm 5.0\n")
		gitTestRaw(t, r.dir, "add", "README.md")
		_, err := core.Git(t.Context(), r.dir, "-c", "core.hooksPath=.githooks", "commit", "-q", "-m", "docs: engine commit")
		if ran := gitTestRan(t, r.marker); ran != "" {
			t.Errorf("core.Git -c core.hooksPath=.githooks commit ran the clone's hooks: %q", ran)
		}
		if err == nil {
			t.Errorf("core.Git -c core.hooksPath=.githooks commit = nil error, want a refusal of the leading option")
		}
		if got := strings.TrimSpace(gitTestRaw(t, r.dir, "rev-list", "--count", "HEAD")); got != "2" {
			t.Errorf("commits after a refused core.Git commit = %s, want 2: the refused commit landed", got)
		}
	})
}

// gitInputEnv returns one shim setting, derived from the product constant (DR-17). SHIM marks
// this test binary as the git shim, REAL names the git it runs, and LOG is the file it appends
// one line to per process start. None of the suffixes is secret-shaped, so CleanEnv keeps them.
func gitInputEnv(setting string) string {
	return strings.ToUpper(core.Product) + "_CORE_TEST_GIT_" + setting
}

// init answers the git shim: a copy of this test binary, named git, records one line per start
// and then runs the real git. A test that counts the lines sees how many processes GitInput
// started. The other helper roles of this package use their own variables and return first.
func init() {
	if os.Getenv(gitInputEnv("SHIM")) != "1" {
		return
	}
	record, err := os.OpenFile(os.Getenv(gitInputEnv("LOG")), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(121)
	}
	if _, err := record.WriteString(strings.Join(os.Args[1:], " ") + "\n"); err != nil {
		os.Exit(122)
	}
	if err := record.Close(); err != nil {
		os.Exit(123)
	}
	cmd := exec.CommandContext(context.Background(), os.Getenv(gitInputEnv("REAL")), os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	if err == nil {
		os.Exit(0)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	os.Exit(124)
}

// gitInputShim installs the shim and returns the file that records one line per git process start.
func gitInputShim(t *testing.T) string {
	t.Helper()
	real := os.Getenv(gitInputEnv("REAL"))
	if real == "" {
		found, err := exec.LookPath("git")
		if err != nil {
			t.Fatalf("git is the only prerequisite (SPEC §6.2) and is not on PATH: %v", err)
		}
		real, err = filepath.Abs(found)
		if err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("this test binary: %v", err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read this test binary %s: %v", self, err)
	}
	dir := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o700); err != nil {
		t.Fatalf("write the git shim: %v", err)
	}
	logPath := filepath.Join(dir, "git.log")
	t.Setenv(gitInputEnv("SHIM"), "1")
	t.Setenv(gitInputEnv("REAL"), real)
	t.Setenv(gitInputEnv("LOG"), logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// gitInputCalls returns one line per git process the shim recorded.
func gitInputCalls(t *testing.T, logPath string) []string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSuffix(string(b), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// TestGitInput guards SPEC §5.21 and the K31 batched reads: one `cat-file --batch` over many
// objects is one process, fed on stdin, and carries the same fixed options as core.Git.
// The process count catches a batched read built from one process per object.
func TestGitInput(t *testing.T) {
	gitTestHome(t)
	r := gitTestNewRepo(t)

	t.Run("stdin feeds one cat-file process", func(t *testing.T) {
		// Catches a batched read that starts one git process per object: two objects are written
		// to one process, and the shim must record that one start.
		const note = "Batch note for a second object.\n"
		gitTestWrite(t, filepath.Join(r.dir, "NOTES.md"), note)
		gitTestRaw(t, r.dir, "add", "NOTES.md")
		gitTestRaw(t, r.dir, "commit", "-q", "-m", "docs: notes for one batch")
		logPath := gitInputShim(t)
		res, err := core.GitInput(t.Context(), r.dir, "HEAD:README.md\nHEAD:NOTES.md\n", "cat-file", "--batch")
		if err != nil {
			t.Fatalf("core.GitInput cat-file --batch: %v", err)
		}
		if res.ExitCode != 0 {
			t.Errorf("exit code = %d, want 0 (stderr %q)", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "# "+core.Product) || !strings.Contains(res.Stdout, note) {
			t.Errorf("core.GitInput returned %q, want README.md and NOTES.md", res.Stdout)
		}
		calls := gitInputCalls(t, logPath)
		if len(calls) != 1 {
			t.Fatalf("one batch of two objects started %d git process(es) %v, want 1", len(calls), calls)
		}
		if !strings.Contains(calls[0], "cat-file") || !strings.Contains(calls[0], "--batch") {
			t.Errorf("process = %q, want one cat-file --batch", calls[0])
		}
	})

	t.Run("the fixed options apply to a call fed through stdin", func(t *testing.T) {
		// Catches a batched call that bypasses core.Git's fixed options: the clone's own
		// core.fsmonitor value would show.
		gitTestRaw(t, r.dir, "config", "core.fsmonitor", "echo fsmonitor >> '"+r.marker+"'")
		res, err := core.GitInput(t.Context(), r.dir, "", "config", "--get", "core.fsmonitor")
		if err != nil {
			t.Fatalf("core.GitInput config --get: %v", err)
		}
		if got := strings.TrimSpace(res.Stdout); got != "false" {
			t.Errorf("config core.fsmonitor in a batched call = %q, want the fixed %q", got, "false")
		}
		if ran := gitTestRan(t, r.marker); ran != "" {
			t.Errorf("core.GitInput ran the clone's core.fsmonitor: %q", ran)
		}
	})
}

// gitTestCheckHooksDir checks that the hooks path of a core.Git call is an existing, empty
// directory outside the clone, so no hook of the clone or of the user can run.
func gitTestCheckHooksDir(t *testing.T, cloneDir, repoHooks, got string) {
	t.Helper()
	if got == "" || got == repoHooks {
		t.Fatalf("core.hooksPath in a core.Git call = %q, want an engine-owned empty directory", got)
	}
	abs, err := filepath.Abs(filepath.FromSlash(got))
	if err != nil {
		t.Fatal(err)
	}
	cloneAbs, err := filepath.Abs(cloneDir)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(cloneAbs, abs); err == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("core.hooksPath %q lies inside the clone %q, which an agent can write", got, cloneDir)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		t.Fatalf("core.hooksPath %q is not a readable directory: %v", got, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("core.hooksPath %q holds %v on %s, want it empty", got, names, runtime.GOOS)
	}
}
