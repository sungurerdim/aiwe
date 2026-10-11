package repocheck

import (
	"slices"
	"testing"
)

// TestCommitMessage catches a commit message outside Conventional Commits that passes the check,
// and a valid one that the check refuses (SPEC §4, §12.3).
func TestCommitMessage(t *testing.T) {
	valid := []struct {
		name string
		msg  string
	}{
		{"type and description", "fix: the gate step reports its exit code\n"},
		{"type, scope and description", "feat(repocheck): the repocheck CLI runs the doctor\n"},
		{"breaking marker", "refactor(core)!: the result envelope drops the legacy field\n"},
		{"every type the release notes group", "ci: the static job restores the linter cache"},
		{"body after one blank line and trailers",
			"fix(core): the temp root is owner-only\n\nThe engine checked the mode after use.\n\n" +
				"Refs sungurerdim/aiwe#153\nCo-Authored-By: Claude <noreply@anthropic.com>\n"},
		{"breaking footer in upper case", "feat(core): one result envelope\n\nBREAKING CHANGE: the field is gone\n"},
		{"breaking footer with a hyphen", "feat(core): one result envelope\n\nBREAKING-CHANGE: the field is gone\n"},
		{"comment lines that git strips", "# Please enter the commit message\ntest(gates): the step times out\n# On branch main\n"},
		{"scissors line and the diff below it",
			"docs: the spec names the hook\n# ------------------------ >8 ------------------------\n" +
				"diff --git a/x b/x\nnot a header at all\n"},
		{"CRLF line ends", "chore: the module is tidy\r\n\r\nBody line.\r\n"},
		{"Unicode description", "docs: the naïve café résumé ✓ 日本\n"},
		{"leading blank lines", "\n\nbuild(deps): bump the setup-go action\n"},
		{"scope with a slash and a dot", "perf(core/process.go): one spawn per gate step\n"},
		{"revert type", "revert: feat(core): one result envelope\n"},
		{"style type", "style: gofmt the gate package\n"},
	}
	for _, tc := range valid {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			if got := CommitMessage(tc.msg); len(got) != 0 {
				t.Fatalf("CommitMessage(%q) = %+v, want no finding", tc.msg, got)
			}
		})
	}

	invalid := []struct {
		name string
		msg  string
		rule string
		line int
	}{
		{"no type", "add the gate step\n", "header_format", 1},
		{"type in upper case", "Feat: the gate step\n", "header_format", 1},
		{"unknown type", "feature: the gate step\n", "unknown_type", 1},
		{"misspelt type", "fixes: the gate step\n", "unknown_type", 1},
		{"empty scope", "feat(): the gate step\n", "header_format", 1},
		{"scope with a space", "feat(core gates): the gate step\n", "header_format", 1},
		{"no space after the colon", "feat:the gate step\n", "header_format", 1},
		{"space before the colon", "feat(core) : the gate step\n", "header_format", 1},
		{"empty description", "feat: \n", "header_format", 1},
		{"description of spaces only", "fix:    \n", "header_format", 1},
		{"no colon", "fix the gate step\n", "header_format", 1},
		{"breaking marker before the scope", "feat!(core): the gate step\n", "header_format", 1},
		{"body without a blank line", "fix: the gate step\nThe body starts at once.\n", "body_separator", 2},
		{"breaking footer in lower case", "feat: one envelope\n\nbreaking change: the field is gone\n",
			"breaking_footer_case", 3},
		{"breaking footer in mixed case", "feat: one envelope\n\nBody.\n\nBreaking-Change: the field is gone\n",
			"breaking_footer_case", 5},
		{"empty message", "", "empty_message", 0},
		{"only blank lines", "\n\n  \n", "empty_message", 0},
		{"only comment lines", "# Please enter the commit message\n# On branch main\n", "empty_message", 0},
		{"header after the scissors line only", "# ------------------------ >8 ------------------------\nfix: x\n",
			"empty_message", 0},
	}
	for _, tc := range invalid {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			got := CommitMessage(tc.msg)
			if !slices.ContainsFunc(got, func(f Finding) bool { return f.Rule == tc.rule && f.Line == tc.line }) {
				t.Fatalf("CommitMessage(%q) = %+v, want a %s finding at line %d", tc.msg, got, tc.rule, tc.line)
			}
		})
	}

	t.Run("names the message as the path of every finding", func(t *testing.T) {
		for _, f := range CommitMessage("Add things\nno blank line\n") {
			if f.Path != commitMessagePath {
				t.Fatalf("finding %+v has path %q, want %q", f, f.Path, commitMessagePath)
			}
		}
	})
}
