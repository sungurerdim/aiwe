package adapters

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// allTools allows every tool one run may use, so a test of the tool set offers them all
// (SPEC §10.2, O14).
func allTools() ToolPermissions {
	return ToolPermissions{Read: true, Write: true, Edit: true, List: true, Shell: true}
}

// commandEnv returns the environment a shell call needs to find a program: PATH, and on Windows
// the extension list and the system root. It leaves out the home, the profile and every repository
// pointer, so a direct tool call can run a command without inheriting the parent (SPEC §6.5, DR-10).
func commandEnv(t *testing.T) []string {
	t.Helper()
	if os.Getenv("PATH") == "" {
		t.Fatal("the test process has no PATH, so a shell command cannot be found")
	}
	var env []string
	for _, name := range []string{"PATH", "PATHEXT", "SYSTEMROOT"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// cloneFixture returns a clone whose tools are framed by a base directory: the clone holds one
// file inside it and the base holds one file outside it, so a test proves a tool stays inside the
// clone (SPEC §10.2, O2).
func cloneFixture(t *testing.T) (base, root, outside string) {
	t.Helper()
	base = t.TempDir()
	root = filepath.Join(base, "clone")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("make the clone root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("seed the clone: %v", err)
	}
	outside = filepath.Join(base, "secret.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
		t.Fatalf("seed the file outside the clone: %v", err)
	}
	return base, root, outside
}

// TestToolsRunInsideTheClone catches a tool that cannot run against the clone at all: read, write,
// edit, list and shell each run inside the root, so one turn changes the clone and observes it
// (SPEC §10.2, O2, O14).
func TestToolsRunInsideTheClone(t *testing.T) {
	_, root, _ := cloneFixture(t)
	tools := NewTools(root, allTools())
	ctx := context.Background()

	got, err := tools.Do(ctx, ToolRead, map[string]any{"path": "note.txt"})
	if err != nil || got != "inside" {
		t.Errorf("read of a file inside the clone = %q, %v, want %q and no error (SPEC §10.2)", got, err, "inside")
	}
	if _, err := tools.Do(ctx, ToolWrite, map[string]any{"path": "made.txt", "content": "made"}); err != nil {
		t.Errorf("write inside the clone: %v; the loop cannot change the clone (SPEC §10.2)", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "made.txt")); err != nil || string(data) != "made" {
		t.Errorf("write left %q, %v, want %q on disk (SPEC §10.2)", data, err, "made")
	}
	if _, err := tools.Do(ctx, ToolEdit, map[string]any{"path": "note.txt", "old": "inside", "new": "edited"}); err != nil {
		t.Errorf("edit inside the clone: %v; the loop cannot change the clone (SPEC §10.2)", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "note.txt")); string(data) != "edited" {
		t.Errorf("edit left %q, want %q (SPEC §10.2)", data, "edited")
	}
	got, err = tools.Do(ctx, ToolList, map[string]any{"path": "."})
	if err != nil || !strings.Contains(got, "note.txt") {
		t.Errorf("list of the clone = %q, %v, want the entries of the clone (SPEC §10.2)", got, err)
	}
	tools.bindRunEnv(commandEnv(t))
	got, err = tools.Do(ctx, ToolShell, map[string]any{"command": "git --version"})
	if err != nil || strings.TrimSpace(got) == "" {
		t.Errorf("shell in the clone = %q, %v, want the output of the command (SPEC §10.2)", got, err)
	}
}

// TestToolsRefuseAPathOutsideTheClone catches a tool that follows a path out of the clone: read,
// write, edit and list each refuse an argument outside the root, a refused write leaves no file
// behind and a refused call never changes the file outside (SPEC §10.2, O2).
func TestToolsRefuseAPathOutsideTheClone(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		args  func(outside string) map[string]any
		catch string
	}{
		{"read of a relative escape", ToolRead,
			func(string) map[string]any { return map[string]any{"path": "../secret.txt"} },
			"a read of ../secret.txt leaves the clone"},
		{"read of an absolute path", ToolRead,
			func(outside string) map[string]any { return map[string]any{"path": outside} },
			"a read of an absolute path outside the clone"},
		{"write outside the clone", ToolWrite,
			func(string) map[string]any { return map[string]any{"path": "../made.txt", "content": "x"} },
			"a write of ../made.txt leaves the clone"},
		{"edit outside the clone", ToolEdit,
			func(string) map[string]any {
				return map[string]any{"path": "../secret.txt", "old": "outside-secret", "new": "x"}
			},
			"an edit of ../secret.txt leaves the clone"},
		{"list outside the clone", ToolList,
			func(string) map[string]any { return map[string]any{"path": ".."} },
			"a list of .. leaves the clone"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, root, outside := cloneFixture(t)
			tools := NewTools(root, allTools())
			if _, err := tools.Do(context.Background(), tc.tool, tc.args(outside)); err == nil {
				t.Errorf("%s accepted a path outside the clone; %s (SPEC §10.2, O2)", tc.tool, tc.catch)
			}
			if _, err := os.Stat(filepath.Join(base, "made.txt")); err == nil {
				t.Errorf("%s left a file outside the clone; %s (SPEC §10.2, O2)", tc.tool, tc.catch)
			}
			if data, _ := os.ReadFile(outside); string(data) != "outside-secret" {
				t.Errorf("%s changed the file outside the clone to %q; %s (SPEC §10.2, O2)", tc.tool, data, tc.catch)
			}
		})
	}
}

