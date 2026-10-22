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
	"slices"
	"strconv"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The rule names of check repo/source-rules (DR-17, DR-18, SPEC §5.3).
const (
	ruleMachinePath      = "machine_path"
	ruleProductName      = "product_name"
	ruleRecursiveRemoval = "recursive_removal"
	ruleVersionLiteral   = "version_literal"
	rulePackageVar       = "package_var"
	ruleParseError       = "parse_error"
	ruleInvalidUTF8      = "invalid_utf8"
	ruleIOImport         = "io_import"
	ruleFileName         = "file_name"
	ruleArchSpecific     = "arch_specific"
	ruleConfigInCode     = "config_in_code"
	ruleShellScript      = "shell_script"
	ruleShellCall        = "shell_call"
)

const (
	// removeFile is the one file that may call os.RemoveAll (DR-11).
	removeFile = "internal/core/remove.go"
	// configFile holds the one table of configuration defaults (SPEC §5.12).
	configFile = "internal/core/config.go"
	// sourceRulesPath is this file, which may import os (SPEC §5.6).
	sourceRulesPath = "internal/repocheck/source_rules.go"
	// configDefaultIndex is the position of Default in an unkeyed core.ConfigSetting literal.
	configDefaultIndex = 2
	// shellScriptAllow keeps .githooks/commit-msg; #269 defers the bridge scripts (K31). Windows git runs a hook that is not a script: $GIT_DIR/hooks/<hookname>.exe is valid (Git RelNotes 2.12.0).
	shellScriptAllow = ".githooks/commit-msg"
	// shellNames are the interpreters a shebang or a shell call names (K31).
	shellNames = "sh bash dash zsh ksh fish"
)

// Compiled patterns; the only package-level variables besides sentinel errors (SPEC §5.3).
var (
	// machinePathPattern matches a drive-letter path or a Linux, Linux root or macOS home directory.
	machinePathPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9])[A-Za-z]:[\\/]|(?:^|[^A-Za-z0-9._-])/(?:(?:home|Users)/[^/\s]|root(?:/|$))`)
	// versionPattern matches a literal that is a whole semantic version, with or without a v.
	versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	// fileNamePattern matches a snake_case Go file name.
	fileNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*\.go$`)
)

