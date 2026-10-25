package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"
)

// timeout bounds a whole run; the gh calls are read-only and short.
const timeout = 10 * time.Minute

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	code := runMain(ctx)
	cancel()
	os.Exit(code)
}

// runMain wires the real processes and files to execute. The record files live in the working
// directory, reached through an os.Root, so the run must start at the repository root.
func runMain(ctx context.Context) int {
	root, err := os.OpenRoot(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "records: open the working directory: %v\n", err)
		return 1
	}
	defer func() { _ = root.Close() }()
	if _, err := root.Stat("go.mod"); err != nil {
		fmt.Fprintln(os.Stderr, "records: run from the repository root (go.mod not found in the working directory)")
		return exitUsage
	}
	d := deps{run: execRun, read: readIn(root), now: time.Now}
	write := func(r record) error { return root.WriteFile(r.name, r.body, 0o600) }
	return execute(ctx, os.Args[1:], d, write, os.Stdout, os.Stderr)
}

// execRun starts one command in the working directory and returns its standard output.
func execRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: the program is gh or git and every argument is built by this tool
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%s was not found in PATH", name)
		}
		first, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		return nil, fmt.Errorf("%w: %s", err, first)
	}
	return stdout.Bytes(), nil
}

// readIn returns a reader of the files under root; a missing file is not an error.
func readIn(root *os.Root) func(string) ([]byte, bool, error) {
	return func(name string) ([]byte, bool, error) {
		data, err := root.ReadFile(name)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return data, err == nil, err
	}
}
