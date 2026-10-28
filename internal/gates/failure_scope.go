package gates

import (
	"path"
	"strings"
)

// RepoScope tells the parsers which stack frames belong to the repository. Module is the Go module
// path: a Go frame is a repository frame when its function lies in that module. Root is the
// repository directory: a JavaScript or Python frame is a repository frame when its file lies
// under it, and a relative path is taken as already relative to it. An absolute path with no Root
// is never a repository frame.
type RepoScope struct {
	Module string
	Root   string
}

// frame is one stack frame of a failure.
type frame struct {
	Func string
	Path string
	Line int
	Go   bool
}

func isAbsSlash(p string) bool {
	return strings.HasPrefix(p, "/") || (len(p) > 2 && p[1] == ':' && p[2] == '/')
}

// relPath returns the repository-relative slash path of a file, and false for a file that is not
// a repository file (outside Root, absolute without Root, a library or runtime path).
func (s RepoScope) relPath(raw string) (string, bool) {
	p := strings.ReplaceAll(raw, `\`, "/")
	if p == "" || strings.HasPrefix(p, "<") || strings.HasPrefix(p, "node:") {
		return "", false
	}
	switch root := strings.TrimRight(strings.ReplaceAll(s.Root, `\`, "/"), "/"); {
	case !isAbsSlash(p):
		// a relative path is already relative to the repository
	case root == "":
		return "", false
	case len(p) <= len(root) || !strings.EqualFold(p[:len(root)], root) || p[len(root)] != '/':
		return "", false
	default:
		p = p[len(root)+1:]
	}
	p = path.Clean(p)
	return p, !isLibraryPath(p)
}

func isLibraryPath(p string) bool {
	if strings.HasPrefix(p, "../") || strings.HasPrefix(p, "vendor/") {
		return true
	}
	for _, mark := range []string{"/node_modules/", "/site-packages/", "/dist-packages/", "/vendor/", "/.venv/", "/venv/", "/pkg/mod/"} {
		if strings.Contains("/"+p, mark) {
			return true
		}
	}
	return false
}

// isTestPath says whether a repository file is test code.
func isTestPath(p string) bool {
	base := path.Base(p)
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.HasPrefix(base, "test_"), strings.HasSuffix(base, "_test.py"),
		base == "conftest.py", strings.Contains(base, ".test."), strings.Contains(base, ".spec."):
		return true
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if seg == "tests" || seg == "test" || seg == "__tests__" {
			return true
		}
	}
	return false
}

// goPackageDir returns the repository directory of a Go import path in the module.
func goPackageDir(pkg, module string) (string, bool) {
	switch {
	case module == "":
		return "", false
	case pkg == module:
		return "", true
	case strings.HasPrefix(pkg, module+"/"):
		return strings.TrimPrefix(pkg, module+"/"), true
	}
	return "", false
}

// goFuncDir returns the repository directory of the package a Go function name belongs to.
func goFuncDir(fn, module string) (string, bool) {
	slash := strings.LastIndex(fn, "/")
	dot := strings.Index(fn[slash+1:], ".")
	if dot < 0 {
		return "", false
	}
	pkg := strings.TrimSuffix(fn[:slash+1+dot], "_test")
	return goPackageDir(pkg, module)
}

// frameRel returns the repository-relative path of a frame, and false for a frame outside the
// repository.
func (s RepoScope) frameRel(f frame) (string, bool) {
	if f.Go && s.Module != "" {
		dir, ok := goFuncDir(f.Func, s.Module)
		if !ok {
			return "", false
		}
		p := path.Join(dir, path.Base(strings.ReplaceAll(f.Path, `\`, "/")))
		return p, !isLibraryPath(p)
	}
	return s.relPath(f.Path)
}

// deepestFirst orders a stack with its deepest frame first. Go and JavaScript list the innermost
// frame first, Python last.
func deepestFirst(frames []frame, innermostFirst bool) []frame {
	if innermostFirst {
		return frames
	}
	out := make([]frame, len(frames))
	for i, f := range frames {
		out[len(frames)-1-i] = f
	}
	return out
}

// locate returns the deepest test frame (testLoc), the deepest product frame (srcLoc) and the
// repository frames (at most failureRepoFrames, deepest first) of a stack. Library and runtime
// frames are removed.
func (s RepoScope) locate(frames []frame, innermostFirst bool) (testLoc, srcLoc string, repo []StackFrame) {
	for _, f := range deepestFirst(frames, innermostFirst) {
		rel, ok := s.frameRel(f)
		if !ok {
			continue
		}
		loc := formatLocation(rel, f.Line)
		if len(repo) < failureRepoFrames {
			repo = append(repo, StackFrame{Location: loc, Func: shorten(f.Func, failureValueMax)})
		}
		switch {
		case isTestPath(rel):
			if testLoc == "" {
				testLoc = loc
			}
		case srcLoc == "":
			srcLoc = loc
		}
	}
	return testLoc, srcLoc, repo
}

// frameExcerpt is the head line of a failure and its repository frames, deepest first: the
// runtime and library frames are not in it.
func frameExcerpt(head string, repo []StackFrame) string {
	lines := []string{head}
	for _, f := range repo {
		lines = append(lines, "at "+f.Location)
	}
	return logExcerpt(lines)
}
