package engine

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/adapters"
	"github.com/sungurerdim/aiwe/internal/core"
)

// The fixtures of the engine tests; lock_test.go, residue_test.go and coverage_test.go use them too.
const (
	// engineTestRunID is the run id of the run under test: a ULID, 26 Crockford base32 letters
	// (SPEC §1.6).
	engineTestRunID       = "01J8Z5R2W6Q1K7YV3M0F2T9C4B"
	engineTestSecondRunID = "01J8Z5R2W6Q1K7YV3M0F2T9C4C"
	engineTestDeadRunID   = "01J8Z5R2W6Q1K7YV3M0F2T9C4D"

	engineTestReadme     = "README.md"
	engineTestReadmeBase = "# readme\n\nthe engine of the run\n"
	engineTestSource     = "internal/engine/lock.go"
)

// engineTestRepo is a throwaway canonical repository with two commits and the second one as its
// base. marker is the file a configured program appends to when git runs it.
type engineTestRepo struct {
	dir    string
	base   string
	marker string
}

// engineTestRepoNew makes the canonical repository in dir: a first commit and the base commit on
// top of it, so a shallow clone of the base holds a cut history.
func engineTestRepoNew(t *testing.T, dir string) engineTestRepo {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r := engineTestRepo{dir: dir, marker: filepath.ToSlash(filepath.Join(filepath.Dir(dir), "ran.log"))}
	engineTestGit(t, dir, "init", "-q")
	engineTestGit(t, dir, "config", "user.name", "Ada Lovelace")
	engineTestGit(t, dir, "config", "user.email", "ada.lovelace@example.com")
	engineTestGit(t, dir, "config", "commit.gpgsign", "false")
	engineTestGit(t, dir, "config", "core.autocrlf", "false")
	engineTestWrite(t, filepath.Join(dir, engineTestReadme), engineTestReadmeBase)
	engineTestGit(t, dir, "add", engineTestReadme)
	engineTestGit(t, dir, "commit", "-q", "-m", "feat(engine): first commit")
	engineTestWrite(t, filepath.Join(dir, filepath.FromSlash(engineTestSource)), "package engine\n")
	engineTestGit(t, dir, "add", engineTestSource)
	engineTestGit(t, dir, "commit", "-q", "-m", "feat(engine): base commit")
	r.base = engineTestGit(t, dir, "rev-parse", "HEAD")
	return r
}

// engineTestRoots makes the roots of one test under one t.TempDir: the home and the canonical
// repository. The state, temp and cache roots are named but left missing, so OpenOwner makes them
// owner-only as in production: a MkdirAll directory inherits the temp parent's ACL, which may let
// other users in, so OpenOwner would refuse it on Windows (SPEC §5.15). The state root override,
// where core.Git keeps its hooks directory, points here, never at a gate's fresh roots (SPEC §7.8).
func engineTestRoots(t *testing.T) core.OwnedRoots {
	t.Helper()
	base := t.TempDir()
	t.Setenv(strings.ToUpper(core.Product)+"_HOME", filepath.Join(base, "git-state"))
	roots := core.OwnedRoots{
		State:     filepath.Join(base, "state"),
		Temp:      filepath.Join(base, "temp"),
		Cache:     filepath.Join(base, "cache"),
		Home:      filepath.Join(base, "home"),
		Canonical: []string{filepath.Join(base, "canonical")},
	}
	for _, d := range []string{roots.Home, roots.Canonical[0]} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return roots
}

// engineTestOwner opens the owner of the run under test.
func engineTestOwner(t *testing.T, roots core.OwnedRoots) *core.Owner {
	t.Helper()
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	o, err := core.OpenOwner(roots, self)
	if err != nil {
		t.Fatalf("OpenOwner: %v", err)
	}
	return o
}

// engineTestGit runs git directly in dir, outside the engine, and fails the test when git fails.
func engineTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// engineTestGitAllowFail runs git directly in dir and ignores its exit code, for a command a trap
// makes fail or loudly succeed.
func engineTestGitAllowFail(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	_, _ = cmd.CombinedOutput()
}

