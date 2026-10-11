package repocheck

import (
	"regexp"
	"slices"
	"strings"
)

// The rule names of the commit message check (SPEC §4, §12.3).
const (
	ruleEmptyMessage       = "empty_message"
	ruleHeaderFormat       = "header_format"
	ruleUnknownType        = "unknown_type"
	ruleBodySeparator      = "body_separator"
	ruleBreakingFooterCase = "breaking_footer_case"
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

// CommitMessage checks one commit message against Conventional Commits (SPEC §4): a header of a
// known type, one blank line before the body, and a BREAKING CHANGE footer in upper case. Comment
// lines and everything below the scissors line are dropped first, as git drops them.
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
	return found
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

// checkHeader checks the header line h: its form, then its type.
func checkHeader(h messageLine) []Finding {
	m := headerPattern.FindStringSubmatch(h.text)
	switch {
	case m == nil:
		return []Finding{{Rule: ruleHeaderFormat, Path: commitMessagePath, Line: h.n}}
	case !slices.Contains(commitTypes(), m[1]):
		return []Finding{{Rule: ruleUnknownType, Path: commitMessagePath, Line: h.n}}
	}
	return nil
}
