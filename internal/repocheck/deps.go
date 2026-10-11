package repocheck

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ruleModuleNotAllowed is the rule name of check repo/deps (DR-41).
const ruleModuleNotAllowed = "module_not_allowed"

// mcpSDKModule is the official MCP Go SDK, the one module outside golang.org/x that DR-41 allows.
const mcpSDKModule = "github.com/modelcontextprotocol/go-sdk"

// Deps checks the modules linked into the binary, as `go list -deps -json ./cmd/...` prints its
// packages, against the allow-list of DR-41: the standard library, golang.org/x/* and the MCP Go
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
	sortFindings(found)
	return found, nil
}

// refusedModules returns the third-party modules that p imports directly and DR-41 does not allow.
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

// allowedModule reports whether DR-41 allows mod by name. The golang.org/x match is per path
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

// installCommands maps each prerequisite to its install command per OS; the tools come from go.mod
// and share one command.
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

// Doctor reports each prerequisite of this repository (SPEC §12.2): git, Go at least at the go.mod
// version, and each `go tool` entry of go.mod resolving. A missing one carries its install command
// for env.GOOS. A repository without a readable go.mod is an error.
func Doctor(env DoctorEnv) ([]Prerequisite, error) {
	body, err := readRepoFile(env.Repo, "go.mod")
	if err != nil {
		return nil, fmt.Errorf("doctor: %w", err)
	}
	want, tools, err := parseGoMod(string(body))
	if err != nil {
		return nil, fmt.Errorf("doctor: go.mod: %w", err)
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
			p.Install = "go get -tool " + tool
		}
		report = append(report, p)
	}
	return report, nil
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
func goPresent(env DoctorEnv, want []int) bool {
	if _, err := env.LookPath("go"); err != nil {
		return false
	}
	v, err := env.GoVersion()
	if err != nil {
		return false
	}
	have, pre, ok := parseGoVersion(strings.TrimPrefix(v, "go"))
	if !ok {
		return false
	}
	c := slices.Compare(padVersion(have, len(want)), padVersion(want, len(have)))
	return c > 0 || (c == 0 && !pre)
}

// parseGoMod returns the go directive's version and the tool entries of a go.mod body.
func parseGoMod(body string) ([]int, []string, error) {
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
	want, pre, ok := parseGoVersion(goLine)
	if !ok || pre {
		return nil, nil, fmt.Errorf("go directive %q is not a release version", goLine)
	}
	return want, tools, nil
}

// parseGoVersion reads a version such as 1.27.9 or 1.28rc1; pre reports a pre-release suffix,
// which sorts below the release of the same numbers.
func parseGoVersion(v string) (nums []int, pre bool, ok bool) {
	if v == "" {
		return nil, false, false
	}
	for _, part := range strings.Split(v, ".") {
		end := strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' })
		if end < 0 {
			end = len(part)
		}
		n, err := strconv.Atoi(part[:end])
		if pre || err != nil {
			return nil, false, false // a part after a pre-release suffix, or no leading digits
		}
		nums = append(nums, n)
		pre = end < len(part)
	}
	return nums, pre, true
}

// padVersion returns v extended with zeros to at least n parts, so 1.27 equals 1.27.0.
func padVersion(v []int, n int) []int {
	out := slices.Clone(v)
	for len(out) < n {
		out = append(out, 0)
	}
	return out
}
