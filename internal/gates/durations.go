package gates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// PackageDuration is how long one package's tests ran, as `go test -json` reports it.
type PackageDuration struct {
	Package string
	Elapsed time.Duration
}

// testEvent is the part of a `go test -json` event that carries a package duration.
type testEvent struct {
	Action  string   `json:"Action"`
	Package string   `json:"Package"`
	Test    string   `json:"Test"`
	Elapsed *float64 `json:"Elapsed"`
}

// ParsePackageDurations reads `go test -json` output and returns each package's duration in the
// order the packages ended. Test events are not packages. It refuses, never skips, a line that is
// not a JSON event and a package end without Elapsed: an unmeasured duration is never 0. Output
// that ends no package measured nothing and is refused, not read as an empty list (DR-6).
func ParsePackageDurations(out []byte) ([]PackageDuration, error) {
	var durations []PackageDuration
	for i, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("parse go test -json line %d: expected a JSON event, found %q: %w", i+1, line, err)
		}
		if ev.Test != "" || ev.Package == "" || !isPackageEnd(ev.Action) {
			continue
		}
		if ev.Elapsed == nil {
			return nil, fmt.Errorf("parse go test -json line %d: package %s ended with no Elapsed", i+1, ev.Package)
		}
		elapsed := time.Duration(math.Round(*ev.Elapsed*1000)) * time.Millisecond
		durations = append(durations, PackageDuration{Package: ev.Package, Elapsed: elapsed})
	}
	if len(durations) == 0 {
		return nil, errors.New("parse go test -json: no package ended, so no duration was measured")
	}
	return durations, nil
}

// isPackageEnd reports whether action ends a package run.
func isPackageEnd(action string) bool {
	return action == "pass" || action == "fail" || action == "skip"
}

// PrepareTestStep returns step with one `-p=workers` flag (DR-3) and a limit of at least
// 2 × the longest recorded package, rounded up to whole seconds (DR-1, SPEC §5.13). The limit is
// never divided by the worker count. A limit of 0 stays 0: no limit beyond the gate's context.
// workers is the chosen count of DR-3, at least 1; the caller never assumes one. The declared step
// is not changed.
func PrepareTestStep(step GateStep, workers int, recorded []PackageDuration) GateStep {
	step.Command = withParallelism(step.Command, workers)
	if step.TimeoutSeconds > 0 {
		var longest time.Duration
		for _, p := range recorded {
			longest = max(longest, p.Elapsed)
		}
		step.TimeoutSeconds = max(step.TimeoutSeconds, ceilSeconds(2*longest))
	}
	return step
}

// ceilSeconds rounds d up to whole seconds.
func ceilSeconds(d time.Duration) int {
	return int(math.Ceil(d.Seconds()))
}

// CheckPackageDurations turns the commit gate red when a package's current duration exceeds half
// the step limit, naming each such package so it is split before it can time out (SPEC §5.13). A
// limit of 0 or less, or no package, cannot be judged: not_measured, never green (DR-6).
func CheckPackageDurations(limit time.Duration, current []PackageDuration) (Result, string) {
	if limit <= 0 {
		return ResultNotMeasured, "no step limit to judge package durations against"
	}
	if len(current) == 0 {
		return ResultNotMeasured, "no package duration was measured"
	}
	var over []string
	for _, p := range current {
		if 2*p.Elapsed > limit {
			over = append(over, fmt.Sprintf("%s ran %s, over half the %s limit", p.Package, p.Elapsed, limit))
		}
	}
	if len(over) == 0 {
		return ResultGreen, ""
	}
	return ResultRed, "split these packages: " + strings.Join(over, "; ")
}
