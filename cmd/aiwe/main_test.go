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
			bin := filepath.Join(t.TempDir(), "aiwe")
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			build := exec.Command(goBin, "build", "-trimpath", "-ldflags", tc.ldflags, "-o", bin, ".")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}

			stdout, stderr, code := runBin(t, bin, "version", "--format", "json")
			if code != 0 {
				t.Fatalf("aiwe version --format json exit code = %d, want 0; stderr %q", code, stderr)
			}
			var env struct {
				Status string `json:"status"`
				Data   struct {
					Version *string `json:"version"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(stdout), &env); err != nil {
				t.Fatalf("aiwe version --format json is not one result envelope: %v; stdout %q", err, stdout)
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
				t.Fatalf("aiwe version exit code = %d, want 0; stderr %q", code, stderr)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("aiwe version text output %q misses %q", text, tc.want)
			}
		})
	}
}

func runBin(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
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
