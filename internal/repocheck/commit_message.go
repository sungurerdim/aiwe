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
	ruleMissingEffect      = "missing_effect"
	ruleClosesOtherRepo    = "closes_other_repo"
	ruleClosesRepoUnknown  = "closes_repo_unknown"
	ruleClosingKeywordForm = "closing_keyword_form"
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

// issueRefPattern matches the issue line every commit carries: Refs or Closes <owner>/<repo>#N; it
// captures the keyword and the <owner>/<repo>.
var issueRefPattern = regexp.MustCompile(`^(Refs|Closes) ([A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+)#[1-9][0-9]*$`)

// closingKeywordPattern matches any GitHub closing keyword, with or without a colon, followed by an
// issue reference (#N, <owner>/<repo>#N, a GitHub issue URL or GH-N), in any case and anywhere in a
// line; the strict whole-line Closes form is the only allowed occurrence.
var closingKeywordPattern = regexp.MustCompile(`(?i)\b(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)\b:?\s*` +
	`(?:(?:[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+)?#[1-9][0-9]*|https?://github\.com/[^/\s]+/[^/\s]+/issues/[0-9]+|GH-[0-9]+)`)

// effectPattern matches the Effect line a closing commit carries: Effect: <before> → <after>.
var effectPattern = regexp.MustCompile(`^Effect: \S.* → \S`)

// slugPattern matches an <owner>/<repo> slug.
var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)

// remotePathPatterns returns the compiled patterns that capture the path of a remote URL:
// the URL form (https, ssh, git) and the scp-like ssh form user@host:path.
func remotePathPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`^(?:https?|ssh|git)://[^/\s]+/(\S+)$`),
		regexp.MustCompile(`^[^@/\s]+@[^:/\s]+:(\S+)$`),
	}
}

// codeSpanPattern matches a line that is one back-quoted code span.
var codeSpanPattern = regexp.MustCompile("^`[^`]+`$")

// trailerPattern matches a git trailer line in the closing trailer block (SPEC §12.3).
var trailerPattern = regexp.MustCompile(`^(?:Effect|Co-Authored-By|Signed-off-by|Run-Id|BREAKING CHANGE|BREAKING-CHANGE): \S`)

// urlPattern matches a URL anywhere in a line.
var urlPattern = regexp.MustCompile(`https?://\S+`)

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
// case, a Refs or Closes <owner>/<repo>#N line, and for a Closes line an Effect line and the slug of
// the repository the commit lands in. An empty slug is unknown: a Closes line is then a finding.
// Comment lines and everything below the scissors line are dropped first, as git drops them.
func CommitMessage(msg, slug string) []Finding {
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
	fenced := fencedLines(rest)
	found = append(found, checkIssueLines(rest, slug, fenced)...)
	return append(found, checkClosingKeywords(lines[start:])...)
}

// checkIssueLines checks the issue lines of body: one Refs or Closes line at least; a Closes line
// names the repository slug and comes with an Effect line. An Effect line inside a fence does not
// count.
func checkIssueLines(body []messageLine, slug string, fenced []bool) []Finding {
	var found []Finding
	refs, closes := false, false
	for _, l := range body {
		m := issueRefPattern.FindStringSubmatch(l.text)
		if m == nil {
			continue
		}
		refs = true
		if m[1] != "Closes" {
			continue
		}
		closes = true
		switch {
		case slug == "":
			found = append(found, Finding{Rule: ruleClosesRepoUnknown, Path: commitMessagePath, Line: l.n})
		case !strings.EqualFold(m[2], slug):
			found = append(found, Finding{Rule: ruleClosesOtherRepo, Path: commitMessagePath, Line: l.n})
		}
	}
	if !refs {
		found = append(found, Finding{Rule: ruleMissingIssueRef, Path: commitMessagePath})
	}
	hasEffect := false
	for i, l := range body {
		if !fenced[i] && effectPattern.MatchString(l.text) {
			hasEffect = true
			break
		}
	}
	if closes && !hasEffect {
		found = append(found, Finding{Rule: ruleMissingEffect, Path: commitMessagePath})
	}
	return found
}

// checkClosingKeywords reports any GitHub closing keyword in lines, the header included, that is
// not the strict whole-line Closes <owner>/<repo>#N form. No line is exempt: quoted and fenced
// lines are exempt from the length limit only, and GitHub acts on a keyword in any line.
func checkClosingKeywords(lines []messageLine) []Finding {
	var found []Finding
	for _, l := range lines {
		if issueRefPattern.MatchString(l.text) {
			continue
		}
		if closingKeywordPattern.MatchString(l.text) {
			found = append(found, Finding{Rule: ruleClosingKeywordForm, Path: commitMessagePath, Line: l.n})
		}
	}
	return found
}

// RepoSlug returns the <owner>/<repo> of a git remote URL in the https, ssh or scp-like form, or ""
// when the URL holds none.
func RepoSlug(remoteURL string) string {
	u := strings.TrimSpace(remoteURL)
	for _, p := range remotePathPatterns() {
		m := p.FindStringSubmatch(u)
		if m == nil {
			continue
		}
		path := strings.TrimSuffix(strings.TrimSuffix(m[1], "/"), ".git")
		if slugPattern.MatchString(path) {
			return path
		}
		return ""
	}
	return ""
}

// checkBodyLines returns a finding for each body line over 72 runes that is not exempt: a line of
// the closing trailer block other than a BREAKING CHANGE footer, which is prose, a line between a
// matched opening and closing ``` fence line, a line that is 72 runes or less once its URLs are
// removed, or a quoted command or output line (indented by four spaces or a tab, or one back-quoted
// code span). Fence lines themselves are checked.
func checkBodyLines(body []messageLine) []Finding {
	trailers := trailerBlock(body)
	fenced := fencedLines(body)
	var found []Finding
	for i, l := range body {
		prose := urlPattern.ReplaceAllString(l.text, "")
		trailer := i >= trailers && !breakingFooterPattern.MatchString(l.text)
		if fenced[i] || trailer || utf8.RuneCountInString(prose) <= maxBodyLineRunes || isQuoted(l.text) {
			continue
		}
		found = append(found, Finding{Rule: ruleBodyLineLength, Path: commitMessagePath, Line: l.n})
	}
	return found
}

// fencedLines marks the lines of body between a ``` fence line and the next one; a fence with no
// closing line marks nothing.
func fencedLines(body []messageLine) []bool {
	fenced := make([]bool, len(body))
	open := -1
	for i, l := range body {
		if !strings.HasPrefix(l.text, "```") {
			continue
		}
		if open < 0 {
			open = i
			continue
		}
		for j := open + 1; j < i; j++ {
			fenced[j] = true
		}
		open = -1
	}
	return fenced
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
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") || codeSpanPattern.MatchString(line)
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
