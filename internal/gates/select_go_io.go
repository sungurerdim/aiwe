package gates

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// goListLimit bounds one `go list` run (DR-1). The run is offline (offlineGoEnv): a cold module
// cache fails closed within the limit instead of spending it on downloads.
const goListLimit = 2 * time.Minute

// goListArgv is the command that prints the import graph (SPEC L66).
func goListArgv() []string {
	return []string{"go", "list", "-deps", "-test", "-json", "./..."}
}

// ListGoPackages runs `go list -deps -test -json ./...` in dir, the repository root and the root
// of its Go module, in the clean environment a step inherits, and returns the import graph of the
// module. A start, wait or stop failure, a non-zero exit and output that holds no package are
// errors that carry the stderr of go, so an unread graph never selects nothing (DR-6).
func ListGoPackages(ctx context.Context, dir string) (*GoGraph, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, goListError("an absolute repository root", err.Error(), err)
	}
	if resolved, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = resolved
	}
	ctx, cancel := context.WithTimeout(ctx, goListLimit)
	defer cancel()
	out, err := runGoList(ctx, root)
	if err != nil {
		return nil, err
	}
	return ParseGoList(out, root)
}

// SelectGoPackages lists the packages of the module in dir and selects the ones the changed files,
// slash paths relative to dir, affect (GoGraph.Select).
func SelectGoPackages(ctx context.Context, dir string, changed []string) (GoSelectResult, error) {
	g, err := ListGoPackages(ctx, dir)
	if err != nil {
		return GoSelectResult{}, err
	}
	return g.Select(changed), nil
}

// runGoList runs the go list command in dir and returns its stdout.
func runGoList(ctx context.Context, dir string) ([]byte, error) {
	argv := goListArgv()
	var stdout, stderr bytes.Buffer
	child, err := core.Start(ctx, core.ChildSpec{Argv: argv, Dir: dir,
		Env: offlineGoEnv(core.CleanEnv(core.Environ(), nil)), Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		return nil, goListError("`"+strings.Join(argv, " ")+"` started in "+dir, err.Error(), err)
	}
	waitErr := child.Wait()
	stopErr := child.StopTree(2 * time.Second)
	if waitErr != nil {
		return nil, goListError("`"+strings.Join(argv, " ")+"` to exit 0 in "+dir,
			waitErr.Error()+": "+strings.TrimSpace(stderr.String()), waitErr)
	}
	if stopErr != nil {
		return nil, goListError("`"+strings.Join(argv, " ")+"` to leave no child", stopErr.Error(), stopErr)
	}
	return stdout.Bytes(), nil
}

// offlineGoEnv returns parent for a go run that never fetches: GOPROXY is off, and -mod=readonly
// is the last flag of GOFLAGS, so a missing module or a go.mod that needs an edit is an error. The
// other GOFLAGS (for example -tags) stay, because they change the import graph. The names match
// without case, as Windows does. It runs after CleanEnv, which drops proxy variables.
func offlineGoEnv(parent []string) []string {
	flags := ""
	env := make([]string, 0, len(parent)+2)
	for _, kv := range parent {
		name, value, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "GOPROXY":
			continue
		case "GOFLAGS":
			flags = value
			continue
		}
		env = append(env, kv)
	}
	flags = strings.TrimSpace(flags + " -mod=readonly")
	return append(env, "GOPROXY=off", "GOFLAGS="+flags)
}
