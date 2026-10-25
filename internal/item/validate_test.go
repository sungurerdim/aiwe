package item_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/item"
)

// fakeEnv is the IO of the validator held in two sets, so a test fixes which commands resolve and
// which paths exist.
type fakeEnv struct {
	commands map[string]bool
	paths    map[string]bool
}

func (e fakeEnv) Resolves(name string) bool { return e.commands[name] }
func (e fakeEnv) Exists(path string) bool   { return e.paths[path] }

// testEnv knows the commands go and git and the file record.go; validate.go does not exist yet.
func testEnv() fakeEnv {
	return fakeEnv{
		commands: map[string]bool{"go": true, "git": true},
		paths:    map[string]bool{"internal/item/record.go": true},
	}
}

// validItem returns a record that breaks no rule of SPEC §8.4 against testEnv; a test breaks one
// thing in it and expects exactly that refusal.
func validItem() item.Record {
	return item.Record{
		V:       item.SchemaVersion,
		ID:      "W-7K3F9Q",
		Title:   "The validator refuses a broken record",
		Type:    item.TypeTask,
		Status:  item.StatusOpen,
		Repo:    "github.com/example/project",
		Outcome: "A broken work item is stopped before anyone relies on it.",
		Flow:    "feature",
		Quality: "reliability",
		Area:    "work/store",
		Scope: &item.Scope{
			In:  []string{"internal/item/record.go", "internal/item/validate.go"},
			New: []string{"internal/item/validate.go"},
		},
		Done: []item.Done{{
			ID:     "the validator refuses a missing field",
			Check:  "go test ./internal/item/ -run TestValidatorRefusesMissingField",
			Expect: "exit 0",
		}},
	}
}

// wantRefused fails unless got holds a refusal that names field and gives a reason.
func wantRefused(t *testing.T, got []item.Refusal, field string) {
	t.Helper()
	for _, r := range got {
		if r.Field == field {
			if strings.TrimSpace(r.Reason) == "" || !strings.Contains(r.Error(), field) {
				t.Errorf("refusal of %q gives reason %q and message %q, want a reason and the field in the message", field, r.Reason, r.Error())
			}
			return
		}
	}
	t.Errorf("no refusal names %q, got %v", field, got)
}

// wantRefusedFor fails unless got holds a refusal that names field and whose reason holds reason,
// so another refusal of the same field cannot pass for it.
func wantRefusedFor(t *testing.T, got []item.Refusal, field, reason string) {
	t.Helper()
	for _, r := range got {
		if r.Field == field && strings.Contains(r.Reason, reason) {
			return
		}
	}
	t.Errorf("no refusal names %q with %q, got %v", field, reason, got)
}

func TestValidatorAcceptsValidRecord(t *testing.T) {
	if got := item.Validate(validItem(), testEnv()); len(got) != 0 {
		t.Fatalf("a record that breaks no rule is refused: %v", got)
	}
}

func TestValidatorRefusesMissingField(t *testing.T) {
	rows := []struct {
		name   string
		field  string
		mutate func(*item.Record)
	}{
		{"id is empty", "id", func(r *item.Record) { r.ID = "" }},
		{"title is empty", "title", func(r *item.Record) { r.Title = "" }},
		{"title is only blanks", "title", func(r *item.Record) { r.Title = " \t " }},
		{"type is empty", "type", func(r *item.Record) { r.Type = "" }},
		{"status is empty", "status", func(r *item.Record) { r.Status = "" }},
		{"repo is empty", "repo", func(r *item.Record) { r.Repo = "" }},
		{"outcome is empty", "outcome", func(r *item.Record) { r.Outcome = "" }},
		{"flow is empty", "flow", func(r *item.Record) { r.Flow = "" }},
		{"quality is empty", "quality", func(r *item.Record) { r.Quality = "" }},
		{"area is empty", "area", func(r *item.Record) { r.Area = "" }},
		{"a done entry has no id", "done[0].id", func(r *item.Record) { r.Done[0].ID = "" }},
		{"a check has no expect", "done[0].expect", func(r *item.Record) { r.Done[0].Expect = "" }},
		{"a judgement has no text", "done[0].text", func(r *item.Record) {
			r.Done[0] = item.Done{ID: "the auditor judges the unit", Judgement: "auditor"}
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := validItem()
			row.mutate(&r)
			wantRefused(t, item.Validate(r, testEnv()), row.field)
		})
	}
}

func TestValidatorRefusesUnknownVersion(t *testing.T) {
	rows := []struct {
		name string
		v    int
	}{
		{"v is missing, so it decodes as 0", 0},
		{"the next version, not yet known", 2},
		{"a negative version", -1},
		{"a far future version", 99},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := validItem()
			r.V = row.v
			wantRefused(t, item.Validate(r, testEnv()), "v")
		})
	}
}

