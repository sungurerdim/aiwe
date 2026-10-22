// Package roles holds the skeleton role cards, the pinned model list each role runs on,
// and the finding lines a judging role returns (SPEC §9.1, §8.1, §7.9).
package roles

import (
	"regexp"
	"strings"

	"github.com/sungurerdim/aiwe/internal/adapters"
	"github.com/sungurerdim/aiwe/internal/core"
)

// Card is one role card: the SPEC §9.1 fields, with the method in Body.
type Card struct {
	ID      string
	Purpose string
	Trigger string
	Needs   string
	Output  string
	Tier    string
	May     []string
	MayNot  []Limit
	Body    string
}

// Limit is one gated boundary of a card.
type Limit struct {
	Text string
	Gate string
}

// Validate refuses a card that is missing a SPEC §9.1 field, has a blank entry, names a tier other
// than base, raised or top, or carries a body that breaks the §9.1 body rule: no shell command and
// no harness, model, tool or path name. The body states the method in prose, so a name in it would
// bind one card to one adapter, model, tool or repository (DR-3, DR-15, DR-17).
func (c Card) Validate() error {
	fields := []struct{ name, value string }{
		{"id", c.ID},
		{"purpose", c.Purpose},
		{"trigger", c.Trigger},
		{"needs", c.Needs},
		{"output", c.Output},
		{"tier", c.Tier},
		{"body", c.Body},
	}
	for _, f := range fields {
		if blank(f.value) {
			return cardInvalid("a non-blank "+f.name, "a blank "+f.name)
		}
	}
	if !validTier(c.Tier) {
		return cardInvalid("tier base, raised or top", c.Tier)
	}
	if err := validateMay(c.May); err != nil {
		return err
	}
	if err := validateLimits(c.MayNot); err != nil {
		return err
	}
	return validateBody(c.Body)
}

func validTier(tier string) bool {
	switch tier {
	case "base", "raised", "top":
		return true
	default:
		return false
	}
}

func blank(s string) bool {
	return strings.TrimSpace(s) == ""
}

func validateMay(may []string) error {
	if len(may) == 0 {
		return cardInvalid("at least one may entry", "no may entry")
	}
	for _, m := range may {
		if blank(m) {
			return cardInvalid("a non-blank may entry", "a blank may entry")
		}
	}
	return nil
}

func validateLimits(limits []Limit) error {
	if len(limits) == 0 {
		return cardInvalid("at least one limit", "no limit")
	}
	for _, m := range limits {
		if blank(m.Text) {
			return cardInvalid("a limit text", "a limit without text")
		}
		if blank(m.Gate) {
			return cardInvalid("a limit gate", "a limit without a gate")
		}
	}
	return nil
}

// backquote is the character that quotes a command or a name in a card body (SPEC §9.1).
const backquote = "`"

// bodyName is one SPEC §9.1 name a card body may not hold: the kind the fault reports and the name.
type bodyName struct {
	kind string
	name string
}

// bodyNames returns the names a SPEC §9.1 card body may not hold: the harness and the model of the
// pinned adapter, and each tool a run may call. Every name is read from its one owner in adapters,
// so none of them is copied here (DR-17).
func bodyNames() []bodyName {
	names := []bodyName{
		{kind: "harness", name: harnessID()},
		{kind: "model", name: adapters.DefaultModel},
	}
	for _, tool := range []string{adapters.ToolRead, adapters.ToolWrite, adapters.ToolEdit,
		adapters.ToolList, adapters.ToolShell} {
		names = append(names, bodyName{kind: "tool", name: tool})
	}
	return names
}

// validateBody refuses one card body that breaks the SPEC §9.1 body rule.
func validateBody(body string) error {
	fault := bodyFault(body)
	if fault == "" {
		return nil
	}
	return cardInvalid("a body without shell commands and without harness, model, tool or path names",
		fault)
}

// bodyFault returns the first SPEC §9.1 fault of one card body, or "" for a body in method prose:
// the shell command it holds, then the harness, model, tool or path name it names. The order is the
// one the rule writes.
func bodyFault(body string) string {
	if command := commandFault(body); command != "" {
		return command
	}
	for _, n := range bodyNames() {
		if holdsName(body, n.name) {
			return "the " + n.kind + " name " + n.name
		}
	}
	if path := pathFault(body); path != "" {
		return "the path name " + path
	}
	return ""
}

// commandFault returns the shell command one card body holds, or "": a quoted span, a shell
// operator, a flag, or a program the body runs after a command verb (SPEC §9.1, DR-8). A role asks
// the engine to run a command; its card names none.
func commandFault(body string) string {
	if span := quotedSpan(body); span != "" {
		return "the quoted command " + backquote + span + backquote
	}
	if operator := shellOperator(body); operator != "" {
		return "the shell operator " + operator
	}
	if flag := flagToken(body); flag != "" {
		return "the flag " + flag
	}
	if run := programRun(body); run != "" {
		return "the program run " + run
	}
	return ""
}

// quotedSpan returns the first quoted span of one card body, or "": method prose quotes neither a
// name nor a command, so a quoted span is one of them (SPEC §9.1).
func quotedSpan(body string) string {
	_, rest, ok := strings.Cut(body, backquote)
	if !ok {
		return ""
	}
	span, _, ok := strings.Cut(rest, backquote)
	if !ok {
		return rest
	}
	return span
}

// shellOperators returns the shell operators a card body may hold (SPEC §9.1): a doubled ampersand
// or pipe, an opened command substitution and the two redirections.
func shellOperators() []string {
	return []string{"&&", "||", "$(", ">", "<"}
}

