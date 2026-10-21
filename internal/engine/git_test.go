package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// This file tests the batched git reads of the engine (K31, #333): reading many paths of a
// repository must start one git process per batch, never one per path. A process start is costly,
// most of all on Windows, so a loop that runs `git` once per file is the fault this file catches.
// TestGitCostPerUnit measures that loop as the baseline and the one batch as the after row, and
// prints both on one `#333` line. The production reads stay one process per batch.

// gitTestShimEnv returns the name of one shim setting, derived from the product constant (DR-17):
// SHIM marks this test binary as the git shim, REAL names the git it runs, LOG is the file it
// appends one line to per process start.
func gitTestShimEnv(setting string) string {
	return strings.ToUpper(core.Product) + "_ENGINE_TEST_GIT_" + setting
}

// init answers the git shim role: this test binary, copied to the name git and put first on PATH,
// records one line per process start and then runs the real git with the same arguments, the same
// streams and the same exit code. A test that counts the lines sees how many git processes the
// call under test started, whatever command it ran.
func init() {
	if os.Getenv(gitTestShimEnv("SHIM")) != "1" {
		return
	}
	record, err := os.OpenFile(os.Getenv(gitTestShimEnv("LOG")), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(121)
	}
	if _, err := record.WriteString(strings.Join(os.Args[1:], " ") + "\n"); err != nil {
		os.Exit(122)
	}
	if err := record.Close(); err != nil {
		os.Exit(123)
	}
	// This process is the git a repository runs, so no caller hands it a context (SPEC §5.18): the
	// caller stops this process tree, and the shim takes the background context that tests may use,
	// exactly as the other helper processes of the tests do.
	cmd := exec.CommandContext(context.Background(), os.Getenv(gitTestShimEnv("REAL")), os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		os.Exit(124)
	}
	os.Exit(0)
}

// gitTestGitShim installs the shim: it copies this test binary into a fresh directory as git, puts
// that directory first on PATH, and returns the file that records one line per git process start.
// The real git is resolved before PATH changes and handed to the shim by absolute path. A second
// call inside one test finds the first shim on PATH, so it takes the path the setting already
// holds instead of looking git up again, which would hand the shim itself to the shim.
func gitTestGitShim(t *testing.T) string {
	t.Helper()
	real := os.Getenv(gitTestShimEnv("REAL"))
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
	shim := filepath.Join(dir, "git"+gitTestExeSuffix())
	if err := os.WriteFile(shim, body, 0o700); err != nil {
		t.Fatalf("write the git shim %s: %v", shim, err)
	}
	record := filepath.Join(dir, "git.log")
	path := os.Getenv("PATH")
	t.Setenv(gitTestShimEnv("SHIM"), "1")
	t.Setenv(gitTestShimEnv("REAL"), real)
	t.Setenv(gitTestShimEnv("LOG"), record)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+path)
	return record
}

