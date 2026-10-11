package repocheck

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The rule names of check repo/rules (SPEC §3, §12.1).
const (
	ruleMissingTest     = "missing_test"
	ruleMissingRepoFile = "missing_repo_file"
)

// specFile is the one source of truth that check repo/rules reads (SPEC §3).
const specFile = "docs/SPEC.md"

// backtickPattern matches one backticked span of the spec and captures its text.
var backtickPattern = regexp.MustCompile("`([^`]+)`")

// Rules checks repo against the rules its docs/SPEC.md states. A test named in the §3 table
// (relative to internal/) is missing_test when it is absent while the source beside it exists; a
// test whose source is not in the tree yet passes only while a §14 Files cell lists that source.
// A repository file the §12.1 list names is missing_repo_file when it is absent. A repository
// without a readable spec is an error: no rule was checked (DR-13).
func Rules(repo string) ([]Finding, error) {
	body, err := readRepoFile(repo, specFile)
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	spec, err := core.ReadText(body)
	if err != nil {
		return nil, fmt.Errorf("rules: %s: %w", specFile, err)
	}
	lines := strings.Split(strings.ReplaceAll(spec, "\r\n", "\n"), "\n")
	tests, err := missingTests(repo, lines)
	if err != nil {
		return nil, err
	}
	files, err := missingRepoFiles(repo, lines)
	if err != nil {
		return nil, err
	}
	found := append(tests, files...)
	sortFindings(found)
	return found, nil
}

// missingTests returns a finding for each §3 test that is absent and not excused by §14.
func missingTests(repo string, lines []string) ([]Finding, error) {
	planned := plannedFiles(section(lines, "## 14.", "## "))
	seen := map[string]bool{}
	var found []Finding
	for _, test := range namedTests(section(lines, "## 3.", "## ")) {
		if seen[test] {
			continue
		}
		seen[test] = true
		missing, err := testMissing(repo, test, planned)
		if err != nil {
			return nil, err
		}
		if missing {
			found = append(found, Finding{Rule: ruleMissingTest, Path: "internal/" + test})
		}
	}
	return found, nil
}

// testMissing reports whether the internal-relative test is absent while its source exists, or
// absent while its source is neither in the tree nor listed by a §14 Files cell.
func testMissing(repo, test string, planned map[string]bool) (bool, error) {
	present, err := fileExists(repo, "internal/"+test)
	if err != nil || present {
		return false, err
	}
	source := strings.TrimSuffix(test, "_test.go") + ".go"
	inTree, err := fileExists(repo, "internal/"+source)
	if err != nil {
		return false, err
	}
	return inTree || !(planned[source] || planned["internal/"+source]), nil
}

// namedTests returns each backticked _test.go path in the table rows of the §3 section.
func namedTests(sec []string) []string {
	var tests []string
	for _, line := range sec {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		for _, m := range backtickPattern.FindAllStringSubmatch(line, -1) {
			if strings.HasSuffix(m[1], "_test.go") {
				tests = append(tests, m[1])
			}
		}
	}
	return tests
}

// plannedFiles returns each backticked path in the Files column of the §14 phase tables.
func plannedFiles(sec []string) map[string]bool {
	planned := map[string]bool{}
	col := -1
	for _, line := range sec {
		if !strings.HasPrefix(line, "|") {
			col = -1
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if i := slices.IndexFunc(cells, func(c string) bool { return strings.TrimSpace(c) == "Files" }); i >= 0 {
			col = i // a header row; its own cell names no file
		}
		if col < 0 || col >= len(cells) {
			continue
		}
		for _, m := range backtickPattern.FindAllStringSubmatch(cells[col], -1) {
			planned[m[1]] = true
		}
	}
	return planned
}

// missingRepoFiles returns a finding for each file of the §12.1 list that is absent.
func missingRepoFiles(repo string, lines []string) ([]Finding, error) {
	var found []Finding
	for _, name := range repoFiles(section(lines, "### 12.1", "#")) {
		present, err := fileExists(repo, name)
		if err != nil {
			return nil, err
		}
		if !present {
			found = append(found, Finding{Rule: ruleMissingRepoFile, Path: name})
		}
	}
	return found, nil
}

// repoFiles returns the files of the §12.1 list: the backticked names of its first sentence,
// outside parentheses. A parenthesis explains a file, and the sentences after the list name what
// does not exist or which commands to run, so neither names a required file.
func repoFiles(sec []string) []string {
	list := firstSentence(stripParens(strings.Join(sec, "\n")))
	var names []string
	for _, m := range backtickPattern.FindAllStringSubmatch(list, -1) {
		names = append(names, m[1])
	}
	return names
}

// stripParens removes every parenthesised span of s, nested ones included.
func stripParens(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// firstSentence returns s up to the first full stop outside backticks that ends a sentence.
func firstSentence(s string) string {
	inTick := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '`':
			inTick = !inTick
		case s[i] == '.' && !inTick && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\n'):
			return s[:i]
		}
	}
	return s
}

// section returns the lines after the heading that starts with head, up to the next line that
// starts with end.
func section(lines []string, head, end string) []string {
	for i, line := range lines {
		if !strings.HasPrefix(line, head) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], end) {
				return lines[i+1 : j]
			}
		}
		return lines[i+1:]
	}
	return nil
}
