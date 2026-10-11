package gates

import (
	"slices"
	"time"
)

const (
	// stepLimitFloor is the shortest step time limit (SPEC §7.8).
	stepLimitFloor = 60 * time.Second
	// stepLimitSample is how many recent green durations set a step limit (SPEC §7.8).
	stepLimitSample = 20
	// stepLimitFactor multiplies the basis duration of a step limit (SPEC §7.8).
	stepLimitFactor = 3
)

// StepRun is one recorded run of a step.
type StepRun struct {
	Result   Result
	Duration time.Duration
}

// StepLimit returns a step's time limit: max(60 s, 3 × p90 of its last 20 green durations), from
// the base proof alone until 20 green durations exist (SPEC §7.8). history is ordered oldest
// first. A base proof that is not green has no measured duration and gives the 60 s floor, never a
// limit taken from 0.
func StepLimit(base ProofStep, history []StepRun) time.Duration {
	var greens []time.Duration
	for _, r := range history {
		if r.Result == ResultGreen {
			greens = append(greens, r.Duration)
		}
	}
	if len(greens) >= stepLimitSample {
		return max(stepLimitFloor, stepLimitFactor*p90(greens[len(greens)-stepLimitSample:]))
	}
	if base.Result != ResultGreen {
		return stepLimitFloor
	}
	return max(stepLimitFloor, stepLimitFactor*time.Duration(base.DurationMs)*time.Millisecond)
}

// p90 is the nearest-rank 90th percentile of a non-empty sample.
func p90(sample []time.Duration) time.Duration {
	sorted := slices.Sorted(slices.Values(sample))
	rank := (len(sorted)*9 + 9) / 10 // ⌈0.9 × n⌉
	return sorted[rank-1]
}