// TestValidatorRefusesUnitWithoutScope pins DR-7: a unit is dispatched with its file set, so a
// story, task, bug, spike or chore without scope.in is refused.
func TestValidatorRefusesUnitWithoutScope(t *testing.T) {
	for _, typ := range []item.Type{item.TypeStory, item.TypeTask, item.TypeBug, item.TypeSpike, item.TypeChore} {
		t.Run(string(typ)+" with no scope", func(t *testing.T) {
			r := validItem()
			r.Type, r.Scope = typ, nil
			wantRefused(t, item.Validate(r, testEnv()), "scope.in")
		})
		t.Run(string(typ)+" with an empty scope.in", func(t *testing.T) {
			r := validItem()
			r.Type, r.Scope.In = typ, nil
			wantRefused(t, item.Validate(r, testEnv()), "scope.in")
		})
	}
}

func TestValidatorAcceptsGroupWithoutScope(t *testing.T) {
	for _, typ := range []item.Type{item.TypeGoal, item.TypeEpic, item.TypeOwnerAction} {
		t.Run(string(typ)+" is no unit, so it needs no scope.in", func(t *testing.T) {
			r := validItem()
			r.Type, r.Scope = typ, nil
			if got := item.Validate(r, testEnv()); len(got) != 0 {
				t.Fatalf("a %s without scope is refused: %v", typ, got)
			}
		})
	}
}

// textRows break a text field with one word; each row names the field the refusal must name.
func textRows(word string) []struct {
	name   string
	field  string
	mutate func(*item.Record)
} {
	return []struct {
		name   string
		field  string
		mutate func(*item.Record)
	}{
		{"in the title", "title", func(r *item.Record) { r.Title = "Fix it " + word }},
		{"in the outcome", "outcome", func(r *item.Record) { r.Outcome = word + " people see less." }},
		{"in problem.symptom", "problem.symptom", func(r *item.Record) { r.Problem = &item.Problem{Symptom: "It fails " + word} }},
		{"in need.smallestChange", "need.smallestChange", func(r *item.Record) { r.Need = &item.Need{SmallestChange: word} }},
		{"in a need alternative", "need.alternatives[0].whyNot", func(r *item.Record) {
			r.Need = &item.Need{Alternatives: []item.Alternative{{What: "keep", WhyNot: "costs more, " + word}}}
		}},
		{"in a scope constraint", "scope.constraints[1]", func(r *item.Record) {
			r.Scope.Constraints = []string{"clean", "stay small " + word}
		}},
		{"in a step", "steps[0].do", func(r *item.Record) { r.Steps = []item.Step{{Do: "write it " + word, At: "internal/item/validate.go"}} }},
		{"in a judgement text", "done[1].text", func(r *item.Record) {
			r.Done = append(r.Done, item.Done{ID: "audit", Judgement: "auditor", Text: "judge " + word})
		}},
		{"in the context", "context", func(r *item.Record) { r.Context = "Seen " + word }},
		{"in a distinct reason", "distinct[0].reason", func(r *item.Record) {
			r.Distinct = []item.Distinct{{ID: "W-ABCDEF", Reason: "differs " + word}}
		}},
		{"in a risk not added", "need.risksNotAdded[0].risk", func(r *item.Record) {
			r.Need = &item.Need{RisksNotAdded: []item.RiskNotAdded{{Risk: "slow " + word}}}
		}},
		{"in a waiting option", "waiting.options[1]", func(r *item.Record) {
			r.Waiting = &item.Waiting{Options: []string{"wait", "go on " + word}}
		}},
		{"in a done expect", "done[0].expect", func(r *item.Record) { r.Done[0].Expect = "exit 0, " + word }},
		{"in the effect of a close", "close.effect.what", func(r *item.Record) {
			r.Close = &item.Close{Effect: item.Effect{What: "refusals " + word}}
		}},
	}
}

func TestValidatorRefusesPlaceholder(t *testing.T) {
	for _, word := range []string{"TBD", "tbd", "etc.", "Etc", "FIXME", "to be determined", "Lorem ipsum"} {
		for _, row := range textRows(word) {
			t.Run(word+" "+row.name, func(t *testing.T) {
				r := validItem()
				row.mutate(&r)
				wantRefused(t, item.Validate(r, testEnv()), row.field)
			})
		}
	}
}

func TestValidatorRefusesRelativeReference(t *testing.T) {
	for _, word := range []string{"above", "Below", "same as", "Same As", "as before", "ditto", "the latter"} {
		for _, row := range textRows(word) {
			t.Run(word+" "+row.name, func(t *testing.T) {
				r := validItem()
				row.mutate(&r)
				wantRefused(t, item.Validate(r, testEnv()), row.field)
			})
		}
	}
}

