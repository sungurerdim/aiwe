package item

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

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

// texts refuses a placeholder or a relative reference in a free-text field. A path, a check, an id
// and a done id are not free text: the id of a done entry may name the very refusal it tests.
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

// textFields lists every free-text field of a record.
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
		ts = append(ts,
			namedText{fmt.Sprintf("done[%d].expect", i), d.Expect},
			namedText{fmt.Sprintf("done[%d].text", i), d.Text})
	}
	for i, d := range r.Distinct {
		ts = append(ts, namedText{fmt.Sprintf("distinct[%d].reason", i), d.Reason})
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
	for i, k := range n.RisksNotAdded {
		ts = append(ts, namedText{fmt.Sprintf("need.risksNotAdded[%d].risk", i), k.Risk})
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
		for i, o := range r.Waiting.Options {
			ts = append(ts, namedText{fmt.Sprintf("waiting.options[%d]", i), o})
		}
		ts = append(ts, namedText{"waiting.recommendation", r.Waiting.Recommendation})
	}
	if r.Cancel != nil {
		ts = append(ts, namedText{"cancel.reason", r.Cancel.Reason})
	}
	if r.Handoff != nil {
		ts = append(ts, namedText{"handoff.state", r.Handoff.State}, namedText{"handoff.next", r.Handoff.Next})
	}
	if r.Close != nil {
		e := r.Close.Effect
		ts = append(ts, namedText{"close.effect.line", e.Line}, namedText{"close.effect.what", e.What},
			namedText{"close.effect.before", e.Before}, namedText{"close.effect.after", e.After})
	}
	return ts
}

// bidi refuses a bidirectional control in any string of val, a record or a part of one, and names
// the field by its JSON keys (STD-22). The item file is never trusted, so a check, a path or an id
// that a reader sees in another order is refused like a free text.
func (v *refusals) bidi(val reflect.Value, field string) {
	switch k := val.Kind(); {
	case k == reflect.String:
		if r, ok := firstBidi(val.String()); ok {
			v.add(field, "holds the bidirectional control U+%04X", r)
		}
	case k == reflect.Pointer && !val.IsNil():
		v.bidi(val.Elem(), field)
	case k == reflect.Slice:
		for i := range val.Len() {
			v.bidi(val.Index(i), fmt.Sprintf("%s[%d]", field, i))
		}
	case k == reflect.Struct:
		v.bidiFields(val, field)
	}
}

// bidiFields applies bidi to each field of the struct val, named by its JSON key.
func (v *refusals) bidiFields(val reflect.Value, field string) {
	for i := range val.NumField() {
		name, _, _ := strings.Cut(val.Type().Field(i).Tag.Get("json"), ",")
		if field != "" {
			name = field + "." + name
		}
		v.bidi(val.Field(i), name)
	}
}
