package gates

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Failure kinds (SPEC L76). A raised exception has the kind FailureExceptionPrefix + its type.
const (
	FailureAssertion       = "assertion"
	FailureCollection      = "collection"
	FailureImport          = "import"
	FailureCompile         = "compile"
	FailureBuild           = "build"
	FailurePanic           = "panic"
	FailureRace            = "race"
	FailureTimeout         = "timeout"
	FailureSetup           = "setup"
	FailureNoTests         = "no_tests"
	FailureExceptionPrefix = "exception:"
)

const (
	failureMessageMax   = 240
	failureValueMax     = 120
	failureExcerptLines = 10
	failureExcerptMax   = 800
	failureFramesMax    = 200
)

// Attachment is one piece of evidence of a failure: the path of a Playwright trace or screenshot,
// or a log excerpt.
type Attachment struct {
	Kind string `json:"kind"` // "trace", "screenshot", "file" or "log"
	Path string `json:"path,omitempty"`
	Text string `json:"text,omitempty"`
}

// Failure is the structured record of one failing test (SPEC L76), read from the report of the
// test framework and never from free output text. Its fields line up with a SARIF result (SPEC
// §7.9): locations, message and properties.evidence (Got and Want).
type Failure struct {
	Test           string       `json:"test"`
	TestLocation   string       `json:"testLocation,omitempty"`   // file:line where the test failed
	SourceLocation string       `json:"sourceLocation,omitempty"` // deepest repository product frame
	Kind           string       `json:"kind"`
	Message        string       `json:"message"`
	Got            string       `json:"got,omitempty"`
	Want           string       `json:"want,omitempty"`
	Rerun          string       `json:"rerun"`
	Attachments    []Attachment `json:"attachments,omitempty"`
}

// RepoScope tells the parsers which stack frames belong to the repository. Module is the Go module
// path: a Go frame is a repository frame when its function lies in that module. Root is the
// repository directory: a JavaScript or Python frame is a repository frame when its file lies
// under it, and a relative path is taken as already relative to it (Root empty).
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

var (
	ansiRe      = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	gotWantRe   = regexp.MustCompile(`(?i)\bgot:?\s+(.+?),?\s+want:?\s+(.+)$`)
	wantGotRe   = regexp.MustCompile(`(?i)\bexpected:?\s+(.+?),?\s+(?:but\s+)?(?:got|actual|received|was):?\s+(.+)$`)
	assertEqRe  = regexp.MustCompile(`^\s*(?:E\s+)?assert\s+(.+?)\s+==\s+(.+)$`)
	gotLabelRe  = regexp.MustCompile(`(?i)^\s*(?:got|actual|received|have)\s*:\s*(\S.*)$`)
	wantLabelRe = regexp.MustCompile(`(?i)^\s*(?:want|expected|exp)\s*:\s*(\S.*)$`)
	safeIDRe    = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,][A-Za-z0-9_./:@%+=,-]*$`)
)

// RerunCommand returns the exact command that runs only the test with the given id (SPEC L76).
// An id with a character the shell reads is single-quoted, so an id from a report never becomes
// a second command.
func RerunCommand(id string) string {
	arg := id
	if !safeIDRe.MatchString(id) {
		arg = "'" + strings.ReplaceAll(id, "'", `'\''`) + "'"
	}
	return core.Product + " check verify --test " + arg
}

func newFailure(test, kind string) Failure {
	if test == "" {
		test = "unknown"
	}
	return Failure{Test: test, Kind: kind, Rerun: RerunCommand(test)}
}

// cleanText drops terminal colour codes and carriage returns.
func cleanText(s string) string {
	return strings.ReplaceAll(ansiRe.ReplaceAllString(s, ""), "\r", "")
}

// shorten cuts s to limit runes, marking the cut.
func shorten(s string, limit int) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "�")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// logExcerpt returns the first lines of text as one log attachment text.
func logExcerpt(lines []string) string {
	var kept []string
	for _, line := range lines {
		if t := strings.TrimRight(line, " \t"); strings.TrimSpace(t) != "" {
			kept = append(kept, t)
		}
		if len(kept) == failureExcerptLines {
			break
		}
	}
	return shorten(strings.Join(kept, "\n"), failureExcerptMax)
}

func logAttachment(text string) []Attachment {
	if text == "" {
		return nil
	}
	return []Attachment{{Kind: "log", Text: text}}
}

// wantGot reads the actual and expected values out of a failure message: "got X, want Y",
// "expected X but got Y", "assert X == Y" and the labelled lines "got:" / "want:" /
// "Expected:" / "Received:".
func wantGot(text string) (got, want string) {
	for _, line := range strings.Split(text, "\n") {
		if m := gotWantRe.FindStringSubmatch(line); m != nil {
			return shorten(m[1], failureValueMax), shorten(m[2], failureValueMax)
		}
		if m := wantGotRe.FindStringSubmatch(line); m != nil {
			return shorten(m[2], failureValueMax), shorten(m[1], failureValueMax)
		}
		if m := assertEqRe.FindStringSubmatch(line); m != nil {
			return shorten(m[1], failureValueMax), shorten(m[2], failureValueMax)
		}
		if m := gotLabelRe.FindStringSubmatch(line); m != nil && got == "" {
			got = shorten(m[1], failureValueMax)
		}
		if m := wantLabelRe.FindStringSubmatch(line); m != nil && want == "" {
			want = shorten(m[1], failureValueMax)
		}
	}
	return got, want
}

// composeMessage is the first line of a failure plus its actual and expected values, shortened.
func composeMessage(first, got, want string) string {
	msg := shorten(first, failureMessageMax)
	if strings.Contains(first, got) && strings.Contains(first, want) {
		return msg
	}
	return shorten(msg+" (got "+got+", want "+want+")", failureMessageMax)
}

// atoi reads a decimal number, and gives 0 for text that is not one.
func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func formatLocation(rel string, line int) string {
	if line <= 0 {
		return rel
	}
	return fmt.Sprintf("%s:%d", rel, line)
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

// locate returns the deepest test frame (testLoc) and the deepest product frame (srcLoc) of a
// stack, with library and runtime frames removed. innermostFirst tells which end of frames is
// the deepest: Go and JavaScript list the innermost frame first, Python last.
func (s RepoScope) locate(frames []frame, innermostFirst bool) (testLoc, srcLoc string) {
	for i := range frames {
		f := frames[i]
		if !innermostFirst {
			f = frames[len(frames)-1-i]
		}
		rel, ok := s.frameRel(f)
		if !ok {
			continue
		}
		switch loc := formatLocation(rel, f.Line); {
		case isTestPath(rel):
			if testLoc == "" {
				testLoc = loc
			}
		case srcLoc == "":
			srcLoc = loc
		}
	}
	return testLoc, srcLoc
}

// frameExcerpt is the head line of a failure and its repository frames, deepest first: the
// runtime and library frames are not in it.
func (s RepoScope) frameExcerpt(head string, frames []frame, innermostFirst bool) string {
	lines := []string{head}
	for i := range frames {
		f := frames[i]
		if !innermostFirst {
			f = frames[len(frames)-1-i]
		}
		if rel, ok := s.frameRel(f); ok {
			lines = append(lines, "at "+formatLocation(rel, f.Line))
		}
	}
	return logExcerpt(lines)
}
