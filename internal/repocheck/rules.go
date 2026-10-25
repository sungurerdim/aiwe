package repocheck

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The rule names of check repo/rules (SPEC §12.1, L2, STD-1, STD-2, STD-29, STD-44).
const (
	ruleMissingRepoFile   = "missing_repo_file"
	ruleWrongLicenceText  = "wrong_licence_text"
	ruleUnstatedStandard  = "unstated_standard"
	ruleWrongSPDX         = "wrong_spdx_identifier"
	licenceFile           = "LICENSE"
	conductFile           = "CODE_OF_CONDUCT.md"
	securityFile          = "SECURITY.md"
	moduleFile            = "go.mod"
	licenceIdentifier     = "MIT"
	covenantMarker        = "contributor-covenant.org/version/3/0"
	privateReportingText  = "report a vulnerability"
	spdxIdentifierPrefix  = "SPDX-License-Identifier:"
	licenceGrantMarker    = "Permission is hereby granted, free of charge, to any person obtaining a copy"
	licenceDisclaimerLine = "THE SOFTWARE IS PROVIDED \"AS IS\""
)

// specFile is the spec whose §12.1 list names the required repository files.
const specFile = "docs/SPEC.md"

// backtickPattern matches one backticked span of the spec and captures its text.
var backtickPattern = regexp.MustCompile("`([^`]+)`")

// Rules checks that every repository file the §12.1 list of docs/SPEC.md names exists, and that the
// four files whose text a standard names say what that standard requires (STD-1, STD-2, STD-29,
// STD-44). An absent one is missing_repo_file and its text is not judged: there is none. It parses
// no spec table (L2). A repository without a readable spec, or a spec whose §12.1 names no file, is
// an error: no rule was checked (DR-6).
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
	text, err := contentFindings(repo, found)
	if err != nil {
		return nil, err
	}
	found = append(found, text...)
	sortFindings(found)
	return found, nil
}

// contentFindings returns the text findings of the standards' files that are present. absent names
// the files the existence rule already reported. A file the list does not name and the repository
// lacks has no text to judge: the file rule owns absence, so it is skipped. A file that is present
// but cannot be read as text is an error, never a pass or a skip: its text was not judged (DR-6).
func contentFindings(repo string, absent []Finding) ([]Finding, error) {
	var found []Finding
	for _, c := range textChecks() {
		if reported(absent, c.path) {
			continue
		}
		body, err := readStandardText(repo, c.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !c.stated(body) {
			found = append(found, Finding{Rule: c.rule, Path: c.path})
		}
	}
	return found, nil
}

// readStandardText reads the file rel of repo as text. The errors name rel and give the reason (an
// oversized or non-regular file, invalid UTF-8); none carries the file's content.
func readStandardText(repo, rel string) (string, error) {
	raw, err := readRepoFile(repo, rel)
	if err != nil {
		return "", fmt.Errorf("rules: %w", err)
	}
	text, err := core.ReadText(raw)
	if err != nil {
		return "", fmt.Errorf("rules: %s: %w", rel, err)
	}
	return text, nil
}

// textCheck is one standard's file and the statement that meets it.
type textCheck struct {
	path   string
	rule   string
	stated func(string) bool
}

// textChecks are the files a standard names, each with the statement that satisfies it.
func textChecks() []textCheck {
	return []textCheck{
		{licenceFile, ruleWrongLicenceText, statesMITLicence},
		{moduleFile, ruleWrongSPDX, statesSPDXIdentifier},
		{conductFile, ruleUnstatedStandard, statesCovenant},
		{securityFile, ruleUnstatedStandard, statesPrivateReporting},
	}
}

// statesMITLicence reports the MIT grant and its disclaimer: a file that names MIT but carries the
// text of another licence misleads the reader about what they may do (STD-1, Q1).
func statesMITLicence(body string) bool {
	return strings.Contains(body, licenceGrantMarker) && strings.Contains(body, licenceDisclaimerLine)
}

// statesCovenant reports a link to Contributor Covenant 3.0, the version STD-2 names. A link to
// another version is not this standard: its text differs from the one the project adopted.
func statesCovenant(body string) bool {
	return strings.Contains(strings.ToLower(body), covenantMarker)
}

// statesPrivateReporting reports the private channel STD-29 names: the repository's own Security tab
// report form, so a reporter never has to open a public issue.
func statesPrivateReporting(body string) bool {
	return strings.Contains(strings.ToLower(body), privateReportingText)
}

// statesSPDXIdentifier reports the licence identifier STD-44 wants in the package metadata: at least
// one `SPDX-License-Identifier:` line, as a `//` comment of go.mod, and every such line naming MIT,
// the licence the LICENSE carries. The owner put it in go.mod, so go.mod is read, not LICENSE. A
// missing identifier is not a pass: a rule that passes on absence can never be red. Another
// identifier, an empty one or a compound one such as "MIT OR Apache-2.0" tells a consumer a
// permission the licence text does not give. The line is read as internal/core/tools.go spdxLicence
// reads it (line start after a byte order mark, spaces and the comment marker, same prefix); unlike
// it, the whole remainder must be the identifier, not its first word.
func statesSPDXIdentifier(body string) bool {
	found := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\uFEFF"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "//"))
		if !strings.HasPrefix(line, spdxIdentifierPrefix) {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(line, spdxIdentifierPrefix)) != licenceIdentifier {
			return false
		}
		found = true
	}
	return found
}

// reported reports whether the existence rule already named path.
func reported(absent []Finding, path string) bool {
	for _, f := range absent {
		if f.Path == path {
			return true
		}
	}
	return false
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

// repoFiles returns the files of the §12.1 list: the backticked names of its first paragraph,
// outside parentheses, except those of a sentence that opens with "No". A parenthesis explains a
// file, a "No" sentence names what does not exist, and the paragraphs after the list name commands,
// so none of them names a required file. The whole paragraph is read, so a full stop inside the
// list, such as "e.g.", never drops the files after it.
func repoFiles(sec []string) []string {
	var names []string
	for _, s := range sentences(stripParens(strings.Join(firstParagraph(sec), "\n"))) {
		if words := strings.Fields(s); len(words) > 0 && words[0] == "No" {
			continue
		}
		for _, m := range backtickPattern.FindAllStringSubmatch(s, -1) {
			names = append(names, m[1])
		}
	}
	return names
}

// firstParagraph returns the lines of sec from its first non-blank line up to the next blank line.
func firstParagraph(sec []string) []string {
	if len(sec) == 0 {
		return nil
	}
	start := 0
	for start < len(sec) && strings.TrimSpace(sec[start]) == "" {
		start++
	}
	for end := start; end < len(sec); end++ {
		if strings.TrimSpace(sec[end]) == "" {
			return sec[start:end]
		}
	}
	return sec[start:]
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

// sentences splits s at every full stop outside backticks that is followed by a space, a line
// break or the end of s.
func sentences(s string) []string {
	var out []string
	inTick := false
	from := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '`':
			inTick = !inTick
		case s[i] == '.' && !inTick && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\n'):
			out = append(out, s[from:i])
			from = i + 1
		}
	}
	return append(out, s[from:])
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
