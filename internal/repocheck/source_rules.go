// Package repocheck holds this repository's own checks; it is never shipped (SPEC §6.1).
package repocheck

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The rule names of check repo/source-rules (DR-25, DR-27, SPEC §5.4).
const (
	ruleMachinePath      = "machine_path"
	ruleProductName      = "product_name"
	ruleRecursiveRemoval = "recursive_removal"
	ruleVersionLiteral   = "version_literal"
	rulePackageVar       = "package_var"
	ruleParseError       = "parse_error"
	ruleInvalidUTF8      = "invalid_utf8"
)

// removeFile is the one file that may call os.RemoveAll (DR-25).
const removeFile = "internal/core/remove.go"

// Compiled patterns; the only package-level variables besides sentinel errors (SPEC §5.4).
var (
	// machinePathPattern matches a drive-letter path or a Linux or macOS home directory.
	machinePathPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9])[A-Za-z]:[\\/]|(?:^|[^A-Za-z0-9._-])/(?:home|Users)/[^/\s]`)
	// versionPattern matches a literal that is a whole semantic version, with or without a v.
	versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	// cmdMainPattern matches the main file of a binary, the one place of the build-injected version.
	cmdMainPattern = regexp.MustCompile(`^cmd/[^/]+/main\.go$`)
)

// SourceFile is one Go source file to check; Path is repository-relative and slash-separated.
type SourceFile struct {
	Path string
	Body []byte
}

// SourceRulesConfig names the product and the one file that may hold its name.
type SourceRulesConfig struct {
	Product     string
	ProductFile string
}

// SourceFinding is one broken rule; Line is 1-based, or 0 when the file has no usable position.
type SourceFinding struct {
	Rule string
	Path string
	Line int
}

// SourceRules checks each file against the source rules and returns every finding; no finding
// means every file was read, parsed and passed.
func SourceRules(cfg SourceRulesConfig, files []SourceFile) []SourceFinding {
	var found []SourceFinding
	for _, f := range files {
		found = append(found, checkSource(cfg, f)...)
	}
	return found
}

// sourceChecker holds what the rules of one parsed file need.
type sourceChecker struct {
	cfg     SourceRulesConfig
	path    string
	fset    *token.FileSet
	imports map[string]string // local name → import path
	found   []SourceFinding
}

// checkSource reads, parses and checks one file; unreadable or unparsable source is a finding.
func checkSource(cfg SourceRulesConfig, f SourceFile) []SourceFinding {
	text, err := core.ReadText(f.Body)
	if err != nil {
		return []SourceFinding{{Rule: ruleInvalidUTF8, Path: f.Path}}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, f.Path, text, parser.SkipObjectResolution)
	if err != nil {
		return []SourceFinding{{Rule: ruleParseError, Path: f.Path, Line: errorLine(err)}}
	}
	c := &sourceChecker{cfg: cfg, path: f.Path, fset: fset, imports: importNames(file)}
	c.checkLiterals(file)
	c.checkRemovals(file)
	c.checkPackageVars(file)
	return c.found
}

// errorLine returns the line of the first parse error, or 0 when the error has no position.
func errorLine(err error) int {
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		return list[0].Pos.Line
	}
	return 0
}

// importNames maps each import's local name to its path.
func importNames(file *ast.File) map[string]string {
	names := map[string]string{}
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = p
	}
	return names
}

func (c *sourceChecker) add(rule string, pos token.Pos) {
	c.found = append(c.found, SourceFinding{Rule: rule, Path: c.path, Line: c.fset.Position(pos).Line})
}

// isPkgSel reports whether sel names fn of the imported package pkg.
func (c *sourceChecker) isPkgSel(sel *ast.SelectorExpr, pkg, fn string) bool {
	x, ok := sel.X.(*ast.Ident)
	return ok && sel.Sel.Name == fn && c.imports[x.Name] == pkg
}

// literalRule is one rule on the value of a string literal; skip exempts a whole file.
type literalRule struct {
	rule  string
	skip  func(c *sourceChecker) bool
	match func(c *sourceChecker, value string) bool
}

func literalRules() []literalRule {
	return []literalRule{
		{ruleMachinePath, func(*sourceChecker) bool { return false }, matchPattern(machinePathPattern)},
		{ruleProductName, (*sourceChecker).isProductFile, (*sourceChecker).hasProduct},
		{ruleVersionLiteral, (*sourceChecker).isTestFile, matchPattern(versionPattern)},
	}
}

func matchPattern(re *regexp.Regexp) func(*sourceChecker, string) bool {
	return func(_ *sourceChecker, value string) bool { return re.MatchString(value) }
}

func (c *sourceChecker) isProductFile() bool { return c.path == c.cfg.ProductFile }

func (c *sourceChecker) isTestFile() bool { return strings.HasSuffix(c.path, "_test.go") }

// hasProduct reports whether value holds the product name in any ASCII case.
func (c *sourceChecker) hasProduct(value string) bool {
	return c.cfg.Product != "" && strings.Contains(asciiLower(value), asciiLower(c.cfg.Product))
}

// asciiLower maps only A–Z; case mapping is limited to ASCII (SPEC §5.14).
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// checkLiterals runs the literal rules on every string literal except import paths.
func (c *sourceChecker) checkLiterals(file *ast.File) {
	var rules []literalRule
	for _, r := range literalRules() {
		if !r.skip(c) {
			rules = append(rules, r)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.BasicLit:
			c.checkLiteral(n, rules)
		}
		return true
	})
}

func (c *sourceChecker) checkLiteral(lit *ast.BasicLit, rules []literalRule) {
	if lit.Kind != token.STRING {
		return
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		value = lit.Value
	}
	for _, r := range rules {
		if r.match(c, value) {
			c.add(r.rule, lit.Pos())
		}
	}
}

// checkRemovals flags every use of os.RemoveAll, called or taken as a value, outside removeFile.
func (c *sourceChecker) checkRemovals(file *ast.File) {
	if c.path == removeFile {
		return
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && c.isPkgSel(sel, "os", "RemoveAll") {
			c.add(ruleRecursiveRemoval, sel.Pos())
		}
		return true
	})
}

// checkPackageVars flags every package-level variable that is not an allowed form (SPEC §5.4).
func (c *sourceChecker) checkPackageVars(file *ast.File) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			c.checkVarSpec(spec.(*ast.ValueSpec))
		}
	}
}

func (c *sourceChecker) checkVarSpec(vs *ast.ValueSpec) {
	for i, name := range vs.Names {
		if !c.allowedVar(vs, i) {
			c.add(rulePackageVar, name.Pos())
		}
	}
}

// allowedVar reports whether name i of vs is a sentinel error, a compiled pattern or the
// build-injected `var version string` of a cmd main.go.
func (c *sourceChecker) allowedVar(vs *ast.ValueSpec, i int) bool {
	if len(vs.Values) == 0 {
		return c.isVersionVar(vs.Names[i], vs.Type)
	}
	return len(vs.Values) == len(vs.Names) && c.isAllowedInit(vs.Values[i])
}

func (c *sourceChecker) isVersionVar(name *ast.Ident, typ ast.Expr) bool {
	t, ok := typ.(*ast.Ident)
	return ok && t.Name == "string" && name.Name == "version" && cmdMainPattern.MatchString(c.path)
}

// allowedInits are the calls whose result may be held in a package-level variable.
func allowedInits() [][2]string {
	return [][2]string{{"errors", "New"}, {"regexp", "MustCompile"}}
}

func (c *sourceChecker) isAllowedInit(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	for _, a := range allowedInits() {
		if c.isPkgSel(sel, a[0], a[1]) {
			return true
		}
	}
	return false
}
