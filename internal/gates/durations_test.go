package gates

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestDurations pins the test step of SPEC §5.13 and DR-38: `go test -json` gives each package's
// duration; the step limit is at least 2 × the longest recorded package and is never divided by
// the worker count; `-p` comes from DR-7; a package whose current duration exceeds half the limit
// turns the commit gate red.
func TestDurations(t *testing.T) {
	t.Run("parse", testDurationsParse)
	t.Run("prepare", testDurationsPrepare)
	t.Run("check", testDurationsCheck)
}

func testDurationsParse(t *testing.T) {
	const out = `{"Time":"2026-10-11T09:14:02.1Z","Action":"start","Package":"github.com/sungurerdim/aiwe/internal/core"}
{"Time":"2026-10-11T09:14:02.2Z","Action":"run","Package":"github.com/sungurerdim/aiwe/internal/core","Test":"TestMeasured"}
{"Time":"2026-10-11T09:14:02.3Z","Action":"output","Package":"github.com/sungurerdim/aiwe/internal/core","Test":"TestMeasured","Output":"=== RUN   TestMeasured\n"}
{"Time":"2026-10-11T09:14:04.9Z","Action":"pass","Package":"github.com/sungurerdim/aiwe/internal/core","Test":"TestMeasured","Elapsed":2.61}
{"Time":"2026-10-11T09:14:14.5Z","Action":"pass","Package":"github.com/sungurerdim/aiwe/internal/core","Elapsed":12.42}
{"Time":"2026-10-11T09:15:47.0Z","Action":"fail","Package":"github.com/sungurerdim/aiwe/internal/gates","Elapsed":94.8}
`
	cases := []struct {
		name    string
		input   string
		want    []PackageDuration
		wantErr bool
	}{
		{
			name:  "package events give durations and test events are not packages",
			input: out,
			want: []PackageDuration{
				{Package: "github.com/sungurerdim/aiwe/internal/core", Elapsed: 12420 * time.Millisecond},
				{Package: "github.com/sungurerdim/aiwe/internal/gates", Elapsed: 94800 * time.Millisecond},
			},
		},
		{
			name:    "a line that is not a JSON event is refused, not skipped",
			input:   "FAIL\tgithub.com/sungurerdim/aiwe/internal/gates [build failed]\n",
			wantErr: true,
		},
		{
			name: "a line that is not a JSON event beside a valid package end is refused, not skipped",
			input: `{"Time":"2026-10-11T09:14:14.5Z","Action":"pass","Package":"github.com/sungurerdim/aiwe/internal/core","Elapsed":12.42}
FAIL	github.com/sungurerdim/aiwe/internal/gates [build failed]
`,
			wantErr: true,
		},
		{
			name:    "empty output measured no package and is refused, not read as an empty green list (DR-13)",
			input:   "",
			wantErr: true,
		},
		{
			name:    "blank lines alone measured no package and are refused (DR-13)",
			input:   "\n  \n\n",
			wantErr: true,
		},
		{
			name: "test events without a package end measured no package and are refused (DR-13)",
			input: `{"Time":"2026-10-11T09:14:02.2Z","Action":"run","Package":"github.com/sungurerdim/aiwe/internal/core","Test":"TestMeasured"}
{"Time":"2026-10-11T09:14:04.9Z","Action":"pass","Package":"github.com/sungurerdim/aiwe/internal/core","Test":"TestMeasured","Elapsed":2.61}
`,
			wantErr: true,
		},
		{
			name:    "a package end without Elapsed measured no duration and is refused, never read as 0 (DR-13)",
			input:   `{"Time":"2026-10-11T09:14:14.5Z","Action":"pass","Package":"github.com/sungurerdim/aiwe/internal/core"}` + "\n",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePackageDurations([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParsePackageDurations = %v, nil; want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePackageDurations: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ParsePackageDurations = %v, want %v", got, tc.want)
			}
		})
	}
}

