//go:build linux

package core

import (
	"math"
	"testing"
)

// TestLinuxBusyPercent pins the CPU-busy share of one Linux interval: the share of the machine
// ticks that were not idle, and no measurement at all when the counters did not move forward or
// one of them went backwards — never a busy share of 0 for an interval nothing measured (DR-6).
func TestLinuxBusyPercent(t *testing.T) {
	cases := []struct {
		name string
		prev linuxCPUTimes
		now  linuxCPUTimes
		want float64
		ok   bool
	}{
		{"a half idle interval is half busy", linuxCPUTimes{total: 1000, idle: 400}, linuxCPUTimes{total: 1200, idle: 500}, 50, true},
		{"a fully idle interval is 0 percent busy", linuxCPUTimes{total: 1000, idle: 400}, linuxCPUTimes{total: 1100, idle: 500}, 0, true},
		{"a fully busy interval is 100 percent busy", linuxCPUTimes{total: 1000, idle: 400}, linuxCPUTimes{total: 1200, idle: 400}, 100, true},
		{"an interval with no new tick measures nothing", linuxCPUTimes{total: 1000, idle: 500}, linuxCPUTimes{total: 1000, idle: 500}, 0, false},
		{"an idle counter that went backwards measures nothing", linuxCPUTimes{total: 1000, idle: 500}, linuxCPUTimes{total: 1100, idle: 490}, 0, false},
		{"a total counter that went backwards measures nothing", linuxCPUTimes{total: 1100, idle: 500}, linuxCPUTimes{total: 1000, idle: 400}, 0, false},
		{"idle that grew by more than the total measures nothing", linuxCPUTimes{total: 1000, idle: 500}, linuxCPUTimes{total: 1100, idle: 700}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := linuxBusyPercent(tc.prev, tc.now)
			if ok != tc.ok {
				t.Fatalf("linuxBusyPercent(%+v, %+v) measured = %v, want %v", tc.prev, tc.now, ok, tc.ok)
			}
			if !ok {
				if got != 0 {
					t.Fatalf("linuxBusyPercent(%+v, %+v) = %v without a measurement, want no reading",
						tc.prev, tc.now, got)
				}
				return
			}
			if math.Abs(got-tc.want) > 0.001 {
				t.Fatalf("linuxBusyPercent(%+v, %+v) = %v, want %v", tc.prev, tc.now, got, tc.want)
			}
		})
	}
}
