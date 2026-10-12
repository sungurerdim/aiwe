package repocheck

import (
	"fmt"
	"go/version"
	"strings"
)

// ruleModuleNotAllowed is the rule name of check repo/deps (DR-21).
const ruleModuleNotAllowed = "module_not_allowed"

// mcpSDKModule is the official MCP Go SDK, the one module outside golang.org/x that DR-21 allows.
const mcpSDKModule = "github.com/modelcontextprotocol/go-sdk"

// Deps checks the modules linked into the binary, as `go list -deps -json ./cmd/...` prints its
// packages, against the allow-list of DR-21: the standard library, golang.org/x/* and the MCP Go
// SDK. A module that only an allowed third-party module imports is one "these require" and passes;
// each other module a package of the main module imports is a module_not_allowed finding whose
// Path is the module path.
func Deps(goList []byte) ([]Finding, error) {
	pkgs, err := decodeGoList(goList)
	if err != nil {
		return nil, err
	}
	if _, err := mainModule(pkgs); err != nil {
		return nil, err
	}
	byPath := make(map[string]listedPackage, len(pkgs))
	for _, p := range pkgs {
		byPath[p.ImportPath] = p
	}
	found, err := notAllowedFindings(pkgs, byPath)
	if err != nil {
		return nil, err
	}
	sortFindings(found)
	return found, nil
}

// notAllowedFindings returns one module_not_allowed finding per module that a package of the main
// module imports directly and DR-21 does not allow.
func notAllowedFindings(pkgs []listedPackage, byPath map[string]listedPackage) ([]Finding, error) {
	seen := map[string]bool{}
	var found []Finding
	for _, p := range pkgs {
		if !p.own() {
			continue
		}
		mods, err := refusedModules(byPath, p)
		if err != nil {
			return nil, err
		}
		for _, mod := range mods {
			if !seen[mod] {
				seen[mod] = true
				found = append(found, Finding{Rule: ruleModuleNotAllowed, Path: mod})
			}
		}
	}
	return found, nil
}

// refusedModules returns the third-party modules that p imports directly and DR-21 does not allow.
func refusedModules(byPath map[string]listedPackage, p listedPackage) ([]string, error) {
	var mods []string
	for _, imp := range p.Imports {
		mod, err := thirdPartyModule(byPath, imp)
		if err != nil {
			return nil, fmt.Errorf("package %s: %w", p.ImportPath, err)
		}
		if mod != "" && !allowedModule(mod) {
			mods = append(mods, mod)
		}
	}
	return mods, nil
}

// thirdPartyModule returns the module of the listed package imp, or "" when imp is in the standard
// library or the main module. An import the output does not list is an error: its module is unknown.
func thirdPartyModule(byPath map[string]listedPackage, imp string) (string, error) {
	if imp == "C" {
		return "", nil // cgo's pseudo-package; go list never lists it
	}
	p, ok := byPath[imp]
	switch {
	case !ok:
		return "", fmt.Errorf("import %s is not listed in the go list output", imp)
	case p.Standard, p.own():
		return "", nil
	case p.Module == nil:
		return "", fmt.Errorf("import %s has no module", imp)
	}
	return p.Module.Path, nil
}

// allowedModule reports whether DR-21 allows mod by name. The golang.org/x match is per path
// element, so a look-alike such as golang.org/xx is refused.
func allowedModule(mod string) bool {
	return strings.HasPrefix(mod, "golang.org/x/") || mod == mcpSDKModule
}

// DoctorEnv is what Doctor reads from the machine; the command passes the real lookups, a test
// passes fakes.
type DoctorEnv struct {
	Repo        string
	GOOS        string
	LookPath    func(name string) (string, error)
	GoVersion   func() (string, error)
	ResolveTool func(tool string) error
}

// Prerequisite is one entry of a doctor report; Install is the install command for the running OS
// when the prerequisite is missing, and empty when it is present.
type Prerequisite struct {
	Name    string
	Present bool
	Install string
}

// installCommands maps each prerequisite to its install command per OS; the tools come from
// tools/go.mod and share one command.
func installCommands() map[string]map[string]string {
	return map[string]map[string]string{
		"git": {
			"windows": "winget install --id git.git",
			"darwin":  "brew install git",
			"linux":   "sudo apt-get install git",
		},
		"go": {
			"windows": "winget install --id golang.go",
			"darwin":  "brew install go",
			"linux":   "sudo snap install go --classic",
		},
	}
}

