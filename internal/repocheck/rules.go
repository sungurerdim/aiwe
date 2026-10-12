package repocheck

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// ruleMissingRepoFile is the one rule of check repo/rules (SPEC §12.1, L2).
const ruleMissingRepoFile = "missing_repo_file"

// specFile is the spec whose §12.1 list names the required repository files.
const specFile = "docs/SPEC.md"

// backtickPattern matches one backticked span of the spec and captures its text.
var backtickPattern = regexp.MustCompile("`([^`]+)`")

// Rules checks that every repository file the §12.1 list of docs/SPEC.md names exists; an absent
// one is missing_repo_file. It parses no spec table (L2). A repository without a readable spec, or
// a spec whose §12.1 names no file, is an error: no rule was checked (DR-6).
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
	found, err := missingRepoFiles(repo, lines)
	if err != nil {
		return nil, err
	}
	sortFindings(found)
	return found, nil
}

// missingRepoFiles returns a finding for each file of the §12.1 list that is absent; a §12.1
// section that is missing or names no file is an error (DR-6).
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
