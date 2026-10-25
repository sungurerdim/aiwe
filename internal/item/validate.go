package item

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Env is the outside world that validation needs, taken through an interface so that Validate stays
// a pure function of its arguments (STD-10): the adapter that implements it lives in an *_io.go
// file. A nil Env resolves nothing and finds nothing, so every check and every path is refused.
type Env interface {
	// Resolves reports whether the command name, the first token of a check, resolves to something
	// that can run.
	Resolves(name string) bool
	// Exists reports whether the path exists in the work tree; for a glob, whether it matches a file.
	Exists(path string) bool
}

// noEnv is the Env that fails closed.
type noEnv struct{}

func (noEnv) Resolves(string) bool { return false }
func (noEnv) Exists(string) bool   { return false }

// Refusal is one defect of a record. Field names the field in the dotted form of the JSON keys,
// with an index for a list entry, such as "done[0].check".
type Refusal struct {
	Field  string
	Reason string
}

// Error gives the field and the reason.
func (r Refusal) Error() string { return r.Field + ": " + r.Reason }

// refusals collects the defects found in a record.
type refusals []Refusal

func (v *refusals) add(field, format string, args ...any) {
	*v = append(*v, Refusal{Field: field, Reason: fmt.Sprintf(format, args...)})
}

// Validate applies the refusals of SPEC §8.4 to a record that already passed Decode and returns every
// defect it finds, none for a record that breaks no rule. It refuses a version v other than
// SchemaVersion, a missing field, a placeholder or a relative reference in a text, a shell operator
// outside quotes or a HEAD-relative base in a check, a check whose first token does not resolve, a
// scope.in path that does not exist and is not in scope.new, a scope.new path that exists, and a done
// entry with neither a check nor a judgement role. Decode does not check v, so this is the place.
// The K44 field rules, done-entry lint and status consistency are other units.
func Validate(r Record, env Env) []Refusal {
	if env == nil {
		env = noEnv{}
	}
	var v refusals
	v.required(r)
	v.texts(r)
	v.scope(r.Scope, env)
	v.done(r.Done, env)
	return v
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// required refuses a version other than SchemaVersion and a blank field that every record carries.
func (v *refusals) required(r Record) {
	if r.V != SchemaVersion {
		v.add("v", "version %d is not %d", r.V, SchemaVersion)
	}
	for _, f := range []struct{ name, value string }{
		{"id", r.ID}, {"title", r.Title}, {"type", string(r.Type)}, {"status", string(r.Status)},
		{"repo", r.Repo}, {"outcome", r.Outcome}, {"flow", r.Flow}, {"quality", r.Quality},
		{"area", r.Area},
	} {
		if blank(f.value) {
			v.add(f.name, "is missing")
		}
	}
}

// scope refuses a scope.in path that is neither there nor in scope.new, and a scope.new path that
// is there already.
func (v *refusals) scope(s *Scope, env Env) {
	if s == nil {
		return
	}
	for i, p := range s.In {
		if !slices.Contains(s.New, p) && !env.Exists(p) {
			v.add(fmt.Sprintf("scope.in[%d]", i), "path %q does not exist and is not in scope.new", p)
		}
	}
	for i, p := range s.New {
		if env.Exists(p) {
			v.add(fmt.Sprintf("scope.new[%d]", i), "path %q exists already", p)
		}
	}
}

// done refuses each entry that is not a check with its expectation or a judgement by a named role
// with its text.
func (v *refusals) done(entries []Done, env Env) {
	for i, d := range entries {
		v.doneEntry(fmt.Sprintf("done[%d]", i), d, env)
	}
}

func (v *refusals) doneEntry(at string, d Done, env Env) {
	if blank(d.ID) {
		v.add(at+".id", "is missing")
	}
	switch {
	case !blank(d.Check):
		if blank(d.Expect) {
			v.add(at+".expect", "is missing")
		}
		if reason := refuseCheck(d.Check, env); reason != "" {
			v.add(at+".check", "%s", reason)
		}
	case !blank(d.Judgement):
		if blank(d.Text) {
			v.add(at+".text", "is missing")
		}
	default:
		v.add(at, "has neither a check nor a judgement role")
	}
}

// headRelative matches a base that moves with HEAD, such as HEAD~1 or HEAD^.
var headRelative = regexp.MustCompile(`HEAD[~^]`)

// refuseCheck returns why a check cannot run unattended, or "" when it can. A check is split into
// argv by shell-style quoting and runs without a shell, so a shell operator outside quotes is
// refused; it is checked here and never run.
func refuseCheck(check string, env Env) string {
	argv, err := splitArgv(check)
	if err != nil {
		return err.Error()
	}
	if len(argv) == 0 {
		return "is empty"
	}
	for _, arg := range argv {
		if headRelative.MatchString(arg) {
			return fmt.Sprintf("argument %q uses a HEAD-relative base", arg)
		}
	}
	if argv[0] == "" || !env.Resolves(argv[0]) {
		return fmt.Sprintf("first token %q does not resolve", argv[0])
	}
	return ""
}

// argvScanner splits a check into argv by shell-style quoting: single quotes are literal, double
// quotes keep a backslash only before a quote or a backslash, and a backslash outside quotes
// escapes the next rune.
type argvScanner struct {
	argv  []string
	cur   []rune
	inArg bool
	quote rune
}

// splitArgv returns the argv of a check, or the reason it has none: a shell operator outside quotes,
// a backslash at the end or a quote that never closes.
func splitArgv(s string) ([]string, error) {
	var sc argvScanner
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		var err error
		if i, err = sc.step(rs, i); err != nil {
			return nil, err
		}
	}
	if sc.quote != 0 {
		return nil, errors.New("a quote is never closed")
	}
	sc.flush()
	return sc.argv, nil
}

