package core_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestArgs pins SPEC §5.12: every path parameter resolves against --repo; a relative path without
// --repo is a usage error; the process working directory is never used.
func TestArgs(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "aiwe")
	elsewhere := filepath.Join(t.TempDir(), "state", "ledger.jsonl")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	resolved := []struct {
		name string
		repo string
		path string
		want string
	}{
		{name: "relative path resolved against the working directory instead of --repo",
			repo: repo, path: "docs/SPEC.md", want: filepath.Join(repo, "docs", "SPEC.md")},
		{name: "relative path with dot segments not cleaned",
			repo: repo, path: "./internal/../internal/core/result.go", want: filepath.Join(repo, "internal", "core", "result.go")},
		{name: "relative Unicode path mangled",
			repo: repo, path: "belgeler/özet.md", want: filepath.Join(repo, "belgeler", "özet.md")},
		{name: "dot path not resolved to the repository root",
			repo: repo, path: ".", want: repo},
		{name: "absolute path rewritten under --repo",
			repo: repo, path: elsewhere, want: elsewhere},
		{name: "absolute path refused without --repo",
			repo: "", path: elsewhere, want: elsewhere},
	}
	for _, tc := range resolved {
		t.Run(tc.name, func(t *testing.T) {
			got, err := core.ResolvePath(tc.repo, tc.path)
			if err != nil {
				t.Fatalf("ResolvePath(%q, %q) error = %v, want nil", tc.repo, tc.path, err)
			}
			if got != tc.want {
				t.Errorf("ResolvePath(%q, %q) = %q, want %q", tc.repo, tc.path, got, tc.want)
			}
			if !filepath.IsAbs(tc.path) && got == filepath.Join(cwd, tc.path) {
				t.Errorf("ResolvePath(%q, %q) = %q resolved against the working directory", tc.repo, tc.path, got)
			}
		})
	}

	refused := []struct {
		name string
		repo string
		path string
	}{
		{name: "relative path accepted without --repo", repo: "", path: "docs/SPEC.md"},
		{name: "relative Unicode path accepted without --repo", repo: "", path: "belgeler/özet.md"},
		{name: "relative --repo resolved against the working directory", repo: "aiwe", path: "docs/SPEC.md"},
		{name: "empty path accepted as the repository root", repo: repo, path: ""},
		{name: "empty path accepted without --repo", repo: "", path: ""},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			got, err := core.ResolvePath(tc.repo, tc.path)
			if err == nil {
				t.Fatalf("ResolvePath(%q, %q) = %q, want a usage error", tc.repo, tc.path, got)
			}
			r := core.ErrorResult(err)
			if r.Status != core.StatusUsage {
				t.Errorf("ResolvePath(%q, %q) error maps to status %q, want %q", tc.repo, tc.path, r.Status, core.StatusUsage)
			}
			if got := r.Status.ExitCode(); got != 2 {
				t.Errorf("ResolvePath(%q, %q) error exits %d, want 2", tc.repo, tc.path, got)
			}
		})
	}
}