// toolsModFile is the tools module: its tool lines are the development tools, run with
// `go tool -modfile=tools/go.mod` (SPEC §12.2).
const toolsModFile = "tools/go.mod"

// Doctor reports each prerequisite of this repository (SPEC §12.2): git, Go at least at the go.mod
// version, and each tool line of tools/go.mod resolving. A missing one carries its install command
// for env.GOOS. A repository without a readable go.mod or tools/go.mod is an error, and so is a
// tool line in go.mod: the main module requires only what the binary links.
func Doctor(env DoctorEnv) ([]Prerequisite, error) {
	want, mainTools, err := readGoMod(env.Repo, "go.mod")
	if err != nil {
		return nil, err
	}
	if len(mainTools) > 0 {
		return nil, fmt.Errorf("doctor: go.mod has tool lines %v; they belong in %s", mainTools, toolsModFile)
	}
	_, tools, err := readGoMod(env.Repo, toolsModFile)
	if err != nil {
		return nil, err
	}
	_, gitErr := env.LookPath("git")
	report := []Prerequisite{
		prerequisite(env.GOOS, "git", gitErr == nil),
		prerequisite(env.GOOS, "go", goPresent(env, want)),
	}
	goOK := report[1].Present
	for _, tool := range tools {
		p := Prerequisite{Name: tool, Present: goOK && env.ResolveTool(tool) == nil}
		if !p.Present {
			p.Install = "go mod download -modfile=" + toolsModFile
		}
		report = append(report, p)
	}
	return report, nil
}

// readGoMod reads the module file rel of repo and returns its go directive and tool entries.
func readGoMod(repo, rel string) (string, []string, error) {
	body, err := readRepoFile(repo, rel)
	if err != nil {
		return "", nil, fmt.Errorf("doctor: %w", err)
	}
	want, tools, err := parseGoMod(string(body))
	if err != nil {
		return "", nil, fmt.Errorf("doctor: %s: %w", rel, err)
	}
	return want, tools, nil
}

// prerequisite builds the entry of name, with its install command for goos when it is missing.
func prerequisite(goos, name string, present bool) Prerequisite {
	p := Prerequisite{Name: name, Present: present}
	if !present {
		p.Install = installCommands()[name][goos]
		if p.Install == "" {
			p.Install = "install " + name + " for " + goos + " from its official site"
		}
	}
	return p
}

// goPresent reports whether go is on the path at a version not below want.
func goPresent(env DoctorEnv, want string) bool {
	if _, err := env.LookPath("go"); err != nil {
		return false
	}
	v, err := env.GoVersion()
	if err != nil {
		return false
	}
	have, ok := toolchainVersion(v)
	return ok && version.Compare(have, want) >= 0
}

// parseGoMod returns the go directive as a Go version, such as go1.28rc1, and the tool entries of a
// go.mod body.
func parseGoMod(body string) (string, []string, error) {
	var goLine string
	var tools []string
	inTools := false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "//", 2)[0])
		switch {
		case inTools:
			inTools = line != ")"
			if inTools && line != "" {
				tools = append(tools, line)
			}
		case line == "tool (":
			inTools = true
		case strings.HasPrefix(line, "tool "):
			tools = append(tools, strings.TrimSpace(strings.TrimPrefix(line, "tool ")))
		case strings.HasPrefix(line, "go "):
			goLine = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	// go/version drops a -custom suffix, which a toolchain name may carry but a directive may not.
	want := "go" + goLine
	if strings.Contains(goLine, "-") || !version.IsValid(want) {
		return "", nil, fmt.Errorf("go directive %q is not a Go version", goLine)
	}
	return want, tools, nil
}

// toolchainVersion returns the Go version a toolchain reports, such as go1.27.9, ordered by
// go/version as Go's own gover orders it. A suffix after a space, such as the ` X:jsonv2`
// experiment list, is ignored. A devel toolchain, such as `devel go1.28-abc Mon Oct 5 …`, counts as
// its language version 1.28, as cmd/go reads it: it meets `go 1.28` but not `go 1.28rc1` or
// `go 1.28.0`. A version gover rejects is not a version.
func toolchainVersion(v string) (string, bool) {
	fields := strings.Fields(v)
	if len(fields) >= 2 && fields[0] == "devel" {
		fields = fields[1:]
	}
	if len(fields) == 0 || !version.IsValid(fields[0]) {
		return "", false
	}
	return fields[0], true
}