// engineTestWrite writes content to path, making its directories.
func engineTestWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// engineTestRead returns the content of path.
func engineTestRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// engineTestRan returns what a trap wrote to the marker; empty means no trap ran.
func engineTestRan(t *testing.T, marker string) string {
	t.Helper()
	b, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// engineTestUnder reports whether path lies under root, as given or, when both resolve, through
// their real paths, since a root may be reached by more than one name.
func engineTestUnder(root, path string) bool {
	if engineTestUnderPath(root, path) {
		return true
	}
	rr, rerr := filepath.EvalSymlinks(root)
	pr, perr := filepath.EvalSymlinks(path)
	return rerr == nil && perr == nil && engineTestUnderPath(rr, pr)
}

func engineTestUnderPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// engineTestEnvValue returns the value of name in env, or "".
func engineTestEnvValue(env []string, name string) string {
	for _, kv := range env {
		n, v, ok := strings.Cut(kv, "=")
		if ok && n == name {
			return v
		}
	}
	return ""
}

// engineTestEnvEntry returns the first entry of env named name, whatever its case, and whether
// there is one, so a variable the run must not inherit is found under any spelling.
func engineTestEnvEntry(env []string, name string) (string, bool) {
	for _, kv := range env {
		if n, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(n, name) {
			return kv, true
		}
	}
	return "", false
}

// engineTestDeadProcess starts a child that exits at once and returns its identity after it is
// gone: a pid whose owner is dead.
func engineTestDeadProcess(t *testing.T) core.ProcessID {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	start := time.Now()
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v", err)
	}
	return core.ProcessID{PID: pid, Start: start}
}

// TestRunClone guards DR-10 and SPEC §6.5: a run works in its own fresh shallow clone at its base
// commit, and never touches the owner's tree. It catches a run that shares the owner's checkout or
// a clone with the whole history.
func TestRunClone(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	owner := engineTestOwner(t, roots)
	r := engineTestRepoNew(t, roots.Canonical[0])

	clone, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: r.base, RunID: engineTestRunID})
	if err != nil {
		t.Fatalf("NewClone: %v", err)
	}

	t.Run("the run gets a fresh shallow clone at the base commit", func(t *testing.T) {
		// Catches a run that works in the canonical repository or in a full clone.
		if clone.Dir == r.dir {
			t.Fatalf("the run clone %s is the canonical repository itself", clone.Dir)
		}
		if !engineTestUnder(roots.Temp, clone.Dir) {
			t.Errorf("the run clone %s does not lie under the temp root %s", clone.Dir, roots.Temp)
		}
		if got := engineTestGit(t, clone.Dir, "rev-parse", "--is-shallow-repository"); got != "true" {
			t.Errorf("is-shallow-repository of the run clone = %q, want a shallow clone", got)
		}
		if got := engineTestGit(t, clone.Dir, "rev-parse", "HEAD"); got != r.base {
			t.Errorf("the run clone is at %s, want the base commit %s", got, r.base)
		}
		if clone.Base != r.base {
			t.Errorf("RunClone.Base = %q, want %q", clone.Base, r.base)
		}
	})

	t.Run("each run gets its own clone", func(t *testing.T) {
		// Catches two runs that share one clone.
		second, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: r.base, RunID: engineTestSecondRunID})
		if err != nil {
			t.Fatalf("NewClone of a second run: %v", err)
		}
		if second.Dir == clone.Dir {
			t.Errorf("two runs work in the same clone %s", clone.Dir)
		}
	})

	t.Run("the run writes only in its clone", func(t *testing.T) {
		// Catches a run that writes into the owner's tree (DR-9, never the owner's checkout).
		engineTestGit(t, clone.Dir, "config", "user.name", "Ada Lovelace")
		engineTestGit(t, clone.Dir, "config", "user.email", "ada.lovelace@example.com")
		engineTestGit(t, clone.Dir, "config", "commit.gpgsign", "false")
		engineTestGit(t, clone.Dir, "checkout", "-q", "-b", "unit/35-run-clone")
		engineTestWrite(t, filepath.Join(clone.Dir, engineTestReadme), "# readme\n\nthe run wrote this\n")
		engineTestGit(t, clone.Dir, "add", engineTestReadme)
		engineTestGit(t, clone.Dir, "commit", "-q", "-m", "feat(engine): run change")

		if got := engineTestGit(t, r.dir, "rev-parse", "HEAD"); got != r.base {
			t.Errorf("the owner's HEAD = %s after the run committed, want %s", got, r.base)
		}
		if got := engineTestGit(t, r.dir, "status", "--porcelain"); got != "" {
			t.Errorf("the owner's work tree is dirty after the run committed: %q", got)
		}
		if got := engineTestRead(t, filepath.Join(r.dir, engineTestReadme)); got != engineTestReadmeBase {
			t.Errorf("the owner's %s = %q, want the base content %q", engineTestReadme, got, engineTestReadmeBase)
		}
	})
}

