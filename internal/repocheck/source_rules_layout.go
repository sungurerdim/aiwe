package repocheck

import (
	"go/ast"
	"go/build/constraint"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"
)

// The layout rules of check repo/source-rules: IO imports, file names, architecture and
// configuration defaults (SPEC §5.6, §5.8, §5.12, §12.2).

// repocheckMainPath is the entry point of repocheck, which may import os (SPEC §5.6).
const repocheckMainPath = "internal/cmd/repocheck/main.go"

// ioImports are the imports that do process, file or network IO (SPEC §5.6).
func ioImports() []string { return []string{"os", "os/exec", "io/ioutil", "net", "net/http"} }

// ioCoreFiles are the core files of units 0.3, 0.20, 0.5, 0.6, 0.18 and 0.21 without their OS
// suffix; they and their _<os> variants may import IO (SPEC §5.6, §14).
func ioCoreFiles() []string {
	return []string{"platform", "modules", "env", "outcomes", "config", "log", "remove", "owner_key",
		"sweep", "footprint", "process", "launcher", "git", "procstart", "realpath"}
}

// osNames are the GOOS values the go tool knows, plus the unix tag used as a file suffix.
func osNames() []string {
	return []string{"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js",
		"linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos", "unix"}
}

// archNames are the GOARCH values the go tool knows (go/build knownArch).
func archNames() []string {
	return []string{"386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64", "mips",
		"mipsle", "mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64", "ppc64le", "riscv",
		"riscv64", "s390", "s390x", "sparc", "sparc64", "wasm"}
}

func (c *sourceChecker) addAt(rule string, line int) {
	c.found = append(c.found, SourceFinding{Rule: rule, Path: c.path, Line: line})
}

// mayImportIO reports whether the file is one of the files SPEC §5.6 lets import IO.
func (c *sourceChecker) mayImportIO() bool {
	p := c.path
	if c.isTestFile() || strings.HasSuffix(p, "_io.go") || strings.HasPrefix(p, "internal/adapters/") ||
		p == "cmd/"+c.cfg.Product+"/main.go" || p == repocheckMainPath || p == sourceRulesPath {
		return true
	}
	dir, name := path.Split(p)
	return dir == "internal/core/" && slices.Contains(ioCoreFiles(), withoutOS(strings.TrimSuffix(name, ".go")))
}

// withoutOS drops one trailing _<os> suffix from a file stem.
func withoutOS(stem string) string {
	i := strings.LastIndex(stem, "_")
	if i > 0 && slices.Contains(osNames(), stem[i+1:]) {
		return stem[:i]
	}
	return stem
}

// ruleEnvRead flags a process environment read outside envFile (SPEC §5.4).
const ruleEnvRead = "env_read"

// envFile is the only file that reads the process environment (SPEC §5.4).
const envFile = "internal/core/env.go"

// envReads are the os functions that read the process environment.
func envReads() []string { return []string{"Environ", "Getenv", "LookupEnv"} }

// checkEnvReads flags each os environment read, called or taken as a value, aliased or under a dot
// import of os, outside envFile and test files (SPEC §5.4).
func (c *sourceChecker) checkEnvReads(file *ast.File) {
	if c.path == envFile || c.isTestFile() {
		return
	}
	ast.Inspect(file, pkgUses{c: c, rule: ruleEnvRead, pkg: "os", names: envReads()}.visit)
}

// ruleRootRead flags a plain os read by path outside rootReadFiles (SPEC §5.6).
const ruleRootRead = "root_read"

// rootReads are the os functions that read a file by path and follow a link out of a root;
// os.DirFS hands out such reads.
func rootReads() []string { return []string{"ReadFile", "Open", "OpenFile", "Stat", "DirFS"} }

// ioutilReads are the io/ioutil functions that read by path, as rootReads do.
func ioutilReads() []string { return []string{"ReadFile", "ReadDir"} }

// rootReadFiles read by a path that is not under a repository root: procstart_unix.go and
// process_procfs_io.go read fixed /proc paths, remove.go stats the roots it compares, owner_key.go
// reads its state-root ledger.
func rootReadFiles() []string {
	return []string{"internal/core/procstart_unix.go", "internal/core/process_procfs_io.go",
		"internal/core/remove.go", "internal/core/owner_key.go"}
}

// checkRootReads flags each plain os read by path outside rootReadFiles and test files; a file
// under a repository or state root is read through an *os.Root, which keeps links inside it
// (SPEC §5.6).
func (c *sourceChecker) checkRootReads(file *ast.File) {
	if slices.Contains(rootReadFiles(), c.path) || c.isTestFile() {
		return
	}
	ast.Inspect(file, pkgUses{c: c, rule: ruleRootRead, pkg: "os", names: rootReads()}.visit)
	ast.Inspect(file, pkgUses{c: c, rule: ruleRootRead, pkg: "io/ioutil", names: ioutilReads()}.visit)
}

// pkgUses flags each use of one of names from the package with import path pkg, called or taken
// as a value.
type pkgUses struct {
	c     *sourceChecker
	rule  string
	pkg   string
	names []string
}

