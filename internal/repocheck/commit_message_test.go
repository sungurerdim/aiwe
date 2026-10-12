package repocheck

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// ref is the issue line every valid test message carries (SPEC §12.3).
const ref = "\n\nRefs octo-org/widget#153\n"

// slug is the <owner>/<repo> of the repository the test messages are committed to.
const slug = "octo-org/widget"

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
		{"Closes line with its Effect line", "fix: the gate step reports its exit code\n\n" +
			"Effect: 3 silent exits → 0 silent exits\nCloses octo-org/widget#7\n"},
		{"Refs to another repository", "docs: the spec names the hook\n\nRefs octo-org/gadget#12\n"},
	}
	for _, tc := range valid {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			if got := CommitMessage(tc.msg, slug); len(got) != 0 {
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
		{"Closes without an Effect line", "fix: the gate step\n\nCloses octo-org/widget#7\n", "missing_effect", 0},
		{"Effect line without an arrow",
			"fix: the gate step\n\nEffect: fewer silent exits\nCloses octo-org/widget#7\n", "missing_effect", 0},
		{"Closes naming another repository",
			"fix: the gate step\n\nEffect: 3 → 0\nCloses octo-org/gadget#7\n", "closes_other_repo", 4},
		{"Closes naming a repository of another owner",
			"fix: the gate step\n\nEffect: 3 → 0\nCloses other-org/widget#7\n", "closes_other_repo", 4},
	}
	for _, tc := range invalid {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			got := CommitMessage(tc.msg, slug)
			if !slices.ContainsFunc(got, func(f Finding) bool { return f.Rule == tc.rule && f.Line == tc.line }) {
				t.Fatalf("CommitMessage(%q) = %+v, want a %s finding at line %d", tc.msg, got, tc.rule, tc.line)
			}
		})
	}

	t.Run("names the message as the path of every finding", func(t *testing.T) {
		for _, f := range CommitMessage("Add things\nno blank line\n", slug) {
			if f.Path != commitMessagePath {
				t.Fatalf("finding %+v has path %q, want %q", f, f.Path, commitMessagePath)
			}
		}
	})

	t.Run("a Closes line with an unknown repository is refused", func(t *testing.T) {
		got := CommitMessage("fix: the gate step\n\nEffect: 3 → 0\nCloses octo-org/widget#7\n", "")
		if !slices.ContainsFunc(got, func(f Finding) bool { return f.Rule == "closes_repo_unknown" && f.Line == 4 }) {
			t.Fatalf("CommitMessage with no repository = %+v, want a closes_repo_unknown finding at line 4", got)
		}
	})

	t.Run("a Refs line with an unknown repository is accepted", func(t *testing.T) {
		if got := CommitMessage("fix: the gate step\n\nRefs octo-org/widget#7\n", ""); len(got) != 0 {
			t.Fatalf("CommitMessage with no repository = %+v, want no finding", got)
		}
	})

	t.Run("an empty message yields only the empty finding", func(t *testing.T) {
		got := CommitMessage("", slug)
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
		for _, f := range CommitMessage(msg, slug) {
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
			{"long URL with long prose", fill(t, "See https://github.com/octo-org/widget/issues/153 and ", 126),
				true, false},
			{"back quote span followed by prose", "`go vet` " + fill(t, "reports nothing ", 117), true, false},
			{"prose quoted with a greater-than sign", "> " + fill(t, "The engine ", 124), true, false},
			{"command in back quotes", "`" + fill(t, "go vet ./internal/repocheck/ ", 78) + "`", false, false},
			{"two back quote spans", "`go vet` " + fill(t, "and `go test ./internal/repocheck/", 115) + "`",
				true, false},
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

	t.Run("an unclosed fence exempts nothing", func(t *testing.T) {
		msg := "test(gates): the step times out\n\nThe run printed:\n\n```\n" +
			fill(t, "The engine ", 126) + ref
		if got := findings(msg, "body_line_length"); len(got) != 1 || got[0].Line != 6 {
			t.Fatalf("body_line_length findings %+v after an unclosed fence, want one at line 6", got)
		}
	})

	t.Run("text after the fence marker is checked", func(t *testing.T) {
		msg := "test(gates): the step times out\n\n```" + fill(t, "sh The engine ", 123) + "\ngo test\n```\n" + ref
		if got := findings(msg, "body_line_length"); len(got) != 1 || got[0].Line != 3 {
			t.Fatalf("body_line_length findings %+v on a fence line with prose, want one at line 3", got)
		}
	})

	t.Run("a long line below the scissors line is not checked", func(t *testing.T) {
		msg := "docs: the spec names the hook" + ref + scissorsLine + "\n" + fill(t, "+The engine ", 120) + "\n"
		if got := findings(msg, "body_line_length"); len(got) != 0 {
			t.Fatalf("body_line_length findings %+v below the scissors line, want none", got)
		}
	})

	t.Run("a Note line in the trailer block is checked for length", func(t *testing.T) {
		msg := "fix: x\n\nBody.\n\nRefs octo-org/widget#1\n" + fill(t, "Note: ", 73) + "\n"
		got := findings(msg, "body_line_length")
		if len(got) != 1 || got[0].Line != 6 {
			t.Fatalf("body_line_length findings %+v for Note trailer, want one at line 6", got)
		}
	})
}

// TestCommitMessageClosingKeywords catches a closing keyword that GitHub would act on but that is
// not the strict whole-line Closes form, and honours the quoting and fence exemptions (SPEC §12.3).
func TestCommitMessageClosingKeywords(t *testing.T) {
	has := func(t *testing.T, msg, rule string, line int) {
		t.Helper()
		got := CommitMessage(msg, slug)
		if !slices.ContainsFunc(got, func(f Finding) bool { return f.Rule == rule && f.Line == line }) {
			t.Fatalf("CommitMessage(%q) = %+v, want a %s finding at line %d", msg, got, rule, line)
		}
	}
	no := func(t *testing.T, msg, rule string) {
		t.Helper()
		got := CommitMessage(msg, slug)
		if slices.ContainsFunc(got, func(f Finding) bool { return f.Rule == rule }) {
			t.Fatalf("CommitMessage(%q) = %+v, want no %s finding", msg, got, rule)
		}
	}
	const refLine = "\n\nRefs octo-org/widget#1\n"

	t.Run("lowercase closes is not the strict form", func(t *testing.T) {
		has(t, "fix: x"+refLine+"closes octo-org/widget#7\n", "closing_keyword_form", 4)
	})

	t.Run("bare number Fixes is not the strict form", func(t *testing.T) {
		has(t, "fix: x\n\nFixes #7\n"+refLine, "closing_keyword_form", 3)
	})

	t.Run("inline closing keyword is not the strict form", func(t *testing.T) {
		has(t, "fix: x\n\nThis fixes octo-org/widget#7 inline.\n"+refLine, "closing_keyword_form", 3)
	})

	t.Run("mixed case Resolved is not the strict form", func(t *testing.T) {
		has(t, "fix: x\n\nResolved octo-org/widget#7\n"+refLine, "closing_keyword_form", 3)
	})

	t.Run("trailing text breaks the strict form", func(t *testing.T) {
		has(t, "fix: x\n\nCloses octo-org/widget#7 trailing\n"+refLine, "closing_keyword_form", 3)
	})

	t.Run("indented closing keyword is exempt", func(t *testing.T) {
		no(t, "fix: x\n\n    closes octo-org/widget#7\n"+refLine, "closing_keyword_form")
	})

	t.Run("strict Closes line is accepted", func(t *testing.T) {
		if got := CommitMessage("fix: x\n\nEffect: a → b\nCloses octo-org/widget#7\n", slug); len(got) != 0 {
			t.Fatalf("CommitMessage(strict Closes) = %+v, want no finding", got)
		}
	})

	t.Run("Effect inside a fence does not satisfy Closes", func(t *testing.T) {
		has(t, "fix: x\n\n```\nEffect: a → b\n```\nCloses octo-org/widget#7\n", "missing_effect", 0)
	})
}

// TestRepoSlug catches a remote URL whose <owner>/<repo> is misread, so a Closes line to another
// repository passes, and an unreadable URL that yields a slug instead of none (SPEC §12.3).
func TestRepoSlug(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "widget")
	cases := []struct {
		url  string
		want string
	}{
		{"https://github.com/octo-org/widget.git", "octo-org/widget"},
		{"https://github.com/octo-org/widget", "octo-org/widget"},
		{"https://github.com/octo-org/widget/", "octo-org/widget"},
		{"git@github.com:octo-org/widget.git", "octo-org/widget"},
		{"ssh://git@github.com/octo-org/widget.git", "octo-org/widget"},
		{"ssh://git@github.com:22/octo-org/widget.git", "octo-org/widget"},
		{"https://github.com/octo-org/widget.git\n", "octo-org/widget"},
		{"", ""},
		{"github.com", ""},
		{"https://github.com/widget.git", ""},
		{"https://github.com/octo org/widget.git", ""},
		{"../widget", ""},
		{localPath, ""},
	}
	for _, tc := range cases {
		if got := RepoSlug(tc.url); got != tc.want {
			t.Errorf("RepoSlug(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}