// TestRunCloneEnv guards DR-10 and SPEC §6.5: the run environment is secret-free, its home, profile
// and temp sit in the run scratch, and BindRequest copies that environment onto the run request.
func TestRunCloneEnv(t *testing.T) {
	leaked := []string{"GITHUB_TOKEN", "OPENAI_API_KEY", "CI_TEST_PASSWORD", "AWS_SECRET_ACCESS_KEY"}
	for _, name := range leaked {
		t.Setenv(name, "leaked-value")
	}
	// The parent's home and profile: the run must see its own scratch, not these.
	userHome := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, userHome)
	}
	roots := engineTestRoots(t)
	owner := engineTestOwner(t, roots)
	r := engineTestRepoNew(t, roots.Canonical[0])

	clone, err := NewClone(t.Context(), owner, CloneSpec{Canonical: r.dir, Base: r.base, RunID: engineTestRunID})
	if err != nil {
		t.Fatalf("NewClone: %v", err)
	}

	req := adapters.RunRequest{RunID: engineTestRunID, Role: "engineer", Task: "keep the profile inside the run scratch"}
	clone.BindRequest(&req)
	if strings.Join(req.Env, "\n") != strings.Join(clone.Env, "\n") {
		t.Fatalf("the request environment differs from the run environment the clone built")
	}
	for _, kv := range req.Env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "CREDENTIAL"} {
			if strings.Contains(upper, marker) {
				t.Errorf("the run environment holds the credential-shaped %s=%s", name, value)
			}
		}
		// GIT_CONFIG_KEY_* names the cleared credential helper, so only those may hold KEY (SPEC §6.5).
		if strings.Contains(upper, "KEY") && !strings.HasPrefix(upper, "GIT_CONFIG_") {
			t.Errorf("the run environment holds the credential-shaped %s=%s", name, value)
		}
		if value == "leaked-value" {
			t.Errorf("the run environment passes %s of the parent to the run", name)
		}
	}

	// Home, profile and temp must sit in the run scratch, including USERPROFILE and the app-data directories.
	for _, name := range scratchEnvNames() {
		value := engineTestEnvValue(req.Env, name)
		if value == "" {
			t.Errorf("the run environment does not set %s", name)
			continue
		}
		if !engineTestUnder(clone.Scratch, value) {
			t.Errorf("run %s = %q, want a directory under the run scratch %q", name, value, clone.Scratch)
		}
		if engineTestUnder(userHome, value) {
			t.Errorf("run %s = %q is the parent's own directory", name, value)
		}
	}
	if !engineTestUnder(roots.Temp, clone.Scratch) {
		t.Errorf("the run scratch %s does not lie under the temp root %s", clone.Scratch, roots.Temp)
	}
}

