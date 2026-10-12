package core_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// errorsTestBare reports whether err is the sentinel itself: errors.Is finds sentinel, err has the
// sentinel's text and err neither wraps an error nor matches by an Is method. With no Unwrap and
// no Is method, errors.Is reduces to identity, so the check is as strict as err == sentinel.
func errorsTestBare(err, sentinel error) bool {
	if !errors.Is(err, sentinel) || err.Error() != sentinel.Error() {
		return false
	}
	switch any(err).(type) {
	case interface{ Unwrap() error }, interface{ Unwrap() []error }, interface{ Is(error) bool }:
		return false
	}
	return true
}

func TestErrors(t *testing.T) {
	t.Run("wrapped or joined sentinel taken for the bare sentinel", func(t *testing.T) {
		other := errors.New("state file unreadable")
		tests := []struct {
			name string
			err  error
			want bool
		}{
			{name: "bare sentinel refused", err: core.ErrConfigInvalid, want: true},
			{name: "joined sentinel accepted", err: errors.Join(core.ErrConfigInvalid, other)},
			{name: "sentinel wrapped twice in one error accepted", err: fmt.Errorf("%w %w", core.ErrConfigInvalid, other)},
			{name: "sentinel wrapped with context accepted", err: fmt.Errorf("read state: %w", core.ErrConfigInvalid)},
			{name: "new error with the sentinel's text accepted", err: errors.New(core.ErrConfigInvalid.Error())},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if got := errorsTestBare(tc.err, core.ErrConfigInvalid); got != tc.want {
					t.Errorf("errorsTestBare(%v, ErrConfigInvalid) = %v, want %v", tc.err, got, tc.want)
				}
			})
		}
	})

	t.Run("error matched by its text instead of errors.Is or errors.As", func(t *testing.T) {
		tests := []struct {
			name    string
			err     error
			outcome core.Outcome
		}{
			{
				name:    "a foreign error with the sentinel's text is taken for the sentinel",
				err:     errors.New("param_duplicate"),
				outcome: "error",
			},
			{
				name:    "a foreign error wrapping the sentinel's text is taken for the sentinel",
				err:     fmt.Errorf("parameter %q: %w", "repo", errors.New("param_undeclared")),
				outcome: "error",
			},
			{
				name:    "a sentinel wrapped with context is not found because its text changed",
				err:     fmt.Errorf("command engine unit-run parameter %q: %w", "repo", core.ErrParamUndeclared),
				outcome: "param_undeclared",
			},
			{
				name:    "a sentinel joined with another error is not found",
				err:     errors.Join(errors.New("close ledger: file already closed"), core.ErrParamDuplicate),
				outcome: "param_duplicate",
			},
			{
				name:    "a core.Error wrapped with context is not found by errors.As",
				err:     fmt.Errorf("clone %s: %w", "sungurerdim/"+core.Product, &core.Error{Outcome: "usage", Expected: "a repository path", Found: "an empty value", Next: "pass --repo"}),
				outcome: "usage",
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if got := core.ErrorResult(tc.err).Outcome; got != tc.outcome {
					t.Errorf("ErrorResult(%q).Outcome = %q, want %q", tc.err, got, tc.outcome)
				}
			})
		}
	})

	t.Run("core.Error hides its cause from errors.Is and errors.As", func(t *testing.T) {
		e := &core.Error{Outcome: "param_duplicate", Expected: "one meaning per name", Found: "repo defined twice",
			Next: "remove one definition", Err: core.ErrParamDuplicate}
		wrapped := fmt.Errorf("check registry: %w", e)
		if !errors.Is(wrapped, core.ErrParamDuplicate) {
			t.Error("errors.Is(wrapped core.Error, ErrParamDuplicate) = false: Unwrap does not return Err")
		}
		if got := errors.Unwrap(e); !errorsTestBare(got, core.ErrParamDuplicate) {
			t.Errorf("(*core.Error).Unwrap() = %v, want ErrParamDuplicate", got)
		}
		var target *core.Error
		if !errors.As(wrapped, &target) || target != e {
			t.Error("errors.As(wrapped, *core.Error) does not find the core.Error")
		}
		if (&core.Error{Outcome: "usage", Expected: "x", Found: "y", Next: "z"}).Unwrap() != nil {
			t.Error("(*core.Error).Unwrap() without Err is not nil")
		}
	})

	t.Run("message not in the form outcome: expected x; found y; next: action", func(t *testing.T) {
		tests := []struct {
			name string
			err  *core.Error
			want string
		}{
			{
				name: "explicit next action not rendered",
				err:  &core.Error{Outcome: "usage", Expected: "--format text or json", Found: "yaml", Next: "pass --format json"},
				want: "usage: expected --format text or json; found yaml; next: pass --format json",
			},
			{
				name: "Unicode in expected and found mangled",
				err:  &core.Error{Outcome: "usage", Expected: "a path under belgeler/özet.md", Found: "çalışma dizini", Next: "pass --repo"},
				want: "usage: expected a path under belgeler/özet.md; found çalışma dizini; next: pass --repo",
			},
			{
				name: "cause text replaces the template",
				err: &core.Error{Outcome: "param_duplicate", Expected: "one meaning per name", Found: "repo defined twice",
					Next: "remove one definition", Err: core.ErrParamDuplicate},
				want: "param_duplicate: expected one meaning per name; found repo defined twice; next: remove one definition",
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if got := tc.err.Error(); got != tc.want {
					t.Errorf("Error() = %q, want %q", got, tc.want)
				}
			})
		}
	})

	// SPEC §5.3: every outcome has a template; a core.Error without Next takes the table's action.
	t.Run("outcome without its message template", func(t *testing.T) {
		rows := core.Outcomes()
		if len(rows) == 0 {
			t.Fatal("core.Outcomes() is empty")
		}
		for _, r := range rows {
			t.Run(string(r.Outcome), func(t *testing.T) {
				e := &core.Error{Outcome: r.Outcome, Expected: "a clean working tree", Found: "2 modified files"}
				want := fmt.Sprintf("%s: expected a clean working tree; found 2 modified files; next: %s", r.Outcome, r.Next)
				if r.Next == "" {
					t.Fatalf("outcome %q has no next action for its template", r.Outcome)
				}
				if got := e.Error(); got != want {
					t.Errorf("Error() = %q, want %q", got, want)
				}
			})
		}
	})
}
