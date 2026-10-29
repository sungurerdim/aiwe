package gates

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/ledger"
)

const (
	// tailLines is how many output lines the tail file of a run that is not green keeps
	// (SPEC-contracts §8.5).
	tailLines = 50
	// slowestTests is how many of a run's slowest tests its record keeps (L67).
	slowestTests = 10
	// outputLogMax bounds the bytes of a step's combined output kept for its tail and red class.
	outputLogMax = 1 << 20
	// checkFormatVersion is the ledger schema version a check record is written in (SPEC §8.5).
	checkFormatVersion = 1
	// recordNone is the value of a record identity that is absent (SPEC §8.5).
	recordNone = "none"
	// findingKind is the kind of an entry that holds a lint, type or format finding.
	findingKind = "finding"
)

// CheckEntry is one entry of failures[] in a check record (L76, SPEC-contracts §8.5): a failing
// test read by a test parser (#608) or a lint, type or format finding read by a static reader
// (#609), in one shape. TestLocation is the file:line where the test failed, or the file:line:col
// of a finding; SourceLocation is the deepest product frame of a test, or the finding's own place.
type CheckEntry struct {
	Test           string       `json:"test,omitempty"`
	Tool           string       `json:"tool,omitempty"`
	TestLocation   string       `json:"testLocation"`
	SourceLocation string       `json:"sourceLocation,omitempty"`
	Kind           string       `json:"kind"`
	Rule           string       `json:"rule,omitempty"`
	Level          string       `json:"level,omitempty"`
	Message        string       `json:"message"`
	Fix            string       `json:"fix,omitempty"`
	Rerun          []string     `json:"rerun,omitempty"`
	Attachments    []Attachment `json:"attachments,omitempty"`
}

// failureEntry is the entry of one failing test.
func failureEntry(f Failure) CheckEntry {
	return CheckEntry{Test: f.Test, TestLocation: f.TestLocation, SourceLocation: f.SourceLocation, Kind: f.Kind,
		Message: f.Message, Rerun: slices.Clone(f.Rerun), Attachments: slices.Clone(f.Attachments)}
}

// findingEntry is the entry of one static finding; its position is file, file:line or file:line:col,
// as much as the tool gave.
func findingEntry(s StaticFinding) CheckEntry {
	loc := s.File
	if s.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, s.Line)
		if s.Col > 0 {
			loc = fmt.Sprintf("%s:%d", loc, s.Col)
		}
	}
	return CheckEntry{Tool: s.Tool, TestLocation: loc, SourceLocation: loc, Kind: findingKind, Rule: s.Rule,
		Level: s.Level, Message: s.Message, Fix: s.Fix}
}

// CheckRecord is what one step leaves after it runs: its ledger check record, the failures[] its
// parsed output holds, and Tail, the last tailLines lines of its output. Tail is set for every run
// that is not green, parsed or not, until the ledger record carries failures[] (#702); the sink
// writes it to a file and names that file in the record's tailPath (SPEC-contracts §8.5).
type CheckRecord struct {
	Check    ledger.Check
	Failures []CheckEntry
	Tail     []string
}

// RunFacts are the facts every record of one run carries, measured once before its steps run. An
// empty value was not measured and reads unknown in the record; an empty Commit reads uncommitted.
// Module is the Go module path the test parsers place frames by.
type RunFacts struct {
	Key       string
	Commit    string
	Tree      string
	Toolchain string
	OS        string
	Module    string
}

// RecordSink writes one check record, as the ledger of the state root does.
type RecordSink interface {
	WriteCheck(ctx context.Context, rec CheckRecord) error
}

// stepOutput is what a step left for its record: the directory and argv it ran with, its stdout,
// its stdout and stderr together, whether it started and was stopped, and how long it ran.
type stepOutput struct {
	dir      string
	argv     []string
	stdout   []byte
	all      []byte
	started  bool
	killed   bool
	duration time.Duration
}

// outputLog keeps the last outputLogMax bytes of a step's stdout and stderr together. The two
// streams write from two goroutines; mu keeps each write whole.
type outputLog struct {
	mu  sync.Mutex
	buf []byte
}

// Write keeps p, and drops the oldest bytes once the log holds twice its bound.
func (l *outputLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if len(l.buf) > 2*outputLogMax {
		l.buf = slices.Clone(l.buf[len(l.buf)-outputLogMax:])
	}
	return len(p), nil
}

