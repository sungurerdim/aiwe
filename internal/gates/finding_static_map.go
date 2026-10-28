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
	// ColOffset is added to a column the tool gave (not to a missing or
	// negative one): StaticFinding columns are 1-based, mypy's are 0-based.
	ColOffset int
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
		Message: "message", Level: "severity", Fix: "hint", DefaultLevel: levelError,
		// mypy's JSON column is 0-based (its text output prints column+1).
		ColOffset: 1, FixHint: "fix the reported type error",
	}
}

// ParseMappedJSON reads JSON tool output (one array, one object, or one value
// per line) into findings through a profile row. It fails closed (DR-6): a
// row without a File or Message path, empty input, a top-level scalar or
// null, an element that is not an object, an element with no list at Items,
// and an item whose File or Message path gives nothing are errors that name
// what is wrong. An empty array is a clean run.
func ParseMappedJSON(data []byte, m StaticMapping) ([]StaticFinding, error) {
	if m.File == "" || m.Message == "" {
		return nil, errors.New("static mapping needs a file field and a message field")
	}
	elems, err := mappedElements(data, m.Tool)
	if err != nil {
		return nil, err
	}
	var out []StaticFinding
	for i, e := range elems {
		fs, err := mapElement(e, m)
		if err != nil {
			return nil, fmt.Errorf("%s output element %d: %w", m.Tool, i, err)
		}
		out = append(out, fs...)
	}
	return out, nil
}

// mappedElements flattens the top-level values: an array gives its elements,
// an object is one element.
func mappedElements(data []byte, tool string) ([]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("%s output is empty (the tool wrote no JSON)", tool)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var elems []any
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			return elems, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s output: %w", tool, err)
		}
		switch x := v.(type) {
		case []any:
			elems = append(elems, x...)
		case map[string]any:
			elems = append(elems, x)
		default:
			return nil, fmt.Errorf("%s output: top-level value is %s, want an array or an object", tool, jsonKind(v))
		}
	}
}

func jsonKind(v any) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

func mapElement(e any, m StaticMapping) ([]StaticFinding, error) {
	obj, ok := e.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("is %s, want an object", jsonKind(e))
	}
	if m.Items == "" {
		f, err := mapFinding(obj, nil, m)
		return []StaticFinding{f}, err
	}
	items, ok := lookupPath(obj, m.Items).([]any)
	if !ok {
		return nil, fmt.Errorf("has no list at %q", m.Items)
	}
	out := make([]StaticFinding, 0, len(items))
	for i, it := range items {
		inner, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is %s, want an object", m.Items, i, jsonKind(it))
		}
		f, err := mapFinding(inner, obj, m)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", m.Items, i, err)
		}
		out = append(out, f)
	}
	return out, nil
}

func mapFinding(in, outer map[string]any, m StaticMapping) (StaticFinding, error) {
	get := func(path string) any {
		if path == "" {
			return nil
		}
		if v := lookupPath(in, path); v != nil {
			return v
		}
		return lookupPath(outer, path)
	}
	col, colOK := anyPos(get(m.Col))
	if colOK {
		col += m.ColOffset
	}
	f := StaticFinding{
		Tool:    m.Tool,
		File:    anyString(get(m.File)),
		Line:    anyInt(get(m.Line)),
		Col:     col,
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
	if f.File == "" {
		return f, fmt.Errorf("mapped path %q (file) is missing or empty", m.File)
	}
	if f.Message == "" {
		return f, fmt.Errorf("mapped path %q (message) is missing or empty", m.Message)
	}
	return f, nil
}

func mappedLevel(raw string, m StaticMapping) string {
	if l, ok := m.Levels[raw]; ok {
		return l
	}
	switch l := strings.ToLower(raw); l {
	case levelError, levelWarning, levelNote:
		return l
	case "none": // SARIF: below note
		return levelNote
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

// anyPos reads a JSON number or numeric string as a position; anything else,
// a negative number or one beyond 2^31 is not a position (false).
func anyPos(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		if x >= 0 && x < 1<<31 {
			return int(x), true
		}
	case string:
		if n, err := strconv.Atoi(x); err == nil && n >= 0 && n < 1<<31 {
			return n, true
		}
	}
	return 0, false
}

// anyInt is anyPos with 0 ("no position") for a value that is not one.
func anyInt(v any) int {
	n, _ := anyPos(v)
	return n
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