// cloneLink makes link a directory link to target: a symlink where the OS allows it, else on
// Windows a junction, which needs no privilege, so the escape case runs on every OS.
func cloneLink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if runtime.GOOS != "windows" {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
	if out, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

// TestToolsRefuseALinkOutOfTheClone catches a tool that follows a link inside the clone to a
// directory outside it: the path itself names no "..", so only the file-system root of the clone
// can refuse it. Read, write, edit and list each refuse the path through the link, a refused write
// leaves no file outside and the file outside is never changed (SPEC §5.6, §10.2, O2).
func TestToolsRefuseALinkOutOfTheClone(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		args  map[string]any
		catch string
	}{
		{"read through the link", ToolRead, map[string]any{"path": "escape/secret.txt"},
			"a read of escape/secret.txt follows the link out of the clone"},
		{"write through the link", ToolWrite, map[string]any{"path": "escape/made.txt", "content": "x"},
			"a write of escape/made.txt follows the link out of the clone"},
		{"edit through the link", ToolEdit,
			map[string]any{"path": "escape/secret.txt", "old": "outside-secret", "new": "x"},
			"an edit of escape/secret.txt follows the link out of the clone"},
		{"list through the link", ToolList, map[string]any{"path": "escape"},
			"a list of escape follows the link out of the clone"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, root, _ := cloneFixture(t)
			// The link points at a directory apart from the clone's own tree, so a cleanup that
			// followed it would never walk back into the clone.
			vault := t.TempDir()
			outside := filepath.Join(vault, "secret.txt")
			if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
				t.Fatalf("seed the file outside the clone: %v", err)
			}
			cloneLink(t, vault, filepath.Join(root, "escape"))
			tools := NewTools(root, allTools())
			got, err := tools.Do(context.Background(), tc.tool, tc.args)
			if err == nil {
				t.Errorf("%s accepted a path through a link out of the clone; %s (SPEC §5.6, O2)", tc.tool, tc.catch)
			}
			if strings.Contains(got, "outside-secret") || strings.Contains(got, "secret.txt") {
				t.Errorf("%s returned %q from outside the clone; %s (SPEC §5.6, O2)", tc.tool, got, tc.catch)
			}
			if _, err := os.Stat(filepath.Join(vault, "made.txt")); err == nil {
				t.Errorf("%s left a file outside the clone; %s (SPEC §5.6, O2)", tc.tool, tc.catch)
			}
			if data, _ := os.ReadFile(outside); string(data) != "outside-secret" {
				t.Errorf("%s changed the file outside the clone to %q; %s (SPEC §5.6, O2)", tc.tool, data, tc.catch)
			}
		})
	}
}

// gitConfig is the content of the git config of a clone fixture; a tool that changed it could plant
// a filter or hook the engine's next git call runs (SPEC §5.21).
const gitConfig = "[core]\n\trepositoryformatversion = 0\n"

