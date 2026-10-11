package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hooksDirName is the engine-owned empty directory under the state root that every git call
// takes as its core.hooksPath, so no hook of a clone or of the user runs (DR-49).
const hooksDirName = "hooks"

// GitResult is the outcome of one git call: its output and its exit code.
type GitResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Git runs git with args in dir through Start, in a clean environment, with the repository's
// programs off (DR-49, SPEC §5.21): fixed -c options before the subcommand, and
// --no-ext-diff --no-textconv after diff, log and show. A non-zero exit is an error; the result
// still carries the output and the exit code. args[0] must be the subcommand: a leading option
// would drop the diff flags or override a fixed option, so git is not started.
func Git(ctx context.Context, dir string, args ...string) (GitResult, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		found := "no arguments"
		if len(args) > 0 {
			found = fmt.Sprintf("the option %q", args[0])
		}
		return GitResult{ExitCode: -1}, &Error{Outcome: OutcomeError,
			Expected: "a git subcommand as the first argument", Found: found,
			Next: "pass the subcommand first and its options after it"}
	}
	hooks, err := emptyHooksDir()
	if err != nil {
		return GitResult{ExitCode: -1}, fmt.Errorf("git: %w", err)
	}
	var stdout, stderr bytes.Buffer
	c, err := Start(ctx, ChildSpec{
		Argv:   gitArgv(hooks, args),
		Dir:    dir,
		Env:    CleanEnv(os.Environ(), nil),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return GitResult{ExitCode: -1}, fmt.Errorf("git %v: %w", args, err)
	}
	waitErr := c.Wait()
	stopErr := c.StopTree(cancelGrace) // a process git left behind must not outlive the call
	res := GitResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: c.cmd.ProcessState.ExitCode()}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			res.ExitCode = -1
		}
		return res, fmt.Errorf("git %v: exit %d: %w: %s", args, res.ExitCode, waitErr, bytes.TrimSpace(stderr.Bytes()))
	}
	if stopErr != nil {
		return res, fmt.Errorf("git %v: %w", args, stopErr)
	}
	return res, nil
}

// gitArgv returns the git argv: the fixed options, the subcommand and, for diff, log and show,
// the flags that turn external diff and textconv programs off, then the caller's arguments.
func gitArgv(hooks string, args []string) []string {
	argv := []string{
		"git",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=" + filepath.ToSlash(hooks),
		"-c", "protocol.ext.allow=never",
		"-c", "submodule.recurse=false",
	}
	argv = append(argv, args[0])
	switch args[0] {
	case "diff", "log", "show":
		argv = append(argv, "--no-ext-diff", "--no-textconv")
	}
	return append(argv, args[1:]...)
}

// emptyHooksDir returns the engine-owned hooks directory under the state root, creating it when
// missing. It refuses a path that is not a real directory or holds any entry (fail-closed).
func emptyHooksDir() (string, error) {
	roots, err := UserRoots()
	if err != nil {
		return "", err
	}
	p := filepath.Join(roots.State, hooksDirName)
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", fmt.Errorf("hooks directory: %w", err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return "", fmt.Errorf("hooks directory: %w", err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("hooks directory %s: not a real directory", p)
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", fmt.Errorf("hooks directory: %w", err)
	}
	if len(entries) != 0 {
		return "", &Error{Outcome: OutcomeError,
			Expected: "an empty hooks directory " + p,
			Found:    fmt.Sprintf("%d entries", len(entries)),
			Next:     "remove every entry from the hooks directory; the engine owns it and keeps it empty"}
	}
	return p, nil
}
