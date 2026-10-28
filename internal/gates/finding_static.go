package gates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// StaticFinding is one lint, type or format finding in the shape of a test
// failure record (L76, SPEC §7.9, SPEC-contracts §8.5): file:line:col, rule
// code, message and a one-line fix hint, read from a tool's machine output
// and never from its text.
//
// Shared shape: the failure record of #608 (failure.go) carries the same
// fields File, Line, Col, Rule, Message and Fix for a build or compile error.
// The wiring unit (#594) unifies the two types; until then this type stands
// alone and depends on nothing in failure.go.
//
// Line and Col are 0 when the tool gives no position (a gofmt file entry has
// no line). Level is "error", "warning" or "note".
type StaticFinding struct {
	Tool    string `json:"tool"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Rule    string `json:"rule"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

// maxStaticMessage bounds a message in runes; only its first line is kept.
const maxStaticMessage = 300

const (
	levelError   = "error"
	levelWarning = "warning"
	levelNote    = "note"
)

// StaticMapping is a profile row that maps one tool's JSON output to the
// StaticFinding fields. Each field below is a dotted path ("location.row")
// looked up in the finding object first and then in the enclosing element, so
// ESLint can read the file from the file entry and the line from the message.
type StaticMapping struct {
	Tool string
	// Items is the path of the list of findings inside each top-level
	// element; empty means each element is itself a finding.
	Items   string
	File    string
	Line    string
	Col     string
	Rule    string
	Message string
	Level   string
	Fix     string
	// Levels maps a raw level value ("2") to error, warning or note; a raw
	// string that is already one of the three needs no entry.
	Levels       map[string]string
	DefaultLevel string
	// FixHint is used when the tool gives no fix text; FixWhenSet is used
	// when the Fix field holds a non-string value, such as an eslint fix
	// object, which means the tool can apply the fix itself.
	FixHint    string
	FixWhenSet string
}

// ESLintMapping is the profile row for `eslint --format json`.
func ESLintMapping() StaticMapping {
	return StaticMapping{
		Tool: "eslint", Items: "messages", File: "filePath", Line: "line",
		Col: "column", Rule: "ruleId", Message: "message", Level: "severity",
		Fix: "fix", Levels: map[string]string{"2": levelError, "1": levelWarning},
		DefaultLevel: levelError, FixHint: "fix the reported issue",
		FixWhenSet: "run eslint --fix",
	}
}

// RuffMapping is the profile row for `ruff check --output-format json`.
func RuffMapping() StaticMapping {
	return StaticMapping{
		Tool: "ruff", File: "filename", Line: "location.row",
		Col: "location.column", Rule: "code", Message: "message",
		Fix: "fix.message", DefaultLevel: levelError,
		FixHint: "fix the reported issue; run ruff check --fix if the rule is fixable",
	}
}

// MypyMapping is the profile row for `mypy --output json` (one object per line).
func MypyMapping() StaticMapping {
	return StaticMapping{
		Tool: "mypy", File: "file", Line: "line", Col: "column", Rule: "code",
		Message: "message", Level: "severity", DefaultLevel: levelError,
		FixHint: "fix the reported type error",
	}
}

// ParseMappedJSON reads JSON tool output (one array, one object, or one value
// per line) into findings through a profile row. A mapping without a File
// path is refused. An element that is not an object is skipped.
func ParseMappedJSON(data []byte, m StaticMapping) ([]StaticFinding, error) {
	if m.File == "" {
		return nil, errors.New("static mapping has no file field")
	}
	var out []StaticFinding
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var v any
		if err := dec.Decode(&v); err == io.EOF {
			return out, nil
		} else if err != nil {
			return nil, fmt.Errorf("%s output: %w", m.Tool, err)
		}
		elems, _ := v.([]any)
		if _, isObj := v.(map[string]any); isObj {
			elems = []any{v}
		}
		for _, e := range elems {
			out = appendMapped(out, e, m)
		}
	}
}

func appendMapped(out []StaticFinding, e any, m StaticMapping) []StaticFinding {
	obj, ok := e.(map[string]any)
	if !ok {
		return out
	}
	if m.Items == "" {
		return appendIfFinding(out, mapFinding(obj, nil, m))
	}
	items, _ := lookupPath(obj, m.Items).([]any)
	for _, it := range items {
		if inner, ok := it.(map[string]any); ok {
			out = appendIfFinding(out, mapFinding(inner, obj, m))
		}
	}
	return out
}

