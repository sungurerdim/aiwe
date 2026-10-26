package core_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// gitFsckTestRef is the ref a test fetch writes into the destination repository.
const gitFsckTestRef = "refs/fsck-test/run"

// TestGitFsck guards S2-b of the 2026-10-26 security review (SPEC §5.21): every core.Git call
// checks the objects it fetches, so a commit whose tree holds a .git entry, which can carry a nested
// repository (the CVE-2014-9390 class), never lands in the repository the engine fetches into. A
// clean shallow clone and fetch, as a run clone makes them, still pass.
func TestGitFsck(t *testing.T) {
	gitTestHome(t)

	t.Run("the fsck options win over the clone's config", func(t *testing.T) {
		// Catches a call without transfer.fsckObjects or fetch.fsckObjects: the clone's false shows,
		// and fetch.fsckObjects=false alone outranks a fixed transfer.fsckObjects=true.
		r := gitTestNewRepo(t)
		for _, key := range []string{"transfer.fsckObjects", "fetch.fsckObjects"} {
			gitTestRaw(t, r.dir, "config", key, "false")
			res, err := core.Git(t.Context(), r.dir, "config", "--get", key)
			if err != nil {
				t.Fatalf("core.Git config --get %s: %v", key, err)
			}
			if got := strings.TrimSpace(res.Stdout); got != "true" {
				t.Errorf("config %s = %q in a core.Git call, want the fixed %q over the clone's value", key, got, "true")
			}
		}
	})

	t.Run("a fetch of a tree with a .git entry is refused", func(t *testing.T) {
		// Catches a fetch that indexes the objects unchecked: the commit would land in the ref.
		src := gitTestNewRepo(t)
		bad := gitFsckTestDotGitCommit(t, src.dir)
		source := gitFsckTestURL(t, src.dir)
		gitTestRaw(t, src.dir, "update-ref", "refs/heads/nested", bad)
		refspec := "+refs/heads/nested:" + gitFsckTestRef
		gitFsckTestArm(t, source, refspec)
		dst := gitFsckTestEmptyRepo(t)
		res, err := core.Git(t.Context(), dst, "fetch", "--no-tags", "--no-write-fetch-head", source, refspec)
		if err == nil || res.ExitCode == 0 {
			t.Errorf("core.Git fetch of the commit %s with a .git entry = exit %d, err %v; want a non-zero exit", bad[:12], res.ExitCode, err)
		}
		if gitFsckTestHasRef(t, dst) {
			t.Errorf("the commit %s whose tree holds a .git entry landed in %s", bad[:12], gitFsckTestRef)
		}
	})

	t.Run("a clean shallow clone and fetch pass", func(t *testing.T) {
		// Catches a fsck option that refuses what a run clone fetches legitimately: a depth 1 clone
		// of the canonical repository and a depth 1 fetch of its base commit.
		src := gitTestNewRepo(t)
		base := strings.TrimSpace(gitTestRaw(t, src.dir, "rev-parse", "HEAD"))
		into := t.TempDir()
		if _, err := core.Git(t.Context(), into, "clone", "--depth", "1", "--no-tags", "--no-checkout",
			"--config", "core.autocrlf=false", gitFsckTestURL(t, src.dir), "clone"); err != nil {
			t.Fatalf("core.Git shallow clone of a clean repository: %v", err)
		}
		dir := filepath.Join(into, "clone")
		if _, err := core.Git(t.Context(), dir, "fetch", "--depth", "1", "--no-tags", "origin", base); err != nil {
			t.Errorf("core.Git shallow fetch of the clean commit %s: %v", base[:12], err)
		}
	})
}

// gitFsckTestDotGitCommit writes, with raw git, a commit on top of HEAD of dir whose tree holds a
// .git directory with a config file, and returns its id. git add refuses such a path, so the tree
// is built with hash-object and mktree.
func gitFsckTestDotGitCommit(t *testing.T, dir string) string {
	t.Helper()
	blob := gitFsckTestInput(t, dir, "[core]\n\tbare = false\n", "hash-object", "-w", "--stdin")
	sub := gitFsckTestInput(t, dir, "100644 blob "+blob+"\tconfig\n", "mktree")
	tree := gitFsckTestInput(t, dir, "040000 tree "+sub+"\t.git\n100644 blob "+blob+"\tREADME.md\n", "mktree")
	return gitFsckTestInput(t, dir, "", "commit-tree", "-m", "feat: nested repository", "-p", "HEAD", tree)
}

// gitFsckTestArm proves the fixture is live: a raw fetch with fsck off must accept the commit, or a
// refused core.Git fetch would prove nothing.
func gitFsckTestArm(t *testing.T, source, refspec string) {
	t.Helper()
	dir := gitFsckTestEmptyRepo(t)
	gitTestRaw(t, dir, "-c", "transfer.fsckObjects=false", "-c", "fetch.fsckObjects=false",
		"fetch", "--no-tags", "--no-write-fetch-head", source, refspec)
	if !gitFsckTestHasRef(t, dir) {
		t.Fatalf("fixture not armed: a raw fetch without fsck wrote no %s, so the core.Git case would prove nothing", gitFsckTestRef)
	}
}

// gitFsckTestEmptyRepo creates an empty repository whose own config turns fetch checks off, as a
// repository of a user may, and returns its directory.
func gitFsckTestEmptyRepo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	gitTestRaw(t, base, "init", "-q", "dst")
	dir := filepath.Join(base, "dst")
	gitTestRaw(t, dir, "config", "transfer.fsckObjects", "false")
	gitTestRaw(t, dir, "config", "fetch.fsckObjects", "false")
	return dir
}

// gitFsckTestHasRef reports whether dir holds the test ref, read with raw git.
func gitFsckTestHasRef(t *testing.T, dir string) bool {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "--verify", "--quiet", gitFsckTestRef)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// gitFsckTestURL returns the file URL of dir, the form the engine fetches a local repository by.
func gitFsckTestURL(t *testing.T, dir string) string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// gitFsckTestInput runs git directly in dir with stdin as its input and returns its trimmed output.
func gitFsckTestInput(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("raw git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}
