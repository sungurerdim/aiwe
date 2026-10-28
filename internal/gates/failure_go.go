package gates

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"strings"
)

// goOutputCap bounds the output kept for one test, so a hostile or runaway report cannot grow
// without limit.
const goOutputCap = 256 << 10

var (
	goFileRe = regexp.MustCompile(`^\s+(\S.*?\.go):(\d+)(?: \+0x[0-9a-fA-F]+)?$`)
	// goLocRe is the "file.go:12: message" prefix of t.Log, t.Error and t.Fatal output; the file
	// is a base name, or a full path under go test -fullpath.
	goLocRe = regexp.MustCompile(`^\s+((?:[A-Za-z]:)?[^\s:][^:]*\.go):(\d+):(?: (.*))?$`)
)

// goEvent is one test2json event of `go test -json` (the build events carry ImportPath).
type goEvent struct {
	Action      string
	Package     string
	Test        string
	Output      string
	ImportPath  string
	FailedBuild string
	Elapsed     float64
}

// goAcc collects the events of one test, one package or one build.
type goAcc struct {
	pkg, test, failedBuild string
	failed                 bool
	elapsed                float64
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
		a.elapsed = ev.Elapsed
		if ev.FailedBuild != "" {
			a.failedBuild = ev.FailedBuild
		}
	}
}

// decodeGoEvent reads one line of the stream; a line that is not a test2json event is skipped.
func decodeGoEvent(line []byte) (goEvent, bool) {
	var ev goEvent
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &ev) != nil {
		return ev, false
	}
	return ev, ev.Action != ""
}

// FailuresFromGoJSON reads the failing tests, builds and packages out of a `go test -json` stream
// (test2json events): one record per failing test, with the test's own output only, since every
// event names its test. A line that is not a JSON event is skipped. A stream that holds no event
// at all (empty, killed before it wrote, not test2json) is an error: it is not "no failures".
func FailuresFromGoJSON(data []byte, scope RepoScope) ([]Failure, error) {
	var r goReport
	events := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		if ev, ok := decodeGoEvent(line); ok {
			r.add(ev)
			events++
		}
	}
	if events == 0 {
		return nil, errors.New("gates: the go test stream holds no test2json event")
	}
	built := map[string]bool{}   // build import paths that already have a record
	covered := map[string]bool{} // packages that already have a record
	out := r.buildFailures(scope, built, covered)
	out = append(out, r.testFailures(scope, covered)...)
	for _, a := range r.pkgs.list() {
		if a.failed && !covered[a.pkg] && !built[a.failedBuild] {
			out = append(out, goPackageFailure(a, scope))
		}
	}
	return out, nil
}

// failedParents marks the parents of failing subtests.
func (r *goReport) failedParents() map[string]bool {
	parents := map[string]bool{}
	for _, a := range r.tests.list() {
		if !a.failed {
			continue
		}
		for i, c := range a.test {
			if c == '/' {
				parents[a.pkg+"\x00"+a.test[:i]] = true
			}
		}
	}
	return parents
}

// testFailures gives one record per failing test. A parent test that fails only because a
// subtest failed has no evidence of its own and gets no record.
func (r *goReport) testFailures(scope RepoScope, covered map[string]bool) []Failure {
	parents := r.failedParents()
	var out []Failure
	for _, a := range r.tests.list() {
		if !a.failed {
			continue
		}
		covered[a.pkg] = true
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
	all := a.lines()
	f.Package, f.DurationMs, f.Output = a.pkg, durationMs(a.elapsed), ownOutput(all)
	body := goOwnLines(all)
	if kind, head := goHeadline(body); kind != "" {
		fillGoStack(&f, kind, head, body, scope)
		return f, true
	}
	fillGoAssertion(&f, body, scope)
	return f, f.TestLocation != ""
}

// fillGoStack fills a record whose evidence is a stack: the repository frames name the
// locations, the runtime and library frames are dropped.
func fillGoStack(f *Failure, kind, head string, lines []string, scope RepoScope) {
	f.Kind = kind
	f.Message = shorten(head, failureMessageMax)
	f.TestLocation, f.SourceLocation, f.Frames = scope.locate(goFrames(lines), true)
	f.Attachments = logAttachment(frameExcerpt(head, f.Frames))
}

// goEntry is one located line of a test's output and the lines that continue it.
type goEntry struct {
	loc string
	msg []string
}

// goLocation makes the file of a "file.go:12:" prefix repository-relative. testing prints the
// base name, which lies in the package directory; -fullpath prints the whole path.
func goLocation(raw string, line int, dir string, dirOK bool, scope RepoScope) string {
	p := strings.ReplaceAll(raw, `\`, "/")
	if isAbsSlash(p) {
		rel, ok := scope.relPath(p)
		switch {
		case ok:
			p = rel
		case dirOK:
			p = path.Join(dir, path.Base(p))
		default:
			return ""
		}
	} else {
		p = path.Join(dir, p)
	}
	return formatLocation(p, line)
}

// goEntries splits the output of a test into located entries; loose holds the lines before the
// first one.
func goEntries(body []string, pkg string, scope RepoScope) (entries []goEntry, loose []string) {
	dir, dirOK := goPackageDir(pkg, scope.Module)
	for _, line := range body {
		if m := goLocRe.FindStringSubmatch(line); m != nil {
			entries = append(entries, goEntry{loc: goLocation(m[1], atoi(m[2]), dir, dirOK, scope), msg: []string{m[3]}})
		} else if n := len(entries); n > 0 {
			entries[n-1].msg = append(entries[n-1].msg, strings.TrimSpace(line))
		} else {
			loose = append(loose, strings.TrimSpace(line))
		}
	}
	return entries, loose
}

// pickGoEntry chooses the entry that is the failure. test2json does not tell t.Log from t.Error,
// so a log line before the assertion would look like one: the entry that holds got and want wins,
// else the last entry, because t.Fatal ends the test.
func pickGoEntry(entries []goEntry) goEntry {
	for _, e := range entries {
		if g, w := wantGot(strings.Join(e.msg, "\n")); g != "" || w != "" {
			return e
		}
	}
	return entries[len(entries)-1]
}

func fillGoAssertion(f *Failure, body []string, scope RepoScope) {
	entries, msg := goEntries(body, f.Package, scope)
	if len(entries) > 0 {
		e := pickGoEntry(entries)
		f.TestLocation, msg = e.loc, e.msg
	}
	text := strings.Join(msg, "\n")
	first := firstLine(text)
	if first == "" {
		first = "test failed"
	}
	f.Got, f.Want = wantGot(text)
	f.Message = composeMessage(first, f.Got, f.Want)
	f.Attachments = logAttachment(logExcerpt(body))
}