// gitTestGitIn runs git directly in dir under ctx, so every process start of this file is handed the
// caller's context (SPEC §5.18), and fails the test when git fails. The shared engineTestGit helper
// of clone_test.go starts git from t.Context() instead, which the lint step reports at a call site
// that holds a context, so this file uses this form.
func gitTestGitIn(ctx context.Context, t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitTestExeSuffix is the name Windows resolves an executable by; the shim must carry it there.
func gitTestExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// gitTestGitCalls returns one line per git process the shim recorded, in start order.
func gitTestGitCalls(t *testing.T, record string) []string {
	t.Helper()
	b, err := os.ReadFile(record)
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

// gitTestOneGitProcess fails the test unless the shim recorded exactly one git process start whose
// command holds every want, so a batched read never starts one process per path.
func gitTestOneGitProcess(t *testing.T, record, batch string, want ...string) {
	t.Helper()
	calls := gitTestGitCalls(t, record)
	if len(calls) != 1 {
		t.Fatalf("%s started %d git process(es) %v, want one", batch, len(calls), calls)
	}
	for _, w := range want {
		if !strings.Contains(calls[0], w) {
			t.Errorf("%s ran %q, want it to hold %q", batch, calls[0], w)
		}
	}
}

// TestGitReadsAreBatched guards the done criterion of #333 and SPEC §5.21: reading many paths of a
// repository starts one git process per batch, never one per path. Each subtest counts the process
// starts the shim recorded, so a loop that runs `git` once per file fails the count.
func TestGitReadsAreBatched(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	r := engineTestRepoNew(t, roots.Canonical[0])

	const (
		firstPath   = "docs/first.md"
		secondPath  = "docs/second.md"
		spacedPath  = "docs/a file.md"
		unicodePath = "docs/文書.md"
		// unicodeUntracked is the untracked path of the status subtest: no character of it can be
		// decomposed, so the same name is one path on every file system and in git's answer.
		unicodeUntracked = "docs/未追跡.txt"
	)
	engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(firstPath)), "first\n")
	engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(secondPath)), "second\n")
	engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(spacedPath)), "spaced\n")
	engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(unicodePath)), "unicode\n")
	gitTestGitIn(ctx, t, r.dir, "add", ".")
	gitTestGitIn(ctx, t, r.dir, "commit", "-q", "-m", "feat(engine): the files of a batched read")

	t.Run("many blobs come from one cat-file process", func(t *testing.T) {
		// Catches a reader that starts one `cat-file` process per path, also for a path whose name
		// holds a space or a non-ASCII byte.
		paths := []string{firstPath, secondPath, spacedPath, unicodePath}
		record := gitTestGitShim(t)
		blobs, err := ReadBlobs(ctx, r.dir, "HEAD", paths)
		if err != nil {
			t.Fatalf("ReadBlobs(%v): %v", paths, err)
		}
		if len(blobs) != len(paths) {
			t.Fatalf("ReadBlobs returned %d blobs, want %d", len(blobs), len(paths))
		}
		for i, want := range []string{"first\n", "second\n", "spaced\n", "unicode\n"} {
			if blobs[i].Path != paths[i] || string(blobs[i].Content) != want {
				t.Errorf("blob %d = (%q, %q), want (%q, %q)", i, blobs[i].Path, blobs[i].Content, paths[i], want)
			}
		}
		gitTestOneGitProcess(t, record, "reading four blobs", "cat-file", "--batch")
	})

	t.Run("changed and untracked paths come from one status process", func(t *testing.T) {
		// Catches a reader that starts one git process per path, and one that skips
		// --porcelain=v2 -z or the diff.external override status needs (SPEC §5.21).
		engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(firstPath)), "first changed\n")
		gitTestGitIn(ctx, t, r.dir, "add", firstPath)
		engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(secondPath)), "second changed\n")
		engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(spacedPath)), "spaced changed\n")
		engineTestWrite(t, filepath.Join(r.dir, "notes.txt"), "untracked\n")
		engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(unicodeUntracked)), "untracked\n")

		record := gitTestGitShim(t)
		paths, err := UncommittedPaths(ctx, r.dir)
		if err != nil {
			t.Fatalf("UncommittedPaths: %v", err)
		}
		want := []string{firstPath, secondPath, spacedPath, unicodeUntracked, "notes.txt"}
		got := slices.Clone(paths)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("UncommittedPaths = %v, want %v", got, want)
		}
		gitTestWorkTreeProcesses(t, record, len(want))
	})

	t.Run("a rename names both paths from one status process", func(t *testing.T) {
		// Catches a reader that drops the original path of a rename: the file it was renamed from
		// differs from HEAD as well, and one status record names both.
		const renamedPath = "docs/renamed.md"
		gitTestGitIn(ctx, t, r.dir, "mv", secondPath, renamedPath)

		record := gitTestGitShim(t)
		paths, err := UncommittedPaths(ctx, r.dir)
		if err != nil {
			t.Fatalf("UncommittedPaths: %v", err)
		}
		want := []string{firstPath, secondPath, spacedPath, unicodeUntracked, renamedPath, "notes.txt"}
		got := slices.Clone(paths)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("UncommittedPaths = %v, want %v", got, want)
		}
		gitTestWorkTreeProcesses(t, record, len(want))
	})
}

// gitTestWorkTreeProcesses fails unless the shim recorded one `status --porcelain=v2 -z` and no
// other git process. The call must carry -c diff.external= because status has no --no-ext-diff
// (SPEC §5.21). One process for every path is the batch; a count above one is one process per path.
func gitTestWorkTreeProcesses(t *testing.T, record string, paths int) {
	t.Helper()
	calls := gitTestGitCalls(t, record)
	if len(calls) != 1 {
		t.Fatalf("reading %d path(s) started %d git process(es) %v, want one status", paths, len(calls), calls)
	}
	for _, want := range []string{" status ", "--porcelain=v2", " -z ", "diff.external=", "--untracked-files=all"} {
		if !strings.Contains(calls[0], want) {
			t.Errorf("work-tree read ran %q, want it to hold %q", calls[0], want)
		}
	}
}

