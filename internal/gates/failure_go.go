package gates

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// goOutputCap bounds the output kept for one test, so a hostile or runaway report cannot grow
// without limit.
const goOutputCap = 256 << 10

var (
	goFileRe    = regexp.MustCompile(`^\s+(\S.*?\.go):(\d+)(?: \+0x[0-9a-fA-F]+)?$`)
	goLocRe     = regexp.MustCompile(`^\s+(\S[^:]*\.go):(\d+):(?: (.*))?$`)
	goCompileRe = regexp.MustCompile(`^(?:vet: )?((?:[A-Za-z]:)?[^:\s][^:]*\.go):(\d+):(\d+): (.+)$`)
)

// goEvent is one test2json event of `go test -json` (the build events carry ImportPath).
type goEvent struct {
	Action      string
	Package     string
	Test        string
	Output      string
	ImportPath  string
	FailedBuild string
}

// goAcc collects the events of one test, one package or one build.
type goAcc struct {
	pkg, test, failedBuild string
	failed                 bool
	size                   int
	parts                  []string
}

func (a *goAcc) add(s string) {
	if a.size+len(s) > goOutputCap {
		return
	}
	a.size += len(s)
	a.parts = append(a.parts, s)
}

func (a *goAcc) lines() []string {
	return strings.Split(cleanText(strings.Join(a.parts, "")), "\n")
}

// goGroup keeps accumulators in the order their first event arrived.
type goGroup struct {
	m     map[string]*goAcc
	order []string
}

func (g *goGroup) get(key, pkg, test string) *goAcc {
	if g.m == nil {
		g.m = map[string]*goAcc{}
	}
	a, ok := g.m[key]
	if !ok {
		a = &goAcc{pkg: pkg, test: test}
		g.m[key] = a
		g.order = append(g.order, key)
	}
	return a
}

func (g *goGroup) list() []*goAcc {
	out := make([]*goAcc, 0, len(g.order))
	for _, key := range g.order {
		out = append(out, g.m[key])
	}
	return out
}

type goReport struct {
	tests, pkgs, builds goGroup
}

func (r *goReport) add(ev goEvent) {
	switch ev.Action {
	case "build-output":
		r.builds.get(ev.ImportPath, ev.ImportPath, "").add(ev.Output)
	case "build-fail":
		r.builds.get(ev.ImportPath, ev.ImportPath, "").failed = true
	case "output", "fail":
		a := r.pkgs.get(ev.Package, ev.Package, "")
		if ev.Test != "" {
			a = r.tests.get(ev.Package+"\x00"+ev.Test, ev.Package, ev.Test)
		}
		if ev.Action == "output" {
			a.add(ev.Output)
			return
		}
		a.failed = true
		if ev.FailedBuild != "" {
			a.failedBuild = ev.FailedBuild
		}
	}
}

// FailuresFromGoJSON reads the failing tests, builds and packages out of a `go test -json` stream
// (test2json events): one record per failing test, with the test's own output only, since every
// event names its test. A line that is not a JSON event is skipped.
func FailuresFromGoJSON(data []byte, scope RepoScope) []Failure {
	var r goReport
	for _, line := range bytes.Split(data, []byte("\n")) {
		var ev goEvent
		if line = bytes.TrimSpace(line); len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &ev) != nil {
			continue
		}
		r.add(ev)
	}
	covered := map[string]bool{} // packages that already have a record
	out := r.buildFailures(scope, covered)
	out = append(out, r.testFailures(scope, covered)...)
	for _, a := range r.pkgs.list() {
		if a.failed && !covered[a.pkg] {
			out = append(out, goPackageFailure(a, scope))
		}
	}
	return out
}

func (r *goReport) buildFailures(scope RepoScope, covered map[string]bool) []Failure {
	var out []Failure
	for _, a := range r.builds.list() {
		if pkg, _, _ := strings.Cut(a.pkg, " "); a.failed {
			out = append(out, goBuildFailure(pkg, a.lines(), scope))
			covered[pkg] = true
		}
	}
	return out
}

// testFailures gives one record per failing test. A parent test that fails only because a
// subtest failed has no evidence of its own and gets no record.
func (r *goReport) testFailures(scope RepoScope, covered map[string]bool) []Failure {
	parents := map[string]bool{}
	for _, a := range r.tests.list() {
		if !a.failed {
			continue
		}
		covered[a.pkg] = true
		for i, c := range a.test {
			if c == '/' {
				parents[a.pkg+"\x00"+a.test[:i]] = true
			}
		}
	}
	var out []Failure
	for _, a := range r.tests.list() {
		if !a.failed {
			continue
		}
		f, own := goTestFailure(a, scope)
		if own || !parents[a.pkg+"\x00"+a.test] {
			out = append(out, f)
		}
	}
	return out
}

