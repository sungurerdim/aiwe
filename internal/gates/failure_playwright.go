package gates

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var pwErrTypeRe = regexp.MustCompile(`^(\w*Error):`)

type pwReport struct {
	Config struct {
		RootDir string `json:"rootDir"`
	} `json:"config"`
	Suites []pwSuite `json:"suites"`
}

type pwSuite struct {
	Title  string    `json:"title"`
	File   string    `json:"file"`
	Specs  []pwSpec  `json:"specs"`
	Suites []pwSuite `json:"suites"`
}

type pwSpec struct {
	Title string   `json:"title"`
	File  string   `json:"file"`
	Line  int      `json:"line"`
	Tests []pwTest `json:"tests"`
}

type pwTest struct {
	ProjectName string     `json:"projectName"`
	Status      string     `json:"status"`
	Results     []pwResult `json:"results"`
}

type pwResult struct {
	Status      string     `json:"status"`
	Duration    float64    `json:"duration"` // milliseconds
	Error       *pwError   `json:"error"`
	Attachments []pwAttach `json:"attachments"`
	Stdout      []pwText   `json:"stdout"`
	Stderr      []pwText   `json:"stderr"`
}

type pwError struct {
	Message  string      `json:"message"`
	Stack    string      `json:"stack"`
	Location *pwLocation `json:"location"`
}

type pwLocation struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

type pwAttach struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Path        string `json:"path"`
}

type pwText struct {
	Text string `json:"text"`
}

// pwCtx is what the walk over the suites needs besides the suite itself.
type pwCtx struct {
	scope   RepoScope
	rootDir string // config.rootDir: the directory the spec files are relative to
}

// FailuresFromPlaywright reads the failing tests out of the JSON reporter of Playwright: a test
// whose status is "unexpected" is one record, built from its last attempt. error.location gives
// the test location, the trace and screenshot paths and the output of the attempt are kept as
// attachments. A report that is not valid JSON, or has no suites array, is an error, never an
// empty result.
func FailuresFromPlaywright(data []byte, scope RepoScope) ([]Failure, error) {
	var rep pwReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("gates: read the Playwright report: %w", err)
	}
	if rep.Suites == nil {
		return nil, errors.New("gates: the Playwright report has no suites array")
	}
	ctx := pwCtx{scope: scope, rootDir: rep.Config.RootDir}
	var out []Failure
	for _, s := range rep.Suites {
		out = pwWalk(out, s, nil, 0, ctx)
	}
	return out, nil
}

// pwWalk collects the failures of a suite and of its nested suites. The suite of a file at the
// top level is the file itself and adds no title.
func pwWalk(out []Failure, s pwSuite, titles []string, depth int, ctx pwCtx) []Failure {
	if depth > 0 || s.Title != s.File {
		titles = append(titles[:len(titles):len(titles)], s.Title)
	}
	for _, spec := range s.Specs {
		file := spec.File
		if file == "" {
			file = s.File
		}
		for _, t := range spec.Tests {
			if f, ok := pwFailure(file, append(titles[:len(titles):len(titles)], spec.Title), spec, t, ctx); ok {
				out = append(out, f)
			}
		}
	}
	for _, sub := range s.Suites {
		out = pwWalk(out, sub, titles, depth+1, ctx)
	}
	return out
}

func pwFailure(file string, titles []string, spec pwSpec, t pwTest, ctx pwCtx) (Failure, bool) {
	if t.Status != "unexpected" || len(t.Results) == 0 {
		return Failure{}, false
	}
	res := t.Results[len(t.Results)-1]
	id := strings.Join(append([]string{file}, titles...), " > ")
	if t.ProjectName != "" {
		id += " [" + t.ProjectName + "]"
	}
	var msg, stack string
	if res.Error != nil {
		msg, stack = cleanText(res.Error.Message), cleanText(res.Error.Stack)
	}
	f := newFailure(id, pwKind(res.Status, msg))
	f.DurationMs = durationMs(res.Duration / 1000)
	f.TestLocation = pwTestLocation(res.Error, file, spec.Line, ctx)
	frames, innermostFirst := stackFrames(stack)
	_, f.SourceLocation, f.Frames = ctx.scope.locate(frames, innermostFirst)
	f.Got, f.Want = wantGot(msg)
	first := firstLine(msg)
	if first == "" {
		first = "test " + res.Status
	}
	f.Message = composeMessage(first, f.Got, f.Want)
	f.Attachments = pwAttachments(res)
	f.Output = ownOutput(pwOutputLines(res))
	return f, true
}

// pwKind names the kind from the attempt status and the error message.
func pwKind(status, msg string) string {
	first := firstLine(msg)
	switch {
	case status == "timedOut":
		return FailureTimeout
	case strings.Contains(first, "expect("):
		return FailureAssertion
	}
	if m := pwErrTypeRe.FindStringSubmatch(first); m != nil {
		return FailureExceptionPrefix + m[1]
	}
	return FailureExceptionPrefix + FailureUnknownType
}

// pwTestLocation is error.location, repository-relative, else the line of the spec. A spec file
// is relative to config.rootDir, not to the repository, so the fallback joins the two; with no
// rootDir in the report the location is left empty.
func pwTestLocation(e *pwError, file string, specLine int, ctx pwCtx) string {
	if e != nil && e.Location != nil {
		if rel, ok := ctx.scope.relPath(e.Location.File); ok {
			return formatLocation(rel, e.Location.Line)
		}
	}
	p := strings.ReplaceAll(file, `\`, "/")
	if !isAbsSlash(p) {
		if ctx.rootDir == "" {
			return ""
		}
		p = path.Join(strings.ReplaceAll(ctx.rootDir, `\`, "/"), p)
	}
	if rel, ok := ctx.scope.relPath(p); ok {
		return formatLocation(rel, specLine)
	}
	return ""
}

func pwOutputLines(res pwResult) []string {
	var lines []string
	for _, t := range append(append([]pwText{}, res.Stdout...), res.Stderr...) {
		lines = append(lines, strings.Split(cleanText(t.Text), "\n")...)
	}
	return lines
}

// pwAttachments keeps the attachments that are files (trace, screenshot, video) and a log excerpt
// of the output of the attempt.
func pwAttachments(res pwResult) []Attachment {
	var out []Attachment
	for _, a := range res.Attachments {
		if a.Path != "" {
			out = append(out, Attachment{Kind: pwAttachKind(a), Path: a.Path})
		}
	}
	return append(out, logAttachment(logExcerpt(pwOutputLines(res)))...)
}

func pwAttachKind(a pwAttach) string {
	name := strings.ToLower(a.Name)
	switch {
	case name == "trace" || strings.HasSuffix(strings.ToLower(a.Path), "trace.zip"):
		return "trace"
	case name == "screenshot" || strings.HasPrefix(a.ContentType, "image/"):
		return "screenshot"
	}
	return "file"
}
