//go:build unix && !darwin

package core

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// procTestStatLine is a /proc/<pid>/stat line of a running bash; field 22, the start tick, is 98765.
const procTestStatLine = "1234 (bash) S 1 1234 1234 34816 1234 4194560 1520 0 0 0 3 1 0 0 20 0 1 0 98765 " +
	"9248768 1210 18446744073709551615 94512345 94523456 140723456 0 0 3670020 1266777851 0 0 0 17 2 0 0 0 0 0\n"

// procTestState returns procTestStatLine with its state, field 3, set to state.
func procTestState(state string) string {
	return strings.Replace(procTestStatLine, "(bash) S ", "(bash) "+state+" ", 1)
}

// TestParseProcStat catches a start tick read from the wrong field of /proc/<pid>/stat, or from a
// command name that holds spaces or ')', and a live process read as not running (U13).
func TestParseProcStat(t *testing.T) {
	cases := []struct {
		name string
		data string
		want uint64
	}{
		{"real line", procTestStatLine, 98765},
		{"running state R", procTestState("R"), 98765},
		{"comm with spaces and parentheses", strings.Replace(procTestStatLine, "(bash)", "(my (odd) ) name)", 1), 98765},
		{"comm with field-like text", strings.Replace(procTestStatLine, "(bash)", "(S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20)", 1), 98765},
		{"comm that ends in a zombie state", strings.Replace(procTestStatLine, "(bash)", "(sh) Z X)", 1), 98765},
		{"last tick a time.Duration holds", strings.Replace(procTestStatLine, " 98765 ", " 922337203685 ", 1), 922337203685},
	}
	for _, c := range cases {
		got, running, err := parseProcStat([]byte(c.data))
		if err != nil || got != c.want || !running {
			t.Errorf("%s: parseProcStat(%q) = %d, %t, %v; want %d, true, nil", c.name, c.data, got, running, err, c.want)
		}
	}
}

// TestParseProcStatNotRunning catches a zombie or dead process read as running: a pid in state Z
// or X has exited, so its start must never vouch for a live holder (U13).
func TestParseProcStatNotRunning(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"zombie state Z", procTestState("Z")},
		{"dead state X", procTestState("X")},
		{"zombie with a comm that holds a running state", strings.Replace(procTestState("Z"), "(bash)", "(bash) S R)", 1)},
	}
	for _, c := range cases {
		got, running, err := parseProcStat([]byte(c.data))
		if err != nil || running {
			t.Errorf("%s: parseProcStat(%q) = %d, %t, %v; want running false, nil", c.name, c.data, got, running, err)
		}
	}
}

// TestParseProcStatMalformed catches a malformed /proc/<pid>/stat read as a start: a truncated
// line, an empty input, no command name, a ')' with no '(' before it, a field 22 that is not a
// tick count, or one past the range of a time.Duration, which would wrap into a false start, must
// each be an error, never a start (U13, fail-closed).
func TestParseProcStatMalformed(t *testing.T) {
	fields := strings.Fields(procTestStatLine)
	cases := []struct {
		name string
		data string
	}{
		{"empty input", ""},
		{"truncated before field 22", strings.Join(fields[:21], " ")},
		{"truncated inside the command name", "1234 (ba"},
		{"nothing after the command name", "1234 (bash)"},
		{"no command name", strings.Join(append(fields[:1], fields[2:]...), " ")},
		{"close parenthesis without an open one", strings.Replace(procTestStatLine, "(bash)", "bash)", 1)},
		{"close parenthesis before the open one", strings.Replace(procTestStatLine, "(bash)", ")bash(", 1)},
		{"non-numeric field 22", strings.Replace(procTestStatLine, " 98765 ", " 98x65 ", 1)},
		{"negative field 22", strings.Replace(procTestStatLine, " 98765 ", " -98765 ", 1)},
		{"field 22 over uint64", strings.Replace(procTestStatLine, " 98765 ", " 18446744073709551616 ", 1)},
		{"field 22 one tick past time.Duration", strings.Replace(procTestStatLine, " 98765 ", " 922337203686 ", 1)},
		{"field 22 at the int64 maximum", strings.Replace(procTestStatLine, " 98765 ", " 9223372036854775807 ", 1)},
		{"zombie truncated before field 22", strings.Join(strings.Fields(procTestState("Z"))[:21], " ")},
		{"zombie with non-numeric field 22", strings.Replace(procTestState("Z"), " 98765 ", " 98x65 ", 1)},
	}
	for _, c := range cases {
		if got, running, err := parseProcStat([]byte(c.data)); err == nil {
			t.Errorf("%s: parseProcStat(%q) = %d, %t, nil; want an error", c.name, c.data, got, running)
		}
	}
}

// TestParseProcStatSelf catches a parser that reads a fixture but not the live /proc format: this
// process must be running, and its start tick must be field 22 of /proc/self/stat as
// procTestLinuxID reads it.
func TestParseProcStatSelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("no /proc/self/stat on %s", runtime.GOOS)
	}
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatalf("read /proc/self/stat: %v", err)
	}
	ticks, running, err := parseProcStat(data)
	if err != nil {
		t.Fatalf("parseProcStat(/proc/self/stat %q): %v", data, err)
	}
	if !running {
		t.Errorf("parseProcStat(/proc/self/stat %q) running = false, want true", data)
	}
	id := procTestLinuxID(t, os.Getpid())
	if want := id[strings.LastIndexByte(id, '/')+1:]; want != strconv.FormatUint(ticks, 10) {
		t.Errorf("parseProcStat(/proc/self/stat) = %d, want %s", ticks, want)
	}
}
