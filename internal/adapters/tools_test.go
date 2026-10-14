package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allTools allows every tool one run may use, so a test of the tool set offers them all
// (SPEC §10.2, O14).
func allTools() ToolPermissions {
	return ToolPermissions{Read: true, Write: true, Edit: true, List: true, Shell: true}
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