func TestValidatorKeepsWordsThatContainAPlaceholder(t *testing.T) {
	r := validItem()
	r.Title = "Read the etcetera, tbdx and aboveground pages"
	r.Outcome = "The scheduler is no longer tabled, nor does it belowcase anything."
	if got := item.Validate(r, testEnv()); len(got) != 0 {
		t.Fatalf("a word that only contains a placeholder is refused: %v", got)
	}
}

func TestValidatorRefusesShellOperator(t *testing.T) {
	rows := []struct{ name, check string }{
		{"and", "go test ./... && echo ok"},
		{"or", "go test ./... || true"},
		{"pipe", "go test ./... | cat"},
		{"semicolon", "go test ./...; ls"},
		{"redirect out", "go test ./... > out.txt"},
		{"redirect in", "go test ./... < in.txt"},
		{"backtick", "go test `id`"},
		{"command substitution", "go test $(id)"},
		{"background", "go test ./... &"},
		{"newline", "go test ./...\nrm -rf x"},
		{"operator after a closed quote", `go test -run "A" && true`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := validItem()
			r.Done[0].Check = row.check
			wantRefused(t, item.Validate(r, testEnv()), "done[0].check")
		})
	}
}

// wantAccepted fails when the record with this check is refused.
func wantAccepted(t *testing.T, check string) {
	t.Helper()
	r := validItem()
	r.Done[0].Check = check
	if got := item.Validate(r, testEnv()); len(got) != 0 {
		t.Errorf("check %q is refused: %v", check, got)
	}
}

func TestValidatorKeepsOperatorInsideQuotes(t *testing.T) {
	rows := []struct{ name, check string }{
		{"and inside double quotes", `go test ./internal/item/ -run "A && B"`},
		{"pipe, semicolon and redirect inside single quotes", `go test ./internal/item/ -run 'a|b;c>d'`},
		{"a fixed range that ends at HEAD", `git diff --name-only 100f8ea...HEAD`},
		{"an escaped semicolon", `go test -run Name\;Escaped`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) { wantAccepted(t, row.check) })
	}
}

func TestValidatorKeepsFixedBase(t *testing.T) {
	rows := []struct{ name, check string }{
		{"a sha as the base", "git diff --name-only 100f8ea"},
		{"an email address holds an at sign", "git log --author=dev@example.com"},
		{"a test name that holds head", "go test -run TestHeadline"},
		{"an at sign inside a word", "git log --grep=x@y"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) { wantAccepted(t, row.check) })
	}
}

func TestValidatorRefusesHeadRelativeBase(t *testing.T) {
	rows := []struct{ name, check string }{
		{"tilde with a number", "git diff --name-only HEAD~1"},
		{"tilde alone", "git diff HEAD~"},
		{"range with a tilde", "git diff HEAD~3..HEAD"},
		{"caret", "git diff HEAD^"},
		{"caret with a number", "git diff HEAD^2"},
		{"merge base range", "git diff origin/main...HEAD^"},
		{"inside quotes", `git diff "HEAD~2"`},
		{"at sign with a tilde", "git diff --name-only @~1"},
		{"at sign with a caret", "git diff @^"},
		{"at sign alone", "git diff @"},
		{"at sign as the base of a range", "git diff @..main"},
		{"at sign with a tilde after a range", "git diff origin/main...@~1"},
		{"reflog of HEAD", "git diff HEAD@{1}"},
		{"previous branch", "git diff @{-1}"},
		{"reflog of the current branch", "git diff @{1}"},
		{"lower case", "git diff head~1"},
		{"mixed case caret", "git diff Head^"},
		{"ORIG_HEAD", "git diff ORIG_HEAD"},
		{"orig_head in lower case", "git diff orig_head"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := validItem()
			r.Done[0].Check = row.check
			wantRefused(t, item.Validate(r, testEnv()), "done[0].check")
		})
	}
}

