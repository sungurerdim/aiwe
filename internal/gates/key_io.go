package gates

import (
	"bytes"
	"context"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// keyProbeLimit bounds the git and toolchain probes that fill one check key (DR-1).
const keyProbeLimit = 15 * time.Second

// ResolveKeyInput measures the key of step in dir, the work tree root it runs in (SPEC §7.8):
// Argv is the step's own command, OS and Arch are this engine's, InputsDigest digests the HEAD
// blobs of the declared inputs (every tracked blob when none), LockDigests each tracked go.sum and
// ToolVersion is the toolchain probe's stdout in dir. clean is false when the work tree differs
// from HEAD under the declared inputs or in a lock file, untracked files included: the key then
// names HEAD, not what the step runs, so the caller neither serves nor stores a result under it. A
// directory that is not the work tree root, a declared input with no blob, or a probe that fails
// or prints nothing is an error, so an unmeasured input is never hashed as a real key (DR-6).
func ResolveKeyInput(ctx context.Context, dir string, step GateStep) (KeyInput, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, keyProbeLimit)
	defer cancel()
	if err := requireWorkTreeRoot(ctx, dir); err != nil {
		return KeyInput{}, false, err
	}
	digest, locks, err := gitKeyParts(ctx, dir, step.Inputs)
	if err != nil {
		return KeyInput{}, false, err
	}
	clean, err := workTreeMatchesHead(ctx, dir, step.Inputs)
	if err != nil {
		return KeyInput{}, false, err
	}
	tool, err := toolchainOutput(ctx, dir)
	if err != nil {
		return KeyInput{}, false, err
	}
	return KeyInput{Argv: slices.Clone(step.Command), InputsDigest: digest, Fingerprint: Fingerprint{
		OS: runtime.GOOS, Arch: runtime.GOARCH, ToolVersion: tool, LockDigests: locks}}, clean, nil
}

// requireWorkTreeRoot refuses a directory that is not the top of its own git work tree: an empty
// directory, one git does not recognise, and a subdirectory have no key of their own (DR-6). The
// prefix git prints is empty only at the top. Any other git failure is returned.
func requireWorkTreeRoot(ctx context.Context, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return workTreeError("an empty directory")
	}
	inside, err := core.Git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if inside.ExitCode == 128 {
			return workTreeError("a directory that is not a git work tree")
		}
		return err
	}
	if strings.TrimSpace(inside.Stdout) != "true" {
		return workTreeError("a directory that is not a git work tree")
	}
	prefix, err := core.Git(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return err
	}
	if strings.TrimSpace(prefix.Stdout) != "" {
		return workTreeError("a subdirectory of a git work tree")
	}
	return nil
}

// gitKeyParts reads the blobs of HEAD with NUL-terminated paths. The inputs digest covers the
// declared inputs, or every blob when none are declared. Each go.sum is a lock digest (SPEC §7.8,
// §8.2).
func gitKeyParts(ctx context.Context, dir string, inputs []string) (string, []string, error) {
	res, err := core.Git(ctx, dir, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	if err != nil {
		return "", nil, gitObjectsError(err)
	}
	objects, err := parseTree(res.Stdout)
	if err != nil {
		return "", nil, err
	}
	digest, err := digestInputs(objects, inputs)
	if err != nil {
		return "", nil, err
	}
	return digest, digestLocks(objects), nil
}

// workTreeMatchesHead reports whether no tracked change and no untracked file of the work tree
// falls under the declared inputs or is a lock file, whose HEAD digest is in the key whatever the
// inputs are. Ignored files are not inputs.
func workTreeMatchesHead(ctx context.Context, dir string, inputs []string) (bool, error) {
	res, err := core.Git(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return false, gitObjectsError(err)
	}
	changed, err := parseStatus(res.Stdout)
	if err != nil {
		return false, err
	}
	dirty := func(p string) bool { return matchesInputs(p, inputs) || isLockFile(p) }
	return !slices.ContainsFunc(changed, dirty), nil
}

// toolchainOutput runs the toolchain probe in dir, in the clean environment a step inherits, and
// returns its stdout byte-exact (DR-18). A start, wait or stop failure, a non-zero exit included,
// and output with no text are errors, so output that was not measured is never a key (DR-6).
func toolchainOutput(ctx context.Context, dir string) (string, error) {
	var stdout, stderr bytes.Buffer
	child, err := core.Start(ctx, core.ChildSpec{Argv: toolchainProbe(), Dir: dir,
		Env: core.CleanEnv(core.Environ(), nil), Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		return "", toolchainError(err.Error())
	}
	waitErr := child.Wait()
	stopErr := child.StopTree(2 * time.Second)
	if waitErr != nil {
		return "", toolchainError(waitErr.Error() + ": " + strings.TrimSpace(stderr.String()))
	}
	if stopErr != nil {
		return "", toolchainError(stopErr.Error())
	}
	if strings.TrimSpace(stdout.String()) == "" {
		return "", toolchainError("a probe that printed nothing")
	}
	return stdout.String(), nil
}
