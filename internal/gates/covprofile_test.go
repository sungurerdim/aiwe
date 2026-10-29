package gates

import (
	"reflect"
	"strings"
	"testing"
)

// covprofileTestGo is a Go cover profile of two files: a.go holds an executed block and an
// unexecuted one, b.go one unexecuted block.
const covprofileTestGo = `mode: set
example.com/m/pkg/a.go:10.2,12.3 2 1
example.com/m/pkg/a.go:14.2,14.20 1 0
example.com/m/b.go:3.1,5.2 3 0
`

// covprofileTestLcov is the lcov file of the same executed lines as covprofileTestGo.
const covprofileTestLcov = `TN:
SF:pkg/a.go
FN:10,first
FNDA:1,first
DA:10,1
DA:11,1
DA:12,1
DA:14,0
LF:4
LH:3
end_of_record
SF:b.go
DA:3,0
DA:4,0
DA:5,0
end_of_record
`

// covprofileTestBlocks is the profile both covprofileTestGo and covprofileTestLcov must give.
func covprofileTestBlocks() CoverProfile {
	return CoverProfile{
		"pkg/a.go": {{Start: 10, End: 12, Count: 1}, {Start: 14, End: 14, Count: 0}},
		"b.go":     {{Start: 3, End: 5, Count: 0}},
	}
}

// TestParseGoCoverProfile catches a profile reader that loses the start, end or count of a block,
// keeps the module prefix of a path, counts a block with count 0 as executed, or accepts a profile
// it cannot read.
func TestParseGoCoverProfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    string
		module  string
		want    CoverProfile
		wantErr bool
	}{
		{"blocks with start, end and count, the module prefix stripped", covprofileTestGo, "example.com/m",
			covprofileTestBlocks(), false},
		{"a path outside the module keeps its name",
			"mode: set\nother.org/x/a.go:1.1,2.2 1 1\n", "example.com/m",
			CoverProfile{"other.org/x/a.go": {{Start: 1, End: 2, Count: 1}}}, false},
		{"a count mode count is kept whole", "mode: count\nm/a.go:4.1,6.2 2 7\n", "m",
			CoverProfile{"a.go": {{Start: 4, End: 6, Count: 7}}}, false},
		{"the same block from two test binaries stays two blocks",
			"mode: set\nm/a.go:4.1,6.2 2 0\nm/a.go:4.1,6.2 2 1\n", "m",
			CoverProfile{"a.go": {{Start: 4, End: 6, Count: 0}, {Start: 4, End: 6, Count: 1}}}, false},
		{"CRLF line ends", "mode: set\r\nm/a.go:1.1,1.5 1 1\r\n", "m",
			CoverProfile{"a.go": {{Start: 1, End: 1, Count: 1}}}, false},
		{"a profile with a mode line only", "mode: set\n", "m", CoverProfile{}, false},
		{"no mode line", "m/a.go:1.1,2.2 1 1\n", "m", nil, true},
		{"an empty profile", "", "m", nil, true},
		{"a block with two fields", "mode: set\nm/a.go:1.1,2.2 1\n", "m", nil, true},
		{"a block with no position", "mode: set\nm/a.go 1 1\n", "m", nil, true},
		{"a position with no column", "mode: set\nm/a.go:1,2 1 1\n", "m", nil, true},
		{"a negative count", "mode: set\nm/a.go:1.1,2.2 1 -1\n", "m", nil, true},
		{"a count that is not a number", "mode: set\nm/a.go:1.1,2.2 1 x\n", "m", nil, true},
		{"an end before the start", "mode: set\nm/a.go:5.1,2.2 1 1\n", "m", nil, true},
		{"line 0", "mode: set\nm/a.go:0.1,2.2 1 1\n", "m", nil, true},
		{"a line number too large to hold", "mode: set\nm/a.go:1.1,99999999999999999999.2 1 1\n", "m", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseGoCoverProfile(tc.text, tc.module)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseGoCoverProfile error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseGoCoverProfile = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseLcov catches an lcov reader that gives other blocks than the Go profile for the same
// executed lines, mixes the lines of two files, loses a count, or accepts a line outside a record.
func TestParseLcov(t *testing.T) {
	fromGo, err := ParseGoCoverProfile(covprofileTestGo, "example.com/m")
	if err != nil {
		t.Fatalf("ParseGoCoverProfile: %v", err)
	}
	for _, tc := range []struct {
		name    string
		text    string
		want    CoverProfile
		wantErr bool
	}{
		{"the same blocks as the Go profile for the same executed lines", covprofileTestLcov, fromGo, false},
		{"lines out of order and a checksum field",
			"SF:a.go\nDA:5,2,abc\nDA:3,2\nDA:4,2\nend_of_record\n",
			CoverProfile{"a.go": {{Start: 3, End: 5, Count: 2}}}, false},
		{"a line given twice adds its counts", "SF:a.go\nDA:3,1\nDA:3,2\nend_of_record\n",
			CoverProfile{"a.go": {{Start: 3, End: 3, Count: 3}}}, false},
		{"lines with different counts stay apart", "SF:a.go\nDA:3,0\nDA:4,1\nDA:6,1\nend_of_record\n",
			CoverProfile{"a.go": {{Start: 3, End: 3, Count: 0}, {Start: 4, End: 4, Count: 1},
				{Start: 6, End: 6, Count: 1}}}, false},
		{"a Windows path and CRLF line ends", "SF:pkg\\a.go\r\nDA:1,1\r\nend_of_record\r\n",
			CoverProfile{"pkg/a.go": {{Start: 1, End: 1, Count: 1}}}, false},
		{"a record with no end line", "SF:a.go\nDA:1,1\n",
			CoverProfile{"a.go": {{Start: 1, End: 1, Count: 1}}}, false},
		{"branch and function lines are not blocks", "SF:a.go\nBRDA:1,0,0,1\nFN:1,f\nend_of_record\n",
			CoverProfile{}, false},
		{"an empty file", "", CoverProfile{}, false},
		{"a line record before any file", "DA:1,1\n", nil, true},
		{"a line record after the end of its file", "SF:a.go\nend_of_record\nDA:1,1\n", nil, true},
		{"a line record with one field", "SF:a.go\nDA:1\nend_of_record\n", nil, true},
		{"a line number that is not a number", "SF:a.go\nDA:x,1\nend_of_record\n", nil, true},
		{"a negative count", "SF:a.go\nDA:1,-1\nend_of_record\n", nil, true},
		{"line 0", "SF:a.go\nDA:0,1\nend_of_record\n", nil, true},
		{"a file with no name", "SF:\nDA:1,1\nend_of_record\n", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLcov(tc.text)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseLcov error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseLcov = %v, want %v", got, tc.want)
			}
		})
	}
}