// TestReadBlobsRefusesMissingPath guards DR-6 and SPEC §5.21: a path that names no blob at the
// revision is a refusal, never an empty blob, and the batch still reads it in one process.
func TestReadBlobsRefusesMissingPath(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	r := engineTestRepoNew(t, roots.Canonical[0])

	record := gitTestGitShim(t)
	blobs, err := ReadBlobs(ctx, r.dir, "HEAD", []string{"docs/nope.md"})
	if err == nil {
		t.Fatalf("ReadBlobs of a path that names no blob = %v, want a refusal", blobs)
	}
	gitTestOneGitProcess(t, record, "the refused read", "cat-file", "--batch")
}

// TestReadBlobsOfNoPath guards SPEC §5.21 and the batching rule of #333: a read of no path starts
// no git process at all, so an empty file set costs nothing, and it answers no blob.
func TestReadBlobsOfNoPath(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	r := engineTestRepoNew(t, roots.Canonical[0])

	record := gitTestGitShim(t)
	blobs, err := ReadBlobs(ctx, r.dir, "HEAD", nil)
	if err != nil {
		t.Fatalf("ReadBlobs of no path: %v", err)
	}
	if len(blobs) != 0 {
		t.Errorf("ReadBlobs of no path = %v, want no blob", blobs)
	}
	if calls := gitTestGitCalls(t, record); len(calls) != 0 {
		t.Errorf("ReadBlobs of no path started %d git process(es) %v, want none", len(calls), calls)
	}
}

// gitTestCostPaths is how many paths one unit reads: enough that one process per path stands out
// against one process per batch on every OS, and small enough to stay cheap.
const gitTestCostPaths = 8

// TestGitCostPerUnit records the baseline and the after cost of #333 with the one command the lead
// runs, `go test ./internal/engine/ ./internal/core/`. The baseline is one `cat-file -p` process per
// path. The after row is ReadBlobs: one `cat-file --batch`. Both rows are printed on one `#333`
// line, so the process counts and the times come from that command (DR-6).
func TestGitCostPerUnit(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	r := engineTestRepoNew(t, roots.Canonical[0])

	paths := make([]string, gitTestCostPaths)
	for i := range paths {
		paths[i] = fmt.Sprintf("docs/cost-%02d.md", i)
		engineTestWrite(t, filepath.Join(r.dir, filepath.FromSlash(paths[i])), fmt.Sprintf("cost %d\n", i))
	}
	gitTestGitIn(ctx, t, r.dir, "add", ".")
	gitTestGitIn(ctx, t, r.dir, "commit", "-q", "-m", "test(engine): the files of the cost measurement")

	baselineRecord := gitTestGitShim(t)
	baselineStart := time.Now()
	for _, path := range paths {
		if _, err := core.Git(ctx, r.dir, "cat-file", "-p", "HEAD:"+path); err != nil {
			t.Fatalf("baseline cat-file -p %s: %v", path, err)
		}
	}
	baselineUs := time.Since(baselineStart).Microseconds()
	baseline := gitTestGitCalls(t, baselineRecord)
	if len(baseline) != len(paths) {
		t.Fatalf("baseline of %d path(s) started %d git process(es) %v, want one cat-file -p per path", len(paths), len(baseline), baseline)
	}
	for _, call := range baseline {
		if !strings.Contains(call, "cat-file") || !strings.Contains(call, " -p ") {
			t.Errorf("baseline ran %q, want one cat-file -p", call)
		}
	}

	afterRecord := gitTestGitShim(t)
	afterStart := time.Now()
	blobs, err := ReadBlobs(ctx, r.dir, "HEAD", paths)
	afterUs := time.Since(afterStart).Microseconds()
	if err != nil {
		t.Fatalf("ReadBlobs of %d path(s): %v", len(paths), err)
	}
	if len(blobs) != len(paths) {
		t.Fatalf("ReadBlobs returned %d blob(s), want %d", len(blobs), len(paths))
	}
	after := gitTestGitCalls(t, afterRecord)
	if len(after) != 1 || !strings.Contains(after[0], "--batch") {
		t.Fatalf("the batched read of %d path(s) started %d git process(es) %v, want 1 cat-file --batch", len(paths), len(after), after)
	}
	if baselineUs <= 0 || afterUs <= 0 {
		t.Fatalf("#333 measured a non-positive time: baseline %d us, after %d us", baselineUs, afterUs)
	}
	fmt.Fprintf(os.Stderr, "#333 paths=%d baseline_processes=%d baseline_us=%d after_processes=%d after_us=%d\n",
		len(paths), len(baseline), baselineUs, len(after), afterUs)
}

