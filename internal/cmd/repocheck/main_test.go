package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// checkNames are the commands this entry point must serve: the doctor and the checks of #14
// (SPEC §12.2). ci-shape arrives with unit 0.10 and is not required here.
func checkNames() []string {
	return []string{"doctor", "rules", "size", "boundaries", "source-rules", "deps"}
}

// TestRepocheckCommands catches a check of #14 or the doctor that is not registered in the one
// command list, and a `repocheck doctor` run that exits 0 while a prerequisite is missing (§5.7,
// §12.2).
func TestRepocheckCommands(t *testing.T) {
	t.Run("registers the doctor and every check of #14 in one valid registry", func(t *testing.T) {
		cmds := commands()
		if err := core.CheckParams(core.Dictionary(), cmds); err != nil {
			t.Fatalf("commands() fails the parameter dictionary: %v", err)
		}
		for _, name := range checkNames() {
			i := slices.IndexFunc(cmds, func(c core.Command) bool { return c.Object == name && c.Verb == "" })
			if i < 0 {
				t.Errorf("commands() has no %q command; `repocheck %s` would be refused", name, name)
				continue
			}
			c := cmds[i]
			if c.Run == nil {
				t.Errorf("command %q has no Run function", name)
			}
			if !slices.Contains(c.Params, "repo") {
				t.Errorf("command %q params = %v, want them to include repo", name, c.Params)
			}
			if c.Purpose == "" {
				t.Errorf("command %q has no purpose for --help", name)
			}
		}
	})

	tests := []struct {
		name     string
		goMod    string
		toolsMod string
		wantZero bool
		wantText []string
	}{
		{
			name:     "exits 0 when git, Go and every tool are present",
			goMod:    "module example.com/fixture\n\ngo 1.24\n",
			toolsMod: "module example.com/fixture/tools\n\ngo 1.24\n",
			wantZero: true,
			wantText: []string{"git", "go"},
		},
		{
			name:  "exits non-zero when a go tool entry does not resolve",
			goMod: "module example.com/fixture\n\ngo 1.24\n",
			toolsMod: "module example.com/fixture/tools\n\ngo 1.24\n\n" +
				"tool example.invalid/absent/cmd/absenttool\n",
			wantZero: false,
			wantText: []string{"example.invalid/absent/cmd/absenttool", "go mod download -modfile=tools/go.mod"},
		},
		{
			name:     "exits non-zero when Go is below the go.mod version",
			goMod:    "module example.com/fixture\n\ngo 1.999\n",
			toolsMod: "module example.com/fixture/tools\n\ngo 1.24\n",
			wantZero: false,
			wantText: []string{"go"},
		},
	}
	for _, tc := range tests {
		t.Run("doctor "+tc.name, func(t *testing.T) {
			// No module download and no toolchain switch: the doctor must judge the local machine.
			t.Setenv("GOPROXY", "off")
			t.Setenv("GOTOOLCHAIN", "local")
			t.Setenv("GOFLAGS", "-mod=readonly")
			repo := t.TempDir()
			if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(tc.goMod), 0o600); err != nil {
				t.Fatalf("write go.mod: %v", err)
			}
			if err := os.Mkdir(filepath.Join(repo, "tools"), 0o700); err != nil {
				t.Fatalf("create tools: %v", err)
			}
			if err := os.WriteFile(filepath.Join(repo, "tools", "go.mod"), []byte(tc.toolsMod), 0o600); err != nil {
				t.Fatalf("write tools/go.mod: %v", err)
			}
			var out bytes.Buffer
			code := core.RunCLI(context.Background(), commands(), []string{"doctor", "--repo", repo}, &out, io.Discard)
			if tc.wantZero && code != 0 {
				t.Fatalf("repocheck doctor exit code = %d, want 0; output %q", code, out.String())
			}
			if !tc.wantZero && code == 0 {
				t.Fatalf("repocheck doctor exit code = 0 with a missing prerequisite; output %q", out.String())
			}
			for _, want := range tc.wantText {
				if !strings.Contains(out.String(), want) {
					t.Errorf("repocheck doctor output %q misses %q", out.String(), want)
				}
			}
		})
	}

	t.Run("doctor refuses a repository without go.mod", func(t *testing.T) {
		var out bytes.Buffer
		code := core.RunCLI(context.Background(), commands(), []string{"doctor", "--repo", t.TempDir()}, &out, io.Discard)
		if code == 0 {
			t.Fatalf("repocheck doctor exit code = 0 without go.mod; output %q", out.String())
		}
	})
}

// allChecks are the checks `repocheck all` runs today; ci-shape is reported, never run (§12.2).
func allChecks() []string {
	return []string{"rules", "size", "boundaries", "source-rules", "deps"}
}

// allReport is the data of one `repocheck all --format json` envelope: one entry per check.
type allReport struct {
	Status string `json:"status"`
	Data   struct {
		Checks []struct {
			Check  string `json:"check"`
			Result string `json:"result"`
		} `json:"checks"`
	} `json:"data"`
}

// writeFixture writes a small module whose size check is red: one source file over 400 lines.
func writeFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	var big strings.Builder
	big.WriteString("package main\n\nfunc main() {}\n")
	for range 420 {
		big.WriteString("\n// filler line that pushes the file over the source size limit\n")
	}
	files := map[string]string{
		"go.mod":                      "module example.com/fixture\n\ngo 1.24\n",
		"cmd/fixture/main.go":         big.String(),
		"internal/fixture/fixture.go": "package fixture\n\n// Answer returns a fixed number.\nfunc Answer() int { return 42 }\n",
	}
	for rel, body := range files {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return repo
}

