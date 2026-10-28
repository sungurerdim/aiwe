package gates

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// anyFix in a want row means "a non-empty fix hint", for hints the code
// supplies as a default and the test does not pin word for word.
const anyFix = "\x00any"

type parseFunc func([]byte) ([]StaticFinding, error)

// findingCase is one table row: catches names the defect the row would expose.
type findingCase struct {
	name, catches, in string
	want              []StaticFinding
}

// errCase is a malformed input that must fail closed with an error that names
// what is wrong (DR-6); msg is a part of that error text.
type errCase struct {
	name, catches, in, msg string
	parse                  parseFunc
}

func sf(tool, file string, line, col int, rule, level, msg, fix string) StaticFinding {
	return StaticFinding{Tool: tool, File: file, Line: line, Col: col,
		Rule: rule, Level: level, Message: msg, Fix: fix}
}

func sameFinding(got, want StaticFinding) bool {
	if want.Fix == anyFix {
		if got.Fix == "" {
			return false
		}
		got.Fix = anyFix
	}
	return got == want
}

func runFindingCases(t *testing.T, parse parseFunc, cases []findingCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parse([]byte(c.in))
			if err != nil {
				t.Fatalf("unexpected error: %v\ncatches: %s", err, c.catches)
			}
			if len(got) != len(c.want) {
				t.Fatalf("findings = %d, want %d\ncatches: %s\ngot: %+v", len(got), len(c.want), c.catches, got)
			}
			for i := range got {
				if !sameFinding(got[i], c.want[i]) {
					t.Errorf("finding %d\ncatches: %s\n got: %+v\nwant: %+v", i, c.catches, got[i], c.want[i])
				}
			}
		})
	}
}

func runErrCases(t *testing.T, cases []errCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.parse([]byte(c.in))
			if err == nil {
				t.Fatalf("no error, findings %+v\ncatches: %s", got, c.catches)
			}
			if len(got) != 0 {
				t.Errorf("error %v came with findings %+v", err, got)
			}
			if !strings.Contains(err.Error(), c.msg) {
				t.Errorf("error %q does not name %q\ncatches: %s", err, c.msg, c.catches)
			}
		})
	}
}

const golangciSARIF = `{
  "version": "2.1.0",
  "runs": [{
    "tool": {"driver": {"name": "golangci-lint"}},
    "results": [
      {"ruleId": "errcheck", "level": "error",
       "message": {"text": "Error return value of os.Remove is not checked"},
       "locations": [{"physicalLocation": {
         "artifactLocation": {"uri": "internal/gates/runner.go"},
         "region": {"startLine": 42, "startColumn": 7}}}]},
      {"ruleId": "unused",
       "message": {"text": "func helper is unused"},
       "properties": {"fix": "delete helper or call it"},
       "locations": [{"physicalLocation": {
         "artifactLocation": {"uri": "file:///work/a%20b/x.go"},
         "region": {"startLine": 3}}}]}
    ]
  }]
}`