// Bytes returns a copy of the last outputLogMax bytes the log holds.
func (l *outputLog) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.buf[max(0, len(l.buf)-outputLogMax):])
}

// record hands the record of one step to the runner's sink. A record that is not written keeps the
// step's result, measured from its exit and output, and is noted in its detail; a runner with no
// sink writes none.
func (r *Runner) record(ctx context.Context, report StepReport, ran stepOutput) StepReport {
	if r.Records == nil {
		return report
	}
	if err := r.Records.WriteCheck(ctx, buildCheckRecord(r.Facts, report, ran, time.Now())); err != nil {
		report.Detail = joinDetail(report.Detail, "check record not written: "+err.Error())
	}
	return report
}

// buildCheckRecord builds the record of one step that ended at at. A green step carries no failure
// and no tail. A red one names the class of the rule table (#593) when a rule matched. A step that is
// not green carries the entries its output parsers read and always keeps its tail: the ledger record
// carries no failures[] until #702, so the tail is its only diagnostic.
func buildCheckRecord(facts RunFacts, report StepReport, ran stepOutput, at time.Time) CheckRecord {
	c := ledger.Check{Header: checkHeader(at), Key: cmp.Or(facts.Key, recordNone), StepID: report.ID,
		Commit: cmp.Or(facts.Commit, ledger.CommitUncommitted), Killed: ran.killed,
		TreeHash: knownText(facts.Tree), Toolchain: knownText(facts.Toolchain), OS: knownText(facts.OS)}
	if ran.started {
		c.DurationMS = core.Measured[int64]{Value: ran.duration.Milliseconds(), Known: true}
	}
	if report.ExitCode >= 0 {
		c.ExitCode = core.Measured[int64]{Value: int64(report.ExitCode), Known: true}
	}
	c.Counts, c.Slowest = testCounts(ran.stdout)
	if report.Result == ResultGreen {
		return CheckRecord{Check: c}
	}
	lines := outputLines(ran.all)
	if report.Result == ResultRed {
		v := ClassifyRed(RedRun{Step: report.ID, Exit: report.ExitCode, Output: lines})
		c.RedClass = ledger.Field[string]{Value: string(v.Class), Known: v.Class != RedUnknown}
	}
	return CheckRecord{Check: c, Failures: parsedEntries(ran, RepoScope{Module: facts.Module, Root: ran.dir}),
		Tail: lastLines(lines, tailLines)}
}

// checkHeader is the header of a check record at at; the record names no goal, unit or run, so each
// reads none (SPEC §8.5). Append sets prev.
func checkHeader(at time.Time) ledger.Header {
	return ledger.Header{FormatVersion: checkFormatVersion, Kind: ledger.KindCheck, At: at.UTC(),
		RepoID: recordNone, Goal: recordNone, Unit: recordNone, RunID: recordNone}
}

// knownText is a measured text field; an empty value was not measured (DR-6).
func knownText(s string) ledger.Field[string] {
	return ledger.Field[string]{Value: s, Known: s != ""}
}

// parsedEntries returns the entries the parsers read from a step's output: its stdout first, then
// its stdout and stderr together, where go vet writes. The readers chosen by the tool name read
// stdout only, because they take any line for a finding.
func parsedEntries(ran stepOutput, scope RepoScope) []CheckEntry {
	if entries := readEntries(ran.stdout, ran.argv, scope); len(entries) > 0 {
		return entries
	}
	return readEntries(ran.all, nil, scope)
}

// readEntries returns the entries of the first parser that understands data and reads at least one
// entry from it: the test parsers first, then the static readers. Output that no parser reads an
// entry from has none.
func readEntries(data []byte, argv []string, scope RepoScope) []CheckEntry {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	for _, read := range testReaders(scope) {
		if fs, err := read(data); err == nil && len(fs) > 0 {
			return mapEntries(fs, failureEntry)
		}
	}
	for _, read := range staticReaders(argv) {
		if fs, err := read(data); err == nil && len(fs) > 0 {
			return mapEntries(fs, findingEntry)
		}
	}
	return nil
}

// testReaders are the failure parsers of #608, each refusing a report that is not its own.
func testReaders(scope RepoScope) []func([]byte) ([]Failure, error) {
	return []func([]byte) ([]Failure, error){
		func(d []byte) ([]Failure, error) { return FailuresFromGoJSON(d, scope) },
		func(d []byte) ([]Failure, error) { return FailuresFromPlaywright(d, scope) },
		func(d []byte) ([]Failure, error) { return FailuresFromJUnit(d, scope) },
	}
}

