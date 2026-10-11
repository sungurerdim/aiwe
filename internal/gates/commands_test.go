package gates

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// commandsTestEnvelope is the JSON result envelope of SPEC §5.3 with the gate report as its data.
type commandsTestEnvelope struct {
	Status  core.Status  `json:"status"`
	Outcome core.Outcome `json:"outcome"`
	Data    GateReport   `json:"data"`
	Detail  string       `json:"detail"`
}

// commandsTestRepo makes a repository directory whose gate.json declares steps. A nil steps
// leaves gate.json out.
func commandsTestRepo(t *testing.T, steps []GateStep) string {
	t.Helper()
	dir := t.TempDir()
	if steps == nil {
		return dir
	}
	data, err := json.Marshal(Definition{FormatVersion: formatVersion, Steps: steps})
	if err != nil {
		t.Fatalf("encode gate.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gate.json"), data, 0o600); err != nil {
		t.Fatalf("write gate.json: %v", err)
	}
	return dir
}

// commandsTestRun runs `aiwe gates gate` through the one command registry and the CLI front, as
// the binary does (SPEC §5.7), and returns the exit code and the decoded envelope.
func commandsTestRun(t *testing.T, args ...string) (int, commandsTestEnvelope, string) {
	t.Helper()
	var out bytes.Buffer
	argv := append([]string{"gates", "gate"}, args...)
	argv = append(argv, "--format", "json")
	code := core.RunCLI(context.Background(), Commands(), argv, &out)
	var env commandsTestEnvelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("aiwe %s printed no JSON envelope: %v\n%s", strings.Join(argv, " "), err, out.String())
	}
	return code, env, out.String()
}

// commandsTestRow returns the outcome table row named o; ok is false when the table has none.
func commandsTestRow(o core.Outcome) (core.OutcomeRow, bool) {
	for _, r := range core.Outcomes() {
		if r.Outcome == o {
			return r, true
		}
	}
	return core.OutcomeRow{}, false
}

