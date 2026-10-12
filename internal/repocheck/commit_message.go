package repocheck

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// The rule names of the commit message check (SPEC §4, §12.3).
const (
	ruleEmptyMessage       = "empty_message"
	ruleHeaderFormat       = "header_format"
	ruleUnknownType        = "unknown_type"
	ruleBodySeparator      = "body_separator"
	ruleBreakingFooterCase = "breaking_footer_case"
	ruleHeaderLength       = "header_length"
	ruleBodyLineLength     = "body_line_length"
	ruleMissingIssueRef    = "missing_issue_ref"
)

// The length limits of a commit message in runes, not bytes (SPEC §12.3).
const (
	maxHeaderRunes   = 50
	maxBodyLineRunes = 72
)

// commitMessagePath is the path every commit message finding names: a message is not a file.
const commitMessagePath = "commit-message"

// scissorsLine is the line below which git drops a commit message (git commit --cleanup=scissors).
const scissorsLine = "# ------------------------ >8 ------------------------"

// headerPattern matches a Conventional Commits header: type, optional scope, optional breaking
// marker, a colon, one space and a description; it captures the type.
var headerPattern = regexp.MustCompile(`^([a-z]+)(?:\([^()\s]+\))?!?: \S`)

// breakingFooterPattern matches a BREAKING CHANGE footer token in any case.
var breakingFooterPattern = regexp.MustCompile(`(?i)^breaking[ -]change:`)

// issueRefPattern matches the issue line every commit carries: Refs or Closes <owner>/<repo>#N.
var issueRefPattern = regexp.MustCompile(`^(?:Refs|Closes) [A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+#[1-9][0-9]*$`)

// trailerPattern matches a git trailer line: a token, a colon, one space and a value.
var trailerPattern = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9-]*|BREAKING CHANGE): \S`)

// urlPattern matches a URL anywhere in a line.
var urlPattern = regexp.MustCompile(`https?://\S`)

// commitTypes are the types a header may carry: the Conventional Commits types that svu and the
// release notes read (SPEC §12.3); feat and fix bump the version, the others do not.
func commitTypes() []string {
	return []string{"feat", "fix", "docs", "style", "refactor", "perf", "test", "build", "ci", "chore", "revert"}
}

// messageLine is one line of a commit message and its 1-based line number.
type messageLine struct {
	n    int
	text string
}

// CommitMessage checks one commit message against Conventional Commits and SPEC §12.3: a header of
// a known type and at most 50 runes, one blank line before the body, body lines of at most 72 runes
// (URLs, trailers and quoted command or output lines exempt), a BREAKING CHANGE footer in upper
// case, and a Refs or Closes <owner>/<repo>#N line. Comment lines and everything below the
// scissors line are dropped first, as git drops them.
func CommitMessage(msg string) []Finding {
	lines := messageLines(msg)
	start := slices.IndexFunc(lines, func(l messageLine) bool { return strings.TrimSpace(l.text) != "" })
	if start < 0 {
		return []Finding{{Rule: ruleEmptyMessage, Path: commitMessagePath}}
	}
	found := checkHeader(lines[start])
	rest := lines[start+1:]
	if len(rest) > 0 && strings.TrimSpace(rest[0].text) != "" {
		found = append(found, Finding{Rule: ruleBodySeparator, Path: commitMessagePath, Line: rest[0].n})
	}
	for _, l := range rest {
		if breakingFooterPattern.MatchString(l.text) &&
			!strings.HasPrefix(l.text, "BREAKING CHANGE:") && !strings.HasPrefix(l.text, "BREAKING-CHANGE:") {
			found = append(found, Finding{Rule: ruleBreakingFooterCase, Path: commitMessagePath, Line: l.n})
		}
	}
	found = append(found, checkBodyLines(rest)...)
	if !slices.ContainsFunc(rest, func(l messageLine) bool { return issueRefPattern.MatchString(l.text) }) {
		found = append(found, Finding{Rule: ruleMissingIssueRef, Path: commitMessagePath})
	}
	return found
}

// checkBodyLines returns a finding for each body line over 72 runes that is not exempt: a line
// with a URL, a line of the closing trailer block, a line inside a ``` fence, or a quoted command
// or output line (indented by four spaces or a tab, quoted with "> ", or opening with a back quote).
func checkBodyLines(body []messageLine) []Finding {
	trailers := trailerBlock(body)
	var found []Finding
	fenced := false
	for i, l := range body {
		fence := strings.HasPrefix(l.text, "```")
		if fence {
			fenced = !fenced
		}
		if fence || fenced || i >= trailers || utf8.RuneCountInString(l.text) <= maxBodyLineRunes ||
			urlPattern.MatchString(l.text) || isQuoted(l.text) {
			continue
		}
		found = append(found, Finding{Rule: ruleBodyLineLength, Path: commitMessagePath, Line: l.n})
	}
	return found
}

// trailerBlock returns the index in body where the closing trailer block starts: the last
// paragraph when its every line is a trailer or an issue line, else len(body).
func trailerBlock(body []messageLine) int {
	end := len(body)
	for end > 0 && body[end-1].text == "" {
		end--
	}
	start := end
	for start > 0 && body[start-1].text != "" {
		start--
	}
	if start == end {
		return len(body)
	}
	for _, l := range body[start:end] {
		if !trailerPattern.MatchString(l.text) && !issueRefPattern.MatchString(l.text) {
			return len(body)
		}
	}
	return start
}

// isQuoted reports whether line is a quoted command or output line.
func isQuoted(line string) bool {
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") ||
		strings.HasPrefix(line, "> ") || strings.HasPrefix(line, "`")
}

// messageLines returns the lines git keeps of msg: no comment line and nothing from the scissors
// line on; each keeps its line number in msg.
func messageLines(msg string) []messageLine {
	all := strings.Split(strings.ReplaceAll(msg, "\r\n", "\n"), "\n")
	kept := make([]messageLine, 0, len(all))
	for i, text := range all {
		if strings.HasPrefix(text, scissorsLine) {
			break
		}
		if strings.HasPrefix(text, "#") {
			continue
		}
		kept = append(kept, messageLine{n: i + 1, text: strings.TrimRight(text, " \t")})
	}
	return kept
}

// checkHeader checks the header line h: its form, then its type, then its length in runes.
func checkHeader(h messageLine) []Finding {
	m := headerPattern.FindStringSubmatch(h.text)
	switch {
	case m == nil:
		return []Finding{{Rule: ruleHeaderFormat, Path: commitMessagePath, Line: h.n}}
	case !slices.Contains(commitTypes(), m[1]):
		return []Finding{{Rule: ruleUnknownType, Path: commitMessagePath, Line: h.n}}
	case utf8.RuneCountInString(h.text) > maxHeaderRunes:
		return []Finding{{Rule: ruleHeaderLength, Path: commitMessagePath, Line: h.n}}
	}
	return nil
}