const sarifFixesLog = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"golangci-lint v2"}},
"results":[{"ruleId":"gosec","level":"none","message":{"text":"first line\nsecond line"},
"fixes":[{"description":{"text":"use a constant"}}],
"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/a%20b.go"},
"region":{"startLine":9,"startColumn":2}}}]}]}]}`

func TestStaticFindingFromGolangciSARIF(t *testing.T) {
	const gl = "golangci-lint"
	runFindingCases(t, ParseGolangciSARIF, []findingCase{
		{"location rule message fix", "a location, rule, level or message read from the wrong field", golangciSARIF, []StaticFinding{
			sf(gl, "internal/gates/runner.go", 42, 7, "errcheck", "error",
				"Error return value of os.Remove is not checked", anyFix),
			sf(gl, "/work/a b/x.go", 3, 0, "unused", "warning", "func helper is unused", "delete helper or call it"),
		}},
		{"fixes fallback none level escape", "the fixes[0] fallback, level none below note, a percent escape in a relative URI, a multi-line message", sarifFixesLog, []StaticFinding{
			sf("golangci-lint v2", "pkg/a b.go", 9, 2, "gosec", "note", "first line", "use a constant"),
		}},
		{"no location no level", "a result without a location or a level", `{"version":"2.1.0","runs":[{"results":[{"ruleId":"r","message":{"text":"m"}}]}]}`, []StaticFinding{
			sf(gl, "", 0, 0, "r", "warning", "m", anyFix),
		}},
		{"clean run", "a valid log with no results read as an error", `{"version":"2.1.0","runs":[{"results":[]}]}`, nil},
	})
}

func TestStaticFindingRejectsMalformedSARIF(t *testing.T) {
	p := ParseGolangciSARIF
	runErrCases(t, []errCase{
		{"empty", "an empty file (the tool did not run) read as clean", "", "empty", p},
		{"blanks", "a whitespace file read as clean", "  \n", "empty", p},
		{"empty object", "{} read as clean", "{}", "not a SARIF 2.1.0 log", p},
		{"null", "null read as clean", "null", "not a SARIF 2.1.0 log", p},
		{"other object", "a non-SARIF object read as clean", `{"foo":1}`, "not a SARIF 2.1.0 log", p},
		{"wrong version", "a SARIF version other than 2.1.0 accepted", `{"version":"2.0.0","runs":[]}`, "not a SARIF 2.1.0 log", p},
		{"short version", "version 2.1 accepted though only the exact 2.1.0 is read", `{"version":"2.1","runs":[]}`, "not a SARIF 2.1.0 log", p},
		{"patch version", "version 2.1.1 accepted though only the exact 2.1.0 is read", `{"version":"2.1.1","runs":[]}`, "not a SARIF 2.1.0 log", p},
		{"pre-release version", "version 2.1.0-rc.1 accepted though only the exact 2.1.0 is read", `{"version":"2.1.0-rc.1","runs":[]}`, "not a SARIF 2.1.0 log", p},
		{"runs absent", "a 2.1.0 log with no runs key read as clean", `{"version":"2.1.0"}`, "not a SARIF 2.1.0 log", p},
		{"runs empty", "a 2.1.0 log with an empty runs array read as clean", `{"version":"2.1.0","runs":[]}`, "not a SARIF 2.1.0 log", p},
		{"run empty", "a run with no results key read as clean", `{"version":"2.1.0","runs":[{}]}`, "no results array", p},
		{"results null", "a run with results null read as clean", `{"version":"2.1.0","runs":[{"results":null}]}`, "no results array", p},
		{"truncated", "truncated JSON accepted", `{"runs": [`, "golangci-lint SARIF:", p},
		{"scalar run", "a scalar run item skipped", `{"version":"2.1.0","runs":[1]}`, "golangci-lint SARIF:", p},
		{"no message", "a result without message.text dropped", `{"version":"2.1.0","runs":[{"results":[{"ruleId":"r"}]}]}`, "no message", p},
	})
}

func TestStaticFindingFromGofmtList(t *testing.T) {
	gf := func(path string) StaticFinding {
		return sf("gofmt", path, 0, 0, "gofmt", "error", "file is not gofmt-formatted", "run gofmt -w "+path)
	}
	cases := []findingCase{
		{"one per file", "a CR, a blank line or padding leaks into a path, or a file yields none or two", "a/b.go\r\n\n  c.go  \nd/e.go\n",
			[]StaticFinding{gf("a/b.go"), gf("c.go"), gf("d/e.go")}},
		{"no trailing newline", "the last line lost without a final newline", "x.go", []StaticFinding{gf("x.go")}},
		{"all formatted", "empty output (every file formatted) read as a finding", "", nil},
	}
	runFindingCases(t, func(b []byte) ([]StaticFinding, error) { return ParseGofmtList(b), nil }, cases)
}

func TestStaticFindingFromGoVetJSON(t *testing.T) {
	const gv = "go vet"
	// An absolute path of this machine: on Windows it has a drive colon.
	abs := filepath.Join(t.TempDir(), "m.go")
	q, _ := json.Marshal(abs + ":12:5")
	qNoLine, _ := json.Marshal(abs)
	vetIn := "# example.com/m\n" +
		`{"example.com/m":{"printf":[{"posn":` + string(q) + `,"message":"bad verb %z"}]}}` + "\n" +
		`{"example.com/n":{"copylocks":[{"posn":"n.go:3:1","message":"copies lock"}],"x":{"error":"boom"}}}` + "\n"
	runFindingCases(t, ParseGoVetJSON, []findingCase{
		{"packages analyzers errors", "an absolute path (a Windows drive colon) split as a position, a # line breaking the stream, an analyzer error dropped", vetIn, []StaticFinding{
			sf(gv, abs, 12, 5, "printf", "error", "bad verb %z", anyFix),
			sf(gv, "n.go", 3, 1, "copylocks", "error", "copies lock", anyFix),
			sf(gv, "", 0, 0, "x", "error", "boom", anyFix),
		}},
		{"position without line", "a position with no number cut at the drive colon", `{"p":{"a":[{"posn":` + string(qNoLine) + `,"message":"m"}]}}`, []StaticFinding{
			sf(gv, abs, 0, 0, "a", "error", "m", anyFix),
		}},
		{"signed number is no position", "a +3 accepted as a line", `{"p":{"a":[{"posn":"a.go:+3:4","message":"m"}]}}`, []StaticFinding{
			sf(gv, "a.go:+3", 4, 0, "a", "error", "m", anyFix),
		}},
		{"empty tree", "an empty package tree read as an error", `{}`, nil},
	})
}

func TestStaticFindingSARIFFileURIKeepsAbsolutePath(t *testing.T) {
	// A file: URI of an absolute path of this machine; on Windows the URI is
	// file:///<drive>:/..., and the slash before the drive must go.
	abs := filepath.Join(t.TempDir(), "a b.go")
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	uri := (&url.URL{Scheme: "file", Path: p}).String()
	in := `{"version":"2.1.0","runs":[{"results":[{"ruleId":"r","message":{"text":"m"},` +
		`"locations":[{"physicalLocation":{"artifactLocation":{"uri":"` + uri + `"}}}]}]}]}`
	runFindingCases(t, ParseGolangciSARIF, []findingCase{
		{"absolute file URI", "a file: URI left with its scheme or with a slash before the drive", in, []StaticFinding{
			sf("golangci-lint", filepath.ToSlash(abs), 0, 0, "r", "warning", "m", anyFix),
		}},
	})
}

func TestStaticFindingRejectsMalformedGoVet(t *testing.T) {
	p := ParseGoVetJSON
	runErrCases(t, []errCase{
		{"empty", "no output read as clean", "", "empty", p},
		{"only comments", "comment lines only read as clean", "# pkg\n# other\n", "empty", p},
		{"null", "null read as an empty tree", "null", "null", p},
		{"scalar analyzer", "a scalar analyzer value skipped", `{"p":{"a":5}}`, "go vet output: p a:", p},
		{"truncated", "truncated JSON accepted", `{"p":{`, "go vet output:", p},
		{"text", "plain compiler text accepted", "a.go:1:1: syntax error\n", "go vet output:", p},
	})
}

const eslintJSON = `[
  {"filePath": "/w/src/a.ts", "messages": [
    {"ruleId": "no-unused-vars", "severity": 2, "message": "'x' is unused", "line": 4, "column": 9},
    {"ruleId": "semi", "severity": 1, "message": "Missing semicolon.", "line": 5, "column": 2,
     "fix": {"range": [1, 2], "text": ";"}}
  ]},
  {"filePath": "/w/src/clean.ts", "messages": []},
  {"filePath": "/w/src/b.ts", "messages": [
    {"ruleId": null, "severity": 2, "message": "Parsing error: x", "line": 1, "column": 1}
  ]}
]`

func eslintParse(b []byte) ([]StaticFinding, error) { return ParseMappedJSON(b, ESLintMapping()) }

func TestStaticFindingFromESLintJSON(t *testing.T) {
	runFindingCases(t, eslintParse, []findingCase{
		{"file entries and messages", "a file read from the message instead of its entry, severity 2 and 1 not mapped, the autofix object not turned into a hint, a null rule", eslintJSON, []StaticFinding{
			sf("eslint", "/w/src/a.ts", 4, 9, "no-unused-vars", "error", "'x' is unused", "fix the reported issue"),
			sf("eslint", "/w/src/a.ts", 5, 2, "semi", "warning", "Missing semicolon.", "run eslint --fix"),
			sf("eslint", "/w/src/b.ts", 1, 1, "", "error", "Parsing error: x", "fix the reported issue"),
		}},
		{"clean file", "a file entry with no messages read as an error", `[{"filePath":"/w/a.ts","messages":[]}]`, nil},
	})
}

func TestStaticFindingFromRuffAndMypyRows(t *testing.T) {
	ruff := func(b []byte) ([]StaticFinding, error) { return ParseMappedJSON(b, RuffMapping()) }
	mypy := func(b []byte) ([]StaticFinding, error) { return ParseMappedJSON(b, MypyMapping()) }
	runFindingCases(t, ruff, []findingCase{
		{"ruff nested paths", "location.row, location.column or fix.message not read as dotted paths", `[{"code":"F401","message":"os imported but unused","filename":"m.py","location":{"row":1,"column":8},"fix":{"message":"Remove unused import: os"}}]`, []StaticFinding{
			sf("ruff", "m.py", 1, 8, "F401", "error", "os imported but unused", "Remove unused import: os"),
		}},
		{"ruff no fix", "a null fix object ending in an empty hint", `[{"code":"E501","message":"too long","filename":"m.py","location":{"row":2,"column":1},"fix":null}]`, []StaticFinding{
			sf("ruff", "m.py", 2, 1, "E501", "error", "too long", anyFix),
		}},
		{"ruff clean", "ruff's [] for a clean run read as an error", `[]`, nil},
	})
	runFindingCases(t, mypy, []findingCase{
		{"mypy column base hint level", "mypy's 0-based column shown as is, its hint ignored, a note or an upper-case severity misread, an unknown column shifted", `{"file":"m.py","line":9,"column":4,"message":"Incompatible types","code":"assignment","severity":"error","hint":"use int"}` + "\n" +
			`{"file":"m.py","line":10,"column":0,"message":"see docs","code":"note","severity":"note","hint":null}` + "\n" +
			`{"file":"m.py","line":11,"column":-1,"message":"bad","code":"misc","severity":"ERROR"}` + "\n", []StaticFinding{
			sf("mypy", "m.py", 9, 5, "assignment", "error", "Incompatible types", "use int"),
			sf("mypy", "m.py", 10, 1, "note", "note", "see docs", "fix the reported type error"),
			sf("mypy", "m.py", 11, 0, "misc", "error", "bad", anyFix),
		}},
	})
}

func TestStaticFindingRejectsMalformedMappedJSON(t *testing.T) {
	typo := ESLintMapping()
	typo.Message = "msg"
	typoParse := func(b []byte) ([]StaticFinding, error) { return ParseMappedJSON(b, typo) }
	noFile := func(b []byte) ([]StaticFinding, error) { return ParseMappedJSON(b, StaticMapping{Tool: "x"}) }
	e := eslintParse
	runErrCases(t, []errCase{
		{"empty", "no output read as clean", "", "empty", e},
		{"empty object", "{} read as clean", "{}", "no list at", e},
		{"null", "null read as clean", "null", "top-level value is null", e},
		{"scalar", "a scalar read as clean", "5", "top-level value is", e},
		{"scalar elements", "scalar items skipped", `[1,"a",null]`, "want an object", e},
		{"scalar message", "a scalar message item skipped", `[{"filePath":"a","messages":[1]}]`, "want an object", e},
		{"item without file", "an item missing the mapped file path kept with an empty file", `[{"messages":[{"message":"m"}]}]`, "(file)", e},
		{"mapping typo", "a renamed tool field (message path wrong) dropping every finding as clean", eslintJSON, "(message)", typoParse},
		{"mapping without file", "a row with no file path accepted", `[]`, "needs a file field", noFile},
		{"truncated", "truncated JSON accepted", `[{`, "eslint output:", e},
	})
}

func TestStaticFindingJSONOmitsUnknownPosition(t *testing.T) {
	b, err := json.Marshal(StaticFinding{File: "a.go", Rule: "gofmt"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"line"`) || strings.Contains(string(b), `"col"`) {
		t.Errorf("0 (no position) must not be written as a position: %s", b)
	}
	b, _ = json.Marshal(StaticFinding{File: "a.go", Line: 3, Col: 1})
	if !strings.Contains(string(b), `"line":3`) || !strings.Contains(string(b), `"col":1`) {
		t.Errorf("a known position must be written: %s", b)
	}
}

func FuzzParseGolangciSARIF(f *testing.F) {
	f.Add([]byte(golangciSARIF))
	f.Add([]byte(`{"version":"2.1.0","runs":[{"results":[{"locations":[{}]}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := ParseGolangciSARIF(data)
		checkFuzzFindings(t, got, err)
	})
}

func FuzzParseGofmtList(f *testing.F) {
	f.Add([]byte("a.go\nb.go\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		checkFuzzFindings(t, ParseGofmtList(data), nil)
	})
}

func FuzzParseGoVetJSON(f *testing.F) {
	f.Add([]byte("# p\n" + `{"p":{"printf":[{"posn":"a.go:1:2","message":"m"}]}}`))
	f.Add([]byte(`{"p":{"x":{"error":"e"}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := ParseGoVetJSON(data)
		checkFuzzFindings(t, got, err)
	})
}

func FuzzParseMappedJSON(f *testing.F) {
	f.Add([]byte(eslintJSON))
	f.Add([]byte(`{"file":"a","line":"3","column":1.5,"message":"m"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, m := range []StaticMapping{ESLintMapping(), RuffMapping(), MypyMapping()} {
			got, err := ParseMappedJSON(data, m)
			checkFuzzFindings(t, got, err)
		}
	})
}

// checkFuzzFindings holds the invariants every parser keeps on any input:
// an error comes with no findings, and no finding has a negative position.
func checkFuzzFindings(t *testing.T, got []StaticFinding, err error) {
	t.Helper()
	if err != nil && len(got) != 0 {
		t.Fatalf("error %v returned with %d findings", err, len(got))
	}
	for _, f := range got {
		if f.Line < 0 || f.Col < 0 {
			t.Fatalf("negative position in %+v", f)
		}
	}
}
