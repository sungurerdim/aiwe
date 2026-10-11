package gates

import (
	"testing"
	"time"
)

// TestLimitsStep pins the step time limit of SPEC §7.8: max(60 s, 3 × p90 of the step's last 20
// green durations), from the base proof alone until 20 green durations exist. History is ordered
// oldest first.
func TestLimitsStep(t *testing.T) {
	green := func(d time.Duration) StepRun { return StepRun{Result: ResultGreen, Duration: d} }
	red := func(d time.Duration) StepRun { return StepRun{Result: ResultRed, Duration: d} }
	// twenty is 20 green runs whose p90 is 40 s under every common p90 method: 17 × 30 s,
	// 2 × 40 s and one 300 s outlier. 3 × p90 = 120 s.
	twenty := func() []StepRun {
		var h []StepRun
		for range 17 {
			h = append(h, green(30*time.Second))
		}
		return append(h, green(40*time.Second), green(300*time.Second), green(40*time.Second))
	}
	repeat := func(n int, r StepRun) []StepRun {
		h := make([]StepRun, n)
		for i := range h {
			h[i] = r
		}
		return h
	}
	base := func(d time.Duration) ProofStep {
		return ProofStep{ID: "test", Result: ResultGreen, ExitCode: 0, DurationMs: d.Milliseconds()}
	}
	cases := []struct {
		name    string
		base    ProofStep
		history []StepRun
		want    time.Duration
	}{
		{"20 greens set the limit, not a slow base run", base(200 * time.Second), twenty(), 120 * time.Second},
		{"20 greens set the limit, not a fast base run", base(10 * time.Second), twenty(), 120 * time.Second},
		{"only the last 20 greens count, older slow ones do not", base(10 * time.Second),
			append(repeat(5, green(500*time.Second)), twenty()...), 120 * time.Second},
		{"red runs are not green durations", base(10 * time.Second),
			append(append(twenty()[:10:10], red(1000*time.Second), red(1000*time.Second)), append(twenty()[10:], red(1000*time.Second))...),
			120 * time.Second},
		{"19 greens are not enough and the base proof holds", base(30 * time.Second), repeat(19, green(100*time.Second)), 90 * time.Second},
		{"19 greens with reds are still not enough", base(30 * time.Second),
			append(repeat(19, green(100*time.Second)), red(100*time.Second)), 90 * time.Second},
		{"no history uses three times the base proof", base(50 * time.Second), nil, 150 * time.Second},
		{"a short base proof is raised to 60 s", base(10 * time.Second), nil, 60 * time.Second},
		{"20 short greens are raised to 60 s", base(10 * time.Second), repeat(20, green(5*time.Second)), 60 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StepLimit(tc.base, tc.history); got != tc.want {
				t.Fatalf("StepLimit = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("an unmeasured base proof never gives a limit under 60 s", func(t *testing.T) {
		b := ProofStep{ID: "test", Result: ResultNotMeasured, ExitCode: exitUnknown}
		if got := StepLimit(b, nil); got < 60*time.Second {
			t.Fatalf("StepLimit of an unmeasured base = %v, want at least 60 s (unknown is never 0)", got)
		}
	})
}
