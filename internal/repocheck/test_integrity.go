package repocheck

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// The rule names of the commit-point test integrity check (SPEC §5.13, L72). ruleTestDeletionUnproven
// covers every deleted test, not only a detail test: a diff cannot tell a detail test from a boundary
// test, so each deleted test needs its Covered-by and Mutation proof.
const (
	ruleBehaviorChangeMissing = "behavior_change_missing"
	ruleTestDeletionUnproven  = "test_deletion_unproven"
	ruleIntegrityNotMeasured  = "test_integrity_not_measured"
)

// Integrity is what the test integrity check found: Warnings never refuse a commit, Findings do.
type Integrity struct {
	Warnings []Finding
	Findings []Finding
}

// IntegrityNotMeasured is the result of a run that could not read a staged diff: one warning, never a
// refusal, so the check is not taken for a silent green (DR-6).
func IntegrityNotMeasured() Integrity {
	return Integrity{Warnings: []Finding{{Rule: ruleIntegrityNotMeasured, Path: commitMessagePath}}}
}

// behaviorChangePattern matches the Behavior change line a commit that changes product code and an
// assertion carries.
var behaviorChangePattern = regexp.MustCompile(`^Behavior change: \S`)

// coveredByPattern matches the Covered-by line of a commit that deletes a detail test.
var coveredByPattern = regexp.MustCompile(`^Covered-by: \S`)

// mutationPattern matches the Mutation line of a commit that deletes a detail test:
// Mutation: <file:line> <test> red, reverted.
var mutationPattern = regexp.MustCompile(`^Mutation: \S+:[1-9][0-9]* \S+ red, reverted$`)

// The check knows Go and Python only (the stacks of aiwe and of the #24 stack profile); a test of
// another language is not recognised, so its deletion is not measured here.

// assertionPattern matches a line that asserts: a Go failure call, a testify assert or require call, a
// Python assert statement or a unittest assert method.
var assertionPattern = regexp.MustCompile(`\.(?:Fatalf?|Errorf|FailNow|Fail)\(|\.Error\([^)]|\bassert\.\w+\(|\brequire\.\w+\(|^\s*assert\s|\bself\.assert\w+\(`)

// testFuncPattern matches the line that declares a test: a Go Test function or a Python test function;
// it captures the name in one of its two groups.
var testFuncPattern = regexp.MustCompile(`^(?:func (Test\w*)\(|\s*def (test\w*)\()`)

// testFilePattern matches the path of a Go or Python test file.
var testFilePattern = regexp.MustCompile(`(?:_test\.(?:go|py)|(?:^|/)test_[^/]*\.py)$`)

// codeExtensions are the file extensions of Go and Python product code, a test file excluded.
func codeExtensions() []string { return []string{".go", ".py"} }

// diffFile is one file of a staged diff: its path and the lines the diff adds and removes.
type diffFile struct {
	path    string
	added   []string
	removed []string
}

// CommitIntegrity checks a commit message against the staged diff it lands with (SPEC §5.13, L72).
// A diff that changes product code and an assertion with no Behavior change line is a warning, never a
// refusal. Each test the diff deletes needs its own Covered-by and Mutation line pair; a test the diff
// deletes without one is a finding. A test moved to another file is no deletion. A line inside a fence
// does not count.
func CommitIntegrity(msg, diff string) Integrity {
	files := parseDiff(diff)
	lines := unfencedLines(msg)
	var res Integrity
	if changesCodeAndAssertion(files) && countLines(lines, behaviorChangePattern) == 0 {
		res.Warnings = []Finding{{Rule: ruleBehaviorChangeMissing, Path: commitMessagePath}}
	}
	deleted := deletedTests(files)
	proven := min(countLines(lines, coveredByPattern), countLines(lines, mutationPattern))
	for _, p := range deleted[min(proven, len(deleted)):] {
		res.Findings = append(res.Findings, Finding{Rule: ruleTestDeletionUnproven, Path: p})
	}
	return res
}

