package gates

import (
	"strconv"
	"strings"
)

// maxDiffLine bounds a line number or a hunk length read from a diff, so a range never overflows.
const maxDiffLine = 1 << 30

// LineRange is a run of lines of one file, 1-based and inclusive at both ends.
type LineRange struct {
	Start, End int
}

// DiffHunks maps a path, as it is named after the change, to the lines the change added or
// replaced, in the order Git printed them. A file with no added line has no entry.
type DiffHunks map[string][]LineRange

// ParseDiffHunks reads the added line ranges from the text of a zero-context diff. The text is
// untrusted: the reader follows the line counts of each hunk header, so an added line that reads as
// a file header or as a hunk header (`+++ b/x`, `+@@ ...`) is body and forges nothing. A deleted
// file (`+++ /dev/null`) gives no range, a rename keeps the new path, and a hunk it cannot read is
// skipped. The result is never nil.
func ParseDiffHunks(diff string) DiffHunks {
	p := hunkParser{out: DiffHunks{}}
	for _, line := range strings.Split(diff, "\n") {
		p.line(strings.TrimSuffix(line, "\r"))
	}
	return p.out
}

// hunkParser is the state of ParseDiffHunks: the file whose hunks are being read and the lines
// still owed by the open hunk.
type hunkParser struct {
	out        DiffHunks
	file       string
	oldLeft    int
	newLeft    int
	inHunkBody bool
}

// line feeds one line of the diff to the parser.
func (p *hunkParser) line(line string) {
	if p.inHunkBody {
		if p.body(line) {
			return
		}
		p.oldLeft, p.newLeft, p.inHunkBody = 0, 0, false
	}
	switch {
	case strings.HasPrefix(line, "diff "):
		p.file = ""
	case strings.HasPrefix(line, "+++ "):
		p.file = diffFilePath(strings.TrimPrefix(line, "+++ "))
	case strings.HasPrefix(line, "@@ "):
		p.header(line)
	}
}

// body consumes one line of an open hunk and reports whether it belonged to it. A hunk is closed
// when both of its counts are used up.
func (p *hunkParser) body(line string) bool {
	switch {
	case strings.HasPrefix(line, "\\"):
		return true
	case strings.HasPrefix(line, "-") && p.oldLeft > 0:
		p.oldLeft--
	case strings.HasPrefix(line, "+") && p.newLeft > 0:
		p.newLeft--
	default:
		return false
	}
	p.inHunkBody = p.oldLeft > 0 || p.newLeft > 0
	return true
}

// header reads `@@ -a[,b] +c[,d] @@` and records the added range c..c+d-1 for the current file.
func (p *hunkParser) header(line string) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[3] != "@@" {
		return
	}
	_, oldCount, okOld := hunkSpan(fields[1], "-")
	start, newCount, okNew := hunkSpan(fields[2], "+")
	if !okOld || !okNew {
		return
	}
	p.oldLeft, p.newLeft = oldCount, newCount
	p.inHunkBody = oldCount > 0 || newCount > 0
	if newCount > 0 && p.file != "" && start >= 1 {
		p.out[p.file] = append(p.out[p.file], LineRange{Start: start, End: start + newCount - 1})
	}
}

// hunkSpan reads `<sign>start[,count]`; a missing count is 1.
func hunkSpan(field, sign string) (start, count int, ok bool) {
	rest, found := strings.CutPrefix(field, sign)
	if !found {
		return 0, 0, false
	}
	startText, countText, hasCount := strings.Cut(rest, ",")
	start, err := strconv.Atoi(startText)
	if err != nil || start < 0 || start > maxDiffLine {
		return 0, 0, false
	}
	count = 1
	if hasCount {
		if count, err = strconv.Atoi(countText); err != nil || count < 0 || count > maxDiffLine {
			return 0, 0, false
		}
	}
	return start, count, true
}

// diffFilePath returns the path a `+++ ` line names, without the `b/` prefix and without the tab
// Git appends after a path with a space. A quoted path is decoded; `/dev/null` (a deleted file) and
// a path it cannot read give "".
func diffFilePath(name string) string {
	name = strings.TrimRight(name, "\t")
	if strings.HasPrefix(name, `"`) {
		unquoted, err := strconv.Unquote(name)
		if err != nil {
			return ""
		}
		name = unquoted
	}
	path, found := strings.CutPrefix(name, "b/")
	if !found {
		return ""
	}
	return path
}