func appendIfFinding(out []StaticFinding, f StaticFinding) []StaticFinding {
	if f.Rule == "" && f.Message == "" {
		return out
	}
	return append(out, f)
}

func mapFinding(in, outer map[string]any, m StaticMapping) StaticFinding {
	get := func(path string) any {
		if path == "" {
			return nil
		}
		if v := lookupPath(in, path); v != nil {
			return v
		}
		return lookupPath(outer, path)
	}
	f := StaticFinding{
		Tool:    m.Tool,
		File:    anyString(get(m.File)),
		Line:    anyInt(get(m.Line)),
		Col:     anyInt(get(m.Col)),
		Rule:    anyString(get(m.Rule)),
		Message: shortMessage(anyString(get(m.Message))),
		Level:   mappedLevel(anyString(get(m.Level)), m),
	}
	switch fix := get(m.Fix).(type) {
	case string:
		f.Fix = fix
	case nil:
	default:
		f.Fix = m.FixWhenSet
	}
	if strings.TrimSpace(f.Fix) == "" {
		f.Fix = m.FixHint
	}
	return f
}

func mappedLevel(raw string, m StaticMapping) string {
	if l, ok := m.Levels[raw]; ok {
		return l
	}
	switch l := strings.ToLower(raw); l {
	case levelError, levelWarning, levelNote:
		return l
	}
	if m.DefaultLevel != "" {
		return m.DefaultLevel
	}
	return levelWarning
}

func lookupPath(obj map[string]any, path string) any {
	if obj == nil || path == "" {
		return nil
	}
	var cur any = obj
	for _, key := range strings.Split(path, ".") {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = next[key]
	}
	return cur
}

func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

// anyInt reads a JSON number or numeric string as a position; anything else,
// a negative number or one beyond 2^31 is 0 ("no position").
func anyInt(v any) int {
	switch x := v.(type) {
	case float64:
		if x >= 0 && x < 1<<31 {
			return int(x)
		}
	case string:
		if n, err := strconv.Atoi(x); err == nil && n >= 0 && n < 1<<31 {
			return n
		}
	}
	return 0
}

// shortMessage keeps the first line of a message, cut to maxStaticMessage runes.
func shortMessage(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if r := []rune(s); len(r) > maxStaticMessage {
		s = string(r[:maxStaticMessage])
	}
	return s
}

// ParseGofmtList reads `gofmt -l` output: one unformatted file per line, one
// finding per file. A gofmt file entry has no line, so Line and Col are 0.
func ParseGofmtList(data []byte) []StaticFinding {
	var out []StaticFinding
	for _, line := range strings.Split(string(data), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		out = append(out, StaticFinding{
			Tool: "gofmt", File: path, Rule: "gofmt", Level: levelError,
			Message: "file is not gofmt-formatted",
			Fix:     "run gofmt -w " + path,
		})
	}
	return out
}

// ParseGoVetJSON reads `go vet -json` output: one JSON object per package,
// {"<pkg>": {"<analyzer>": [{"posn": "file:line:col", "message": "..."}]}},
// with "# pkg" comment lines in between. An analyzer that failed to run
// ({"error": "..."}) becomes a finding with no position.
func ParseGoVetJSON(data []byte) ([]StaticFinding, error) {
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	var out []StaticFinding
	dec := json.NewDecoder(strings.NewReader(strings.Join(kept, "\n")))
	for {
		var pkgs map[string]map[string]json.RawMessage
		if err := dec.Decode(&pkgs); err == io.EOF {
			return out, nil
		} else if err != nil {
			return nil, fmt.Errorf("go vet output: %w", err)
		}
		for _, pkg := range sortedKeys(pkgs) {
			for _, analyzer := range sortedKeys(pkgs[pkg]) {
				fs, err := vetAnalyzer(analyzer, pkgs[pkg][analyzer])
				if err != nil {
					return nil, fmt.Errorf("go vet output: %s %s: %w", pkg, analyzer, err)
				}
				out = append(out, fs...)
			}
		}
	}
}

