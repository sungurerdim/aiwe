package item

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
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
// with an index for a list entry, such as "done[0].check". Reason never repeats the refused value,
// which is untrusted text.
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

// Validate applies the validator refusals of SPEC §8.4 to a record that already passed Decode and
// returns every defect it finds, none for a record that breaks no rule. It refuses a version v other
// than SchemaVersion, a missing field, a unit without scope.in (DR-7), text that FilterText refuses
// in any field (STD-22), a placeholder or a relative reference in a free text, a shell operator outside
// quotes or a revision that moves with HEAD in a check, a check whose first token does not resolve,
// a scope.in path that does not exist and is not in scope.new, a scope.new path that exists, and a
// done entry with neither a check nor a judgement role. Decode does not check v, so this is the
// place. The K44 field rules, done-entry lint and status consistency are other units.
func Validate(r Record, env Env) []Refusal {
	if env == nil {
		env = noEnv{}
	}
	var v refusals
	v.required(r)
	v.hidden(reflect.ValueOf(r), "")
	v.texts(r)
	v.scope(r, env)
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

// isUnit reports whether t is a dispatchable leaf (SPEC §8.4): a unit carries its file set.
func isUnit(t Type) bool {
	switch t {
	case TypeStory, TypeTask, TypeBug, TypeSpike, TypeChore:
		return true
	case TypeGoal, TypeEpic, TypeOwnerAction:
		return false
	}
	return false
}

// scope refuses a unit without scope.in, a scope.in path that is neither there nor in scope.new, and
// a scope.new path that is there already.
func (v *refusals) scope(r Record, env Env) {
	var s Scope
	if r.Scope != nil {
		s = *r.Scope
	}
	if len(s.In) == 0 && isUnit(r.Type) {
		v.add("scope.in", "is missing: a unit names its file set (DR-7)")
	}
	for i, p := range s.In {
		if !slices.Contains(s.New, p) && !env.Exists(p) {
			v.add(fmt.Sprintf("scope.in[%d]", i), "does not exist and is not in scope.new")
		}
	}
	for i, p := range s.New {
		if env.Exists(p) {
			v.add(fmt.Sprintf("scope.new[%d]", i), "exists already")
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

// headRelative matches a revision relative to HEAD, in any case because git resolves refs
// case-insensitively on some file systems: HEAD or @ followed by ~ or ^, the reflogs HEAD@{n}, @{n}
// and @{-n}, and ORIG_HEAD. The revision must start a token: the argument, or follow a byte that
// is no letter, digit, _ or -, such as the dots of a range or an equals sign. So TestAhead^ in a
// -run pattern, an email address and a branch reflog are not refused, nor are bare HEAD and @.
var headRelative = regexp.MustCompile(`(?i)(?:^|[^\w-])(?:(?:head|@)[~^]|head@\{|@\{|orig_head)`)

// refuseCheck returns why a check cannot run unattended, or "" when it can. A check is split into
// argv by shell-style quoting and runs without a shell, so a shell operator outside quotes is
// refused; it is checked here and never run. The reason never repeats the check.
func refuseCheck(check string, env Env) string {
	argv, err := splitArgv(check)
	if err != nil {
		return err.Error()
	}
	if slices.ContainsFunc(argv, headRelative.MatchString) {
		return "an argument names a revision that moves with HEAD"
	}
	// A check that is not blank holds a token; the length test only keeps argv[0] in range.
	if len(argv) == 0 || argv[0] == "" || !env.Resolves(argv[0]) {
		return "the first token does not resolve"
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