// FuzzParseGoCoverProfile catches a Go profile reader that panics on untrusted text or that returns
// a block with a line below 1, an end before its start or a negative count (STD-19).
func FuzzParseGoCoverProfile(f *testing.F) {
	f.Add(covprofileTestGo, "example.com/m")
	f.Add("mode: set\nm/a.go:1.1,2147483648.2 1 1\n", "m")
	f.Add("mode: set\n:1.1,2.2 1 1\n::\n", "")
	f.Add("mode: count\nm/a.go:1.1,2.2 1 9223372036854775808\n", "m")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, text, module string) {
		profile, err := ParseGoCoverProfile(text, module)
		if err == nil {
			covprofileTestCheck(t, text, profile)
		}
	})
}

// FuzzParseLcov catches an lcov reader that panics on untrusted text or that returns an invalid
// block (STD-19).
func FuzzParseLcov(f *testing.F) {
	f.Add(covprofileTestLcov)
	f.Add("SF:a\nDA:2147483647,1\nDA:2147483648,1\nend_of_record\n")
	f.Add("SF:a\nDA:1,9223372036854775807\nDA:1,9223372036854775807\n")
	f.Add("DA:\nSF:\nend_of_record")
	f.Add("")
	f.Fuzz(func(t *testing.T, text string) {
		profile, err := ParseLcov(text)
		if err == nil {
			covprofileTestCheck(t, text, profile)
		}
	})
}

// covprofileTestCheck fails the test for a profile with an unnamed file or an invalid block.
func covprofileTestCheck(t *testing.T, text string, profile CoverProfile) {
	t.Helper()
	for file, blocks := range profile {
		if file == "" || strings.ContainsRune(file, '\\') || len(blocks) == 0 {
			t.Fatalf("profile of %q holds the entry %q with %d block(s)", text, file, len(blocks))
		}
		for _, b := range blocks {
			if b.Start < 1 || b.End < b.Start || b.Count < 0 {
				t.Fatalf("profile of %q holds the invalid block %+v for %q", text, b, file)
			}
		}
	}
}