func vetAnalyzer(name string, raw json.RawMessage) ([]StaticFinding, error) {
	fix := "fix the reported " + name + " issue; go vet explains each analyzer"
	var diags []struct{ Posn, Message string }
	if err := json.Unmarshal(raw, &diags); err != nil {
		var fail struct{ Error string }
		if json.Unmarshal(raw, &fail) != nil {
			return nil, err
		}
		return []StaticFinding{{
			Tool: "go vet", Rule: name, Level: levelError,
			Message: shortMessage(fail.Error), Fix: fix,
		}}, nil
	}
	out := make([]StaticFinding, 0, len(diags))
	for _, d := range diags {
		file, line, col := splitPosn(d.Posn)
		out = append(out, StaticFinding{
			Tool: "go vet", File: file, Line: line, Col: col, Rule: name,
			Level: levelError, Message: shortMessage(d.Message), Fix: fix,
		})
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// splitPosn splits "file:line:col" from the right, so a Windows drive colon
// stays in the file name.
func splitPosn(p string) (file string, line, col int) {
	i := strings.LastIndex(p, ":")
	if i < 0 {
		return p, 0, 0
	}
	last, ok := posInt(p[i+1:])
	if !ok {
		return p, 0, 0
	}
	head := p[:i]
	if j := strings.LastIndex(head, ":"); j >= 0 {
		if n, ok := posInt(head[j+1:]); ok {
			return head[:j], n, last
		}
	}
	return head, last, 0
}

func posInt(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0 && n < 1<<31 && !strings.HasPrefix(s, "+")
}

// sarifLog is the part of SARIF 2.1.0 (SPEC §7.9) that a finding reads.
type sarifLog struct {
	Runs []struct {
		Tool struct {
			Driver struct{ Name string }
		}
		Results []sarifResult
	}
}

type sarifResult struct {
	RuleID  string
	Level   string
	Message struct{ Text string }
	Fixes   []struct {
		Description struct{ Text string }
	}
	Properties map[string]any
	Locations  []struct {
		PhysicalLocation struct {
			ArtifactLocation struct{ URI string }
			Region           struct{ StartLine, StartColumn int }
		}
	}
}

// ParseGolangciSARIF reads the SARIF 2.1.0 file of `golangci-lint run
// --output.sarif.path`. A result without a location keeps empty File and 0 for
// Line and Col; a missing level is "warning" as SARIF defines.
func ParseGolangciSARIF(data []byte) ([]StaticFinding, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, fmt.Errorf("golangci-lint SARIF: %w", err)
	}
	var out []StaticFinding
	for _, run := range log.Runs {
		tool := run.Tool.Driver.Name
		if tool == "" {
			tool = "golangci-lint"
		}
		for _, r := range run.Results {
			out = append(out, sarifFinding(tool, r))
		}
	}
	return out, nil
}

func sarifFinding(tool string, r sarifResult) StaticFinding {
	f := StaticFinding{
		Tool: tool, Rule: r.RuleID, Level: mappedLevel(r.Level, StaticMapping{}),
		Message: shortMessage(r.Message.Text),
	}
	if len(r.Locations) > 0 {
		loc := r.Locations[0].PhysicalLocation
		f.File = sarifPath(loc.ArtifactLocation.URI)
		f.Line, f.Col = clampPos(loc.Region.StartLine), clampPos(loc.Region.StartColumn)
	}
	f.Fix = anyString(r.Properties["fix"])
	if f.Fix == "" && len(r.Fixes) > 0 {
		f.Fix = shortMessage(r.Fixes[0].Description.Text)
	}
	if strings.TrimSpace(f.Fix) == "" {
		f.Fix = "fix the reported issue; golangci-lint run --fix applies fixable ones"
	}
	return f
}

func clampPos(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// sarifPath turns an artifact URI into a path: a file: URI loses its scheme
// (and the slash before a drive letter) and percent escapes are decoded.
func sarifPath(uri string) string {
	if strings.HasPrefix(uri, "file:") {
		u, err := url.Parse(uri)
		if err != nil {
			return uri
		}
		p := u.Path
		if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		return p
	}
	if p, err := url.PathUnescape(uri); err == nil {
		return p
	}
	return uri
}