// SourceFile is one repository file to check; Path is repository-relative and slash-separated.
type SourceFile struct {
	Path       string
	Body       []byte
	shebangCut bool // Body is a shebang prefix with no LF; the interpreter may sit past it (DR-6)
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

// SourceRules returns every finding; none means each file was read, parsed and passed.
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

// checkSource refuses a shell script outside the allow-list, then checks one Go file; unreadable or unparsable source is a finding, and any other tracked file has no Go rule to break (K31).
func checkSource(cfg SourceRulesConfig, f SourceFile) []SourceFinding {
	if isShellScript(f) {
		if f.Path == shellScriptAllow {
			return nil
		}
		return []SourceFinding{{Rule: ruleShellScript, Path: f.Path}}
	}
	if !strings.HasSuffix(f.Path, ".go") {
		return nil
	}
	text, err := core.ReadText(f.Body)
	if err != nil {
		return []SourceFinding{{Rule: ruleInvalidUTF8, Path: f.Path}}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, f.Path, text, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return []SourceFinding{{Rule: ruleParseError, Path: f.Path, Line: errorLine(err)}}
	}
	c := &sourceChecker{cfg: cfg, path: f.Path, fset: fset, imports: importNames(file)}
	c.checkLiterals(file)
	c.checkRemovals(file)
	c.checkPackageVars(file)
	c.checkIOImports(file)
	c.checkFileName()
	c.checkBuildLines(file)
	c.checkConfigLiterals(file)
	c.checkShellCalls(file)
	return c.found
}

// isShellScript reports a shell script by extension or shebang; shebangCut refuses a prefix whose interpreter may sit past shebangHeadBytes (K31, DR-6).
func isShellScript(f SourceFile) bool {
	switch strings.ToLower(path.Ext(f.Path)) {
	case ".sh", ".ps1":
		return true
	}
	line := firstBodyLine(f.Body)
	return strings.HasPrefix(line, "#!") && (f.shebangCut || namesShell(strings.Fields(line[2:])))
}
func firstBodyLine(body []byte) string {
	line, _, _ := strings.Cut(string(body), "\n")
	return strings.TrimSuffix(line, "\r")
}

// namesShell reports whether a shebang names a shell. After env, flags, a bare flag argument and NAME=VALUE are skipped (K31).
func namesShell(fields []string) bool {
	env := len(fields) > 0 && strings.TrimSuffix(asciiLower(path.Base(strings.ReplaceAll(fields[0], `\`, "/"))), ".exe") == "env"
	if env {
		fields = fields[1:]
	}
	for n := envSkip(env, fields); n > 0; n = envSkip(env, fields) {
		fields = fields[n:]
	}
	return len(fields) > 0 && isShellName(fields[0])
}
func envSkip(env bool, fields []string) int {
	if len(fields) == 0 {
		return 0
	}
	if strings.HasPrefix(fields[0], "-") {
		if env && len(fields) > 1 && !strings.HasPrefix(fields[1], "-") && !strings.Contains(fields[1], "=") && !isShellName(fields[1]) {
			return 2
		}
		return 1
	}
	if env && strings.Contains(fields[0], "=") {
		return 1
	}
	return 0
}
func errorLine(err error) int {
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		return list[0].Pos.Line
	}
	return 0
}

// importNames maps each local name to its path; a dot import's key is "." + path, so none overwrite.
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
		if name == "." {
			name += p
		}
		names[name] = p
	}
	return names
}
func (c *sourceChecker) add(rule string, pos token.Pos) {
	c.found = append(c.found, SourceFinding{Rule: rule, Path: c.path, Line: c.fset.Position(pos).Line})
}
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
func (c *sourceChecker) isTestFile() bool    { return strings.HasSuffix(c.path, "_test.go") }
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

// checkLiterals checks string literals except import paths; comments may name the product, so they are skipped.
func (c *sourceChecker) checkLiterals(file *ast.File) {
	rules := slices.DeleteFunc(literalRules(), func(r literalRule) bool { return r.skip(c) })
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

// checkRemovals flags every RemoveAll selector outside removeFile, called or taken as a value, including a bare name under `import . "os"`; without types an *os.Root fails closed (DR-11).
func (c *sourceChecker) checkRemovals(file *ast.File) {
	if c.path == removeFile {
		return
	}
	ast.Inspect(file, c.visitRemoval)
}

// visitRemoval flags n when it is a RemoveAll selector or, under a dot import of os, a bare RemoveAll; the selected name of a selector is never visited as a bare identifier.
func (c *sourceChecker) visitRemoval(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.SelectorExpr:
		if n.Sel.Name == "RemoveAll" {
			c.add(ruleRecursiveRemoval, n.Pos())
		}
		ast.Inspect(n.X, c.visitRemoval)
		return false
	case *ast.Ident:
		if n.Name == "RemoveAll" && c.imports[".os"] == "os" {
			c.add(ruleRecursiveRemoval, n.Pos())
		}
	}
	return true
}

// checkPackageVars flags every package-level variable that is not an allowed form (SPEC §5.3).
func (c *sourceChecker) checkPackageVars(file *ast.File) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if ok && gd.Tok == token.VAR {
			for _, spec := range gd.Specs {
				c.checkVarSpec(spec.(*ast.ValueSpec))
			}
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

// allowedVar reports whether name i of vs is a sentinel, a compiled pattern or `var version string`.
func (c *sourceChecker) allowedVar(vs *ast.ValueSpec, i int) bool {
	if len(vs.Values) == 0 {
		return c.isVersionVar(vs.Names[i], vs.Type)
	}
	return len(vs.Values) == len(vs.Names) && c.isAllowedInit(vs.Values[i])
}
func (c *sourceChecker) isVersionVar(name *ast.Ident, typ ast.Expr) bool {
	t, ok := typ.(*ast.Ident)
	return ok && t.Name == "string" && name.Name == "version" && c.path == "cmd/"+c.cfg.Product+"/main.go"
}

// allowedInits are the calls whose result may be held in a package-level variable.
func allowedInits() [][2]string { return [][2]string{{"errors", "New"}, {"regexp", "MustCompile"}} }
func (c *sourceChecker) isAllowedInit(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return slices.ContainsFunc(allowedInits(), func(a [2]string) bool { return c.isPkgSel(sel, a[0], a[1]) })
}

// checkShellCalls flags a shell Command or CommandContext with -c or -lc in its arguments. A non-literal shell argument fails closed; a non-literal program does so only with a literal -c (K31, §5.20).
func (c *sourceChecker) checkShellCalls(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if args := commandArgs(call, c.imports); len(args) > 0 && shellArgs(args) {
			c.add(ruleShellCall, call.Pos())
		}
		return true
	})
}
func commandArgs(call *ast.CallExpr, imports map[string]string) []ast.Expr {
	pkg, name := execTarget(call.Fun, imports)
	shift := slices.Index([]string{"Command", "CommandContext"}, name)
	if pkg != "os/exec" || shift < 0 || shift >= len(call.Args) {
		return nil
	}
	return call.Args[shift:]
}
func execTarget(fun ast.Expr, imports map[string]string) (string, string) {
	switch fun := fun.(type) {
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok {
			return imports[x.Name], fun.Sel.Name
		}
	case *ast.Ident:
		return imports[".os/exec"], fun.Name
	}
	return "", ""
}
func shellArgs(args []ast.Expr) bool {
	prog, ok := stringValue(args[0])
	if ok && !isShellName(prog) {
		return false
	}
	return slices.ContainsFunc(args[1:], func(arg ast.Expr) bool { return shellOpt(arg, ok) })
}
func shellOpt(arg ast.Expr, progLit bool) bool {
	opt, lit := stringValue(arg)
	return (lit && dashC(opt)) || (!lit && progLit)
}

// dashC reports a short option cluster that contains c, such as -c or -lc. A flag with a non-letter, such as -test.count, is not one.
func dashC(flag string) bool {
	return len(flag) > 1 && flag[0] == '-' && flag[1] != '-' && strings.TrimLeft(flag[1:], "abcdefghijklmnopqrstuvwxyz") == "" && strings.ContainsRune(flag, 'c')
}

// isShellName reports a shell stem after either slash and a trailing .exe, in ASCII lower case (K31).
func isShellName(name string) bool {
	return slices.Contains(strings.Fields(shellNames), strings.TrimSuffix(asciiLower(path.Base(strings.ReplaceAll(name, `\`, "/"))), ".exe"))
}
func stringValue(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}
