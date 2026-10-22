package adapters

import "time"

// OffPeakWindow is a daily price window of a model API, pinned in UTC: the open edge, the close
// edge and the source and access date the pin is read from (SPEC §17, O14). A window whose start is
// later than its end crosses midnight.
type OffPeakWindow struct {
	Start      time.Duration
	End        time.Duration
	Source     string
	AccessedAt string
}

// Covers reports whether the instant at lies inside the window, the open edge included and the close
// edge excluded. The comparison is on the instant in UTC, so a local wall clock, a half-hour offset
// and the repeated hour of a DST fall-back never move an edge (O14).
func (w OffPeakWindow) Covers(at time.Time) bool {
	utc := at.UTC()
	since := time.Duration(utc.Hour())*time.Hour + time.Duration(utc.Minute())*time.Minute +
		time.Duration(utc.Second())*time.Second + time.Duration(utc.Nanosecond())
	if w.Start <= w.End {
		return since >= w.Start && since < w.End
	}
	return since >= w.Start || since < w.End
}

// DeepSeekOffPeakWindow returns DeepSeek's published off-peak discount window, pinned in UTC: it
// opens 16:30 UTC and closes 00:30 UTC, so it crosses midnight. Source: DeepSeek's pricing page
// (SPEC §17, O14).
func DeepSeekOffPeakWindow() OffPeakWindow {
	return OffPeakWindow{
		Start:      16*time.Hour + 30*time.Minute,
		End:        30 * time.Minute,
		Source:     "DeepSeek API pricing (off-peak discounts), https://api-docs.deepseek.com/quick_start/pricing",
		AccessedAt: "2026-10-12",
	}
}

// OffPeakSession is the one price-window decision of a session: the device's local time zone is read
// once at the session's start and the decision holds until the session ends (O14).
type OffPeakSession struct {
	offPeak bool
}

// StartOffPeakSession decides once, from the local instant at, whether the session is off-peak: the
// decision is the start moment's and is never re-read during the session (O14).
func StartOffPeakSession(w OffPeakWindow, at time.Time) OffPeakSession {
	return OffPeakSession{offPeak: w.Covers(at)}
}

// OffPeak reports the session's start decision (O14).
func (s OffPeakSession) OffPeak() bool { return s.offPeak }
