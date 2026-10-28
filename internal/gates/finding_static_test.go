package gates

import (
	"strings"
	"testing"
)

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
         "artifactLocation": {"uri": "file:///D:/repo/a%20b/x.go"},
         "region": {"startLine": 3}}}]}
    ]
  }]
}`

func TestStaticFindingFromGolangciSARIF(t *testing.T) {
	got, err := ParseGolangciSARIF([]byte(golangciSARIF))
	if err != nil {
		t.Fatalf("ParseGolangciSARIF: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("findings = %d, want 2: %+v", len(got), got)
	}
	f := got[0]
	if f.File != "internal/gates/runner.go" || f.Line != 42 || f.Col != 7 {
		t.Errorf("location = %s:%d:%d", f.File, f.Line, f.Col)
	}
	if f.Rule != "errcheck" || f.Level != "error" || f.Tool != "golangci-lint" {
		t.Errorf("rule/level/tool = %q %q %q", f.Rule, f.Level, f.Tool)
	}
	if f.Message != "Error return value of os.Remove is not checked" {
		t.Errorf("message = %q", f.Message)
	}
	if f.Fix == "" {
		t.Error("fix hint empty: a finding always carries one")
	}
	g := got[1]
	if g.File != "D:/repo/a b/x.go" || g.Line != 3 || g.Col != 0 {
		t.Errorf("file URI = %s:%d:%d", g.File, g.Line, g.Col)
	}
	if g.Level != "warning" || g.Fix != "delete helper or call it" {
		t.Errorf("level/fix = %q %q", g.Level, g.Fix)
	}
}

func TestStaticFindingFromGolangciSARIFRejectsBadInput(t *testing.T) {
	if got, err := ParseGolangciSARIF([]byte("  \n")); err != nil || len(got) != 0 {
		t.Errorf("empty input = %v, %v; want none, nil", got, err)
	}
	if _, err := ParseGolangciSARIF([]byte(`{"runs": [`)); err == nil {
		t.Error("truncated JSON: want an error")
	}
}

func TestStaticFindingFromGofmtList(t *testing.T) {
	got := ParseGofmtList([]byte("a/b.go\r\n\n  c.go  \nd/e.go\n"))
	if len(got) != 3 {
		t.Fatalf("findings = %d, want one per file (3): %+v", len(got), got)
	}
	for i, want := range []string{"a/b.go", "c.go", "d/e.go"} {
		f := got[i]
		if f.File != want || f.Rule != "gofmt" || f.Tool != "gofmt" || f.Level != "error" {
			t.Errorf("finding %d = %+v", i, f)
		}
		if !strings.Contains(f.Fix, "gofmt -w "+want) {
			t.Errorf("fix %q does not name the command for %s", f.Fix, want)
		}
	}
	if len(ParseGofmtList(nil)) != 0 {
		t.Error("empty list must give no findings")
	}
}

func TestStaticFindingFromGoVetJSON(t *testing.T) {
	in := "# example.com/m\n" +
		`{"example.com/m":{"printf":[{"posn":"D:\\r\\m.go:12:5","message":"bad verb %z"}]}}` + "\n" +
		`{"example.com/n":{"copylocks":[{"posn":"n.go:3:1","message":"copies lock"}],"x":{"error":"boom"}}}` + "\n"
	got, err := ParseGoVetJSON([]byte(in))
	if err != nil {
		t.Fatalf("ParseGoVetJSON: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("findings = %d, want 3: %+v", len(got), got)
	}
	f := got[0]
	if f.File != `D:\r\m.go` || f.Line != 12 || f.Col != 5 || f.Rule != "printf" || f.Tool != "go vet" {
		t.Errorf("windows path finding = %+v", f)
	}
	if f.Message != "bad verb %z" || f.Fix == "" {
		t.Errorf("message/fix = %q %q", f.Message, f.Fix)
	}
	if got[1].File != "n.go" || got[1].Rule != "copylocks" {
		t.Errorf("second = %+v", got[1])
	}
	if got[2].Rule != "x" || got[2].Message != "boom" || got[2].File != "" {
		t.Errorf("analyzer error = %+v", got[2])
	}
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

func TestStaticFindingFromESLintJSON(t *testing.T) {
	got, err := ParseMappedJSON([]byte(eslintJSON), ESLintMapping())
	if err != nil {
		t.Fatalf("ParseMappedJSON: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("findings = %d, want 3: %+v", len(got), got)
	}
	f := got[0]
	if f.File != "/w/src/a.ts" || f.Line != 4 || f.Col != 9 || f.Rule != "no-unused-vars" {
		t.Errorf("first = %+v", f)
	}
	if f.Level != "error" || f.Tool != "eslint" || f.Message != "'x' is unused" || f.Fix == "" {
		t.Errorf("first level/tool/message/fix = %+v", f)
	}
	if got[1].Level != "warning" || !strings.Contains(got[1].Fix, "--fix") {
		t.Errorf("autofixable warning = %+v", got[1])
	}
	if got[2].File != "/w/src/b.ts" || got[2].Rule != "" || got[2].Fix == "" {
		t.Errorf("rule-less message = %+v", got[2])
	}
}

func TestStaticFindingFromRuffAndMypyRows(t *testing.T) {
	ruff := `[{"code":"F401","message":"os imported but unused","filename":"m.py",
	  "location":{"row":1,"column":8},"fix":{"message":"Remove unused import: os"}}]`
	got, err := ParseMappedJSON([]byte(ruff), RuffMapping())
	if err != nil || len(got) != 1 {
		t.Fatalf("ruff = %+v, %v", got, err)
	}
	if f := got[0]; f.File != "m.py" || f.Line != 1 || f.Col != 8 || f.Rule != "F401" ||
		f.Fix != "Remove unused import: os" || f.Tool != "ruff" {
		t.Errorf("ruff finding = %+v", f)
	}
	mypy := `{"file":"m.py","line":9,"column":4,"message":"Incompatible types","code":"assignment","severity":"error"}` + "\n" +
		`{"file":"m.py","line":10,"column":0,"message":"hint","code":"note","severity":"note"}` + "\n"
	got, err = ParseMappedJSON([]byte(mypy), MypyMapping())
	if err != nil || len(got) != 2 {
		t.Fatalf("mypy = %+v, %v", got, err)
	}
	if got[0].Rule != "assignment" || got[0].Level != "error" || got[0].Line != 9 || got[1].Level != "note" {
		t.Errorf("mypy findings = %+v", got)
	}
}

func TestStaticFindingMappingRejectsBadRow(t *testing.T) {
	if _, err := ParseMappedJSON([]byte(`[]`), StaticMapping{Tool: "x"}); err == nil {
		t.Error("a mapping without a file field: want an error")
	}
	if _, err := ParseMappedJSON([]byte(`[{`), ESLintMapping()); err == nil {
		t.Error("truncated JSON: want an error")
	}
	if got, err := ParseMappedJSON([]byte(`[1,"a",null]`), ESLintMapping()); err != nil || len(got) != 0 {
		t.Errorf("non-object elements = %v, %v; want none, nil", got, err)
	}
}

func FuzzParseGolangciSARIF(f *testing.F) {
	f.Add([]byte(golangciSARIF))
	f.Add([]byte(`{"runs":[{"results":[{"locations":[{}]}]}]}`))
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
	f.Add([]byte(`{"file":"a","line":"3","column":1.5}`))
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
