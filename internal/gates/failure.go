package gates

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Failure kinds (SPEC L76). A raised exception has the kind FailureExceptionPrefix + its type;
// when the report names no type, the type is FailureUnknownType.
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
	FailureUnknownType     = "unknown"
)

const (
	failureMessageMax   = 240
	failureValueMax     = 120
	failureExcerptLines = 10
	failureExcerptMax   = 800
	failureFramesMax    = 200
	failureOutputLines  = 200  // lines of a test's own output kept in a record
	failureLineMax      = 1000 // runes of one kept output line
	failureRepoFrames   = 10   // repository frames kept in a record
)

// Attachment is one piece of evidence of a failure: the path of a Playwright trace or screenshot,
// or a log excerpt.
type Attachment struct {
	Kind string `json:"kind"` // "trace", "screenshot", "file" or "log"
	Path string `json:"path,omitempty"`
	Text string `json:"text,omitempty"`
}

// StackFrame is one stack frame inside a repository file.
type StackFrame struct {
	Location string `json:"location"` // file:line, repository relative
	Func     string `json:"func,omitempty"`
}

// Failure is the structured record of one failing test (SPEC L76), read from the report of the
// test framework and never from free output text. Its fields line up with a SARIF result (SPEC
// §7.9): locations, message and properties.evidence (Got and Want).
type Failure struct {
	Test           string `json:"test"`
	Package        string `json:"package,omitempty"` // the Go import path; empty where the report has none
	TestLocation   string `json:"testLocation,omitempty"`
	SourceLocation string `json:"sourceLocation,omitempty"` // deepest repository product frame
	Kind           string `json:"kind"`
	Message        string `json:"message"`
	Got            string `json:"got,omitempty"`
	Want           string `json:"want,omitempty"`
	// Rerun is the argument vector that runs only this test (SPEC L76). It is not shell text:
	// the id is one element whatever characters it holds.
	Rerun       []string     `json:"rerun"`
	DurationMs  int64        `json:"durationMs,omitempty"`
	Output      []string     `json:"output,omitempty"` // the test's own lines, the last failureOutputLines
	Frames      []StackFrame `json:"frames,omitempty"` // repository frames only, deepest first, at most 10
	Attachments []Attachment `json:"attachments,omitempty"`
}

var (
	ansiRe      = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	gotWantRe   = regexp.MustCompile(`(?i)\bgot:?\s+(.+?),?\s+want:?\s+(.+)$`)
	wantGotRe   = regexp.MustCompile(`(?i)\bexpected:?\s+(.+?),?\s+(?:but\s+)?(?:got|actual|received|was):?\s+(.+)$`)
	assertEqRe  = regexp.MustCompile(`^\s*(?:E\s+)?assert\s+(.+?)\s+==\s+(.+)$`)
	gotLabelRe  = regexp.MustCompile(`(?i)^\s*(?:got|actual|received|have)\s*:\s*(\S.*)$`)
	wantLabelRe = regexp.MustCompile(`(?i)^\s*(?:want|expected|exp)\s*:\s*(\S.*)$`)
)

// RerunCommand returns the argument vector that runs only the test with the given id (SPEC L76,
// DR-3): no shell is assumed, so the id needs no quoting and stays one element.
func RerunCommand(id string) []string {
	return []string{core.Product, "check", "verify", "--test", id}
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

// cutRunes cuts s to limit runes, marking the cut.
func cutRunes(s string, limit int) string {
	s = strings.ToValidUTF8(s, "�")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}

func shorten(s string, limit int) string { return cutRunes(strings.TrimSpace(s), limit) }

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

// ownOutput keeps the last failureOutputLines lines of a test's own output, the ones nearest the
// failure, without the blank lines that end it.
func ownOutput(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	lines = lines[max(0, end-failureOutputLines):end]
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = cutRunes(strings.TrimRight(l, " \t\r"), failureLineMax)
	}
	return out
}

// durationMs turns seconds into whole milliseconds; a value that is not a usable duration is 0.
func durationMs(seconds float64) int64 {
	if math.IsNaN(seconds) || seconds <= 0 || seconds > 1e9 {
		return 0
	}
	return int64(seconds * 1000)
}

// parseSeconds reads a decimal number of seconds, and gives 0 for text that is not one.
func parseSeconds(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
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

// gotWantOnLine reads "got X, want Y", "expected X but got Y" and "assert X == Y" from one line.
func gotWantOnLine(line string) (got, want string, ok bool) {
	if m := gotWantRe.FindStringSubmatch(line); m != nil {
		return shorten(m[1], failureValueMax), shorten(m[2], failureValueMax), true
	}
	if m := wantGotRe.FindStringSubmatch(line); m != nil {
		return shorten(m[2], failureValueMax), shorten(m[1], failureValueMax), true
	}
	if m := assertEqRe.FindStringSubmatch(line); m != nil {
		return shorten(m[1], failureValueMax), shorten(m[2], failureValueMax), true
	}
	return "", "", false
}

func labelValue(re *regexp.Regexp, line string) string {
	if m := re.FindStringSubmatch(line); m != nil {
		return shorten(m[1], failureValueMax)
	}
	return ""
}

// wantGot reads the actual and expected values out of a failure message: one line that holds
// both (gotWantOnLine), or the labelled lines "got:" / "want:" / "Expected:" / "Received:".
func wantGot(text string) (got, want string) {
	for _, line := range strings.Split(text, "\n") {
		if g, w, ok := gotWantOnLine(line); ok {
			return g, w
		}
		if got == "" {
			got = labelValue(gotLabelRe, line)
		}
		if want == "" {
			want = labelValue(wantLabelRe, line)
		}
	}
	return got, want
}

// composeMessage is the first line of a failure plus the actual and expected values that the
// line does not already hold, shortened. A side the report does not give is left out.
func composeMessage(first, got, want string) string {
	msg := shorten(first, failureMessageMax)
	var extra []string
	if got != "" && !strings.Contains(first, got) {
		extra = append(extra, "got "+got)
	}
	if want != "" && !strings.Contains(first, want) {
		extra = append(extra, "want "+want)
	}
	if len(extra) == 0 {
		return msg
	}
	return shorten(msg+" ("+strings.Join(extra, ", ")+")", failureMessageMax)
}
