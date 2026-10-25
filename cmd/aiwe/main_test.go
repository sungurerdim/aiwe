package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/install"
)

// TestVersion builds the real binary the way GoReleaser does: its default
// ldflags inject the version with -X main.version=<tag> (SPEC §6.3, §12.3).
func TestVersion(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go toolchain not on PATH: %v", err)
	}
	tests := []struct {
		name    string
		ldflags string
		want    string
	}{
		{
			name:    "prints a version other than the one injected at build time",
			ldflags: "-X main.version=v1.4.2",
			want:    "v1.4.2",
		},
		{
			name:    "prints a blank or made-up version when none was injected",
			ldflags: "",
			want:    "unknown",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), core.Product)
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			build := exec.CommandContext(t.Context(), goBin, "build", "-trimpath", "-ldflags", tc.ldflags, "-o", bin, ".")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}

			stdout, stderr, code := runBin(t, bin, "version", "--format", "json")
			if code != 0 {
				t.Fatalf("%s version --format json exit code = %d, want 0; stderr %q", core.Product, code, stderr)
			}
			var env struct {
				Status string `json:"status"`
				Data   struct {
					Version *string `json:"version"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(stdout), &env); err != nil {
				t.Fatalf("%s version --format json is not one result envelope: %v; stdout %q", core.Product, err, stdout)
			}
			if env.Status != "ok" {
				t.Fatalf("envelope status = %q, want %q; stdout %q", env.Status, "ok", stdout)
			}
			if env.Data.Version == nil {
				t.Fatalf("envelope data has no version key; stdout %q", stdout)
			}
			if *env.Data.Version != tc.want {
				t.Fatalf("data.version = %q, want %q", *env.Data.Version, tc.want)
			}

			text, stderr, code := runBin(t, bin, "version")
			if code != 0 {
				t.Fatalf("%s version exit code = %d, want 0; stderr %q", core.Product, code, stderr)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("%s version text output %q misses %q", core.Product, text, tc.want)
			}
		})
	}
}

// TestRegistryServesUnitCheck proves the binary's registry serves `unit check`: a call without a body
// reaches the engine command and gets its usage refusal, not an unknown-command error. A registry
// that leaves the engine commands out ships a binary in which no session can check a unit body
// (SPEC §5.7, §7.2).
func TestRegistryServesUnitCheck(t *testing.T) {
	var out, stream bytes.Buffer
	code := core.RunCLI(t.Context(), registry(), []string{"unit", "check", "--format", "json"}, &out, &stream)
	if code != core.StatusUsage.ExitCode() {
		t.Fatalf("unit check without a body exit code = %d, want %d; stdout %q",
			code, core.StatusUsage.ExitCode(), out.String())
	}
	var env struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("unit check printed no JSON envelope: %v; stdout %q", err, out.String())
	}
	if !strings.Contains(env.Detail, "pass the unit body") {
		t.Errorf("unit check detail = %q, want the engine's refusal that asks for the unit body", env.Detail)
	}
}

// TestRegistryServesEveryInstalledHook proves each hook command that `aiwe install` writes into a
// harness is a command of this binary's registry, and that `install` itself is registered
// (DR-16). An entry that calls an unregistered command would make the harness fail on every event.
func TestRegistryServesEveryInstalledHook(t *testing.T) {
	registered := map[string]bool{}
	for _, c := range registry() {
		registered[strings.TrimSpace(c.Object+" "+c.Verb)] = true
	}
	if !registered["install"] {
		t.Errorf("the registry has no %q command: no harness could be wired", "install")
	}
	for _, h := range install.Harnesses() {
		for _, e := range h.Entries() {
			name := strings.TrimPrefix(e.Command, core.Product+" ")
			if !registered[name] {
				t.Errorf("harness %s event %s calls %q, which the registry does not serve", h.Name, e.Event, e.Command)
			}
		}
	}
}

func runBin(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
		return out.String(), errOut.String(), exitErr.ExitCode()
	}
	return out.String(), errOut.String(), 0
}
