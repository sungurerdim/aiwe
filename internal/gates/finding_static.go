package gates

import (
	"bytes"
	"encoding/json"
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
