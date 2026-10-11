package gates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
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
// order the packages ended. Test events are not packages. A package event without Elapsed has no
// measured duration and is left out, never read as 0. A line that is not a JSON event is refused,
// not skipped (fail-closed, SPEC §4).
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
		if ev.Test != "" || ev.Package == "" || ev.Elapsed == nil || !isPackageEnd(ev.Action) {
			continue
		}
		elapsed := time.Duration(math.Round(*ev.Elapsed*1000)) * time.Millisecond
		durations = append(durations, PackageDuration{Package: ev.Package, Elapsed: elapsed})
	}
	return durations, nil
}

// isPackageEnd reports whether action ends a package run.
func isPackageEnd(action string) bool {
	return action == "pass" || action == "fail" || action == "skip"
}

// PrepareTestStep returns step with one `-p=workers` flag (DR-7) and a limit of at least
// 2 × the longest recorded package, rounded up to whole seconds (DR-38, SPEC §5.13). The limit is
// never divided by the worker count. A declared limit of 0 stays 0: no limit beyond the gate's
// context. The declared step is not changed.
func PrepareTestStep(step GateStep, workers int, recorded []PackageDuration) GateStep {
	workers = max(workers, 1)
	step.Command = withParallelism(step.Command, workers)
	if step.TimeoutSeconds > 0 {
		var longest time.Duration
		for _, p := range recorded {
			longest = max(longest, p.Elapsed)
		}
		twice := int(math.Ceil((2 * longest).Seconds()))
		step.TimeoutSeconds = max(step.TimeoutSeconds, twice)
	}
	return step
}

// withParallelism returns a copy of argv with every `-p` flag removed and one `-p=workers` put
// right after the `test` subcommand, or after the program when there is none.
func withParallelism(argv []string, workers int) []string {
	out := make([]string, 0, len(argv)+1)
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "-p" {
			i++ // the flag's value is the next argument
			continue
		}
		if strings.HasPrefix(a, "-p=") {
			continue
		}
		out = append(out, a)
	}
	at := min(1, len(out))
	if i := slices.Index(out, "test"); i >= 0 {
		at = i + 1
	}
	return slices.Insert(out, at, "-p="+strconv.Itoa(workers))
}

// CheckPackageDurations turns the commit gate red when a package's current duration exceeds half
// the step limit, naming each such package so it is split before it can time out (SPEC §5.13). A
// limit of 0 or less cannot judge a duration: not_measured.
func CheckPackageDurations(limit time.Duration, current []PackageDuration) (Result, string) {
	if limit <= 0 {
		return ResultNotMeasured, "no step limit to judge package durations against"
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
