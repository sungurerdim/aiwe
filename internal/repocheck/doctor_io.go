package repocheck

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// MachineDoctorEnv returns the DoctorEnv of this machine for repo: the real program lookup, the
// Go version and the tool resolution through the tools module, each go call started through
// core.Start (SPEC §5.20, §12.2).
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
			_, err := RunGo(ctx, repo, "tool", "-modfile="+toolsModFile, "-n", tool)
			return err
		},
	}
}

// RunGo runs the go command with args in dir through core.Start and returns its standard output;
// a start failure or a non-zero exit is an error that carries the standard error.
func RunGo(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return runProgram(ctx, dir, "go", args...)
}

// OriginURL returns the URL of the origin remote of the repository at repo, as
// `git remote get-url origin` prints it; no origin remote, or no git, is an error.
func OriginURL(ctx context.Context, repo string) (string, error) {
	out, err := runProgram(ctx, repo, "git", "remote", "get-url", "origin")
	return strings.TrimSpace(string(out)), err
}

// runProgram runs program with args in dir through core.Start and returns its standard output; a
// start failure or a non-zero exit is an error that carries the standard error.
func runProgram(ctx context.Context, dir, program string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	child, err := core.Start(ctx, core.ChildSpec{Argv: append([]string{program}, args...), Dir: dir,
		Env: core.Environ(), Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		return nil, err
	}
	if err := child.Wait(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", program, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// GoFiles reads every .go file of repo, skipping hidden directories, testdata and vendor; each
// Path is repository-relative and slash-separated. A repository without a .go file is an error.
// The walk and every read go through an os.Root at repo, so a link, or a directory swapped for
// one during the walk, never leads a read outside the repository: it is an error.
// A .go entry that is not a regular file, such as a FIFO, or is over maxRepoFileBytes is an error.
func GoFiles(repo string) ([]SourceFile, error) {
	return readRepoFiles(repo, repoWalk{kind: "go files", empty: "no .go file found", goOnly: true, skip: skipDir})
}

// SourceFiles reads every file repo/source-rules judges: the Go files GoFiles reads, and every
// other regular file, including one in a dot directory such as .githooks and one under testdata
// or vendor. A tracked shell script then reaches SourceRules instead of being dropped (K31).
// .git is skipped because it is not tracked content. A tree with no file is an error.
func SourceFiles(repo string) ([]SourceFile, error) {
	return readRepoFiles(repo, repoWalk{kind: "source files", empty: "no file found", skip: skipSourceDir})
}

// repoWalk chooses which directories and files one repository walk keeps.
type repoWalk struct {
	kind   string
	empty  string
	goOnly bool
	skip   func(p, name string) error
}

// readRepoFiles walks repo through an os.Root and returns the files w keeps. Zero files is an
// error: nothing measured is never a pass (DR-6).
func readRepoFiles(repo string, w repoWalk) ([]SourceFile, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, fmt.Errorf("read %s of %s: %w", w.kind, repo, err)
	}
	defer func() { _ = root.Close() }()
	var files []SourceFile
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return w.skip(p, d.Name())
		}
		if w.goOnly && !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		body, cut, rerr := readWalkFile(root, p, d)
		if rerr != nil {
			return rerr
		}
		files = append(files, SourceFile{Path: p, Body: body, shebangCut: cut})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s of %s: %w", w.kind, repo, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("read %s of %s: %s", w.kind, repo, w.empty)
	}
	return files, nil
}

// shebangHeadBytes is the prefix kept from a non-Go file over maxRepoFileBytes.
// A shebang interpreter fits in it, so the cap is not read as an empty file (K31, DR-6).
const shebangHeadBytes = 4096

// readWalkFile reads p. A non-regular file, or a .go file over maxRepoFileBytes, is an error.
// A non-.go file over the cap keeps its first line: .sh and .ps1 still match by name, and an
// extensionless shebang is still judged instead of passing on an empty body (K31, DR-6).
// cut reports a shebang whose first line does not end within shebangHeadBytes.
func readWalkFile(root *os.Root, p string, d fs.DirEntry) ([]byte, bool, error) {
	body, err := core.ReadRegularCapped(root, p, maxRepoFileBytes)
	if err == nil {
		return body, false, nil
	}
	if strings.HasSuffix(d.Name(), ".go") || !fileOverCap(d) {
		return nil, false, fmt.Errorf("%s: %w", p, err)
	}
	return shebangHead(root, p)
}

// fileOverCap reports a regular file over maxRepoFileBytes. The size comes from the directory
// entry: ReadRegularCapped's refusal is plain text with no sentinel (SPEC §5.3).
func fileOverCap(d fs.DirEntry) bool {
	info, err := d.Info()
	if err != nil || info == nil || !info.Mode().IsRegular() {
		return false
	}
	return info.Size() > maxRepoFileBytes
}

// shebangHead reads the start of p through its first LF, at most shebangHeadBytes.
// cut is true when that prefix has no LF and starts with a shebang, so the interpreter may sit
// past it; an unfinished shebang is not a pass (DR-6). A non-regular file or a failed read is an
// error, never an empty body.
func shebangHead(root *os.Root, p string) ([]byte, bool, error) {
	f, err := root.Open(p)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", p, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", p, err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s: not a regular file: %s", p, info.Mode().Type())
	}
	buf := make([]byte, shebangHeadBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", p, err)
	}
	buf = buf[:n]
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		return buf[:i+1], false, nil
	}
	return buf, bytes.HasPrefix(buf, []byte("#!")), nil
}

// skipDir skips a hidden directory, testdata and vendor below the repository root.
func skipDir(p, name string) error {
	if p == "." {
		return nil
	}
	if strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" {
		return filepath.SkipDir
	}
	return nil
}

// skipSourceDir skips .git only. testdata and vendor are walked: a tracked script there is still
// a script (K31). Another dot directory, such as .githooks, is checked. .git is not tracked content.
func skipSourceDir(p, name string) error {
	if p == "." {
		return nil
	}
	if strings.EqualFold(name, ".git") {
		return filepath.SkipDir
	}
	return nil
}
