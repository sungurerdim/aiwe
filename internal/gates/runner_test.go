package gates

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"
)

// runnerSleepEnv makes this test binary a helper step that runs for runnerSleepFor and then exits
// with code 1, the code a Windows job gives a process it stops. It is portable and opens no window.
const (
	runnerSleepEnv = "AIWE_GATES_TEST_SLEEP"
	runnerSleepFor = 30 * time.Second
)

func init() {
	if os.Getenv(runnerSleepEnv) != "1" {
		return
	}
	time.Sleep(runnerSleepFor)
	os.Exit(1)
}

// runnerTestGo returns the absolute path of the go tool that runs this test. Its subcommands give
// a real child process with a known exit code on every OS: `go version` exits 0 and `go help` on
// an unknown topic exits 2.
func runnerTestGo(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go tool is not on PATH: %v", err)
	}
	return bin
}

// runnerTestSteps builds the steps the runner cases share: two green steps, one red step and one
// step whose tool does not exist.
func runnerTestSteps(t *testing.T) (green1, green2, red, missing GateStep) {
	t.Helper()
	goBin := runnerTestGo(t)
	green1 = GateStep{ID: "format", Command: []string{goBin, "version"}, Required: true}
	green2 = GateStep{ID: "test", Command: []string{goBin, "version"}, Required: true}
	red = GateStep{ID: "vuln", Command: []string{goBin, "help", "aiwe-no-such-help-topic"}, Required: true}
	missing = GateStep{ID: "lint", Command: []string{"aiwe-gate-tool-that-is-not-installed"}, Required: true}
	return green1, green2, red, missing
}

