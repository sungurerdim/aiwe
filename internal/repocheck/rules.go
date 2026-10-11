package repocheck

import (
	"encoding/json"
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
	ruleEmptyServes     = "empty_serves"
	ruleMissingGateStep = "missing_gate_step"
)

// specFile is the one source of truth that check repo/rules reads (SPEC §3).
const specFile = "docs/SPEC.md"

// gateFile is the gate declaration whose steps check repo/rules compares with §12.2.
const gateFile = "gate.json"

// backtickPattern matches one backticked span of the spec and captures its text.
var backtickPattern = regexp.MustCompile("`([^`]+)`")

// Rules checks repo against the rules its docs/SPEC.md states. A test named in the §3 table
// (relative to internal/) is missing_test when it is absent while the source beside it exists; a
// test whose source is not in the tree yet passes only while a §14 Files cell lists that source.
// A repository file the §12.1 list names is missing_repo_file when it is absent. A §3 row with an
// empty Serves cell is empty_serves, and a §12.2 gate step that gate.json lacks is
// missing_gate_step, each at its spec line. A repository without a readable spec is an error: no
// rule was checked (DR-13).
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
	serves, err := emptyServes(lines)
	if err != nil {
		return nil, err
	}
	steps, err := missingGateSteps(repo, lines)
	if err != nil {
		return nil, err
	}
	found := slices.Concat(tests, files, serves, steps)
	sortFindings(found)
	return found, nil
}

// emptyServes returns a finding for each §3 table row whose Serves cell is empty; a §3 section
// without a Serves column is an error (DR-13).
func emptyServes(lines []string) ([]Finding, error) {
	start := sectionStart(lines, "## 3.")
	rows := 0
	var found []Finding
	tableColumn(section(lines, "## 3.", "## "), "Serves", func(i int, cell string) {
		rows++
		if cell == "" {
			found = append(found, Finding{Rule: ruleEmptyServes, Path: specFile, Line: start + i + 1})
		}
	})
	if rows == 0 {
		return nil, fmt.Errorf("rules: %s: §3 has no Serves column; the Serves rule is unchecked", specFile)
	}
	return found, nil
}

// missingGateSteps returns a finding at the spec row of each §12.2 gate step that gate.json does
// not declare. A §12.2 section that names no step, or a gate.json that cannot be read, is an
// error (DR-13); an absent gate.json declares no step.
func missingGateSteps(repo string, lines []string) ([]Finding, error) {
	start := sectionStart(lines, "### 12.2")
	var found []Finding
	named := 0
	declared, err := gateStepIDs(repo)
	if err != nil {
		return nil, err
	}
	tableColumn(section(lines, "### 12.2", "#"), "Step", func(i int, cell string) {
		m := backtickPattern.FindStringSubmatch(cell)
		if m == nil {
			return
		}
		named++
		if !declared[m[1]] {
			found = append(found, Finding{Rule: ruleMissingGateStep, Path: specFile, Line: start + i + 1})
		}
	})
	if named == 0 {
		return nil, fmt.Errorf("rules: %s: §12.2 names no gate step; the gate step rule is unchecked", specFile)
	}
	return found, nil
}

// gateStepIDs returns the step ids gate.json declares; an absent gate.json declares none.
func gateStepIDs(repo string) (map[string]bool, error) {
	body, present, err := readRepoFileIfPresent(repo, gateFile)
	if err != nil || !present {
		return map[string]bool{}, err
	}
	var gate struct {
		Steps []struct {
			ID string `json:"id"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(body, &gate); err != nil {
		return nil, fmt.Errorf("rules: %s: %w", gateFile, err)
	}
	ids := map[string]bool{}
	for _, s := range gate.Steps {
		ids[s.ID] = true
	}
	return ids, nil
}

// tableColumn calls fn with the index in sec and the trimmed cell of the column named name, for
// each body row of a table whose header row names that column. A row shorter than the header
// yields an empty cell.
func tableColumn(sec []string, name string, fn func(i int, cell string)) {
	col := -1
	for i, line := range sec {
		if !strings.HasPrefix(line, "|") {
			col = -1
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if h := slices.IndexFunc(cells, func(c string) bool { return strings.TrimSpace(c) == name }); h >= 0 {
			col = h
			continue
		}
		if col < 0 {
			continue
		}
		cell := ""
		if col < len(cells) {
			cell = strings.TrimSpace(cells[col])
		}
		if cell != "" && strings.Trim(cell, "-:") == "" {
			continue // the separator row under the header
		}
		fn(i, cell)
	}
}

// missingTests returns a finding for each §3 test that is absent and not excused by §14. A §3 or
// §14 section that is missing or names nothing is an error: that rule was not checked (DR-13).
func missingTests(repo string, lines []string) ([]Finding, error) {
	planned := plannedFiles(section(lines, "## 14.", "## "))
	if len(planned) == 0 {
		return nil, fmt.Errorf("rules: %s: no §14 Files cell lists a file; the phase rule is unchecked", specFile)
	}
	tests := namedTests(section(lines, "## 3.", "## "))
	if len(tests) == 0 {
		return nil, fmt.Errorf("rules: %s: the §3 table names no test; the test rule is unchecked", specFile)
	}
	seen := map[string]bool{}
	var found []Finding
	for _, test := range tests {
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
	tableColumn(sec, "Files", func(_ int, cell string) {
		for _, m := range backtickPattern.FindAllStringSubmatch(cell, -1) {
			planned[m[1]] = true
		}
	})
	return planned
}

// missingRepoFiles returns a finding for each file of the §12.1 list that is absent; a §12.1
// section that is missing or names no file is an error (DR-13).
func missingRepoFiles(repo string, lines []string) ([]Finding, error) {
	names := repoFiles(section(lines, "### 12.1", "#"))
	if len(names) == 0 {
		return nil, fmt.Errorf("rules: %s: §12.1 names no repository file; the file rule is unchecked", specFile)
	}
	var found []Finding
	for _, name := range names {
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
	start := sectionStart(lines, head)
	if start < 0 {
		return nil
	}
	for j := start; j < len(lines); j++ {
		if strings.HasPrefix(lines[j], end) {
			return lines[start:j]
		}
	}
	return lines[start:]
}

// sectionStart returns the index of the line after the heading that starts with head, or -1
// when no line starts with head.
func sectionStart(lines []string, head string) int {
	for i, line := range lines {
		if strings.HasPrefix(line, head) {
			return i + 1
		}
	}
	return -1
}
