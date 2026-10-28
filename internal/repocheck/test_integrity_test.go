package repocheck

import (
	"strings"
	"testing"
)

// diffFileText renders one modified file of a unified staged diff with the given removed and added lines.
func diffFileText(path string, removed, added []string) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path + "\n@@ -1 +1 @@\n")
	for _, l := range removed {
		b.WriteString("-" + l + "\n")
	}
	for _, l := range added {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

// deletedFileText renders a staged diff that deletes the file path whose lines were removed.
func deletedFileText(path string, removed []string) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\ndeleted file mode 100644\n--- a/" + path + "\n+++ /dev/null\n@@ -1 +0,0 @@\n")
	for _, l := range removed {
		b.WriteString("-" + l + "\n")
	}
	return b.String()
}

const (
	codeChange   = "diff --git a/internal/core/run.go b/internal/core/run.go\n--- a/internal/core/run.go\n+++ b/internal/core/run.go\n@@ -3 +3 @@\n-\treturn 1\n+\treturn 2\n"
	assertChange = "diff --git a/internal/core/run_test.go b/internal/core/run_test.go\n--- a/internal/core/run_test.go\n+++ b/internal/core/run_test.go\n@@ -9 +9 @@\n-\t\tt.Fatalf(\"Run() = %d, want 1\", got)\n+\t\tt.Fatalf(\"Run() = %d, want 2\", got)\n"
	plainMessage = "fix: the run returns two\n\nThe run counted from zero.\n"
	behaviorLine = "fix: the run returns two\n\nBehavior change: Run returns 2, not 1, on an empty input.\n"
)

// ruleSet returns the rule of each finding.
func ruleSet(found []Finding) []string {
	rules := make([]string, 0, len(found))
	for _, f := range found {
		rules = append(rules, f.Rule)
	}
	return rules
}

// TestBehaviorChangeLineWarnsWhenMissing catches a commit that changes product code and an assertion
// with no Behavior change line passing without a warning, and a warning that refuses the commit
// (SPEC §5.13, L72).
func TestBehaviorChangeLineWarnsWhenMissing(t *testing.T) {
	tests := []struct {
		name     string
		msg      string
		diff     string
		warnings []string
	}{
		{"code and an assertion with no line warns once", plainMessage, codeChange + assertChange, []string{ruleBehaviorChangeMissing}},
		{"code only is no behavior claim", plainMessage, codeChange, nil},
		{"an assertion only is no code change", plainMessage, assertChange, nil},
		{"a test that gains no assertion line is no assertion change", plainMessage,
			codeChange + diffFileText("internal/core/run_test.go", nil, []string{"\t// the run is counted"}), nil},
		{"a docs file changed beside an assertion is no code change", plainMessage,
			diffFileText("docs/SPEC.md", []string{"a"}, []string{"b"}) + assertChange, nil},
		{"an empty diff has nothing to warn on", plainMessage, "", nil},
		{"a Python assertion beside Python code warns", plainMessage,
			diffFileText("app/run.py", []string{"    return 1"}, []string{"    return 2"}) +
				diffFileText("tests/test_run.py", []string{"    assert run() == 1"}, []string{"    assert run() == 2"}),
			[]string{ruleBehaviorChangeMissing}},
		{"a Behavior change line inside a code fence does not count",
			"fix: x\n\n```\nBehavior change: Run returns 2.\n```\n", codeChange + assertChange, []string{ruleBehaviorChangeMissing}},
		{"a Behavior change line with no text does not count", "fix: x\n\nBehavior change:\n",
			codeChange + assertChange, []string{ruleBehaviorChangeMissing}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CommitIntegrity(tc.msg, tc.diff)
			if len(got.Findings) != 0 {
				t.Fatalf("CommitIntegrity(%q, diff) findings = %+v, want none: a missing Behavior change line is a warning, never a refusal", tc.msg, got.Findings)
			}
			if w := ruleSet(got.Warnings); strings.Join(w, ",") != strings.Join(tc.warnings, ",") {
				t.Fatalf("CommitIntegrity(%q, diff) warnings = %v, want %v", tc.msg, w, tc.warnings)
			}
		})
	}
}