// goNoise says whether an output line is the test framework's own bookkeeping.
func goNoise(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "=== ") || strings.HasPrefix(t, "--- ") {
		return true
	}
	for _, p := range []string{"FAIL", "PASS", "ok ", "exit status ", "coverage: "} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func goOwnLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if !goNoise(line) {
			out = append(out, line)
		}
	}
	return out
}

// goHeadline finds the first line that makes a failure a panic, a timeout or a race.
func goHeadline(lines []string) (kind, head string) {
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "panic: test timed out"):
			return FailureTimeout, t
		case strings.HasPrefix(line, "panic: "):
			return FailurePanic, t
		case t == "WARNING: DATA RACE", strings.Contains(t, "race detected during execution of test"):
			return FailureRace, t
		}
	}
	return "", ""
}

// goFrames reads the stack frames out of a panic or race report: a function line followed by an
// indented file:line line. The deepest frame comes first.
func goFrames(lines []string) []frame {
	var out []frame
	for i := 1; i < len(lines) && len(out) < failureFramesMax; i++ {
		m := goFileRe.FindStringSubmatch(lines[i])
		fn := strings.TrimSpace(lines[i-1])
		if m == nil || fn == "" || goFileRe.MatchString(lines[i-1]) {
			continue
		}
		out = append(out, frame{Func: fn, Path: m[1], Line: atoi(m[2]), Go: true})
	}
	return out
}

func goTestFailure(a *goAcc, scope RepoScope) (Failure, bool) {
	f := newFailure(a.pkg+"."+a.test, FailureAssertion)
	body := goOwnLines(a.lines())
	if kind, head := goHeadline(body); kind != "" {
		fillGoStack(&f, kind, head, body, scope)
		return f, true
	}
	dir, _ := goPackageDir(a.pkg, scope.Module)
	var msg []string
	for _, line := range body {
		m := goLocRe.FindStringSubmatch(line)
		if m == nil {
			msg = append(msg, strings.TrimSpace(line))
			continue
		}
		if f.TestLocation == "" {
			f.TestLocation = formatLocation(path.Join(dir, strings.ReplaceAll(m[1], `\`, "/")), atoi(m[2]))
		}
		msg = append(msg, m[3])
	}
	text := strings.Join(msg, "\n")
	first := firstLine(text)
	if first == "" {
		first = "test failed"
	}
	f.Got, f.Want = wantGot(text)
	f.Message = composeMessage(first, f.Got, f.Want)
	f.Attachments = logAttachment(logExcerpt(msg))
	return f, f.TestLocation != ""
}

// fillGoStack fills a record whose evidence is a stack: the repository frames name the
// locations, the runtime and library frames are dropped.
func fillGoStack(f *Failure, kind, head string, lines []string, scope RepoScope) {
	frames := goFrames(lines)
	f.Kind = kind
	f.Message = shorten(head, failureMessageMax)
	f.TestLocation, f.SourceLocation = scope.locate(frames, true)
	f.Attachments = logAttachment(scope.frameExcerpt(head, frames, true))
}

// goBuildFailure names the first compiler error as file:line:col with the compiler's message; a
// build failure without one keeps its first line.
func goBuildFailure(pkg string, lines []string, scope RepoScope) Failure {
	f := newFailure(pkg, FailureBuild)
	for _, line := range lines {
		if m := goCompileRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			f.Kind = FailureCompile
			f.TestLocation = formatLocation(goBuildPath(m[1], pkg, scope), atoi(m[2])) + ":" + m[3]
			f.Message = shorten(m[4], failureMessageMax)
			break
		}
	}
	if f.Kind == FailureBuild {
		f.Message = "build failed"
		for _, line := range lines {
			if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "FAIL") {
				f.Message = shorten(t, failureMessageMax)
				break
			}
		}
	}
	f.Attachments = logAttachment(logExcerpt(lines))
	return f
}

// goBuildPath makes a compiler path repository-relative: "./x.go" is relative to the package.
func goBuildPath(raw, pkg string, scope RepoScope) string {
	p := strings.ReplaceAll(raw, `\`, "/")
	if rest, ok := strings.CutPrefix(p, "./"); ok {
		dir, _ := goPackageDir(pkg, scope.Module)
		return path.Join(dir, rest)
	}
	if rel, ok := scope.relPath(p); ok {
		return rel
	}
	return p
}

// goPackageFailure is a package that failed with no failing test and no build event: a build
// error of an older Go, a panic in TestMain, a timeout or a failed setup.
func goPackageFailure(a *goAcc, scope RepoScope) Failure {
	lines := goOwnLines(a.lines())
	if a.failedBuild != "" {
		return goBuildFailure(a.pkg, lines, scope)
	}
	f := newFailure(a.pkg, FailureSetup)
	if kind, head := goHeadline(lines); kind != "" {
		fillGoStack(&f, kind, head, lines, scope)
		return f
	}
	f.Message = shorten(firstLine(strings.Join(lines, "\n")), failureMessageMax)
	if f.Message == "" {
		f.Message = "package failed"
	}
	f.Attachments = logAttachment(logExcerpt(lines))
	return f
}