// TestRunCloneScrubsRepoPointers guards DR-10: a parent GIT_DIR, GIT_WORK_TREE or GIT_INDEX_FILE
// never reaches the request environment, so the run's git cannot touch another repository.
func TestRunCloneScrubsRepoPointers(t *testing.T) {
	pointers := repoEnvNames()
	base := t.TempDir()
	for _, name := range pointers {
		t.Setenv(name, filepath.Join(base, strings.ToLower(name)))
	}
	// The fixture is armed: the parent holds every pointer, with a value that carries no secret marker.
	for _, name := range pointers {
		if engineTestEnvValue(core.Environ(), name) == "" {
			t.Fatalf("fixture not armed: the parent environment holds no %s", name)
		}
	}

	// The environment NewClone builds is what BindRequest copies onto the run request.
	clone := &Clone{Env: runEnv(t.TempDir())}
	req := adapters.RunRequest{RunID: engineTestRunID, Role: "engineer", Task: "leave the outside repository unchanged"}
	clone.BindRequest(&req)
	for _, name := range pointers {
		if entry, ok := engineTestEnvEntry(req.Env, name); ok {
			t.Errorf("the request environment passes the parent's %s to the run", entry)
		}
	}
}

// TestRunCloneIsolationFailed guards SPEC §7.4: a run whose isolation fails does not start. It
// catches a run that goes on without its own clone.
func TestRunCloneIsolationFailed(t *testing.T) {
	roots := engineTestRoots(t)
	owner := engineTestOwner(t, roots)
	notARepository := filepath.Join(filepath.Dir(roots.Canonical[0]), "not-a-repository")
	if err := os.MkdirAll(notARepository, 0o700); err != nil {
		t.Fatal(err)
	}
	clone, err := NewClone(t.Context(), owner, CloneSpec{Canonical: notARepository, Base: "0000000000000000000000000000000000000000", RunID: engineTestRunID})
	if err == nil {
		t.Fatalf("NewClone of a directory that is no repository = %+v, want an isolation failure", clone)
	}
}

// engineTestTrap is a hook script that appends name to marker; git runs it through sh.
func engineTestTrap(marker, name string) string {
	return "#!/bin/sh\necho " + name + " >> '" + marker + "'\n"
}

