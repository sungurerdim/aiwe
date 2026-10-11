package core_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestArgs pins SPEC §5.12: every path parameter resolves against --repo; a relative path without
// --repo is a usage error; ResolvePath never uses the process working directory. The one exception,
// a relative --repo at the CLI boundary, is pinned by TestRepoFlag.
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

// TestRepoFlag pins the one exception of SPEC §5.12: a relative --repo resolves once, at the CLI
// boundary, against the working directory; the command sees an absolute repo and every other
// value as given, and a later relative path resolves against that repo, never the working directory.
func TestRepoFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	elsewhere := filepath.Join(t.TempDir(), "aiwe")

	run := func(t *testing.T, args ...string) (int, core.Input, bool, string) {
		t.Helper()
		var got core.Input
		ran := false
		cmds := []core.Command{{Module: "gates", Object: "gates", Verb: "gate", Params: []string{"repo", "step", "format"},
			Purpose: "run the gate",
			Run: func(_ context.Context, in core.Input) (core.Result[any], error) {
				got, ran = in, true
				return core.Result[any]{Status: core.StatusOK, Outcome: core.OutcomeOK}, nil
			}}}
		var out bytes.Buffer
		code := core.RunCLI(context.Background(), cmds, append([]string{"gates", "gate", "--format", "json"}, args...), &out, io.Discard)
		return code, got, ran, out.String()
	}

	resolved := []struct {
		name string
		repo string
		want string
	}{
		{name: "dot --repo refused or passed on as a relative path", repo: ".", want: cwd},
		{name: "relative --repo passed on as a relative path", repo: "aiwe", want: filepath.Join(cwd, "aiwe")},
		{name: "relative --repo with dot segments not cleaned", repo: "./x/../aiwe", want: filepath.Join(cwd, "aiwe")},
		{name: "relative Unicode --repo mangled", repo: "depolar/özet", want: filepath.Join(cwd, "depolar", "özet")},
		{name: "absolute --repo rewritten against the working directory", repo: elsewhere, want: elsewhere},
	}
	for _, tc := range resolved {
		t.Run(tc.name, func(t *testing.T) {
			code, in, ran, raw := run(t, "--repo", tc.repo, "--step", "lint")
			if code != 0 || !ran {
				t.Fatalf("--repo %q: exit %d ran %v, want exit 0 and the command run\n%s", tc.repo, code, ran, raw)
			}
			if got := in.Params["repo"]; got != tc.want {
				t.Errorf("--repo %q reached the command as %q, want %q", tc.repo, got, tc.want)
			}
			if got := in.Params["step"]; got != "lint" {
				t.Errorf("--step lint reached the command as %q; only --repo resolves at the CLI boundary", got)
			}
			p, err := core.ResolvePath(in.Params["repo"], "docs/SPEC.md")
			if err != nil {
				t.Fatalf("ResolvePath(%q, docs/SPEC.md) after the CLI boundary: %v", in.Params["repo"], err)
			}
			if want := filepath.Join(tc.want, "docs", "SPEC.md"); p != want {
				t.Errorf("a path after --repo %q resolved to %q, want %q under the repo", tc.repo, p, want)
			}
		})
	}

	t.Run("working directory used as repo when --repo is absent", func(t *testing.T) {
		code, in, ran, raw := run(t, "--step", "lint")
		if code != 0 || !ran {
			t.Fatalf("no --repo: exit %d ran %v, want exit 0 and the command run\n%s", code, ran, raw)
		}
		if got, ok := in.Params["repo"]; ok {
			t.Errorf("no --repo reached the command as repo %q; the working directory is used only for a given --repo", got)
		}
	})

	t.Run("empty --repo resolved to the working directory", func(t *testing.T) {
		code, _, ran, raw := run(t, "--repo=")
		if code != core.StatusUsage.ExitCode() || ran {
			t.Errorf("--repo= exit %d ran %v, want a usage refusal (exit %d) before the command runs\n%s",
				code, ran, core.StatusUsage.ExitCode(), raw)
		}
	})
}
