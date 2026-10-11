package repocheck

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// MachineDoctorEnv returns the DoctorEnv of this machine for repo: the real program lookup, the
// Go version and the `go tool` resolution, each go call started through core.Start (SPEC §5.20).
func MachineDoctorEnv(ctx context.Context, repo string) DoctorEnv {
	return DoctorEnv{
		Repo:     repo,
		GOOS:     runtime.GOOS,
		LookPath: exec.LookPath,
		GoVersion: func() (string, error) {
			out, err := RunGo(ctx, repo, "env", "GOVERSION")
			return strings.TrimSpace(string(out)), err
		},
		ResolveTool: func(tool string) error {
			_, err := RunGo(ctx, repo, "tool", "-n", tool)
			return err
		},
	}
}

// RunGo runs the go command with args in dir through core.Start and returns its standard output;
// a start failure or a non-zero exit is an error that carries the standard error.
func RunGo(ctx context.Context, dir string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	child, err := core.Start(ctx, core.ChildSpec{Argv: append([]string{"go"}, args...), Dir: dir,
		Env: os.Environ(), Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		return nil, err
	}
	if err := child.Wait(); err != nil {
		return nil, fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// GoFiles reads every .go file of repo, skipping hidden directories, testdata and vendor; each
// Path is repository-relative and slash-separated.
func GoFiles(repo string) ([]SourceFile, error) {
	var files []SourceFile
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return skipDir(repo, p, d.Name())
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, SourceFile{Path: filepath.ToSlash(rel), Body: body})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read go files of %s: %w", repo, err)
	}
	return files, nil
}

// skipDir skips a hidden directory, testdata and vendor below the repository root.
func skipDir(repo, p, name string) error {
	if p == repo {
		return nil
	}
	if strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" {
		return filepath.SkipDir
	}
	return nil
}
