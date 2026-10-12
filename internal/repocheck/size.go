package repocheck

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// The rule names and limits of check repo/size (SPEC §5.13, Q23).
const (
	ruleFileTooLong = "file_too_long"
	ruleFuncTooLong = "func_too_long"

	maxSourceLines = 400
	maxTestLines   = 500
	maxFuncLines   = 60
)

// Size checks each file against the size limits of SPEC §5.13: a source file holds at most 400
// lines, a test file at most 500, and a function outside a test file at most 60, counted from its
// func keyword through its closing brace. A test file is bounded by its file limit alone (Q23). A
// file that does not parse is a parse_error finding, so no function goes uncounted.
func Size(files []SourceFile) []Finding {
	var found []Finding
	for _, f := range files {
		found = append(found, checkSize(f)...)
	}
	return found
}

// checkSize returns the size findings of one file.
func checkSize(f SourceFile) []Finding {
	isTest := strings.HasSuffix(f.Path, "_test.go")
	limit := maxSourceLines
	if isTest {
		limit = maxTestLines
	}
	var found []Finding
	if lineCount(f.Body) > limit {
		found = append(found, Finding{Rule: ruleFileTooLong, Path: f.Path, Line: limit + 1})
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, f.Path, f.Body, parser.SkipObjectResolution)
	if err != nil {
		return append(found, Finding{Rule: ruleParseError, Path: f.Path, Line: errorLine(err)})
	}
	if isTest {
		return found
	}
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		start, end := fset.Position(fd.Pos()).Line, fset.Position(fd.End()).Line
		if end-start+1 > maxFuncLines {
			found = append(found, Finding{Rule: ruleFuncTooLong, Path: f.Path, Line: start})
		}
	}
	return found
}

// lineCount returns the number of lines in body; a last line without a line feed counts too.
func lineCount(body []byte) int {
	n := bytes.Count(body, []byte{'\n'})
	if len(body) > 0 && body[len(body)-1] != '\n' {
		n++
	}
	return n
}