// TestBatchBlobsRefusesUnreadTail guards DR-6: a batch answer that continues after the last blob
// is a refusal. Returning the blobs and dropping the tail would hide a short read.
func TestBatchBlobsRefusesUnreadTail(t *testing.T) {
	const oid = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
	out := oid + " blob 5\nhello\nTRAIL"
	blobs, err := batchBlobs([]string{"docs/cost-00.md"}, out)
	if err == nil {
		t.Fatalf("batchBlobs with a trailing byte = %v, want a refusal", blobs)
	}
}

// TestPorcelainPathsRefusesAPartialRecord guards DR-6: a porcelain answer this reader cannot
// read whole is a refusal, never a path list with a record dropped.
func TestPorcelainPathsRefusesAPartialRecord(t *testing.T) {
	const oid = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
	ordinary := "1 .M N... 100644 100644 100644 " + oid + " " + oid + " README.md"
	rename := "2 R. N... 100644 100644 100644 " + oid + " " + oid + " R100 docs/renamed.md\x00"
	cases := []struct {
		name string
		out  string
	}{
		{name: "a record without its NUL", out: ordinary},
		{name: "an unknown record", out: "3 nope\x00"},
		{name: "a rename without its original path", out: rename},
		{name: "an untracked record with an empty path", out: "? \x00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Catches a parser that returns the paths it understood and skips the rest.
			paths, err := porcelainPaths(tc.out)
			if err == nil {
				t.Fatalf("porcelainPaths(%q) = %v, want a refusal", tc.out, paths)
			}
		})
	}
}

// TestUncommittedPathsRunsNoCloneDiffProgram guards SPEC §5.21: the work-tree read must not run a
// diff program the clone configures. The command under test is status, so the marker is taken from
// raw status with UncommittedPaths' own arguments, not from git diff. Porcelain status does not
// start an external diff or a textconv driver; when the raw command did start one, the protected
// call must not. status has no --no-textconv. The call carries -c diff.external= instead
// (gitTestWorkTreeProcesses).
func TestUncommittedPathsRunsNoCloneDiffProgram(t *testing.T) {
	cases := []struct {
		name   string
		config func(t *testing.T, r engineTestRepo)
	}{
		{
			name: "diff.external",
			config: func(t *testing.T, r engineTestRepo) {
				engineTestGit(t, r.dir, "config", "diff.external", "echo external >> '"+r.marker+"'")
			},
		},
		{
			name: "textconv",
			config: func(t *testing.T, r engineTestRepo) {
				engineTestWrite(t, filepath.Join(r.dir, ".gitattributes"), "*.md diff=upper\n")
				engineTestGit(t, r.dir, "config", "diff.upper.textconv", "echo textconv >> '"+r.marker+"' && cat")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Catches a clean marker credited to git diff, and a status call that runs the program.
			ctx := t.Context()
			roots := engineTestRoots(t)
			r := engineTestRepoNew(t, roots.Canonical[0])
			tc.config(t, r)
			engineTestWrite(t, filepath.Join(r.dir, engineTestReadme), engineTestReadmeBase+"changed\n")
			engineTestGitAllowFail(t, r.dir, "status", "--porcelain=v2", "-z", "--untracked-files=all")
			raw := engineTestRan(t, r.marker)
			if raw != "" {
				if err := os.Remove(r.marker); err != nil {
					t.Fatal(err)
				}
			}
			paths, err := UncommittedPaths(ctx, r.dir)
			if err != nil {
				t.Fatalf("UncommittedPaths: %v", err)
			}
			if ran := engineTestRan(t, r.marker); ran != "" {
				t.Fatalf("UncommittedPaths ran a diff program the clone configures: %q (raw status wrote %q)", ran, raw)
			}
			if !slices.Contains(paths, engineTestReadme) {
				t.Errorf("UncommittedPaths = %v, want %s", paths, engineTestReadme)
			}
		})
	}
}
