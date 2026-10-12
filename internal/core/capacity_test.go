package core_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestCapacity pins DR-3: workers = measured cores ÷ concurrent jobs, minimum 1, and the chosen
// value is printed with the limit that bound it. An unmeasured core count is unknown, never 0.
func TestCapacity(t *testing.T) {
	known := func(n int64) core.Measured[int64] { return core.Measured[int64]{Value: n, Known: true} }
	cases := []struct {
		name         string
		cores        core.Measured[int64]
		jobs         int
		workers      int
		printedCores string // the printed core count
		bound        string // the printed limit that bound the value
	}{
		{"one job takes every measured core", known(8), 1, 8, "cores=8", "bound=cores/jobs"},
		{"cores split evenly across jobs", known(8), 4, 2, "cores=8", "bound=cores/jobs"},
		{"an uneven split rounds down so jobs never exceed the cores", known(8), 3, 2, "cores=8", "bound=cores/jobs"},
		{"a large machine splits by jobs", known(64), 3, 21, "cores=64", "bound=cores/jobs"},
		{"one core and one job gives one worker", known(1), 1, 1, "cores=1", "bound=cores/jobs"},
		{"one core shared by four jobs stays at the minimum of 1", known(1), 4, 1, "cores=1", "bound=minimum"},
		{"fewer cores than jobs stays at the minimum of 1", known(4), 6, 1, "cores=4", "bound=minimum"},
		{"unmeasured cores fall to the minimum and print unknown", core.Measured[int64]{}, 2, 1, "cores=unknown", "bound=minimum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := core.CapacityFor(tc.cores, tc.jobs)
			if c.Workers != tc.workers {
				t.Fatalf("CapacityFor(%+v, %d).Workers = %d, want %d", tc.cores, tc.jobs, c.Workers, tc.workers)
			}
			if c.Workers < 1 {
				t.Fatalf("workers %d fell below the minimum of 1", c.Workers)
			}
			printed := c.String()
			for _, want := range []string{fmt.Sprintf("workers=%d", tc.workers), tc.printedCores, fmt.Sprintf("jobs=%d", tc.jobs), tc.bound} {
				if !strings.Contains(printed, want) {
					t.Errorf("printed capacity %q lacks %q", printed, want)
				}
			}
			if !tc.cores.Known && strings.Contains(printed, "cores=0") {
				t.Errorf("printed capacity %q reports unmeasured cores as 0", printed)
			}
		})
	}

	t.Run("cores are measured at run time and never exceed the machine", func(t *testing.T) {
		got := core.MeasureCores()
		if !got.Known {
			t.Fatal("MeasureCores() is unknown on a machine whose cores the runtime reports")
		}
		if got.Value < 1 || got.Value > int64(runtime.NumCPU()) {
			t.Fatalf("MeasureCores() = %d, want 1..%d", got.Value, runtime.NumCPU())
		}
	})

	t.Run("the production worker count uses measured cores and the formula", func(t *testing.T) {
		cores := core.MeasureCores()
		for _, jobs := range []int{1, 2, 3, runtime.NumCPU()*4 + 1} {
			want := core.CapacityFor(cores, jobs).Workers
			if got := core.Workers(jobs); got != want {
				t.Errorf("Workers(%d) = %d, want %d from measured cores %+v", jobs, got, want, cores)
			}
		}
		if got := core.Workers(runtime.NumCPU()*4 + 1); got != 1 {
			t.Errorf("Workers with more jobs than cores = %d, want the minimum 1", got)
		}
	})
}
