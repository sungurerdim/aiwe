package repocheck_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// depPackage is a package of a third-party module.
func depPackage(module, path string, imports ...string) goListPackage {
	return goListPackage{ImportPath: path, Module: &goListModule{Path: module}, Imports: imports}
}

const (
	mcpSDK     = "github.com/modelcontextprotocol/go-sdk"
	jsonSchema = "github.com/google/jsonschema-go"
	xSys       = "golang.org/x/sys"
	cobra      = "github.com/spf13/cobra"
)

func TestDeps(t *testing.T) {
	t.Run("allow-listed modules and the modules they require pass", func(t *testing.T) {
		// Guards against a check that rejects what DR-41 allows: the standard library,
		// golang.org/x/*, the MCP Go SDK, and a module only the SDK requires.
		out := goListOutput(t,
			stdPackage("fmt"),
			stdPackage("encoding/json"),
			depPackage(jsonSchema, jsonSchema+"/jsonschema", "encoding/json"),
			depPackage(mcpSDK, mcpSDK+"/mcp", jsonSchema+"/jsonschema", "fmt"),
			depPackage(xSys, xSys+"/windows"),
			ownPackage("internal/core", "fmt", xSys+"/windows"),
			ownPackage("internal/mcp", own("internal/core"), mcpSDK+"/mcp"),
			ownPackage("cmd/zentra", own("internal/core"), own("internal/mcp")),
		)
		got, err := repocheck.Deps(out)
		if err != nil {
			t.Fatalf("Deps(known-good) error = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Fatalf("Deps(known-good) = %+v, want no finding", got)
		}
	})

	t.Run("a module outside the allow-list linked into the binary", func(t *testing.T) {
		out := goListOutput(t,
			stdPackage("fmt"),
			depPackage(cobra, cobra, "fmt"),
			ownPackage("internal/core", "fmt"),
			ownPackage("cmd/zentra", own("internal/core"), cobra),
		)
		got, err := repocheck.Deps(out)
		if err != nil {
			t.Fatalf("Deps error = %v, want nil", err)
		}
		if !hasFinding(got, "module_not_allowed", cobra) {
			t.Fatalf("Deps = %+v, want a module_not_allowed finding for %s", got, cobra)
		}
	})

	t.Run("a module outside the allow-list reached through an own package", func(t *testing.T) {
		const yaml = "go.yaml.in/yaml/v3"
		out := goListOutput(t,
			depPackage(yaml, yaml),
			ownPackage("internal/core", yaml),
			ownPackage("cmd/zentra", own("internal/core")),
		)
		got, err := repocheck.Deps(out)
		if err != nil {
			t.Fatalf("Deps error = %v, want nil", err)
		}
		if !hasFinding(got, "module_not_allowed", yaml) {
			t.Fatalf("Deps = %+v, want a module_not_allowed finding for %s", got, yaml)
		}
	})

	t.Run("a look-alike of golang.org/x is not allowed", func(t *testing.T) {
		// Guards against a prefix match that admits a typosquat such as golang.org/xx.
		const lookAlike = "golang.org/xx/sys"
		out := goListOutput(t,
			depPackage(lookAlike, lookAlike),
			ownPackage("cmd/zentra", lookAlike),
		)
		got, err := repocheck.Deps(out)
		if err != nil {
			t.Fatalf("Deps error = %v, want nil", err)
		}
		if !hasFinding(got, "module_not_allowed", lookAlike) {
			t.Fatalf("Deps = %+v, want a module_not_allowed finding for %s", got, lookAlike)
		}
	})

	t.Run("empty go list output is an error, not a pass", func(t *testing.T) {
		// Guards against absence read as evidence (DR-13).
		got, err := repocheck.Deps(nil)
		if err == nil {
			t.Fatalf("Deps(empty) = %+v, nil error; want an error", got)
		}
	})
}

// doctorRepo writes a go.mod with the given go line and tool entries into a fresh directory.
func doctorRepo(t *testing.T, goLine string) string {
	t.Helper()
	dir := t.TempDir()
	mod := strings.Join([]string{
		"module " + fixtureModule,
		"",
		"go " + goLine,
		"",
		"tool github.com/goreleaser/goreleaser/v2",
		"",
		"tool (",
		"\tgolang.org/x/tools/cmd/deadcode",
		"\tgolang.org/x/vuln/cmd/govulncheck",
		")",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}

// doctorTools are the tool entries doctorRepo writes.
func doctorTools() []string {
	return []string{
		"github.com/goreleaser/goreleaser/v2",
		"golang.org/x/tools/cmd/deadcode",
		"golang.org/x/vuln/cmd/govulncheck",
	}
}

var errNotFound = errors.New("executable file not found in PATH")

// doctorEnv is an environment where every prerequisite is present at goVersion.
func doctorEnv(repo, goos, goVersion string) repocheck.DoctorEnv {
	return repocheck.DoctorEnv{
		Repo:        repo,
		GOOS:        goos,
		LookPath:    func(name string) (string, error) { return filepath.Join("bin", name), nil },
		GoVersion:   func() (string, error) { return goVersion, nil },
		ResolveTool: func(string) error { return nil },
	}
}

// prerequisite returns the named entry of a doctor report.
func prerequisite(t *testing.T, got []repocheck.Prerequisite, name string) repocheck.Prerequisite {
	t.Helper()
	for _, p := range got {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("Doctor = %+v, want an entry %q", got, name)
	return repocheck.Prerequisite{}
}

func TestDoctor(t *testing.T) {
	oses := []string{"windows", "darwin", "linux"}

	t.Run("every prerequisite present", func(t *testing.T) {
		repo := doctorRepo(t, "1.27.9")
		for _, goVersion := range []string{"go1.27.9", "go1.27.10", "go1.28.0"} {
			got, err := repocheck.Doctor(doctorEnv(repo, "linux", goVersion))
			if err != nil {
				t.Fatalf("Doctor(%s) error = %v, want nil", goVersion, err)
			}
			for _, name := range append([]string{"git", "go"}, doctorTools()...) {
				if p := prerequisite(t, got, name); !p.Present {
					t.Errorf("Doctor(%s) %s = %+v, want present", goVersion, name, p)
				}
			}
		}
	})

	// installs checks that a missing prerequisite names its install command for each OS.
	installs := func(t *testing.T, name string, env func(goos string) repocheck.DoctorEnv) {
		t.Helper()
		seen := map[string]string{}
		for _, goos := range oses {
			got, err := repocheck.Doctor(env(goos))
			if err != nil {
				t.Fatalf("Doctor(%s) error = %v, want nil", goos, err)
			}
			p := prerequisite(t, got, name)
			if p.Present {
				t.Fatalf("Doctor(%s) %s = %+v, want missing", goos, name, p)
			}
			if !strings.Contains(p.Install, name) {
				t.Fatalf("Doctor(%s) %s install = %q, want a command that names %s", goos, name, p.Install, name)
			}
			if other, dup := seen[p.Install]; dup {
				t.Fatalf("Doctor(%s) and Doctor(%s) give the same %s install %q, want one per OS",
					goos, other, name, p.Install)
			}
			seen[p.Install] = goos
		}
	}

	t.Run("git missing names its install command for the running OS", func(t *testing.T) {
		repo := doctorRepo(t, "1.27.9")
		installs(t, "git", func(goos string) repocheck.DoctorEnv {
			env := doctorEnv(repo, goos, "go1.27.9")
			env.LookPath = func(name string) (string, error) {
				if name == "git" {
					return "", errNotFound
				}
				return filepath.Join("bin", name), nil
			}
			return env
		})
	})

	t.Run("go missing names its install command for the running OS", func(t *testing.T) {
		repo := doctorRepo(t, "1.27.9")
		installs(t, "go", func(goos string) repocheck.DoctorEnv {
			env := doctorEnv(repo, goos, "")
			env.LookPath = func(name string) (string, error) {
				if name == "go" {
					return "", errNotFound
				}
				return filepath.Join("bin", name), nil
			}
			env.GoVersion = func() (string, error) { return "", errNotFound }
			return env
		})
	})

	t.Run("go below the go.mod version names its install command for the running OS", func(t *testing.T) {
		// Guards against a lexical compare: go1.27.10 is newer than 1.27.9, go1.27.8 is older.
		repo := doctorRepo(t, "1.27.9")
		for _, old := range []string{"go1.27.8", "go1.26.12"} {
			installs(t, "go", func(goos string) repocheck.DoctorEnv {
				return doctorEnv(repo, goos, old)
			})
		}
	})

	t.Run("a go tool entry that does not resolve is reported missing", func(t *testing.T) {
		const broken = "golang.org/x/vuln/cmd/govulncheck"
		env := doctorEnv(doctorRepo(t, "1.27.9"), "linux", "go1.27.9")
		env.ResolveTool = func(tool string) error {
			if tool == broken {
				return errors.New("no such tool")
			}
			return nil
		}
		got, err := repocheck.Doctor(env)
		if err != nil {
			t.Fatalf("Doctor error = %v, want nil", err)
		}
		if p := prerequisite(t, got, broken); p.Present {
			t.Fatalf("Doctor %s = %+v, want missing", broken, p)
		}
		for _, name := range doctorTools() {
			if name != broken && !prerequisite(t, got, name).Present {
				t.Errorf("Doctor %s missing, want present: only %s fails to resolve", name, broken)
			}
		}
	})

	t.Run("a go version with an experiment suffix is present", func(t *testing.T) {
		// Guards against a parse that rejects the GOEXPERIMENT suffix `go version` prints.
		got, err := repocheck.Doctor(doctorEnv(doctorRepo(t, "1.27.9"), "linux", "go1.27.9 X:jsonv2"))
		if err != nil {
			t.Fatalf("Doctor error = %v, want nil", err)
		}
		if p := prerequisite(t, got, "go"); !p.Present {
			t.Fatalf("Doctor go = %+v, want present", p)
		}
	})

	t.Run("a pre-release orders below its release and above the previous release", func(t *testing.T) {
		// Guards against a compare that drops the rc suffix: go1.28rc2 comes before go1.28.0.
		cases := []struct {
			goLine, have string
			present      bool
		}{
			{"1.28.0", "go1.28rc2", false},
			{"1.27.9", "go1.28rc2", true},
			{"1.28rc1", "go1.28rc2", true},
			{"1.28rc2", "go1.28rc1", false},
			{"1.28rc1", "go1.28.0", true},
			{"1.28rc1", "go1.27.9", false},
		}
		for _, tc := range cases {
			got, err := repocheck.Doctor(doctorEnv(doctorRepo(t, tc.goLine), "linux", tc.have))
			if err != nil {
				t.Fatalf("Doctor(go %s, %s) error = %v, want nil", tc.goLine, tc.have, err)
			}
			if p := prerequisite(t, got, "go"); p.Present != tc.present {
				t.Errorf("Doctor(go %s, %s) go = %+v, want present=%v", tc.goLine, tc.have, p, tc.present)
			}
		}
	})

	t.Run("a go tool entry that does not resolve names a command that leaves go.mod unchanged", func(t *testing.T) {
		// Guards against `go get -tool`, which rewrites go.mod; `go mod download` only fetches
		// the modules go.mod already pins.
		env := doctorEnv(doctorRepo(t, "1.27.9"), "linux", "go1.27.9")
		env.ResolveTool = func(string) error { return errors.New("no such tool") }
		got, err := repocheck.Doctor(env)
		if err != nil {
			t.Fatalf("Doctor error = %v, want nil", err)
		}
		for _, name := range doctorTools() {
			p := prerequisite(t, got, name)
			if !strings.HasPrefix(p.Install, "go mod download") || strings.Contains(p.Install, "go get") {
				t.Errorf("Doctor %s install = %q, want a `go mod download` command", name, p.Install)
			}
		}
	})

	t.Run("a repository without go.mod is an error", func(t *testing.T) {
		got, err := repocheck.Doctor(doctorEnv(t.TempDir(), "linux", "go1.27.9"))
		if err == nil {
			t.Fatalf("Doctor(no go.mod) = %+v, nil error; want an error", got)
		}
	})
}
