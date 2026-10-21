// Package engine holds the git calls of one integration in git.go: the tip the run branch names in
// the run clone, the push of that branch to the remote (SPEC §7.3 step 11, DR-14), and the batched
// reads of many paths of a repository, which answer from one git process per batch (K31). Every
// call goes through core.Git or core.GitInput, so no program a repository configures runs (DR-10,
// SPEC §5.21), and every ref is built from a name, so no name becomes an option of a call.
package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// branchRef returns the ref of branch inside a repository.
func branchRef(branch string) string { return "refs/heads/" + branch }

// branchCommit returns the commit the branch of dir names, or "" when the branch names none; a
// failure other than a ref that names no commit is returned. An integration reads the tip it is
// about to merge here, so it never trusts a tip a caller named alone.
func branchCommit(ctx context.Context, dir, branch string) (string, error) {
	res, err := core.Git(ctx, dir, "rev-parse", "--verify", "--quiet", branchRef(branch)+"^{commit}")
	if err != nil {
		if namesNoCommit(res) {
			return "", nil
		}
		return "", fmt.Errorf("read the branch %q of %s: %w", branch, dir, err)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// namesNoCommit reports whether a failed call is git's answer that the ref names no commit:
// `rev-parse --verify --quiet` exits 1 and prints nothing for a ref it does not know. That is a
// measured absence and not a failure, but it never becomes a tip that passes a check on its own
// (DR-6): the caller refuses it.
func namesNoCommit(res core.GitResult) bool {
	return res.ExitCode == 1 && strings.TrimSpace(res.Stdout) == ""
}

// pushBranch pushes the branch of dir to remote under the same branch name (SPEC §7.3 step 11). The
// push carries no tag and no force flag: a remote branch that already moved is refused by git,
// never overwritten, and the refusal names its outcome, push_rejected (§7.4), with its cause. The
// refspec names one ref, so no other ref of the remote changes.
func pushBranch(ctx context.Context, dir, remote, branch string) error {
	target, err := pushTarget(remote)
	if err != nil {
		return err
	}
	refspec := branchRef(branch) + ":" + branchRef(branch)
	if _, err := core.Git(ctx, dir, "push", "--no-tags", target, refspec); err != nil {
		return &core.Error{Outcome: "push_rejected", Err: err,
			Expected: "a remote that accepts the run branch " + branch + " without a force",
			Found:    "the push of " + refspec + " was refused: " + err.Error(),
			Next:     "fetch the branch the remote moved, re-run the unit on its new base, then integrate the tip the auditor audited"}
	}
	return nil
}

// pushTarget returns the destination a push is given: the absolute path of a local remote becomes a
// file URL, so a path with a space or another special byte stays one destination (SPEC §5.21,
// §6.3), and a URL or a configured remote name is used as it is. A destination that names nothing
// or that git could read as an option is refused, so no remote name becomes an argument.
func pushTarget(remote string) (string, error) {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return "", fmt.Errorf("remote %q: want a remote name, a URL or an absolute path", remote)
	}
	if !filepath.IsAbs(remote) {
		return remote, nil
	}
	return fileURL(remote)
}

// Blob is the content of one path of one revision, as a batched read returned it (K31): Path is the
// path the caller named, never a path git printed, so a caller pairs each content with the file it
// asked for.
type Blob struct {
	Path    string
	Content []byte
}

// ReadBlobs returns the content of every path at rev in dir, in the order the paths were named, read
// through one `git cat-file --batch` process (K31, SPEC §5.21). A caller that needs many files of
// one revision pays one process start and not one per path, which costs most on Windows. A path
// that names no blob at rev, a revision git cannot resolve, an answer that stops early, and an
// answer that continues after the last blob are refusals, never an empty or a partial blob (DR-6).
// No path starts no process.
//
// Cost of one unit (8 paths) is measured by one command, which prints both rows (TestGitCostPerUnit):
//
//	go test ./internal/engine/ ./internal/core/
//
// Baseline is one `git cat-file -p` process per path. After is one `git cat-file --batch`. The
// `#333` row below is copied from that command's output (DR-6). Per-path time is that row's
// microsecond count divided by its path count. The test prints the row again on every run and
// asserts the process counts; the times are this run's sample.
//
//	#333 paths=8 baseline_processes=8 baseline_us=1387309 after_processes=1 after_us=798425
//
// Baseline: 1 process per path, 173413 us per path (1387309/8, remainder 5). After: 1 process
// for the 8 paths, 99803 us per path (798425/8, remainder 1).
func ReadBlobs(ctx context.Context, dir, rev string, paths []string) ([]Blob, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	request, err := batchRequest(rev, paths)
	if err != nil {
		return nil, err
	}
	res, err := core.GitInput(ctx, dir, request, "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("read %d blob(s) of %s at %s: %w", len(paths), dir, rev, err)
	}
	return batchBlobs(paths, res.Stdout)
}

// batchRequest returns the input of one `cat-file --batch` call: one `<rev>:<path>` line per path,
// so one process answers for every path. A revision or a path the batch protocol cannot carry is
// refused with its name, so a request never reads as more objects than the caller named.
func batchRequest(rev string, paths []string) (string, error) {
	if err := checkBatchField(rev, "revision"); err != nil {
		return "", err
	}
	var request strings.Builder
	for _, path := range paths {
		if err := checkBatchField(path, "path"); err != nil {
			return "", err
		}
		request.WriteString(rev)
		request.WriteByte(':')
		request.WriteString(path)
		request.WriteByte('\n')
	}
	return request.String(), nil
}

// checkBatchField refuses a revision or a path a `cat-file --batch` input line cannot name: an empty
// one, and one that holds a line break, which would end the line or start a further object.
func checkBatchField(field, what string) error {
	if field == "" || strings.ContainsAny(field, "\r\n") {
		return &core.Error{Outcome: core.OutcomeError,
			Expected: "a non-empty " + what + " without a line break",
			Found:    fmt.Sprintf("the %s %q", what, field),
			Next:     "read the blob through a revision and a path one batch line can carry"}
	}
	return nil
}

// batchBlobs reads the answer of one `cat-file --batch` process: for every path a header
// `<oid> <blob> <size>`, the content bytes and one newline, in the order of the requests. Git
// answers a path it cannot resolve with `<object> missing`, and names the type of every other
// object, so an answer that holds no blob is refused with the path named (DR-6).
func batchBlobs(paths []string, out string) ([]Blob, error) {
	blobs := make([]Blob, 0, len(paths))
	rest := out
	for _, path := range paths {
		header, tail, ok := strings.Cut(rest, "\n")
		if !ok {
			return nil, blobFault(path, "the batch answer ended before its header")
		}
		size, err := blobSize(path, header)
		if err != nil {
			return nil, err
		}
		if size > len(tail) || !strings.HasPrefix(tail[size:], "\n") {
			return nil, blobFault(path, fmt.Sprintf("its content stops after %d of %d byte(s)", len(tail), size))
		}
		blobs = append(blobs, Blob{Path: path, Content: []byte(tail[:size])})
		rest = tail[size+1:]
	}
	if rest != "" {
		return nil, blobFault(batchTailPath(paths),
			fmt.Sprintf("the batch answer continues for %d byte(s) after the last blob", len(rest)))
	}
	return blobs, nil
}

// batchTailPath names the path an unread tail belongs to: the last path requested, or the batch
// itself when the answer held no path.
func batchTailPath(paths []string) string {
	if len(paths) == 0 {
		return "the batch"
	}
	return paths[len(paths)-1]
}

// blobSize returns the size of the blob one header line of a batch answer announces; a header that
// names no blob, names another type of object, or carries no size is refused, named (DR-6).
func blobSize(path, header string) (int, error) {
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return 0, blobFault(path, "the revision holds no blob at that path")
	}
	if len(fields) != 3 || fields[1] != "blob" {
		return 0, blobFault(path, fmt.Sprintf("the batch answer header %q", header))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil || size < 0 {
		return 0, blobFault(path, fmt.Sprintf("the batch answer size %q", fields[2]))
	}
	return size, nil
}

// blobFault is the refusal of one path of a batched read: the path names no blob at the revision, or
// the answer of the batch process is not the header and the content of a blob, so a caller never
// reads an empty or a partial blob as the file it asked for (DR-6).
func blobFault(path, found string) error {
	return &core.Error{Outcome: core.OutcomeError,
		Expected: "the header and the content of the blob of " + path + " in one `cat-file --batch` answer",
		Found:    found,
		Next:     "name a path of the revision that holds a blob, and read it through ReadBlobs"}
}

// UncommittedPaths returns the paths the work tree of dir has left uncommitted: every path that
// differs from HEAD, both paths of a rename, and every untracked path (SPEC §6.5). One
// `git status --porcelain=v2 -z` process names them all, so a work tree of many paths does not
// start one git process per path (K31). Git prints each path unquoted and NUL-terminated, so a
// path with a space or a non-ASCII byte stays one path. core.Git adds `-c diff.external=` on
// status, because status has no --no-ext-diff and a clone's external diff must not run (SPEC §5.21).
func UncommittedPaths(ctx context.Context, dir string) ([]string, error) {
	res, err := core.Git(ctx, dir, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("read the uncommitted paths of %s: %w", dir, err)
	}
	return porcelainPaths(res.Stdout)
}

// porcelainPaths returns every path of one `git status --porcelain=v2 -z` answer, in git's order:
// the path of an ordinary or unmerged change, both paths of a rename or a copy, and every
// untracked path. A record this reader does not know, and a record that ends before its path, is
// refused, so a path is never dropped without a word (DR-6).
func porcelainPaths(out string) ([]string, error) {
	if out == "" {
		return nil, nil
	}
	var paths []string
	rest := out
	for rest != "" {
		got, tail, err := porcelainOne(rest)
		if err != nil {
			return nil, err
		}
		paths = append(paths, got...)
		rest = tail
	}
	return paths, nil
}

// porcelainOne reads one porcelain v2 record from the front of rest. The fixed fields before the
// path are those of the v2 record: eight for an ordinary change (`1`), nine for a rename or a copy
// (`2`, then the score), ten for an unmerged change (`u`). The path is the remainder, so a space
// in it stays part of the path. A rename's original path is the next NUL-terminated field.
func porcelainOne(rest string) ([]string, string, error) {
	rec, tail, ok := strings.Cut(rest, "\x00")
	if !ok {
		return nil, "", workTreeFault("a record ends before its NUL")
	}
	switch {
	case strings.HasPrefix(rec, "1 "):
		return porcelainSingle(rec, tail, 8)
	case strings.HasPrefix(rec, "u "):
		return porcelainSingle(rec, tail, 10)
	case strings.HasPrefix(rec, "2 "):
		return porcelainRename(rec, tail)
	case strings.HasPrefix(rec, "? "):
		if len(rec) < 3 {
			return nil, "", workTreeFault("an untracked record has an empty path")
		}
		return []string{rec[2:]}, tail, nil
	default:
		return nil, "", workTreeFault(fmt.Sprintf("the record %q", rec))
	}
}

// porcelainSingle returns the one path of an ordinary or unmerged record.
func porcelainSingle(rec, tail string, fixed int) ([]string, string, error) {
	path, err := porcelainPath(rec, fixed)
	if err != nil {
		return nil, "", err
	}
	return []string{path}, tail, nil
}

// porcelainRename returns the new path and the original path of a rename or a copy.
func porcelainRename(rec, tail string) ([]string, string, error) {
	path, err := porcelainPath(rec, 9)
	if err != nil {
		return nil, "", err
	}
	orig, rest, ok := strings.Cut(tail, "\x00")
	if !ok || orig == "" {
		return nil, "", workTreeFault("a rename ends before its original path")
	}
	return []string{path, orig}, rest, nil
}

// porcelainPath returns the path that follows the fixed space-separated fields of one record.
func porcelainPath(rec string, fixed int) (string, error) {
	rest := rec
	for i := 0; i < fixed; i++ {
		sp := strings.IndexByte(rest, ' ')
		if sp < 0 || sp == len(rest)-1 {
			return "", workTreeFault(fmt.Sprintf("the record %q ends before its path", rec))
		}
		rest = rest[sp+1:]
	}
	return rest, nil
}

// workTreeFault is the refusal of one record of a work-tree status this reader cannot read whole,
// so it never drops a path without a word (DR-6).
func workTreeFault(found string) error {
	return &core.Error{Outcome: core.OutcomeError,
		Expected: "one `git status --porcelain=v2 -z` record with its path",
		Found:    found,
		Next:     "read the work tree through UncommittedPaths"}
}