func testDurationsPrepare(t *testing.T) {
	pkgs := func(longest time.Duration) []PackageDuration {
		return []PackageDuration{
			{Package: "github.com/sungurerdim/aiwe/internal/core", Elapsed: 12 * time.Second},
			{Package: "github.com/sungurerdim/aiwe/internal/gates", Elapsed: longest},
		}
	}
	cases := []struct {
		name     string
		timeout  int
		workers  int
		recorded []PackageDuration
		want     int // TimeoutSeconds of the prepared step
	}{
		{"one worker keeps the declared limit", 600, 1, pkgs(90 * time.Second), 600},
		{"eight workers never divide the limit", 600, 8, pkgs(90 * time.Second), 600},
		{"a long package raises the limit to twice its duration", 600, 1, pkgs(400 * time.Second), 800},
		{"sixteen workers never divide the raised limit", 600, 16, pkgs(400 * time.Second), 800},
		{"twice a fractional duration rounds up to whole seconds", 60, 4, pkgs(90400 * time.Millisecond), 181},
		{"no recorded package keeps the declared limit", 600, 8, nil, 600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := GateStep{ID: "test", Command: []string{"go", "test", "-json", "./..."}, Required: true, TimeoutSeconds: tc.timeout}
			before := slices.Clone(step.Command)
			got := PrepareTestStep(step, tc.workers, tc.recorded)
			if got.TimeoutSeconds != tc.want {
				t.Errorf("TimeoutSeconds = %d, want %d (workers %d)", got.TimeoutSeconds, tc.want, tc.workers)
			}
			var p []string
			for _, a := range got.Command {
				if strings.HasPrefix(a, "-p=") || a == "-p" {
					p = append(p, a)
				}
			}
			if want := []string{"-p=" + strconv.Itoa(tc.workers)}; !slices.Equal(p, want) {
				t.Errorf("command %q sets parallelism %q, want %q from DR-7", got.Command, p, want)
			}
			for _, a := range before {
				if !slices.Contains(got.Command, a) {
					t.Errorf("command %q lost the declared argument %q", got.Command, a)
				}
			}
			if !slices.Equal(step.Command, before) {
				t.Errorf("declared command changed to %q; want it untouched", step.Command)
			}
		})
	}
}

func testDurationsCheck(t *testing.T) {
	const core = "github.com/sungurerdim/aiwe/internal/core"
	const gatesPkg = "github.com/sungurerdim/aiwe/internal/gates"
	limit := 600 * time.Second
	cases := []struct {
		name     string
		current  []PackageDuration
		want     Result
		inDetail []string
	}{
		{"packages under half the limit leave the gate green", []PackageDuration{{core, 290 * time.Second}, {gatesPkg, 120 * time.Second}}, ResultGreen, nil},
		{"a package at exactly half the limit is not over it", []PackageDuration{{core, 300 * time.Second}}, ResultGreen, nil},
		{"a package one second over half the limit turns the gate red", []PackageDuration{{core, 12 * time.Second}, {gatesPkg, 301 * time.Second}}, ResultRed, []string{gatesPkg}},
		{"every package over half the limit is named", []PackageDuration{{core, 310 * time.Second}, {gatesPkg, 450 * time.Second}}, ResultRed, []string{core, gatesPkg}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := CheckPackageDurations(limit, tc.current)
			if got != tc.want {
				t.Fatalf("CheckPackageDurations = %q (%q), want %q", got, detail, tc.want)
			}
			for _, p := range tc.inDetail {
				if !strings.Contains(detail, p) {
					t.Errorf("detail %q does not name the package %q to split", detail, p)
				}
			}
		})
	}

	unmeasuredCases := []struct {
		name    string
		limit   time.Duration
		current []PackageDuration
	}{
		{"no package list is not_measured, not green (DR-13)", limit, nil},
		{"an empty package list is not_measured, not green (DR-13)", limit, []PackageDuration{}},
		{"no step limit cannot judge a package", 0, []PackageDuration{{core, 12 * time.Second}}},
	}
	for _, tc := range unmeasuredCases {
		t.Run(tc.name, func(t *testing.T) {
			if got, detail := CheckPackageDurations(tc.limit, tc.current); got != ResultNotMeasured {
				t.Fatalf("CheckPackageDurations(%v, %v) = %q (%q), want %q", tc.limit, tc.current, got, detail, ResultNotMeasured)
			}
		})
	}

	t.Run("the prepared limit, not a divided one, judges the current run", func(t *testing.T) {
		step := GateStep{ID: "test", Command: []string{"go", "test", "-json", "./..."}, Required: true, TimeoutSeconds: 600}
		got := PrepareTestStep(step, 8, []PackageDuration{{gatesPkg, 400 * time.Second}})
		limit := time.Duration(got.TimeoutSeconds) * time.Second
		if r, detail := CheckPackageDurations(limit, []PackageDuration{{gatesPkg, 390 * time.Second}}); r != ResultGreen {
			t.Errorf("390 s under an 800 s limit = %q (%q), want green", r, detail)
		}
		if r, _ := CheckPackageDurations(limit, []PackageDuration{{gatesPkg, 401 * time.Second}}); r != ResultRed {
			t.Errorf("401 s over half of an 800 s limit = %q, want red", r)
		}
	})
}

// durationsJSONEnv makes this test binary a helper test step: it prints the file the variable
// names, as `go test -json` would, and exits 0. It is portable and opens no window.
const durationsJSONEnv = "AIWE_GATES_TEST_JSON"

func init() {
	path := os.Getenv(durationsJSONEnv)
	if path == "" {
		return
	}
	out, err := os.ReadFile(path)
	if err != nil {
		os.Exit(3)
	}
	_, _ = os.Stdout.Write(out)
	os.Exit(0)
}

