package gates

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestParseDefinitionStepRefusalText catches a refusal of one step that prints its status word
// twice on the CLI text line, or that drops which step was refused (#160).
func TestParseDefinitionStepRefusalText(t *testing.T) {
	cases := []struct {
		name, body, step string
	}{
		{"empty id in step 1", `{"formatVersion":1,"steps":[{"id":"","command":["go","test","./..."]}]}`, "step 1"},
		{"no program in step 2", `{"formatVersion":1,"steps":[` +
			`{"id":"vet","command":["go","vet","./..."]},{"id":"test","command":[]}]}`, "step 2"},
		{"bad pattern in step 1", `{"formatVersion":1,"steps":[` +
			`{"id":"test","command":["go","test"],"expect":{"exitCode":0,"outputMatches":"("}}]}`, "step 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDefinition([]byte(tc.body))
			if err == nil {
				t.Fatal("accepted a gate with an invalid step")
			}
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Outcome != core.OutcomeError {
				t.Fatalf("refusal is not a core.Error with outcome %q: %v", core.OutcomeError, err)
			}
			var out bytes.Buffer
			if err := core.WriteResult(&out, core.FormatText, core.ErrorResult(err)); err != nil {
				t.Fatalf("write text result: %v", err)
			}
			line := out.String()
			if n := strings.Count(line, "error:"); n != 1 {
				t.Errorf("text line names the status %d times, want 1: %q", n, line)
			}
			if !strings.HasPrefix(line, "error: ") {
				t.Errorf("text line does not start with the status word: %q", line)
			}
			if !strings.Contains(line, tc.step) {
				t.Errorf("text line drops %q: %q", tc.step, line)
			}
		})
	}
}