// engineTestTouch moves the modification time of path back, so git cannot answer a content question
// about it from the index stat cache: the clean filter of the path runs, and a trap it carries is
// proven live.
func engineTestTouch(t *testing.T, path string) {
	t.Helper()
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

// engineTestArmHostile writes the programs of SPEC §5.21 into the repository dir: a status command,
// an external diff, a textconv driver, a filter driver whose clean and smudge commands are covered
// by an attribute, and a hooks path whose reference-transaction hook runs on every ref update. It
// then proves, with raw git and the arguments the engine uses, that the traps are live, and clears
// the marker; an empty marker afterwards proves the engine's own calls ran none of them.
func engineTestArmHostile(t *testing.T, dir, marker string) {
	t.Helper()
	hooks := filepath.Join(dir, ".hostile-hooks")
	engineTestWrite(t, filepath.Join(hooks, "reference-transaction"), engineTestTrap(marker, "hook"))
	engineTestGit(t, dir, "config", "core.hooksPath", filepath.ToSlash(hooks))
	engineTestGit(t, dir, "config", "core.fsmonitor", "echo fsmonitor >> '"+marker+"'")
	engineTestGit(t, dir, "config", "diff.external", "echo external >> '"+marker+"'")
	engineTestWrite(t, filepath.Join(dir, ".gitattributes"), "*.md diff=hostile filter=hostile\n")
	engineTestGit(t, dir, "config", "diff.hostile.textconv", "echo textconv >> '"+marker+"'")
	// A filter driver the repository configures: git runs its clean command whenever it converts a
	// work-tree file, so an engine call that asked for a conversion would run it. Both commands pass
	// their input through, so the liveness proof leaves the content as it was.
	engineTestGit(t, dir, "config", "filter.hostile.clean", "echo filter >> '"+marker+"' && cat")
	engineTestGit(t, dir, "config", "filter.hostile.smudge", "echo filter >> '"+marker+"' && cat")

	// A work-tree file of a filtered path, whose stat no longer matches the index, so raw git must run
	// the clean filter to answer about it; hash-object asks for the conversion of such a path too.
	engineTestTouch(t, filepath.Join(dir, engineTestReadme))
	engineTestGitAllowFail(t, dir, "add", engineTestReadme)
	engineTestGitAllowFail(t, dir, "hash-object", "--stdin", "--path=hostile-probe.md")
	engineTestGitAllowFail(t, dir, "status", "--porcelain")
	engineTestGitAllowFail(t, dir, "diff", "HEAD^", "HEAD")
	engineTestGitAllowFail(t, dir, "update-ref", "refs/hostile/armed", "HEAD")
	if got := engineTestRan(t, marker); got == "" {
		t.Fatalf("fixture not armed in %s: raw git ran none of the configured programs", dir)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
}

// TestRunCloneIgnoresHostileConfig guards DR-10 and SPEC §5.21: no git call of the engine runs a
// program the canonical repository or the run clone configures. It catches an engine git call that
// does not run with the repository's programs off.
func TestRunCloneIgnoresHostileConfig(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	owner := engineTestOwner(t, roots)
	r := engineTestRepoNew(t, roots.Canonical[0])

	engineTestArmHostile(t, r.dir, r.marker)

	clone, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: r.base, RunID: engineTestRunID})
	if err != nil {
		t.Fatalf("NewClone of a hostile canonical repository: %v", err)
	}
	if ran := engineTestRan(t, r.marker); ran != "" {
		t.Fatalf("NewClone ran a program the canonical repository configures: %q", ran)
	}

	// The agent commits on its branch and leaves one path uncommitted.
	const branch = "unit/35-run-clone"
	engineTestGit(t, clone.Dir, "config", "user.name", "Ada Lovelace")
	engineTestGit(t, clone.Dir, "config", "user.email", "ada.lovelace@example.com")
	engineTestGit(t, clone.Dir, "config", "commit.gpgsign", "false")
	engineTestGit(t, clone.Dir, "checkout", "-q", "-b", branch)
	engineTestWrite(t, filepath.Join(clone.Dir, filepath.FromSlash(engineTestSource)),
		"package engine\n\n// the claim\n")
	engineTestGit(t, clone.Dir, "add", engineTestSource)
	engineTestGit(t, clone.Dir, "commit", "-q", "-m", "feat(engine): claim a unit")

	engineTestArmHostile(t, clone.Dir, r.marker)

	residue, err := Collect(ctx, owner, clone, branch)
	if err != nil {
		t.Fatalf("Collect in a hostile configuration: %v", err)
	}
	if ran := engineTestRan(t, r.marker); ran != "" {
		t.Fatalf("Collect ran a program the repository configures: %q", ran)
	}

	diff, err := ObserveDiff(ctx, r.dir, r.base, residue.Tip)
	if err != nil {
		t.Fatalf("ObserveDiff in a hostile configuration: %v", err)
	}
	if ran := engineTestRan(t, r.marker); ran != "" {
		t.Fatalf("ObserveDiff ran a program the repository configures: %q", ran)
	}
	if len(diff.Paths) == 0 {
		t.Errorf("ObserveDiff found no changed path; the run's commit is not visible in %s", r.dir)
	}
}

// TestRunCloneRefusesForeignRepoEnv guards DR-10 and the done criterion of [sk-07]: the engine
// refuses a run whose own environment points a git call at another repository. It catches an engine
// whose own git calls work in a repository the parent named.
func TestRunCloneRefusesForeignRepoEnv(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	owner := engineTestOwner(t, roots)
	r := engineTestRepoNew(t, roots.Canonical[0])
	t.Setenv("GIT_DIR", t.TempDir())

	_, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: r.base, RunID: engineTestRunID})
	if !errors.Is(err, ErrForeignRepoEnv) {
		t.Errorf("NewClone with GIT_DIR in the engine's own environment = %v, want ErrForeignRepoEnv", err)
	}
	if _, err := ObserveDiff(ctx, r.dir, r.base, r.base); !errors.Is(err, ErrForeignRepoEnv) {
		t.Errorf("ObserveDiff with GIT_DIR in the engine's own environment = %v, want ErrForeignRepoEnv", err)
	}
}