// gitCloneFixture returns a clone that holds a git directory with a config and one tracked file, so
// a test proves a tool keeps out of the git directory and still edits the work tree (SPEC §5.21).
func gitCloneFixture(t *testing.T) (root, config string) {
	t.Helper()
	_, root, _ = cloneFixture(t)
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatalf("make the git directory: %v", err)
	}
	config = filepath.Join(root, ".git", "config")
	if err := os.WriteFile(config, []byte(gitConfig), 0o600); err != nil {
		t.Fatalf("seed the git config: %v", err)
	}
	return root, config
}

// TestToolsRefuseAPathIntoTheGitDirectory catches a tool that reaches the git directory of the
// clone: a filter or hook written into .git/config runs on the engine's next git call, as the user
// that runs aiwe. Every spelling one OS resolves to .git — any case, a trailing dot or space, the
// 8.3 short name, an NTFS stream and a walk back into it — is refused with the named error before
// any IO, and the config stays as it was (SPEC §5.21, §10.2, O2).
func TestToolsRefuseAPathIntoTheGitDirectory(t *testing.T) {
	paths := []string{
		".git/config", ".GIT/config", ".Git/config", ".git./config", ".git /config", ".git. ./config",
		"GIT~1/config", "git~2/config", ".git::$INDEX_ALLOCATION/config", "sub/../.git/config", ".git",
	}
	calls := []struct {
		tool string
		args func(path string) map[string]any
	}{
		{ToolWrite, func(path string) map[string]any {
			return map[string]any{"path": path, "content": "[filter \"x\"]\n\tclean = echo pwned\n"}
		}},
		{ToolEdit, func(path string) map[string]any {
			return map[string]any{"path": path, "old": "repositoryformatversion = 0", "new": "repositoryformatversion = 1"}
		}},
		{ToolRead, func(path string) map[string]any { return map[string]any{"path": path} }},
		{ToolList, func(path string) map[string]any { return map[string]any{"path": path} }},
	}
	for _, call := range calls {
		for _, path := range paths {
			t.Run(call.tool+" "+path, func(t *testing.T) {
				root, config := gitCloneFixture(t)
				tools := NewTools(root, allTools())
				got, err := tools.Do(context.Background(), call.tool, call.args(path))
				if !errors.Is(err, ErrGitPath) {
					t.Errorf("%s of %q = %q, %v, want %v: a tool reached the git directory (SPEC §5.21, O2)",
						call.tool, path, got, err, ErrGitPath)
				}
				if data, _ := os.ReadFile(config); string(data) != gitConfig {
					t.Errorf("%s of %q changed the git config to %q (SPEC §5.21, O2)", call.tool, path, data)
				}
			})
		}
	}
}

// TestToolsKeepTheWorkTreeOfAGitClone catches a git-directory guard that refuses too much: a
// tracked file, a name that only starts with .git and a directory named like one still read,
// write and edit (SPEC §10.2).
func TestToolsKeepTheWorkTreeOfAGitClone(t *testing.T) {
	root, _ := gitCloneFixture(t)
	tools := NewTools(root, allTools())
	ctx := context.Background()
	if _, err := tools.Do(ctx, ToolEdit, map[string]any{"path": "note.txt", "old": "inside", "new": "edited"}); err != nil {
		t.Errorf("edit of the tracked file note.txt: %v; the guard refuses the work tree (SPEC §10.2)", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "note.txt")); string(data) != "edited" {
		t.Errorf("the tracked file holds %q, want %q (SPEC §10.2)", data, "edited")
	}
	for _, path := range []string{".gitignore", ".github.txt", "docs.git"} {
		if _, err := tools.Do(ctx, ToolWrite, map[string]any{"path": path, "content": "kept"}); err != nil {
			t.Errorf("write of %q: %v; only the git directory is refused (SPEC §10.2)", path, err)
		}
		if got, err := tools.Do(ctx, ToolRead, map[string]any{"path": path}); err != nil || got != "kept" {
			t.Errorf("read of %q = %q, %v, want %q (SPEC §10.2)", path, got, err, "kept")
		}
	}
}