// TestBehaviorChangeLineClearsTheWarning catches a Behavior change line in the commit body that does
// not clear the warning (SPEC §5.13, L72).
func TestBehaviorChangeLineClearsTheWarning(t *testing.T) {
	tests := []struct {
		name string
		msg  string
	}{
		{"the line in the body", behaviorLine},
		{"the line among the trailers", "fix: x\n\nBehavior change: Run returns 2.\n\nEffect: 1 → 2\n"},
		{"the line with CRLF line ends", "fix: x\r\n\r\nBehavior change: Run returns 2.\r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CommitIntegrity(tc.msg, codeChange+assertChange)
			if len(got.Warnings) != 0 || len(got.Findings) != 0 {
				t.Fatalf("CommitIntegrity(%q, code and assertion) = %+v, want no warning and no finding", tc.msg, got)
			}
		})
	}
}

const (
	deletedTestBody = "func TestRunCountsFromZero(t *testing.T) {\n\tif got := run(); got != 1 {\n\t\tt.Fatalf(\"run() = %d, want 1\", got)\n\t}\n}"
	coverLine       = "Covered-by: TestRunThroughTheCLI"
	mutationLine    = "Mutation: internal/core/run.go:3 TestRunThroughTheCLI red, reverted"
)

// TestDetailTestDeletionNeedsCoverAndMutation catches a deleted detail test that passes without a
// Covered-by line and a Mutation line, a malformed line that counts, and a moved test that is
// taken for a deletion (SPEC §5.13, L72).
func TestDetailTestDeletionNeedsCoverAndMutation(t *testing.T) {
	deletion := deletedFileText("internal/core/run_internal_test.go", strings.Split(deletedTestBody, "\n"))
	moved := deletion + diffFileText("internal/core/run_test.go", nil, strings.Split(deletedTestBody, "\n"))
	head := "test: drop the detail test\n\n"
	tests := []struct {
		name  string
		msg   string
		diff  string
		rules []string
	}{
		{"no line is a must finding", head + "The test pinned run().\n", deletion, []string{ruleTestDeletionUnproven}},
		{"Covered-by alone is a must finding", head + coverLine + "\n", deletion, []string{ruleTestDeletionUnproven}},
		{"Mutation alone is a must finding", head + mutationLine + "\n", deletion, []string{ruleTestDeletionUnproven}},
		{"both lines are clean", head + coverLine + "\n" + mutationLine + "\n", deletion, nil},
		{"a Mutation line without file:line is a must finding", head + coverLine + "\nMutation: TestRunThroughTheCLI red, reverted\n",
			deletion, []string{ruleTestDeletionUnproven}},
		{"a Mutation line that was not reverted is a must finding", head + coverLine + "\nMutation: internal/core/run.go:3 TestRunThroughTheCLI red\n",
			deletion, []string{ruleTestDeletionUnproven}},
		{"a Covered-by line with no test is a must finding", head + "Covered-by:\n" + mutationLine + "\n", deletion, []string{ruleTestDeletionUnproven}},
		{"lines inside a fence do not count", head + "```\n" + coverLine + "\n" + mutationLine + "\n```\n", deletion, []string{ruleTestDeletionUnproven}},
		{"a test moved to another file is no deletion", head + "The test moved.\n", moved, nil},
		{"a deleted Python test is a must finding", head + "The test pinned run().\n",
			deletedFileText("tests/test_run.py", []string{"def test_run():", "    assert run() == 1"}), []string{ruleTestDeletionUnproven}},
		{"a deleted function that is not a test is no deletion", head + "Dropped a helper.\n",
			deletedFileText("internal/core/helper_test.go", []string{"func helper() int {", "\treturn 1", "}"}), nil},
		{"one pair of lines covers one of two deleted tests",
			head + coverLine + "\n" + mutationLine + "\n",
			deletion + deletedFileText("internal/core/other_internal_test.go", []string{"func TestOtherDetail(t *testing.T) {", "}"}),
			[]string{ruleTestDeletionUnproven}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CommitIntegrity(tc.msg, tc.diff)
			if r := ruleSet(got.Findings); strings.Join(r, ",") != strings.Join(tc.rules, ",") {
				t.Fatalf("CommitIntegrity(%q, diff) findings = %v, want %v", tc.msg, r, tc.rules)
			}
			for _, f := range got.Findings {
				if !isTestFile(f.Path) {
					t.Errorf("finding %+v names path %q, want the deleted test file", f, f.Path)
				}
			}
		})
	}
}