// runnerTestIDs returns the step ids of a report in report order.
func runnerTestIDs(steps []StepReport) []string {
	ids := make([]string, 0, len(steps))
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// TestRunner pins DR-10: a gate is a list of steps, every step is reported, and a skipped or unrun
// step makes the whole gate `incomplete`, never green.
func TestRunner(t *testing.T) {
	green1, green2, red, missing := runnerTestSteps(t)
	expectTwo := red
	expectTwo.ID = "tidy"
	expectTwo.Expect = Expect{ExitCode: 2}
	rejects := green1
	rejects.ID = "deadcode"
	rejects.Expect = Expect{ExitCode: 0, OutputRejects: `\S`}
	matches := green2
	matches.ID = "repo"
	matches.Expect = Expect{ExitCode: 0, OutputMatches: `^ok$`}

	cases := []struct {
		name string
		// catches names the defect the case fails on.
		catches string
		steps   []GateStep
		only    []string
		gate    string
		partial bool
		// stepResult is the expected result per step id; a missing id is not checked.
		stepResult map[string]string
		// notGreen lists step ids whose report must exist and must not read green.
		notGreen []string
	}{
		{name: "every step green", catches: "a gate of green steps not reported green, or a step dropped from the report",
			steps: []GateStep{green1, green2}, gate: "green",
			stepResult: map[string]string{"format": "green", "test": "green"}},
		{name: "partial run", catches: "a --step run that leaves a declared step unrun reported green instead of incomplete (DR-10, SPEC §7.8)",
			steps: []GateStep{green1, green2}, only: []string{"format"}, gate: "incomplete", partial: true,
			stepResult: map[string]string{"format": "green"}, notGreen: []string{"test"}},
		{name: "missing tool", catches: "a step that could not start reported green, or dropped so the gate reads green (DR-10, DR-13)",
			steps: []GateStep{green1, missing, green2}, gate: "incomplete",
			stepResult: map[string]string{"format": "green", "lint": "not_measured", "test": "green"}},
		{name: "red step", catches: "a step that exits non-zero reported green, or the gate not red",
			steps: []GateStep{green1, red}, gate: "red",
			stepResult: map[string]string{"format": "green", "vuln": "red"}},
		{name: "declared exit code", catches: "a step judged by exit code 0 instead of its declared expect.exitCode (SPEC §8.3)",
			steps: []GateStep{green1, expectTwo}, gate: "green",
			stepResult: map[string]string{"format": "green", "tidy": "green"}},
		{name: "output expectation without capture", catches: "a step that declares outputRejects or outputMatches read green on its exit code alone, though the runner never saw its output (DR-13)",
			steps: []GateStep{rejects, matches}, gate: "incomplete",
			stepResult: map[string]string{"deadcode": "not_measured", "repo": "not_measured"}},
		{name: "unrun step before red step", catches: "an unmeasured step hiding a red step, so the gate reads incomplete instead of red: red beats incomplete (#116)",
			steps: []GateStep{missing, red}, gate: "red",
			stepResult: map[string]string{"lint": "not_measured", "vuln": "red"}},
		{name: "partial run with red step", catches: "a --step run whose run step is red reported incomplete instead of red: red beats incomplete (#116)",
			steps: []GateStep{green1, red}, only: []string{"vuln"}, gate: "red", partial: true,
			stepResult: map[string]string{"vuln": "red"}, notGreen: []string{"format"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := Definition{FormatVersion: 1, Steps: tc.steps}
			report, err := (&Runner{}).Run(context.Background(), def, t.TempDir(), tc.only)
			if err != nil {
				t.Fatalf("catches %s: Run returned an error, but a decided gate is a result (SPEC §5.3): %v", tc.catches, err)
			}
			if string(report.Result) != tc.gate {
				t.Errorf("catches %s: gate result = %q, want %q", tc.catches, report.Result, tc.gate)
			}
			if report.Partial != tc.partial {
				t.Errorf("catches %s: partial = %v, want %v", tc.catches, report.Partial, tc.partial)
			}
			want := make([]string, 0, len(tc.steps))
			for _, s := range tc.steps {
				want = append(want, s.ID)
			}
			if got := runnerTestIDs(report.Steps); !slices.Equal(got, want) {
				t.Fatalf("catches %s: reported steps = %v, want every declared step in order %v", tc.catches, got, want)
			}
			for _, s := range report.Steps {
				if w, ok := tc.stepResult[s.ID]; ok && string(s.Result) != w {
					t.Errorf("catches %s: step %q result = %q, want %q", tc.catches, s.ID, s.Result, w)
				}
				if slices.Contains(tc.notGreen, s.ID) && (s.Result == "" || string(s.Result) == "green") {
					t.Errorf("catches %s: unrun step %q reported %q, want a named result that is not green", tc.catches, s.ID, s.Result)
				}
			}
		})
	}
}

// TestRunnerExitCode catches a red step reported without the exit code that made it red (BC-4).
func TestRunnerExitCode(t *testing.T) {
	green1, _, red, _ := runnerTestSteps(t)
	def := Definition{FormatVersion: 1, Steps: []GateStep{green1, red}}
	report, err := (&Runner{}).Run(context.Background(), def, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, s := range report.Steps {
		want := map[string]int{"format": 0, "vuln": 2}[s.ID]
		if s.ExitCode != want {
			t.Errorf("step %q exit code = %d, want %d", s.ID, s.ExitCode, want)
		}
	}
}

// TestRunnerNothingRun catches a gate with no step run read as green: an empty gate and a gate
// whose context ended before any step started measured nothing (DR-10, DR-13).
func TestRunnerNothingRun(t *testing.T) {
	green1, green2, _, _ := runnerTestSteps(t)

	empty, err := (&Runner{}).Run(context.Background(), Definition{FormatVersion: 1}, t.TempDir(), nil)
	if err == nil && string(empty.Result) == "green" {
		t.Errorf("a gate with zero steps reported green; want not green or an error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	def := Definition{FormatVersion: 1, Steps: []GateStep{green1, green2}}
	stopped, err := (&Runner{}).Run(ctx, def, t.TempDir(), nil)
	if err != nil {
		return // could not decide: an error is never a green gate
	}
	if string(stopped.Result) != "incomplete" {
		t.Errorf("a cancelled gate result = %q, want incomplete", stopped.Result)
	}
	for _, s := range stopped.Steps {
		if string(s.Result) == "green" {
			t.Errorf("step %q reported green although its context ended before it started", s.ID)
		}
	}
}

// TestRunnerTimeout catches a step stopped at its timeoutSeconds read green because the stopped
// process happened to exit with the declared code: a step that ran out of time never exited on its
// own, so it is not_measured and the gate is not green (DR-13, DR-15).
func TestRunnerTimeout(t *testing.T) {
	t.Setenv(runnerSleepEnv, "1")
	slow := GateStep{ID: "test", Command: []string{os.Args[0], "-test.run=^$"}, Required: true,
		TimeoutSeconds: 1, Expect: Expect{ExitCode: 1}}
	def := Definition{FormatVersion: 1, Steps: []GateStep{slow}}
	start := time.Now()
	report, err := (&Runner{}).Run(context.Background(), def, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if took := time.Since(start); took >= runnerSleepFor {
		t.Errorf("the step ran %v; timeoutSeconds 1 did not stop it before its %v sleep ended", took, runnerSleepFor)
	}
	if len(report.Steps) != 1 {
		t.Fatalf("reported steps = %+v, want the one declared step", report.Steps)
	}
	if s := report.Steps[0]; s.Result != ResultNotMeasured || s.ExitCode != exitUnknown {
		t.Errorf("a timed-out step reported result %q exit code %d, want %q and %d", s.Result, s.ExitCode, ResultNotMeasured, exitUnknown)
	}
	if report.Result == ResultGreen {
		t.Errorf("a gate whose only step timed out reported green")
	}
}

// runnerTestStored is the stored gate sample of SPEC §8.3; a project gate.json uses the same format.
const runnerTestStored = `{ "formatVersion": 1,
  "steps": [ { "id": "test", "command": ["go", "test", "./..."], "required": true,
               "inputs": ["**"], "timeoutSeconds": 124, "expect": { "exitCode": 0 } } ],
  "origin": "derived", "inputHash": "4f2a",
  "proof": { "baseCommit": "c13d3b9", "steps": [ { "id": "test", "result": "green", "exitCode": 0, "durationMs": 41230 } ] } }`

// TestParseDefinition catches a gate.json that is malformed or ambiguous accepted as a gate, and
// the SPEC §8.3 sample refused.
func TestParseDefinition(t *testing.T) {
	def, err := ParseDefinition([]byte(runnerTestStored))
	if err != nil {
		t.Fatalf("the SPEC §8.3 sample was refused: %v", err)
	}
	if len(def.Steps) != 1 || def.Steps[0].ID != "test" || !def.Steps[0].Required ||
		!slices.Equal(def.Steps[0].Command, []string{"go", "test", "./..."}) || def.Steps[0].Expect.ExitCode != 0 {
		t.Errorf("the SPEC §8.3 sample parsed as %+v", def)
	}

	refused := []struct{ name, catches, body string }{
		{"shell string", "a command given as a shell string run through a shell (SPEC §8.3: argv only)",
			`{"formatVersion":1,"steps":[{"id":"test","command":"go test ./...","required":true}]}`},
		{"empty command", "a step with nothing to run counted as a step",
			`{"formatVersion":1,"steps":[{"id":"test","command":[],"required":true}]}`},
		{"missing id", "a step that no report line can name",
			`{"formatVersion":1,"steps":[{"command":["go","vet","./..."],"required":true}]}`},
		{"duplicate id", "two steps under one id, so one result hides the other",
			`{"formatVersion":1,"steps":[{"id":"test","command":["go","test","./..."]},{"id":"test","command":["go","vet","./..."]}]}`},
		{"unknown version", "a format this engine does not know read as version 1",
			`{"formatVersion":2,"steps":[{"id":"test","command":["go","test","./..."]}]}`},
		{"no version", "a file without formatVersion read as version 1",
			`{"steps":[{"id":"test","command":["go","test","./..."]}]}`},
		{"misspelt field", "a misspelt field such as requried silently dropped (fail-closed, SPEC §4)",
			`{"formatVersion":1,"steps":[{"id":"test","command":["go","test","./..."],"requried":true}]}`},
		{"zero steps", "an empty gate accepted, so nothing is measured (DR-13)",
			`{"formatVersion":1,"steps":[]}`},
		{"not json", "a truncated file read as an empty gate", `{"formatVersion":1,"steps":[`},
		{"empty file", "an empty file read as an empty gate", ``},
		{"invalid utf8", "invalid UTF-8 bytes accepted (SPEC §5.14)",
			"{\"formatVersion\":1,\"steps\":[{\"id\":\"t\xffst\",\"command\":[\"go\",\"test\"]}]}"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ParseDefinition([]byte(tc.body)); err == nil {
				t.Errorf("catches %s: accepted as %+v", tc.catches, got)
			}
		})
	}
}

// FuzzParseDefinition catches a parser of untrusted gate.json that panics, or accepts a gate that
// a runner cannot report step by step (SPEC §5.13).
func FuzzParseDefinition(f *testing.F) {
	f.Add([]byte(runnerTestStored))
	f.Add([]byte(`{"formatVersion":1,"steps":[{"id":"format","command":["gofmt","-l","."]}]}`))
	f.Add([]byte(`{"formatVersion":1,"steps":[{"id":"ünïcode","command":["go","version"]}]}`))
	f.Add([]byte(`{"formatVersion":1,"steps":null}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		def, err := ParseDefinition(data)
		if err != nil {
			return
		}
		if len(def.Steps) == 0 {
			t.Fatalf("accepted a gate with zero steps from %q", data)
		}
		seen := map[string]bool{}
		for _, s := range def.Steps {
			if s.ID == "" || len(s.Command) == 0 || seen[s.ID] {
				t.Fatalf("accepted step %+v (empty id, empty command or duplicate id) from %q", s, data)
			}
			seen[s.ID] = true
		}
	})
}

// TestRepositoryGate catches this repository's gate.json missing, unparsable, or lacking a step
// that SPEC §12.2 declares, so `aiwe gates gate --repo .` would not run the declared gate.
func TestRepositoryGate(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "gate.json"))
	if err != nil {
		t.Fatalf("read the repository gate.json: %v", err)
	}
	def, err := ParseDefinition(data)
	if err != nil {
		t.Fatalf("the repository gate.json was refused: %v", err)
	}
	got := make([]string, 0, len(def.Steps))
	for _, s := range def.Steps {
		got = append(got, s.ID)
	}
	for _, want := range []string{"format", "lint", "vuln", "tidy", "deadcode", "repo", "test"} {
		if !slices.Contains(got, want) {
			t.Errorf("gate.json steps %v lack the SPEC §12.2 step %q", got, want)
		}
	}

	// gofmt -l and gofmt -d exit 0 even when a file is not formatted (measured on go1.27.2), so a
	// format step judged by exit code alone is always green. It must reject any listed file.
	unformatted := "internal/gates/runner.go\n"
	for _, s := range def.Steps {
		if s.ID != "format" {
			continue
		}
		if s.Expect.OutputRejects == "" {
			t.Fatalf("catches a format step that is green on gofmt's exit code alone: gate.json format step %+v has no outputRejects", s)
		}
		re, err := regexp.Compile(s.Expect.OutputRejects)
		if err != nil {
			t.Fatalf("format outputRejects %q: %v", s.Expect.OutputRejects, err)
		}
		if !re.MatchString(unformatted) {
			t.Errorf("catches a format step that passes a listed unformatted file: outputRejects %q does not match %q", s.Expect.OutputRejects, unformatted)
		}
	}
}