// TestToolsRefuseALinkInsideTheClone catches a tool that follows a link the clone holds: a link
// to the git directory names no .git in the path, so the tool refuses every path through a link
// with the named error and the config stays as it was (SPEC §5.21, §10.2, O2).
func TestToolsRefuseALinkInsideTheClone(t *testing.T) {
	for _, tool := range []string{ToolWrite, ToolEdit, ToolRead, ToolList} {
		t.Run(tool, func(t *testing.T) {
			root, config := gitCloneFixture(t)
			link := filepath.Join(root, "cfg")
			// A relative link stays inside the root, so only the tools' own check refuses it; a
			// Windows junction, made when the OS refuses a symlink, is absolute.
			if err := os.Symlink(".git", link); err != nil {
				cloneLink(t, filepath.Join(root, ".git"), link)
			}
			args := map[string]any{"path": "cfg/config", "content": "[filter \"x\"]\n", "old": "[core]", "new": "[x]"}
			if tool == ToolList {
				args["path"] = "cfg"
			}
			got, err := NewTools(root, allTools()).Do(context.Background(), tool, args)
			if !errors.Is(err, ErrPathLink) {
				t.Errorf("%s through the link = %q, %v, want %v (SPEC §5.21, O2)", tool, got, err, ErrPathLink)
			}
			if data, _ := os.ReadFile(config); string(data) != gitConfig {
				t.Errorf("%s through the link changed the git config to %q (SPEC §5.21, O2)", tool, data)
			}
		})
	}
}

// TestToolsCapARead catches a read that loads a file of any size into the next turn: a file of the
// cap reads whole, one byte more is refused with the named error, and an edit of it changes nothing
// (SPEC §10.2).
func TestToolsCapARead(t *testing.T) {
	_, root, _ := cloneFixture(t)
	tools := NewTools(root, allTools())
	ctx := context.Background()
	atCap := strings.Repeat("a", maxToolReadBytes)
	if err := os.WriteFile(filepath.Join(root, "at-cap.txt"), []byte(atCap), 0o600); err != nil {
		t.Fatalf("seed the file at the cap: %v", err)
	}
	if got, err := tools.Do(ctx, ToolRead, map[string]any{"path": "at-cap.txt"}); err != nil || len(got) != maxToolReadBytes {
		t.Errorf("read of a file of %d bytes returned %d bytes, %v, want the whole file (SPEC §10.2)",
			maxToolReadBytes, len(got), err)
	}
	over := atCap + "b"
	if err := os.WriteFile(filepath.Join(root, "over-cap.txt"), []byte(over), 0o600); err != nil {
		t.Fatalf("seed the file over the cap: %v", err)
	}
	if got, err := tools.Do(ctx, ToolRead, map[string]any{"path": "over-cap.txt"}); !errors.Is(err, ErrReadTooLarge) || got != "" {
		t.Errorf("read of a file over the cap returned %d bytes, %v, want %v and no content (SPEC §10.2)",
			len(got), err, ErrReadTooLarge)
	}
	_, err := tools.Do(ctx, ToolEdit, map[string]any{"path": "over-cap.txt", "old": "b", "new": "c"})
	if !errors.Is(err, ErrReadTooLarge) {
		t.Errorf("edit of a file over the cap: %v, want %v (SPEC §10.2)", err, ErrReadTooLarge)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "over-cap.txt")); string(data) != over {
		t.Error("an edit of a file over the cap changed it (SPEC §10.2)")
	}
}

// TestToolsRefuseAToolTheRunDoesNotAllow catches a run that runs a tool it never allowed: a
// read-only run refuses write and leaves the clone unchanged (SPEC §10.2).
func TestToolsRefuseAToolTheRunDoesNotAllow(t *testing.T) {
	_, root, _ := cloneFixture(t)
	tools := NewTools(root, ToolPermissions{Read: true})
	if _, err := tools.Do(context.Background(), ToolWrite, map[string]any{"path": "made.txt", "content": "x"}); err == nil {
		t.Error("a read-only run ran write; a tool a run does not allow is never offered (SPEC §10.2)")
	}
	if _, err := os.Stat(filepath.Join(root, "made.txt")); err == nil {
		t.Error("the refused write still made a file in the clone (SPEC §10.2)")
	}
}
