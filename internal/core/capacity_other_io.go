//go:build !darwin && !linux && !windows

package core

// The reason an OS with no load source of its own gives for every signal (DR-6).
const otherReasonUnsupported = "os_not_supported"

// newLoadSource returns the load sample source of an OS aiwe has no reader for: every signal is
// unknown with its reason, never a number, so the verdict is at most wait (SPEC §6.6, DR-6).
func newLoadSource() loadSource {
	return func() loadReading {
		return loadReading{
			CPU:        loadPoint{Reason: otherReasonUnsupported},
			RunQueue:   loadPoint{Reason: otherReasonUnsupported},
			FreeMemory: LoadLowest{Reason: otherReasonUnsupported},
		}
	}
}
