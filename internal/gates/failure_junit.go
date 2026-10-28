package gates

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
)

const jsPathPat = `((?:[A-Za-z]:)?[^:()]*[./\\][^:()]*)`

var (
	pyFrameRe     = regexp.MustCompile(`^\s*File "(.+)", line (\d+)(?:, in (.+))?$`)
	pytestFrameRe = regexp.MustCompile(`^((?:[A-Za-z]:)?[^:\s][^:]*\.py):(\d+): (.+)$`)
	jsFrameRe     = regexp.MustCompile(`^\s*at (?:(.+?) \()?` + jsPathPat + `:(\d+)(?::\d+)?\)?\s*$`)
	vitestFrameRe = regexp.MustCompile(`^\s*❯\s+(?:[\w$.<>]+\s+)?` + jsPathPat + `:(\d+)(?::\d+)?\s*$`)
	excTypeRe     = regexp.MustCompile(`^((?:[A-Za-z_][\w.]*)?(?:Error|Exception))\b`)
	timeoutRe     = regexp.MustCompile(`(?i)(^|: )(timeout\b|timed out)|timeout of \d+\s*ms exceeded|exceeded.*timeout`)
)

type junitSuite struct {
	Suites []junitSuite `xml:"testsuite"`
	Cases  []junitCase  `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	File      string        `xml:"file,attr"`
	Line      string        `xml:"line,attr"`
	Failure   *junitProblem `xml:"failure"`
	Error     *junitProblem `xml:"error"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

// FailuresFromJUnit reads the failing tests out of a JUnit XML report (pytest, jest, vitest): a
// testcase with a failure or an error element is one record. A report that is not well formed
// XML is an error, never an empty result.
func FailuresFromJUnit(data []byte, scope RepoScope) ([]Failure, error) {
	var root junitSuite
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("gates: read the JUnit report: %w", err)
	}
	var out []Failure
	var walk func(s junitSuite)
	walk = func(s junitSuite) {
		for _, c := range s.Cases {
			if f, ok := junitFailure(c, scope); ok {
				out = append(out, f)
			}
		}
		for _, sub := range s.Suites {
			walk(sub)
		}
	}
	walk(root)
	return out, nil
}

func junitID(c junitCase) string {
	if c.Classname == "" || strings.HasPrefix(c.Name, c.Classname) {
		return c.Name
	}
	return c.Classname + "." + c.Name
}

func junitFailure(c junitCase, scope RepoScope) (Failure, bool) {
	p, elem := c.Failure, "failure"
	if p == nil {
		p, elem = c.Error, "error"
	}
	if p == nil {
		return Failure{}, false
	}
	msg, body := cleanText(p.Message), cleanText(p.Body)
	f := newFailure(junitID(c), junitKind(elem, p.Type, msg, body))
	frames, innermostFirst := stackFrames(body)
	f.TestLocation, f.SourceLocation = scope.locate(frames, innermostFirst)
	if rel, ok := scope.relPath(c.File); ok && f.TestLocation == "" {
		f.TestLocation = formatLocation(rel, atoi(c.Line))
	}
	first := firstLine(msg)
	if first == "" {
		first = firstLine(body)
	}
	f.Got, f.Want = wantGot(msg + "\n" + body)
	f.Message = composeMessage(first, f.Got, f.Want)
	f.Attachments = logAttachment(logExcerpt(strings.Split(body, "\n")))
	return f, true
}

// junitKind names the kind of a failure from the framework's own words: the element, its type
// attribute and its message.
func junitKind(elem, typ, msg, body string) string {
	head := msg + "\n" + firstLine(body)
	low := strings.ToLower(typ + "\n" + head)
	switch {
	case strings.Contains(strings.ToLower(typ), "timeout") || timeoutRe.MatchString(firstLine(head)):
		return FailureTimeout
	case containsAny(low, "importerror", "modulenotfounderror", "cannot find module"):
		return FailureImport
	case containsAny(low, "collection failure", "error collecting"):
		return FailureCollection
	case containsAny(low, "failed on setup", "test setup failure"):
		return FailureSetup
	}
	t := strings.TrimSpace(typ)
	if t == "" {
		if m := excTypeRe.FindStringSubmatch(firstLine(head)); m != nil {
			t = m[1]
		}
	}
	if elem == "failure" && (t == "" || strings.Contains(strings.ToLower(t), "assert") || strings.Contains(head, "expect(")) {
		return FailureAssertion
	}
	if t == "" {
		t = "Error"
	}
	return FailureExceptionPrefix + t
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// frameOf reads one stack line: py tells a Python frame from a JavaScript one.
func frameOf(line string) (fr frame, py, ok bool) {
	if m := pyFrameRe.FindStringSubmatch(line); m != nil {
		return frame{Func: m[3], Path: m[1], Line: atoi(m[2])}, true, true
	}
	if m := pytestFrameRe.FindStringSubmatch(line); m != nil {
		return frame{Func: m[3], Path: m[1], Line: atoi(m[2])}, true, true
	}
	if m := jsFrameRe.FindStringSubmatch(line); m != nil {
		return frame{Func: m[1], Path: m[2], Line: atoi(m[3])}, false, true
	}
	if m := vitestFrameRe.FindStringSubmatch(line); m != nil {
		return frame{Path: m[1], Line: atoi(m[2])}, false, true
	}
	return frame{}, false, false
}

// stackFrames reads the frames of a Python or JavaScript failure body. A Python traceback lists
// the outermost frame first, a JavaScript stack the innermost first.
func stackFrames(text string) (frames []frame, innermostFirst bool) {
	var py, js []frame
	for _, line := range strings.Split(text, "\n") {
		fr, isPy, ok := frameOf(line)
		switch {
		case !ok || len(py)+len(js) >= failureFramesMax:
		case isPy:
			py = append(py, fr)
		default:
			js = append(js, fr)
		}
	}
	if len(py) > 0 {
		return py, false
	}
	return js, true
}