func (sc *argvScanner) flush() {
	if sc.inArg {
		sc.argv = append(sc.argv, string(sc.cur))
	}
	sc.cur, sc.inArg = nil, false
}

// step reads the rune at rs[i] and returns the index of the last rune it used.
func (sc *argvScanner) step(rs []rune, i int) (int, error) {
	switch sc.quote {
	case '\'':
		if rs[i] == '\'' {
			sc.quote = 0
		} else {
			sc.cur = append(sc.cur, rs[i])
		}
		return i, nil
	case '"':
		return sc.inDouble(rs, i), nil
	}
	return sc.unquoted(rs, i)
}

func (sc *argvScanner) inDouble(rs []rune, i int) int {
	switch {
	case rs[i] == '"':
		sc.quote = 0
	case rs[i] == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\'):
		i++
		sc.cur = append(sc.cur, rs[i])
	default:
		sc.cur = append(sc.cur, rs[i])
	}
	return i
}

func (sc *argvScanner) unquoted(rs []rune, i int) (int, error) {
	c := rs[i]
	switch {
	case c == '\'' || c == '"':
		sc.quote, sc.inArg = c, true
	case c == '\\':
		if i+1 >= len(rs) {
			return i, errors.New("a backslash ends the check")
		}
		i++
		sc.cur, sc.inArg = append(sc.cur, rs[i]), true
	case c == ' ' || c == '\t':
		sc.flush()
	case strings.ContainsRune("&|;<>`\n\r", c):
		return i, fmt.Errorf("shell operator %q outside quotes", string(c))
	case c == '$' && i+1 < len(rs) && rs[i+1] == '(':
		return i, errors.New(`shell operator "$(" outside quotes`)
	default:
		sc.cur, sc.inArg = append(sc.cur, c), true
	}
	return i, nil
}

// wordRule is a kind of word that a text must not hold, with the phrases of that kind.
type wordRule struct {
	kind    string
	phrases []string
}

// wordRules are the placeholders and the relative references of SPEC §8.4, in lower case.
func wordRules() []wordRule {
	return []wordRule{
		{"placeholder", []string{"tbd", "tba", "tbc", "etc", "fixme", "lorem ipsum", "to be determined", "to be decided"}},
		{"relative reference", []string{"above", "below", "ditto", "same as", "as before", "as mentioned", "the latter", "the former"}},
	}
}

// words returns the words of s in lower case, each with a space before and after it. A word is a
// run of letters, digits, underscore, hyphen and apostrophe; only ASCII is lower-cased (STD-37).
func words(s string) string {
	lower := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
	parts := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '\''
	})
	return " " + strings.Join(parts, " ") + " "
}

// namedText is one text field with its dotted name.
type namedText struct{ field, value string }

// texts refuses a placeholder or a relative reference in a text field. A path, a check, an id and a
// done id are not text: the id of a done entry may name the very refusal it tests.
func (v *refusals) texts(r Record) {
	for _, t := range textFields(r) {
		padded := words(t.value)
		for _, rule := range wordRules() {
			for _, p := range rule.phrases {
				if strings.Contains(padded, " "+p+" ") {
					v.add(t.field, "holds the %s %q", rule.kind, p)
				}
			}
		}
	}
}

// textFields lists the free-text fields of a record.
func textFields(r Record) []namedText {
	ts := []namedText{{"title", r.Title}, {"outcome", r.Outcome}, {"context", r.Context}}
	ts = append(ts, problemTexts(r.Problem)...)
	ts = append(ts, needTexts(r.Need)...)
	if r.Scope != nil {
		for i, c := range r.Scope.Constraints {
			ts = append(ts, namedText{fmt.Sprintf("scope.constraints[%d]", i), c})
		}
	}
	for i, s := range r.Steps {
		ts = append(ts, namedText{fmt.Sprintf("steps[%d].do", i), s.Do})
	}
	for i, d := range r.Done {
		ts = append(ts, namedText{fmt.Sprintf("done[%d].text", i), d.Text})
	}
	return append(ts, statusTexts(r)...)
}

func problemTexts(p *Problem) []namedText {
	if p == nil {
		return nil
	}
	return []namedText{
		{"problem.symptom", p.Symptom}, {"problem.expected", p.Expected},
		{"problem.cause.text", p.Cause.Text}, {"problem.impact", p.Impact},
	}
}

func needTexts(n *Need) []namedText {
	if n == nil {
		return nil
	}
	ts := []namedText{
		{"need.problemEvidence", n.ProblemEvidence}, {"need.deleteOption", n.DeleteOption},
		{"need.existing", n.Existing}, {"need.smallestChange", n.SmallestChange},
		{"need.ifNotDone", n.IfNotDone},
	}
	for i, a := range n.Alternatives {
		ts = append(ts,
			namedText{fmt.Sprintf("need.alternatives[%d].what", i), a.What},
			namedText{fmt.Sprintf("need.alternatives[%d].whyNot", i), a.WhyNot})
	}
	return ts
}

// statusTexts lists the text fields that only some statuses carry.
func statusTexts(r Record) []namedText {
	var ts []namedText
	if r.Defer != nil {
		ts = append(ts, namedText{"defer.reason", r.Defer.Reason})
	}
	if r.Waiting != nil {
		ts = append(ts, namedText{"waiting.recommendation", r.Waiting.Recommendation})
	}
	if r.Cancel != nil {
		ts = append(ts, namedText{"cancel.reason", r.Cancel.Reason})
	}
	if r.Handoff != nil {
		ts = append(ts, namedText{"handoff.state", r.Handoff.State}, namedText{"handoff.next", r.Handoff.Next})
	}
	if r.Close != nil {
		ts = append(ts, namedText{"close.effect.line", r.Close.Effect.Line})
	}
	return ts
}