// durationsTestPackage is the `go test -json` output of one package that ran one test and ended
// green after elapsed seconds.
func durationsTestPackage(pkg string, elapsed float64) string {
	return fmt.Sprintf(`{"Time":"2026-10-11T09:14:02.1Z","Action":"start","Package":%q}
{"Time":"2026-10-11T09:14:02.2Z","Action":"run","Package":%q,"Test":"TestRunner"}
{"Time":"2026-10-11T09:14:04.9Z","Action":"pass","Package":%q,"Test":"TestRunner","Elapsed":2.61}
{"Time":"2026-10-11T09:15:47.0Z","Action":"pass","Package":%q,"Elapsed":%g}
`, pkg, pkg, pkg, pkg, elapsed)
}

// durationsTestStep returns a test step that prints output, as `go test -json` would, and exits 0.
// The repository gate.json carries `-json` in its test command (SPEC §12.2); the runner adds only
// `-p`, so this step stands for that declared command.
func durationsTestStep(t *testing.T, output string) GateStep {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go-test.json")
	if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
		t.Fatalf("write the go test -json fixture: %v", err)
	}
	t.Setenv(durationsJSONEnv, path)
	return GateStep{ID: "test", Command: []string{os.Args[0], "-test.run=^$"}, Required: true}
}

// TestDurationsRunner pins the runner wiring of SPEC §5.13 and §7.8: on every gate run the test
// step runs under the limit StepLimit gives (3 × p90 of its last 20 green durations, else 3 × the
// base proof, at least 60 s), raised to 2 × the longest recorded package and never divided by the
// worker count; its `go test -json` output gives the package durations; a package over half that
// limit turns the gate red; an empty or unparsable duration list is not_measured (DR-13, DR-38).
func TestDurationsRunner(t *testing.T) {
	const pkg = "github.com/sungurerdim/aiwe/internal/gates"
	green := func(d time.Duration) StepRun { return StepRun{Result: ResultGreen, Duration: d} }
	// twenty is 20 green runs whose p90 is 40 s: the limit is 3 × 40 s = 120 s.
	twenty := func() []StepRun {
		var h []StepRun
		for range 17 {
			h = append(h, green(30*time.Second))
		}
		return append(h, green(40*time.Second), green(300*time.Second), green(40*time.Second))
	}
	nineteen := func() []StepRun {
		h := make([]StepRun, 19)
		for i := range h {
			h[i] = green(100 * time.Second)
		}
		return h
	}
	pkgEnd := func(elapsed float64) string { return durationsTestPackage(pkg, elapsed) }
	// The base proof of the test step ran 30 s: 3 × 30 s = 90 s until 20 greens exist.
	proof := &Proof{BaseCommit: "8f5f1db", Steps: []ProofStep{{ID: "test", Result: ResultGreen, ExitCode: 0, DurationMs: 30000}}}
	cases := []struct {
		name     string
		output   string
		history  []StepRun
		recorded []PackageDuration
		want     Result // the test step's result
		named    bool   // the detail names the package to split
	}{
		{"a package over half the p90 limit turns the gate red", pkgEnd(61), twenty(), nil, ResultRed, true},
		{"a package under half the p90 limit, judged by a limit not divided by 4 workers, stays green", pkgEnd(59), twenty(), nil, ResultGreen, false},
		{"before 20 greens the base proof sets the limit, so 46 s is over half of 90 s", pkgEnd(46), nineteen(), nil, ResultRed, true},
		{"before 20 greens the base proof sets the limit, so 44 s is under half of 90 s", pkgEnd(44), nineteen(), nil, ResultGreen, false},
		{"a long recorded package raises the limit to twice its duration", pkgEnd(390), twenty(), []PackageDuration{{pkg, 400 * time.Second}}, ResultGreen, false},
		{"empty test output is not_measured, not green", "", twenty(), nil, ResultNotMeasured, false},
		{"output that is not go test -json is not_measured, not green", "ok  \t" + pkg + "\t12.420s\n", twenty(), nil, ResultNotMeasured, false},
		{"test events without a package end are not_measured, not green", `{"Action":"pass","Package":"` + pkg + `","Test":"TestRunner","Elapsed":2.61}` + "\n", twenty(), nil, ResultNotMeasured, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := durationsTestStep(t, tc.output)
			def := Definition{FormatVersion: 1, Steps: []GateStep{step}, Proof: proof}
			r := &Runner{
				Capacity: core.CapacityFor(core.Measured[int64]{Value: 4, Known: true}, 1),
				History:  map[string][]StepRun{"test": tc.history},
				Recorded: tc.recorded,
				Owner:    rootsTestOwner(t),
			}
			report, err := r.Run(context.Background(), def, t.TempDir(), nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(report.Steps) != 1 {
				t.Fatalf("reported steps = %+v, want the one declared step", report.Steps)
			}
			s := report.Steps[0]
			if s.Result != tc.want {
				t.Fatalf("test step result = %q (%q), want %q", s.Result, s.Detail, tc.want)
			}
			if tc.want != ResultGreen && report.Result == ResultGreen {
				t.Errorf("gate result = green with a %q test step", s.Result)
			}
			if tc.named && !strings.Contains(s.Detail, pkg) {
				t.Errorf("detail %q does not name the package %q to split", s.Detail, pkg)
			}
		})
	}
}