func TestValidatorRefusesUnresolvedCheck(t *testing.T) {
	rows := []struct{ name, check string }{
		{"the first token is no command", "nosuchtool --flag"},
		{"the first token is quoted and unknown", `"nosuchtool" --flag`},
		{"the first token is an empty string", `"" test`},
		{"a quote never closes", `go test -run "A`},
		{"a backslash ends the check", `go test x\`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := validItem()
			r.Done[0].Check = row.check
			wantRefused(t, item.Validate(r, testEnv()), "done[0].check")
		})
	}
	t.Run("no environment resolves nothing", func(t *testing.T) {
		wantRefused(t, item.Validate(validItem(), nil), "done[0].check")
	})
}

func TestValidatorRefusesMissingPath(t *testing.T) {
	t.Run("a path that is not in new and does not exist", func(t *testing.T) {
		r := validItem()
		r.Scope.In = append(r.Scope.In, "internal/item/nothere.go")
		wantRefused(t, item.Validate(r, testEnv()), "scope.in[2]")
	})
	t.Run("no environment finds nothing", func(t *testing.T) {
		wantRefused(t, item.Validate(validItem(), nil), "scope.in[0]")
	})
}

func TestValidatorRefusesExistingNewPath(t *testing.T) {
	r := validItem()
	r.Scope.New = []string{"internal/item/validate.go", "internal/item/record.go"}
	got := item.Validate(r, testEnv())
	wantRefused(t, got, "scope.new[1]")
	for _, ref := range got {
		if ref.Field == "scope.new[0]" {
			t.Errorf("a new path that does not exist is refused: %v", ref)
		}
	}
}

func TestValidatorRefusesDoneWithoutCheckOrRole(t *testing.T) {
	rows := []struct {
		name string
		done item.Done
	}{
		{"only an id", item.Done{ID: "something is done"}},
		{"only a text", item.Done{ID: "something is done", Text: "looks fine"}},
		{"only an expect", item.Done{ID: "something is done", Expect: "exit 0"}},
		{"a blank check and a blank judgement", item.Done{ID: "something is done", Check: " ", Judgement: " "}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := validItem()
			r.Done = append(r.Done, row.done)
			wantRefused(t, item.Validate(r, testEnv()), "done[1]")
		})
	}
}

// TestValidatorRefusesBidiControl pins STD-22 for the item file, which is never trusted: a
// bidirectional control in any text field is refused, a check and a path included.
func TestValidatorRefusesBidiControl(t *testing.T) {
	for _, c := range bidiControls() {
		t.Run(fmt.Sprintf("U+%04X in the title", c), func(t *testing.T) {
			r := validItem()
			r.Title = "Fix " + string(c) + "evil thing"
			wantRefusedFor(t, item.Validate(r, testEnv()), "title", "bidirectional")
		})
	}
	const rlo = "\u202e"
	rows := []struct {
		name   string
		field  string
		mutate func(*item.Record)
	}{
		{"in the outcome", "outcome", func(r *item.Record) { r.Outcome = "Less " + rlo + "work" }},
		{"in a check", "done[0].check", func(r *item.Record) { r.Done[0].Check = "go test " + rlo + "./x" }},
		{"in a done id", "done[0].id", func(r *item.Record) { r.Done[0].ID = "it " + rlo + "works" }},
		{"in a scope.in path", "scope.in[0]", func(r *item.Record) { r.Scope.In[0] += rlo }},
		{"in a step path", "steps[0].at", func(r *item.Record) { r.Steps = []item.Step{{Do: "write", At: "a" + rlo + ".go"}} }},
		{"in a distinct reason", "distinct[0].reason", func(r *item.Record) {
			r.Distinct = []item.Distinct{{ID: "W-ABCDEF", Reason: rlo}}
		}},
		{"in a risk not added", "need.risksNotAdded[0].risk", func(r *item.Record) {
			r.Need = &item.Need{RisksNotAdded: []item.RiskNotAdded{{Risk: rlo}}}
		}},
		{"in a waiting option", "waiting.options[0]", func(r *item.Record) { r.Waiting = &item.Waiting{Options: []string{rlo}} }},
		{"in the problem cause", "problem.cause.text", func(r *item.Record) { r.Problem = &item.Problem{Cause: item.Cause{Text: rlo}} }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := validItem()
			row.mutate(&r)
			wantRefusedFor(t, item.Validate(r, testEnv()), row.field, "bidirectional")
		})
	}
}

// TestValidatorRefusalHidesValue pins that a refusal names the field and never repeats the refused
// value, which is untrusted text.
func TestValidatorRefusalHidesValue(t *testing.T) {
	rows := []struct {
		name   string
		field  string
		value  string
		mutate func(*item.Record, string)
	}{
		{"a missing path", "scope.in[2]", "internal/item/nothere.go", func(r *item.Record, v string) { r.Scope.In = append(r.Scope.In, v) }},
		{"an existing new path", "scope.new[1]", "internal/item/record.go", func(r *item.Record, v string) { r.Scope.New = append(r.Scope.New, v) }},
		{"a first token that does not resolve", "done[0].check", "nosuchtool", func(r *item.Record, v string) { r.Done[0].Check = v + " --flag" }},
		{"a base relative to HEAD", "done[0].check", "HEAD~7", func(r *item.Record, v string) { r.Done[0].Check = "git diff " + v }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := validItem()
			row.mutate(&r, row.value)
			got := item.Validate(r, testEnv())
			wantRefused(t, got, row.field)
			for _, ref := range got {
				if strings.Contains(ref.Error(), row.value) {
					t.Errorf("the refusal %q repeats the value %q", ref.Error(), row.value)
				}
			}
		})
	}
}
