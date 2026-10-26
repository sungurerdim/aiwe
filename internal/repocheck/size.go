package repocheck

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"unicode"
)

// The rule names and limits of check repo/size (SPEC §5.13, Q23).
const (
	ruleFileTooLong = "file_too_long"
	ruleFuncTooLong = "func_too_long"
	ruleDocTooLong  = "doc_too_long"
	ruleBareSkip    = "bare_skip"

	maxSourceLines = 400
	maxTestLines   = 500
	maxFuncLines   = 60
	// maxSpecLines is the L1 cap: SPEC carries product, architecture and contracts and stays short,
	// because the delivery plan and the unknowns live in tracker issues instead (L1, #309).
	maxSpecLines = 900
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

// cappedDocs lists the documents L1 caps at maxSpecLines; the check reads the list from here alone.
func cappedDocs() []string { return []string{specFile, contractsFile} }

// specCapFindings returns the L1 finding of one file: a capped document (cappedDocs) over its
// 900-line cap, reported at the first line the cap excludes, which is where a reader must cut. No
// other file carries the cap, and these two are prose: the Go line limits and the Go parser never
// read them (L1, #309).
func specCapFindings(f SourceFile) []SourceFinding {
	if !slices.Contains(cappedDocs(), f.Path) || lineCount(f.Body) <= maxSpecLines {
		return nil
	}
	return []SourceFinding{{Rule: ruleDocTooLong, Path: f.Path, Line: maxSpecLines + 1}}
}

// checkSkips records one bare_skip per `Skip`, `Skipf` or `SkipNow` call in file that states no
// reason: a skipped test is a behaviour nobody checks, so the tree says why it is skipped (DR-12,
// #309). The rule asks for a statement, not an issue number, because the skips in this tree name
// what the test itself found (the platform, a missing privilege, an unsupported file type).
// A call counts only on a testing receiver: an identifier that a parameter of the enclosing
// declaration (a function, or a var holding a function literal), or of a literal inside it, binds
// to *testing.T, *testing.B, *testing.F or testing.TB. A receiver of another type, or a field such as s.t, is not read as a skipped test.
func (c *sourceChecker) checkSkips(file *ast.File) {
	for _, decl := range file.Decls {
		c.skipsIn(decl)
	}
}

// skipsIn records the bare skips of one top-level declaration: a function, or a var that holds a
// function literal, whose parameters bind the testing receivers.
func (c *sourceChecker) skipsIn(decl ast.Decl) {
	recv := c.testingParams(decl)
	ast.Inspect(decl, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && bareSkip(call, recv) {
			c.add(ruleBareSkip, call.Pos())
		}
		return true
	})
}

// bareSkip reports a skip call on a testing receiver that states no reason.
func bareSkip(call *ast.CallExpr, recv map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !skipMarker(sel.Sel.Name) || reasonStated(call.Args) {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && recv[id.Name]
}

// testingParams returns the names that a parameter of decl, or of a function literal in it, binds to
// a testing type. A name rebound to another type inside the body is not tracked: the file's own
// declarations are the evidence, and a rebinding is rare enough to leave unread.
func (c *sourceChecker) testingParams(decl ast.Decl) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(decl, func(n ast.Node) bool {
		ft, ok := n.(*ast.FuncType)
		if !ok || ft.Params == nil {
			return true
		}
		for _, field := range ft.Params.List {
			if c.isTestingType(field.Type) {
				for _, name := range field.Names {
					names[name.Name] = true
				}
			}
		}
		return true
	})
	return names
}

// isTestingType reports `testing.TB`, or a pointer to `testing.T`, `testing.B` or `testing.F`,
// with testing under whatever local name the file imports it.
func (c *sourceChecker) isTestingType(e ast.Expr) bool {
	if star, ok := e.(*ast.StarExpr); ok {
		e = star.X
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && c.imports[pkg.Name] == "testing" && slices.Contains([]string{"T", "B", "F", "TB"}, sel.Sel.Name)
}

// skipMarker reports a testing.TB method that leaves a test out.
func skipMarker(name string) bool {
	return name == "Skip" || name == "Skipf" || name == "SkipNow"
}

// reasonStated reports whether the arguments of a skip say why: one argument is enough. A call with
// no argument says nothing. Neither does a nil, a non-string literal, or a string constant (literals
// joined by +) that is empty or holds only white space and control or format characters, such as a
// zero-width space. Any other argument, such as a message, a call or a variable holding the reason,
// is a statement: the check does not follow a value it cannot fold.
func reasonStated(args []ast.Expr) bool {
	for _, arg := range args {
		arg = ast.Unparen(arg)
		if id, ok := arg.(*ast.Ident); ok && id.Name == "nil" {
			continue
		}
		if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind != token.STRING {
			continue
		}
		if text, ok := constString(arg); !ok || visible(text) {
			return true
		}
	}
	return false
}

// constString folds string literals joined by +, in any parentheses, to their value.
func constString(e ast.Expr) (string, bool) {
	switch x := ast.Unparen(e).(type) {
	case *ast.BasicLit:
		return stringValue(x)
	case *ast.BinaryExpr:
		l, lok := constString(x.X)
		r, rok := constString(x.Y)
		return l + r, x.Op == token.ADD && lok && rok
	}
	return "", false
}

// visible reports a rune a reader sees: not white space, not a control and not a format character.
func visible(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool {
		return !unicode.IsSpace(r) && !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r)
	}) >= 0
}

// lineCount returns the number of lines in body; a last line without a line feed counts too.
func lineCount(body []byte) int {
	n := bytes.Count(body, []byte{'\n'})
	if len(body) > 0 && body[len(body)-1] != '\n' {
		n++
	}
	return n
}
