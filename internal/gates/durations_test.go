package gates

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
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
