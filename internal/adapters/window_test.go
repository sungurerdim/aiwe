package adapters

import (
	"strings"
	"testing"
	"time"
)

// TestDeepSeekOffPeakWindowIsPinned catches an off-peak window that is not pinned, is pinned to the
// wrong interval, or carries no source or access date: DeepSeek's published off-peak window is
// 16:30–00:30 UTC, crosses midnight and is pinned with its source and access date (SPEC §17, O14).
func TestDeepSeekOffPeakWindowIsPinned(t *testing.T) {
	w := DeepSeekOffPeakWindow()
	if w.Start != 16*time.Hour+30*time.Minute {
		t.Errorf("the off-peak window opens at %v, want 16:30 UTC (SPEC §17)", w.Start)
	}
	if w.End != 30*time.Minute {
		t.Errorf("the off-peak window closes at %v, want 00:30 UTC (SPEC §17)", w.End)
	}
	if w.Start <= w.End {
		t.Errorf("the off-peak window is %v–%v, want a window that crosses midnight (O14)", w.Start, w.End)
	}
	if strings.TrimSpace(w.Source) == "" {
		t.Error("the off-peak window names no source, so its price window is not traced (SPEC §17)")
	}
	if !strings.Contains(strings.ToLower(w.Source), "deepseek") {
		t.Errorf("the off-peak window source is %q, want DeepSeek's published pricing page (SPEC §17)", w.Source)
	}
	if strings.TrimSpace(w.AccessedAt) == "" {
		t.Error("the off-peak window names no access date, so its price window is not traced (SPEC §17)")
	}
}

// TestOffPeakWindowCoversTable catches a window compared by the local wall clock instead of the
// instant, a crossing-midnight window read as a start<end range, or a zone offset applied twice: the
// window covers 16:30 UTC up to but not including 00:30 UTC, a +05:30 offset never moves the edge,
// and the two passes of the DST fall-back repeated hour stay apart (O14).
func TestOffPeakWindowCoversTable(t *testing.T) {
	w := DeepSeekOffPeakWindow()
	kolkata := time.FixedZone("IST", 5*3600+1800) // +05:30, a half-hour offset
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatalf("LoadLocation(Europe/London): %v", err)
	}
	tests := []struct {
		name  string
		at    time.Time
		want  bool
		catch string
	}{
		{"before the window opens", time.Date(2026, 10, 12, 16, 29, 0, 0, time.UTC), false, "16:29 UTC is before the 16:30 open edge"},
		{"at the window opens", time.Date(2026, 10, 12, 16, 30, 0, 0, time.UTC), true, "16:30 UTC is the open edge, included"},
		{"inside the window before midnight", time.Date(2026, 10, 12, 23, 59, 0, 0, time.UTC), true, "23:59 UTC is inside a window that crosses midnight"},
		{"inside the window after midnight", time.Date(2026, 10, 13, 0, 15, 0, 0, time.UTC), true, "00:15 UTC is inside the window after midnight"},
		{"at the window closes", time.Date(2026, 10, 13, 0, 30, 0, 0, time.UTC), false, "00:30 UTC is the close edge, excluded"},
		{"after the window closes", time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC), false, "01:00 UTC is after the window"},
		{"at the open edge in a half-hour offset zone", time.Date(2026, 10, 12, 22, 0, 0, 0, kolkata), true, "22:00 +05:30 is 16:30 UTC, the open edge"},
		{"before the open edge in a half-hour offset zone", time.Date(2026, 10, 12, 21, 59, 0, 0, kolkata), false, "21:59 +05:30 is 16:29 UTC, before the window"},
		{"a local clock inside the window that is outside in UTC", time.Date(2026, 10, 12, 17, 0, 0, 0, kolkata), false, "17:00 +05:30 is 11:30 UTC, outside the UTC window, so the local wall clock must not decide"},
		{"the first pass of the DST repeated hour", time.Date(2026, 10, 25, 0, 15, 0, 0, time.UTC).In(london), true, "00:15 UTC is inside, though the London wall clock repeats at the fall-back"},
		{"the second pass of the DST repeated hour", time.Date(2026, 10, 25, 1, 15, 0, 0, time.UTC).In(london), false, "01:15 UTC is outside, though the London wall clock repeats at the fall-back"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.Covers(tc.at); got != tc.want {
				t.Errorf("Covers(%v) = %v, want %v; %s (O14)", tc.at, got, tc.want, tc.catch)
			}
		})
	}
}

// TestOffPeakSessionDecidesOnceAtStart catches a session that re-reads the window during a run: at
// session start aiwe reads the local time zone once and decides, so a session started at 23:59 local
// keeps its start decision though it runs past the 00:30 UTC window edge, and a session started
// after the edge keeps its own opposite decision (O14).
func TestOffPeakSessionDecidesOnceAtStart(t *testing.T) {
	w := DeepSeekOffPeakWindow()
	kolkata := time.FixedZone("IST", 5*3600+1800)
	// start is 2026-10-12 23:59 +05:30, that is 18:29 UTC, inside the window.
	start := time.Date(2026, 10, 12, 23, 59, 0, 0, kolkata)
	// later is 2026-10-13 06:30 +05:30, that is 01:00 UTC, past the 00:30 edge.
	later := time.Date(2026, 10, 13, 6, 30, 0, 0, kolkata)
	if !w.Covers(start) {
		t.Fatalf("fixture: %v (=%v UTC) must be off-peak", start, start.UTC())
	}
	if w.Covers(later) {
		t.Fatalf("fixture: %v (=%v UTC) must be past the window edge", later, later.UTC())
	}
	session := StartOffPeakSession(w, start)
	if !session.OffPeak() {
		t.Errorf("a session started at %v (=%v UTC) is not off-peak; the decision is the start moment's (O14)", start, start.UTC())
	}
	if session.OffPeak() == w.Covers(later) {
		t.Errorf("the session's decision equals a fresh decision at %v past the edge, so it re-read the window; the decision is made once (O14)", later)
	}
	after := StartOffPeakSession(w, later)
	if after.OffPeak() {
		t.Errorf("a session started at %v (=%v UTC) is off-peak; the window does not cover the start moment (O14)", later, later.UTC())
	}
	if session.OffPeak() == after.OffPeak() {
		t.Errorf("two sessions on either side of the edge decided the same; the decision is per session and per start moment (O14)")
	}
}
