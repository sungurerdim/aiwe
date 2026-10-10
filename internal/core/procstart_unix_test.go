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

// TestParseProcStat catches a start tick read from the wrong field of /proc/<pid>/stat, or from a
// command name that holds spaces or ')' (U13).
func TestParseProcStat(t *testing.T) {
	cases := []struct {
		name string
		data string
		want uint64
	}{
		{"real line", procTestStatLine, 98765},
		{"comm with spaces and parentheses", strings.Replace(procTestStatLine, "(bash)", "(my (odd) ) name)", 1), 98765},
		{"comm with field-like text", strings.Replace(procTestStatLine, "(bash)", "(S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20)", 1), 98765},
	}
	for _, c := range cases {
		got, err := parseProcStat([]byte(c.data))
		if err != nil || got != c.want {
			t.Errorf("%s: parseProcStat(%q) = %d, %v; want %d, nil", c.name, c.data, got, err, c.want)
		}
	}
}

// TestParseProcStatMalformed catches a malformed /proc/<pid>/stat read as a start: a truncated
// line, an empty input, no command name, or a field 22 that is not a tick count must each be an
// error, never a start (U13, fail-closed).
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
		{"non-numeric field 22", strings.Replace(procTestStatLine, " 98765 ", " 98x65 ", 1)},
		{"negative field 22", strings.Replace(procTestStatLine, " 98765 ", " -98765 ", 1)},
		{"field 22 over uint64", strings.Replace(procTestStatLine, " 98765 ", " 18446744073709551616 ", 1)},
	}
	for _, c := range cases {
		if got, err := parseProcStat([]byte(c.data)); err == nil {
			t.Errorf("%s: parseProcStat(%q) = %d, nil; want an error", c.name, c.data, got)
		}
	}
}

// TestParseProcStatSelf catches a parser that reads a fixture but not the live /proc format: the
// start tick of this process must be field 22 of /proc/self/stat as procTestLinuxID reads it.
func TestParseProcStatSelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("no /proc/self/stat on %s", runtime.GOOS)
	}
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatalf("read /proc/self/stat: %v", err)
	}
	ticks, err := parseProcStat(data)
	if err != nil {
		t.Fatalf("parseProcStat(/proc/self/stat %q): %v", data, err)
	}
	id := procTestLinuxID(t, os.Getpid())
	if want := id[strings.LastIndexByte(id, '/')+1:]; want != strconv.FormatUint(ticks, 10) {
		t.Errorf("parseProcStat(/proc/self/stat) = %d, want %s", ticks, want)
	}
}