// unfencedLines returns the lines of msg that git keeps and that lie outside a ``` fence.
func unfencedLines(msg string) []messageLine {
	all := messageLines(msg)
	fenced := fencedLines(all)
	kept := make([]messageLine, 0, len(all))
	for i, l := range all {
		if !fenced[i] {
			kept = append(kept, l)
		}
	}
	return kept
}

// countLines counts the lines that match p.
func countLines(lines []messageLine, p *regexp.Regexp) int {
	n := 0
	for _, l := range lines {
		if p.MatchString(l.text) {
			n++
		}
	}
	return n
}

// isTestFile reports whether p is the path of a test file.
func isTestFile(p string) bool { return testFilePattern.MatchString(p) }

// isCodeFile reports whether p is a product code file: a code extension and not a test file.
func isCodeFile(p string) bool {
	return slices.Contains(codeExtensions(), path.Ext(p)) && !isTestFile(p)
}

// changesCodeAndAssertion reports whether files change product code and an assertion line of a test file.
func changesCodeAndAssertion(files []diffFile) bool {
	code, assertion := false, false
	for _, f := range files {
		switch {
		case isCodeFile(f.path):
			code = true
		case isTestFile(f.path):
			assertion = assertion || slices.ContainsFunc(slices.Concat(f.added, f.removed), assertionPattern.MatchString)
		}
	}
	return code && assertion
}

// testName returns the name of the test that line declares, or "".
func testName(line string) string {
	m := testFuncPattern.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1] + m[2]
}

// testFiles returns the files of the diff that are test files.
func testFiles(files []diffFile) []diffFile {
	return slices.DeleteFunc(slices.Clone(files), func(f diffFile) bool { return !isTestFile(f.path) })
}

// declaredTests returns the names of the tests that lines declare.
func declaredTests(lines []string) []string {
	var names []string
	for _, l := range lines {
		if n := testName(l); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// deletedTests returns the path of the test file for each test the diff removes whose name no test
// file adds, in diff order; a test moved to another file is no deletion.
func deletedTests(files []diffFile) []string {
	tests := testFiles(files)
	added := map[string]bool{}
	for _, f := range tests {
		for _, n := range declaredTests(f.added) {
			added[n] = true
		}
	}
	var paths []string
	for _, f := range tests {
		for _, n := range declaredTests(f.removed) {
			if !added[n] {
				paths = append(paths, f.path)
			}
		}
	}
	return paths
}

// diffParser reads a unified diff line by line.
type diffParser struct {
	files   []diffFile
	inHunk  bool
	oldPath string
}

// parseDiff returns the files of a unified diff that carry a path.
func parseDiff(diff string) []diffFile {
	p := &diffParser{}
	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		p.feed(line)
	}
	return slices.DeleteFunc(p.files, func(f diffFile) bool { return f.path == "" })
}

// feed reads one line of the diff: a file header, a hunk header, or an added or removed line.
func (p *diffParser) feed(line string) {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		p.files = append(p.files, diffFile{})
		p.inHunk, p.oldPath = false, ""
	case len(p.files) == 0:
	case strings.HasPrefix(line, "@@"):
		p.inHunk = true
	case p.inHunk:
		p.content(line)
	case strings.HasPrefix(line, "--- "):
		p.oldPath = diffPath(line[len("--- "):], "a/")
	case strings.HasPrefix(line, "+++ "):
		p.files[len(p.files)-1].path = firstSet(diffPath(line[len("+++ "):], "b/"), p.oldPath)
	}
}

// content records an added or removed line in the current file.
func (p *diffParser) content(line string) {
	f := &p.files[len(p.files)-1]
	switch {
	case strings.HasPrefix(line, "+"):
		f.added = append(f.added, line[1:])
	case strings.HasPrefix(line, "-"):
		f.removed = append(f.removed, line[1:])
	}
}

// firstSet returns first when it is set, else second: a deleted file has no new path.
func firstSet(first, second string) string {
	if first != "" {
		return first
	}
	return second
}

// diffPath returns the repository-relative path of a ---/+++ header value without its a/ or b/
// prefix; /dev/null is "".
func diffPath(s, prefix string) string {
	s = strings.TrimRight(s, "\t")
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, prefix)
}
