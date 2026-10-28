package gates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
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