// shellOperator returns the first shell operator of one card body, or "": a doubled ampersand or
// pipe, an opened command substitution or a redirection. The first operator in the body is the
// fault. A single ampersand or dollar is no operator, a lone `|` is the `GREEN|RED` of a return
// format (§9.3), and a semicolon is prose punctuation (SPEC §9.1).
func shellOperator(body string) string {
	found, at := "", -1
	for _, operator := range shellOperators() {
		if i := strings.Index(body, operator); i >= 0 && (at < 0 || i < at) {
			found, at = operator, i
		}
	}
	return found
}

// flagToken returns the first flag of one card body, or "": method prose names no option of a
// program, so a token that starts with a dash is an argument of a command (SPEC §9.1).
func flagToken(body string) string {
	for _, token := range strings.Fields(body) {
		if len(token) > 1 && token[0] == '-' {
			return token
		}
	}
	return ""
}

// pathFault returns one path name of one card body, or "": a token that holds a path separator names
// a file or a directory, and the same card serves every repository (SPEC §9.1, DR-3). It fails
// closed: a token with a separator is read as a path.
func pathFault(body string) string {
	for _, token := range strings.Fields(body) {
		if len(token) > 1 && strings.ContainsAny(token, `/\`) {
			return token
		}
	}
	return ""
}

// commandVerbs returns the verbs after which a card body would name a program to run (SPEC §9.1).
func commandVerbs() []string {
	return []string{"run", "runs", "running", "execute", "executes", "executing", "invoke", "invokes"}
}

// proseWords returns the words that follow a command verb in method prose without naming a program:
// an article, a determiner, a pronoun, a preposition, a conjunction, a form of "be", "have" or "do",
// and a few adverbs. Two of them after the verb name no program.
func proseWords() []string {
	return []string{
		"a", "an", "the", "this", "that", "these", "those", "any", "each", "every", "one", "some",
		"no", "not", "only", "all", "its", "it", "they", "their", "our", "your", "his", "her",
		"of", "in", "on", "at", "by", "for", "to", "from", "with", "without", "into", "over",
		"under", "against", "after", "before", "between", "when", "while", "as", "than", "whether",
		"and", "or", "but", "then", "again", "once", "twice", "first", "last", "same", "own",
		"is", "are", "was", "were", "be", "been", "being", "has", "have", "had", "does", "do", "did",
	}
}

// programRun returns the program and its argument one card body runs after a command verb, or "":
// two words after the verb that are no prose word name a program with an argument, as in "run go
// test". One word, or a prose word, names none: "run tests" and "run the check" stay prose.
func programRun(body string) string {
	words := strings.Fields(body)
	for i, token := range words {
		if !isWord(token, commandVerbs()) {
			continue
		}
		if i+2 >= len(words) {
			continue
		}
		program, argument := tokenWord(words[i+1]), tokenWord(words[i+2])
		if namesProgram(program) && namesProgram(argument) {
			return backquote + program + " " + argument + backquote
		}
	}
	return ""
}

// namesProgram reports whether one word after a command verb names a program or an argument: a word
// of lowercase letters, digits, dashes, dots, slashes, tildes or equals signs that method prose does
// not write, so "go" and "test" name a program and "the" does not.
func namesProgram(w string) bool {
	if w == "" || isWord(w, proseWords()) {
		return false
	}
	letters := false
	for i := 0; i < len(w); i++ {
		c := w[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		switch {
		case c >= 'a' && c <= 'z':
			letters = true
		case c >= '0' && c <= '9', strings.IndexByte("-._/~=", c) >= 0:
		default:
			return false
		}
	}
	return letters
}

// tokenWord returns one whitespace-separated token without the prose punctuation around it, so
// "test" and "test." are the same word.
func tokenWord(token string) string {
	return strings.Trim(token, `.,;:!?"'()[]{}`+backquote)
}

// isWord reports whether w, whatever its case, is one of words; the punctuation around w is not part
// of the word.
func isWord(w string, words []string) bool {
	w = tokenWord(w)
	for _, candidate := range words {
		if strings.EqualFold(w, candidate) {
			return true
		}
	}
	return false
}

// holdsName reports whether body holds name as a whole word, whatever its case: the byte before and
// after a match is outside a name, so "reader" holds no "read" and the method prose of a shipped
// card stays valid (SPEC §9.1).
func holdsName(body, name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i+len(name) <= len(body); i++ {
		if !strings.EqualFold(body[i:i+len(name)], name) {
			continue
		}
		if !nameByte(body, i-1) && !nameByte(body, i+len(name)) {
			return true
		}
	}
	return false
}

// nameByte reports whether the byte at i of s is part of a name: a letter, a digit, a hyphen, an
// underscore or a dot. An index outside s is part of no name.
func nameByte(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '-' || c == '_' || c == '.'
}

func cardInvalid(expected, found string) error {
	return &core.Error{Outcome: core.OutcomeError, Expected: expected, Found: found,
		Next: "correct the card to the format of SPEC §9.1"}
}

// findingSep separates the five fields of one finding line (K8).

// githubToken, pemPrivateKey and drCite match a token, a private key and a DR id (K8, DR-19).
var (
	githubToken   = regexp.MustCompile(`(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{22,255}`)
	pemPrivateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	drCite        = regexp.MustCompile(`\bDR-[0-9]+\b`)
)

// Finding is one parsed finding line: where, which rule, what was seen, who loses what, and the class.

// Judgement is a judging role's return: affected parties, kept findings, and whether a RED opens a fix round (K8).

// ParseJudgement reads a judging return. Findings dedupe by location and rule and take derived severity (K8).

// maskSecrets replaces a GitHub token, a PEM private key and a URL password. Words around them stay (DR-19, DR-10).
