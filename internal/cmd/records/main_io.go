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

// runMain wires the real processes and files to execute. The record files are written at the
// working directory, so the run must start at the repository root.
func runMain(ctx context.Context) int {
	if _, err := os.Stat("go.mod"); err != nil {
		fmt.Fprintln(os.Stderr, "records: run from the repository root (go.mod not found in the working directory)")
		return exitUsage
	}
	d := deps{run: execRun, read: readFile, now: time.Now}
	return execute(ctx, os.Args[1:], d, writeFile, os.Stdout, os.Stderr)
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

// readFile reads a file of the working directory; a missing file is not an error.
func readFile(name string) ([]byte, bool, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// writeFile replaces one record file.
func writeFile(r record) error {
	return os.WriteFile(r.name, r.body, 0o600)
}
