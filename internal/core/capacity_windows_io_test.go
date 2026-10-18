//go:build windows

package core

import (
	"math"
	"testing"
)

// TestWinBusyPercent pins the CPU-busy share of one Windows interval: the share of the machine ticks
// that were not idle, and no measurement at all for counters that did not move forward, that went
// backwards or whose idle time exceeds the kernel time that includes it — never a busy share of 0
// (DR-6).
func TestWinBusyPercent(t *testing.T) {
	cases := []struct {
		name string
		prev winCPUTimes
		now  winCPUTimes
		want float64
		ok   bool
	}{
		{"a half idle interval is half busy", winCPUTimes{idle: 10, kernel: 10, user: 10}, winCPUTimes{idle: 60, kernel: 110, user: 10}, 50, true},
		{"a fully idle interval is 0 percent busy", winCPUTimes{idle: 10, kernel: 10, user: 10}, winCPUTimes{idle: 30, kernel: 30, user: 10}, 0, true},
		{"a fully busy interval is 100 percent busy", winCPUTimes{idle: 10, kernel: 10, user: 10}, winCPUTimes{idle: 10, kernel: 30, user: 30}, 100, true},
		{"an interval with no new tick measures nothing", winCPUTimes{idle: 100, kernel: 100, user: 100}, winCPUTimes{idle: 100, kernel: 100, user: 100}, 0, false},
		{"counters that went backwards measure nothing", winCPUTimes{idle: 100, kernel: 100, user: 100}, winCPUTimes{idle: 60, kernel: 60, user: 60}, 0, false},
		{"an idle time above the kernel time measures nothing", winCPUTimes{idle: 100, kernel: 100, user: 100}, winCPUTimes{idle: 130, kernel: 120, user: 100}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := winBusyPercent(tc.prev, tc.now)
			if ok != tc.ok {
				t.Fatalf("winBusyPercent(%+v, %+v) measured = %v, want %v", tc.prev, tc.now, ok, tc.ok)
			}
			if !ok {
				if got != 0 {
					t.Fatalf("winBusyPercent(%+v, %+v) = %v without a measurement, want no reading",
						tc.prev, tc.now, got)
				}
				return
			}
			if math.Abs(got-tc.want) > 0.001 {
				t.Fatalf("winBusyPercent(%+v, %+v) = %v, want %v", tc.prev, tc.now, got, tc.want)
			}
		})
	}
}