// staticReaders are the static readers of #609: the ones that refuse output that is not their own,
// then the one the step's tool name picks.
func staticReaders(argv []string) []func([]byte) ([]StaticFinding, error) {
	readers := []func([]byte) ([]StaticFinding, error){ParseGolangciSARIF, ParseGoVetJSON, ParseTscText}
	mapped := func(m StaticMapping) func([]byte) ([]StaticFinding, error) {
		return func(d []byte) ([]StaticFinding, error) { return ParseMappedJSON(d, m) }
	}
	switch toolName(argv) {
	case "gofmt":
		readers = append(readers, func(d []byte) ([]StaticFinding, error) { return ParseGofmtList(d), nil })
	case "eslint":
		readers = append(readers, mapped(ESLintMapping()))
	case "ruff":
		readers = append(readers, mapped(RuffMapping()))
	case "mypy":
		readers = append(readers, mapped(MypyMapping()))
	}
	return readers
}

// toolName is the lower-case base name of the program argv runs, without .exe.
func toolName(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	base := path.Base(strings.ReplaceAll(argv[0], `\`, "/"))
	return strings.TrimSuffix(strings.ToLower(base), ".exe")
}

// mapEntries maps each value of in to its entry.
func mapEntries[T any](in []T, entry func(T) CheckEntry) []CheckEntry {
	out := make([]CheckEntry, 0, len(in))
	for _, v := range in {
		out = append(out, entry(v))
	}
	return out
}

// testCounts reads the tests of a `go test -json` stream: the count of tests by result (pass, fail,
// skip) and the slowest tests that passed or failed, slowest first. A stream with no test result
// leaves both unknown (DR-6).
func testCounts(stdout []byte) (ledger.Field[map[string]int64], ledger.Field[[]ledger.Timed]) {
	counts := map[string]int64{}
	timed := []ledger.Timed{}
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		ev, ok := decodeGoEvent(line)
		if !ok || ev.Test == "" || !slices.Contains([]string{"pass", "fail", "skip"}, ev.Action) {
			continue
		}
		counts[ev.Action]++
		if ev.Action != "skip" {
			timed = append(timed, ledger.Timed{ID: ev.Package + "." + ev.Test, DurationMS: durationMs(ev.Elapsed)})
		}
	}
	if len(counts) == 0 {
		return ledger.Field[map[string]int64]{}, ledger.Field[[]ledger.Timed]{}
	}
	slices.SortStableFunc(timed, func(a, b ledger.Timed) int {
		return cmp.Or(cmp.Compare(b.DurationMS, a.DurationMS), cmp.Compare(a.ID, b.ID))
	})
	return ledger.Field[map[string]int64]{Value: counts, Known: true},
		ledger.Field[[]ledger.Timed]{Value: timed[:min(len(timed), slowestTests)], Known: true}
}

// outputLines splits output into lines without their line ends; empty output has none.
func outputLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// lastLines returns a copy of the last n of lines.
func lastLines(lines []string, n int) []string {
	return slices.Clone(lines[max(0, len(lines)-n):])
}

// tailNameChars are the characters of a step id that a tail file name keeps.
const tailNameChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"

// tailName is the n-th candidate file name of the tail of step at at: the step id with each
// character outside tailNameChars replaced by an underscore, so a step id never leads out of the tail
// directory, then the instant and n, so two tails of one step in one clock tick get two names.
func tailName(step string, at time.Time, n int) string {
	safe := strings.Map(func(r rune) rune {
		if strings.ContainsRune(tailNameChars, r) {
			return r
		}
		return '_'
	}, step)
	return fmt.Sprintf("%s-%d-%d.log", safe, at.UnixNano(), n)
}

// tailText is the content of a tail file: one line per line, each ended by a newline.
func tailText(lines []string) []byte {
	return []byte(strings.Join(lines, "\n") + "\n")
}

// toolchainText is the toolchain probe's output on one line.
func toolchainText(out string) string {
	return strings.Join(strings.Fields(out), " ")
}

// goModulePath is the module path a go.mod declares, or "" when it declares none.
func goModulePath(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`)
		}
	}
	return ""
}
