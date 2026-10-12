package repocheck

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// ref is the issue line every valid test message carries (SPEC §12.3).
const ref = "\n\nRefs octo-org/widget#153\n"

// TestCommitMessage catches a commit message outside Conventional Commits that passes the check,
// and a valid one that the check refuses (SPEC §4, §12.3).
func TestCommitMessage(t *testing.T) {
	valid := []struct {
		name string
		msg  string
	}{
		{"type and description", "fix: the gate step reports its exit code" + ref},
		{"type, scope and description", "feat(repocheck): the repocheck CLI runs the doctor" + ref},
		{"breaking marker", "refactor(core)!: the result drops the legacy field" + ref},
		{"every type the release notes group", "ci: the static job restores the linter cache" + ref},
		{"body after one blank line and trailers",
			"fix(core): the temp root is owner-only\n\nThe engine checked the mode after use.\n\n" +
				"Refs octo-org/widget#153\nCo-Authored-By: Claude <noreply@anthropic.com>\n"},
		{"breaking footer in upper case",
			"feat(core): one result envelope\n\nBREAKING CHANGE: the field is gone" + ref},
		{"breaking footer with a hyphen",
			"feat(core): one result envelope\n\nBREAKING-CHANGE: the field is gone" + ref},
		{"comment lines that git strips",
			"# Please enter the commit message\ntest(gates): the step times out\n# On branch main" + ref},
		{"scissors line and the diff below it",
			"docs: the spec names the hook" + ref + "# ------------------------ >8 ------------------------\n" +
				"diff --git a/x b/x\nnot a header at all\n"},
		{"CRLF line ends", "chore: the module is tidy\r\n\r\nBody line.\r\n\r\nRefs octo-org/widget#153\r\n"},
		{"Unicode description", "docs: the naïve café résumé ✓ 日本" + ref},
		{"leading blank lines", "\n\nbuild(deps): bump the setup-go action" + ref},
		{"scope with a slash and a dot", "perf(core/process.go): one spawn per gate step" + ref},
		{"revert type", "revert: feat(core): one result envelope" + ref},
		{"style type", "style: gofmt the gate package" + ref},
		{"Closes line instead of Refs", "fix: the gate step reports its exit code\n\nCloses octo-org/widget#7\n"},
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
		{"no issue line", "fix: the gate step reports its exit code\n\nThe body has no issue.\n",
			"missing_issue_ref", 0},
		{"header only", "fix: the gate step reports its exit code\n", "missing_issue_ref", 0},
		{"issue line without an owner", "fix: the gate step\n\nRefs widget#153\n", "missing_issue_ref", 0},
		{"issue line with a bare number", "fix: the gate step\n\nCloses #153\n", "missing_issue_ref", 0},
		{"issue line in lower case", "fix: the gate step\n\nrefs octo-org/widget#153\n", "missing_issue_ref", 0},
		{"issue reference inside prose", "fix: the gate step\n\nThis Refs octo-org/widget#153 inline.\n",
			"missing_issue_ref", 0},
		{"issue line below the scissors line",
			"fix: the gate step\n# ------------------------ >8 ------------------------\nRefs octo-org/widget#153\n",
			"missing_issue_ref", 0},
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

	t.Run("an empty message yields only the empty finding", func(t *testing.T) {
		got := CommitMessage("")
		if len(got) != 1 || got[0].Rule != ruleEmptyMessage {
			t.Fatalf("CommitMessage(\"\") = %+v, want one empty_message finding", got)
		}
	})
}

// fill returns prefix padded with prose to exactly n runes; it fails the test when prefix is longer.
func fill(t *testing.T, prefix string, n int) string {
	t.Helper()
	s := prefix
	for utf8.RuneCountInString(s) < n {
		s += "the gate step reports its exit code "
	}
	// git and the check drop trailing spaces, so a cut that ends in a space ends in a letter instead.
	s = strings.TrimRight(string([]rune(s)[:n]), " ")
	s += strings.Repeat("e", n-utf8.RuneCountInString(s))
	if utf8.RuneCountInString(s) != n || !strings.HasPrefix(s, prefix) {
		t.Fatalf("fill(%q, %d) = %q, not %d runes with that prefix", prefix, n, s, n)
	}
	return s
}

// TestCommitMessageLengths catches a header over 50 runes or a prose body line over 72 runes that
// passes, and a line at the limit or an exempt long line that is refused (SPEC §12.3). Lengths
// count runes, not bytes: a header of 50 non-ASCII runes is within the limit.
func TestCommitMessageLengths(t *testing.T) {
	findings := func(msg, rule string) []Finding {
		var out []Finding
		for _, f := range CommitMessage(msg) {
			if f.Rule == rule {
				out = append(out, f)
			}
		}
		return out
	}

	t.Run("header", func(t *testing.T) {
		cases := []struct {
			name   string
			header string
			want   bool
		}{
			{"50 runes, type and scope included", fill(t, "fix(repocheck): ", 50), false},
			{"51 runes", fill(t, "fix(repocheck): ", 51), true},
			{"50 non-ASCII runes, 94 bytes", "docs: " + strings.Repeat("ü", 44), false},
			{"51 non-ASCII runes", "docs: " + strings.Repeat("ü", 45), true},
			{"51 runes with a breaking marker", fill(t, "refactor(core)!: ", 51), true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := findings(tc.header+ref, "header_length")
				if tc.want != (len(got) == 1 && got[0].Line == 1) || (!tc.want && len(got) != 0) {
					t.Fatalf("header %q (%d runes): header_length findings %+v, want one at line 1: %v",
						tc.header, utf8.RuneCountInString(tc.header), got, tc.want)
				}
			})
		}
	})

	t.Run("body line", func(t *testing.T) {
		const head = "fix(core): the temp root is owner-only\n\n"
		cases := []struct {
			name    string
			line    string
			want    bool
			trailer bool // the line sits in the last paragraph, below the issue line
		}{
			{"72 runes of prose", fill(t, "The engine ", 72), false, false},
			{"73 runes of prose", fill(t, "The engine ", 73), true, false},
			{"72 non-ASCII runes", strings.Repeat("ş", 72), false, false},
			{"73 non-ASCII runes", strings.Repeat("ş", 73), true, false},
			{"long URL",
				"See https://github.com/octo-org/widget/blob/main/docs/SPEC.md#123-release-notes-and-tags", false, false},
			{"long trailer",
				"Co-Authored-By: A Very Long Contributor Name With Many Parts <contributor.name@example.com>",
				false, true},
			{"long Effect trailer", fill(t, "Effect: 12 findings → 0 findings; ", 90), false, true},
			{"trailer-shaped prose above the trailer block", fill(t, "Note: ", 73), true, false},
			{"indented command",
				"    " + fill(t, "go test -count=1 -run TestCommitMessage ./internal/repocheck/", 80), false, false},
			{"tab-indented output", "\t" + fill(t, "--- FAIL: TestCommitMessageLengths/body_line ", 80), false, false},
			{"quoted output", "> " + fill(t, "commit_message_test.go:42: CommitMessage(...) = [] ", 80), false, false},
			{"command in back quotes", "`" + fill(t, "go vet ./internal/repocheck/ ", 78) + "`", false, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				msg, line := head+tc.line+ref, 3
				if tc.trailer {
					msg, line = head+"Refs octo-org/widget#153\n"+tc.line+"\n", 4
				}
				got := findings(msg, "body_line_length")
				if tc.want != (len(got) == 1 && got[0].Line == line) || (!tc.want && len(got) != 0) {
					t.Fatalf("body line %q (%d runes): body_line_length findings %+v, want one at line %d: %v",
						tc.line, utf8.RuneCountInString(tc.line), got, line, tc.want)
				}
			})
		}
	})

	t.Run("fenced block lines are exempt", func(t *testing.T) {
		msg := "test(gates): the step times out\n\nThe run printed:\n\n```\n" +
			fill(t, "--- FAIL: TestStepTimeout (0.00s) step.go:88: ", 95) + "\n```\n" + ref
		if got := findings(msg, "body_line_length"); len(got) != 0 {
			t.Fatalf("body_line_length findings %+v inside a fenced block, want none", got)
		}
	})

	t.Run("a long line below the scissors line is not checked", func(t *testing.T) {
		msg := "docs: the spec names the hook" + ref + scissorsLine + "\n" + fill(t, "+The engine ", 120) + "\n"
		if got := findings(msg, "body_line_length"); len(got) != 0 {
			t.Fatalf("body_line_length findings %+v below the scissors line, want none", got)
		}
	})
}