// visit flags n when it is <pkg>.<name> under any import name or, under a dot import of pkg, a
// bare <name>; the selected name of a selector is never visited as a bare identifier.
func (u pkgUses) visit(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.SelectorExpr:
		if x, ok := n.X.(*ast.Ident); ok && u.c.imports[x.Name] == u.pkg && slices.Contains(u.names, n.Sel.Name) {
			u.c.add(u.rule, n.Pos())
		}
		ast.Inspect(n.X, u.visit)
		return false
	case *ast.Ident:
		if u.c.imports["."+u.pkg] == u.pkg && slices.Contains(u.names, n.Name) {
			u.c.add(u.rule, n.Pos())
		}
	}
	return true
}

// checkIOImports flags each IO import, aliased or grouped, in a file that may not import IO, each
// environment read outside envFile, which is process IO too, and each plain read by path.
func (c *sourceChecker) checkIOImports(file *ast.File) {
	c.checkEnvReads(file)
	c.checkRootReads(file)
	if c.mayImportIO() {
		return
	}
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err == nil && slices.Contains(ioImports(), p) {
			c.add(ruleIOImport, imp.Path.Pos())
		}
	}
}

// checkFileName flags a file name that is not snake_case (SPEC §5.8) and one whose last suffix,
// after _test, names an architecture, which makes the file architecture-specific (SPEC §12.2).
func (c *sourceChecker) checkFileName() {
	name := path.Base(c.path)
	if !fileNamePattern.MatchString(name) {
		c.addAt(ruleFileName, 0)
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test"), "_")
	if len(parts) > 1 && slices.Contains(archNames(), parts[len(parts)-1]) {
		c.addAt(ruleArchSpecific, 0)
	}
}

// checkBuildLines flags each build constraint before the package clause that names an
// architecture, negated or not.
func (c *sourceChecker) checkBuildLines(file *ast.File) {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			return
		}
		for _, cm := range group.List {
			expr, err := constraint.Parse(cm.Text)
			if err == nil && hasArch(expr) {
				c.add(ruleArchSpecific, cm.Pos())
			}
		}
	}
}

// hasArch walks the whole expression; Expr.Eval short-circuits and would miss a tag.
func hasArch(e constraint.Expr) bool {
	switch e := e.(type) {
	case *constraint.TagExpr:
		return slices.Contains(archNames(), e.Tag)
	case *constraint.NotExpr:
		return hasArch(e.X)
	case *constraint.AndExpr:
		return hasArch(e.X) || hasArch(e.Y)
	case *constraint.OrExpr:
		return hasArch(e.X) || hasArch(e.Y)
	}
	return false
}

// checkConfigLiterals flags a ConfigSetting literal with a Default outside the §5.12 table and tests.
func (c *sourceChecker) checkConfigLiterals(file *ast.File) {
	if c.path == configFile || c.isTestFile() {
		return
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.CompositeLit); ok {
			c.checkConfigLit(lit)
		}
		return true
	})
}

// checkConfigLit checks a typed ConfigSetting literal and the type-elided elements of a slice,
// array or map of ConfigSetting.
func (c *sourceChecker) checkConfigLit(lit *ast.CompositeLit) {
	if c.isConfigType(lit.Type) {
		c.checkDefault(lit)
	}
	elem := elementType(lit.Type)
	if elem == nil || !c.isConfigType(elem) {
		return
	}
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			e = kv.Value
		}
		if inner, ok := e.(*ast.CompositeLit); ok && inner.Type == nil {
			c.checkDefault(inner)
		}
	}
}

func elementType(t ast.Expr) ast.Expr {
	switch t := t.(type) {
	case *ast.ArrayType:
		return t.Elt
	case *ast.MapType:
		return t.Value
	}
	return nil
}

// isConfigType reports whether t is ConfigSetting inside core or under a dot import of core, or
// <core import>.ConfigSetting, with or without a pointer.
func (c *sourceChecker) isConfigType(t ast.Expr) bool {
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	switch t := t.(type) {
	case *ast.Ident:
		return t.Name == "ConfigSetting" && (path.Dir(c.path) == "internal/core" || c.dotImportsCore())
	case *ast.SelectorExpr:
		x, ok := t.X.(*ast.Ident)
		return ok && t.Sel.Name == "ConfigSetting" && strings.HasSuffix(c.imports[x.Name], "/internal/core")
	}
	return false
}

// dotImportsCore reports whether the file has `import . "<module>/internal/core"`; a dot import
// is keyed "." + path in c.imports.
func (c *sourceChecker) dotImportsCore() bool {
	for name := range c.imports {
		if strings.HasPrefix(name, ".") && strings.HasSuffix(name, "/internal/core") {
			return true
		}
	}
	return false
}

// checkDefault flags a keyed or unkeyed Default that is not the empty string.
func (c *sourceChecker) checkDefault(lit *ast.CompositeLit) {
	for i, e := range lit.Elts {
		value := e
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			key, _ := kv.Key.(*ast.Ident)
			if key == nil || key.Name != "Default" {
				continue
			}
			value = kv.Value
		} else if i != configDefaultIndex {
			continue
		}
		if !isEmptyString(value) {
			c.add(ruleConfigInCode, e.Pos())
		}
	}
}

func isEmptyString(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && (lit.Value == `""` || lit.Value == "``")
}