// TestRepocheckAll catches a gate `repo` step (`repocheck all`, §12.2) that skips a check of #14,
// reports the missing ci-shape as green, or exits 0 while a check is red.
func TestRepocheckAll(t *testing.T) {
	t.Run("registers all", func(t *testing.T) {
		cmds := commands()
		i := slices.IndexFunc(cmds, func(c core.Command) bool { return c.Object == "all" && c.Verb == "" })
		if i < 0 {
			t.Fatal("commands() has no \"all\" command; the gate step `repocheck all` would be refused")
		}
		if cmds[i].Run == nil || !slices.Contains(cmds[i].Params, "repo") || !slices.Contains(cmds[i].Params, "format") {
			t.Fatalf("all command = %+v, want a Run function and params repo and format", cmds[i])
		}
	})

	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "-mod=readonly")
	repo := writeFixture(t)
	var out bytes.Buffer
	code := core.RunCLI(context.Background(), commands(), []string{"all", "--repo", repo, "--format", "json"}, &out, io.Discard)
	var rep allReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("repocheck all --format json is not one result envelope: %v; output %q", err, out.String())
	}
	results := map[string]string{}
	for _, c := range rep.Data.Checks {
		if _, dup := results[c.Check]; dup {
			t.Errorf("check %q reported twice; output %q", c.Check, out.String())
		}
		results[c.Check] = c.Result
	}

	t.Run("runs every check of #14 and skips none", func(t *testing.T) {
		for _, name := range allChecks() {
			res, ok := results[name]
			if !ok {
				t.Errorf("repocheck all did not report check %q; output %q", name, out.String())
				continue
			}
			if res == "" || res == "not_measured" || res == "skipped" {
				t.Errorf("check %q result = %q, want it run (green, red or error)", name, res)
			}
		}
	})

	t.Run("reports ci-shape as not_measured, never green", func(t *testing.T) {
		if got, ok := results["ci-shape"]; !ok || got != "not_measured" {
			t.Fatalf("ci-shape result = %q (reported %v), want not_measured; output %q", got, ok, out.String())
		}
	})

	t.Run("exits non-zero when a check is red", func(t *testing.T) {
		if results["size"] != "red" {
			t.Fatalf("size result = %q on a source file over 400 lines, want red; output %q",
				results["size"], out.String())
		}
		if code == 0 {
			t.Fatalf("repocheck all exit code = 0 with check size red; output %q", out.String())
		}
		if rep.Status == "ok" {
			t.Fatalf("envelope status = ok with check size red; output %q", out.String())
		}
	})

	// A check that read zero files measured nothing; green would be a fail-open (DR-13).
	for _, name := range []string{"size", "source-rules"} {
		t.Run(name+" is not green when it reads zero Go files", func(t *testing.T) {
			empty := t.TempDir()
			if err := os.WriteFile(filepath.Join(empty, "go.mod"),
				[]byte("module example.com/empty\n\ngo 1.24\n"), 0o600); err != nil {
				t.Fatalf("write go.mod: %v", err)
			}
			t.Chdir(empty)
			var buf bytes.Buffer
			code := core.RunCLI(context.Background(), commands(),
				[]string{name, "--repo", ".", "--format", "json"}, &buf, io.Discard)
			if code == 0 {
				t.Fatalf("repocheck %s exit code = 0 on a module without Go files; output %q", name, buf.String())
			}
			var one allReport
			if err := json.Unmarshal(buf.Bytes(), &one); err != nil {
				t.Fatalf("repocheck %s --format json is not one result envelope: %v; output %q", name, err, buf.String())
			}
			for _, c := range one.Data.Checks {
				if c.Result == "green" {
					t.Fatalf("check %s result = green after reading zero Go files; output %q", c.Check, buf.String())
				}
			}
		})
	}
}

// TestRepocheckAllWorkflowLink catches a ci-shape check that follows a .github/workflows link out
// of the repository and measures the outside ci.yml instead of refusing it (#214).
func TestRepocheckAllWorkflowLink(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "-mod=readonly")
	repo := writeFixture(t)
	outside := t.TempDir()
	ci := "name: ci\non: [push]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
	if err := os.WriteFile(filepath.Join(outside, "ci.yml"), []byte(ci), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".github"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, ".github", "workflows")
	if runtime.GOOS == "windows" {
		// A junction needs no privilege, so the case runs on Windows too.
		if out, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J %s %s: %v: %s", link, outside, err, out)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := core.RunCLI(t.Context(), commands(), []string{"all", "--repo", repo, "--format", "json"}, &out, io.Discard)
	var rep allReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("repocheck all --format json is not one result envelope: %v; output %q", err, out.String())
	}
	for _, c := range rep.Data.Checks {
		if c.Check == "ci-shape" && c.Result != "error" {
			t.Fatalf("ci-shape result = %q through a workflows link out of the repository, want error; output %q",
				c.Result, out.String())
		}
	}
	if code == 0 {
		t.Fatalf("repocheck all exit code = 0 with ci-shape reading outside the repository; output %q", out.String())
	}
}