// TestGatesCommand pins that `aiwe gates gate` takes its own repo and step params, runs the gate
// of <repo>/gate.json, and maps each gate result to one named outcome of the table: a red or
// incomplete gate never exits 0 (BC-1, DR-10).
func TestGatesCommand(t *testing.T) {
	green1, green2, red, missing := runnerTestSteps(t)

	t.Run("a command whose repo or step param is not in the dictionary is refused at every call", func(t *testing.T) {
		if err := core.CheckParams(core.Dictionary(), Commands()); err != nil {
			t.Fatalf("the gates commands do not pass the dictionary check: %v", err)
		}
	})

	cases := []struct {
		name string
		// catches names the defect the case fails on.
		catches string
		steps   []GateStep
		args    func(repo string) []string
		// wantCode is the exit code; -1 means any code but 0.
		wantCode    int
		wantStatus  core.Status
		wantResult  Result
		wantPartial bool
	}{
		{
			name:        "green gate exits 0 with every step reported",
			catches:     "a green gate refused for its --repo param, or reported without its steps",
			steps:       []GateStep{green1, green2},
			args:        func(repo string) []string { return []string{"--repo", repo} },
			wantCode:    0,
			wantStatus:  core.StatusOK,
			wantResult:  ResultGreen,
			wantPartial: false,
		},
		{
			name:        "red gate exits with the red code",
			catches:     "a gate with a failing step that exits 0 or is not reported red (DR-10)",
			steps:       []GateStep{green1, red, green2},
			args:        func(repo string) []string { return []string{"--repo", repo} },
			wantCode:    core.StatusRed.ExitCode(),
			wantStatus:  core.StatusRed,
			wantResult:  ResultRed,
			wantPartial: false,
		},
		{
			name:        "gate with a step that cannot start is incomplete and exits non-zero",
			catches:     "an unrun step that lets the gate exit 0 (DR-10, DR-13)",
			steps:       []GateStep{green1, missing},
			args:        func(repo string) []string { return []string{"--repo", repo} },
			wantCode:    -1,
			wantResult:  ResultIncomplete,
			wantPartial: false,
		},
		{
			name:        "gate run for one step is incomplete and exits non-zero",
			catches:     "a --step run refused for its own param, or a partial run that exits 0 (DR-10)",
			steps:       []GateStep{green1, green2},
			args:        func(repo string) []string { return []string{"--repo", repo, "--step", green2.ID} },
			wantCode:    -1,
			wantResult:  ResultIncomplete,
			wantPartial: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := commandsTestRepo(t, tc.steps)
			code, env, raw := commandsTestRun(t, tc.args(repo)...)
			if tc.wantCode == -1 && code == 0 || tc.wantCode != -1 && code != tc.wantCode {
				t.Fatalf("%s: exit code %d, want %d (-1: any but 0)\n%s", tc.catches, code, tc.wantCode, raw)
			}
			if tc.wantStatus != "" && env.Status != tc.wantStatus {
				t.Errorf("%s: status %q, want %q\n%s", tc.catches, env.Status, tc.wantStatus, raw)
			}
			if code != env.Status.ExitCode() {
				t.Errorf("%s: exit code %d does not match status %q\n%s", tc.catches, code, env.Status, raw)
			}
			if env.Data.Result != tc.wantResult || env.Data.Partial != tc.wantPartial {
				t.Errorf("%s: gate result %q partial %v, want %q partial %v\n%s",
					tc.catches, env.Data.Result, env.Data.Partial, tc.wantResult, tc.wantPartial, raw)
			}
			if len(env.Data.Steps) != len(tc.steps) {
				t.Errorf("%s: %d step reports, want one per declared step (%d)\n%s",
					tc.catches, len(env.Data.Steps), len(tc.steps), raw)
			}
			row, ok := commandsTestRow(env.Outcome)
			if !ok || row.Status != env.Status || row.Next == "" {
				t.Errorf("%s: outcome %q is not a table row with status %q and a next action (BC-1)\n%s",
					tc.catches, env.Outcome, env.Status, raw)
			}
		})
	}

	t.Run("red and incomplete gates are two different named outcomes", func(t *testing.T) {
		_, redEnv, redRaw := commandsTestRun(t, "--repo", commandsTestRepo(t, []GateStep{green1, red}))
		_, incEnv, incRaw := commandsTestRun(t, "--repo", commandsTestRepo(t, []GateStep{green1, missing}))
		if redEnv.Outcome == incEnv.Outcome {
			t.Fatalf("a red and an incomplete gate share the outcome %q, so a reader cannot tell them apart (BC-1)\n%s\n%s",
				redEnv.Outcome, redRaw, incRaw)
		}
		for _, o := range []core.Outcome{redEnv.Outcome, incEnv.Outcome} {
			if o == core.OutcomeOK || o == core.OutcomeError || o == core.OutcomeUsage {
				t.Errorf("gate outcome %q is a generic core outcome, not a named gate outcome (BC-1)", o)
			}
		}
	})

	refusals := []struct {
		name    string
		catches string
		steps   []GateStep
		args    func(repo string) []string
		want    core.Status
		// found is text the §5.10 detail must name.
		found string
	}{
		{
			name:    "unknown step name is a usage refusal naming the step",
			catches: "a --step typo that runs nothing and reports green, or is refused as an undeclared param",
			steps:   []GateStep{green1, green2},
			args:    func(repo string) []string { return []string{"--repo", repo, "--step", "tset"} },
			want:    core.StatusUsage,
			found:   "tset",
		},
		{
			name:    "repository without gate.json is an error, never a green gate",
			catches: "a repository with no gate.json that exits 0 as if its gate held (DR-13)",
			steps:   nil,
			args:    func(repo string) []string { return []string{"--repo", repo} },
			want:    core.StatusError,
			found:   "gate.json",
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			code, env, raw := commandsTestRun(t, tc.args(commandsTestRepo(t, tc.steps))...)
			if env.Status != tc.want || code != tc.want.ExitCode() {
				t.Fatalf("%s: status %q exit %d, want %q exit %d\n%s", tc.catches, env.Status, code, tc.want, tc.want.ExitCode(), raw)
			}
			if env.Outcome == "param_undeclared" {
				t.Fatalf("%s: refused for an undeclared param\n%s", tc.catches, raw)
			}
			prefix := string(env.Outcome) + ": expected "
			if !strings.HasPrefix(env.Detail, prefix) || !strings.Contains(env.Detail, "; found ") ||
				!strings.Contains(env.Detail, "; next: ") || !strings.Contains(env.Detail, tc.found) {
				t.Errorf("%s: detail %q is not \"<outcome>: expected <x>; found <y>; next: <action>\" naming %q (SPEC §5.10)",
					tc.catches, env.Detail, tc.found)
			}
		})
	}
}
