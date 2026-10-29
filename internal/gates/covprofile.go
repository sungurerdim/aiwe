package gates

import (
	"fmt"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
)

// CoverBlock is a run of lines of one file that a test run executed Count times; Count 0 means no
// test executed it.
type CoverBlock struct {
	Start, End int
	Count      int
}

// CoverProfile maps a repository-relative path, with forward slashes, to the blocks of a coverage
// profile. A file with no block has no entry.
type CoverProfile map[string][]CoverBlock

// ParseGoCoverProfile reads a Go cover profile (`go test -coverprofile`): a `mode:` line, then one
// `name:startLine.startCol,endLine.endCol numStmt count` line per block. The text is untrusted: a
// line it cannot read is an error that names the line. module is the module path; it is stripped
// from each name so the path is repository-relative. A block that two test binaries report stays
// two blocks.
func ParseGoCoverProfile(text, module string) (CoverProfile, error) {
	lines := strings.Split(text, "\n")
	if !strings.HasPrefix(strings.TrimSuffix(lines[0], "\r"), "mode: ") {
		return nil, fmt.Errorf("cover profile line 1: want a `mode:` line")
	}
	out := CoverProfile{}
	for i, raw := range lines[1:] {
		line := strings.TrimSuffix(raw, "\r")
		if line == "" {
			continue
		}
		file, block, err := parseGoBlock(line, module)
		if err != nil {
			return nil, fmt.Errorf("cover profile line %d: %w", i+2, err)
		}
		out[file] = append(out[file], block)
	}
	return out, nil
}

// parseGoBlock reads one block line of a Go cover profile.
func parseGoBlock(line, module string) (string, CoverBlock, error) {
	cut := strings.LastIndex(line, ":")
	fields := strings.Fields(line[cut+1:])
	if cut < 0 || len(fields) != 3 {
		return "", CoverBlock{}, fmt.Errorf("want `name:start.col,end.col statements count`, found %q", line)
	}
	name := line[:cut]
	if module != "" {
		name = strings.TrimPrefix(name, module+"/")
	}
	file := cleanProfilePath(name)
	startText, endText, _ := strings.Cut(fields[0], ",")
	start, okStart := goPosition(startText)
	end, okEnd := goPosition(endText)
	stmts, errStmts := strconv.Atoi(fields[1])
	count, errCount := strconv.Atoi(fields[2])
	if file == "" || !okStart || !okEnd || end < start || errStmts != nil || stmts < 0 ||
		errCount != nil || count < 0 {
		return "", CoverBlock{}, fmt.Errorf("want a named block, lines from 1 and counts from 0, found %q", line)
	}
	return file, CoverBlock{Start: start, End: end, Count: count}, nil
}

// goPosition reads the line of a `line.col` position of a Go cover profile.
func goPosition(pos string) (int, bool) {
	lineText, colText, found := strings.Cut(pos, ".")
	if _, err := strconv.ParseUint(colText, 10, 31); !found || err != nil {
		return 0, false
	}
	return profileLine(lineText)
}

// profileLine reads a line number: a whole number from 1 that cannot overflow a range.
func profileLine(text string) (int, bool) {
	n, err := strconv.Atoi(text)
	return n, err == nil && n >= 1 && n <= maxDiffLine
}

// cleanProfilePath returns the repository-relative form of a profile path: forward slashes, no
// `./`. An empty name gives "".
func cleanProfilePath(name string) string {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." {
		return ""
	}
	return clean
}

// ParseLcov reads an lcov file: `SF:` opens the record of a file, each `DA:line,count[,checksum]`
// is one executed line, `end_of_record` closes the record; every other line (branches, functions,
// totals) is not a block. The text is untrusted: a line record outside a file or one it cannot read
// is an error that names the line. Consecutive lines with the same count become one block, and a
// line given twice adds its counts, so the blocks equal those of a Go cover profile for the same
// executed lines.
func ParseLcov(text string) (CoverProfile, error) {
	r := lcovReader{out: CoverProfile{}}
	for i, raw := range strings.Split(text, "\n") {
		if err := r.line(strings.TrimSuffix(raw, "\r")); err != nil {
			return nil, fmt.Errorf("lcov line %d: %w", i+1, err)
		}
	}
	r.flush()
	return r.out, nil
}

// lcovReader is the state of ParseLcov: the file of the open record and its executed lines.
type lcovReader struct {
	out  CoverProfile
	file string
	hits map[int]int
}

// line feeds one line of the lcov file to the reader.
func (r *lcovReader) line(line string) error {
	switch {
	case strings.HasPrefix(line, "SF:"):
		r.flush()
		r.file = cleanProfilePath(strings.TrimPrefix(line, "SF:"))
		if r.file == "" {
			return fmt.Errorf("want a file name after SF:, found %q", line)
		}
		r.hits = map[int]int{}
	case line == "end_of_record":
		r.flush()
	case strings.HasPrefix(line, "DA:"):
		return r.hit(strings.TrimPrefix(line, "DA:"))
	}
	return nil
}

// hit records one `line,count[,checksum]` of the open record.
func (r *lcovReader) hit(fields string) error {
	parts := strings.Split(fields, ",")
	if r.file == "" || len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("want `DA:line,count` inside a file record, found %q", fields)
	}
	n, okLine := profileLine(parts[0])
	count, err := strconv.Atoi(parts[1])
	if !okLine || err != nil || count < 0 {
		return fmt.Errorf("want a line from 1 and a count from 0, found %q", fields)
	}
	r.hits[n] += min(count, math.MaxInt-r.hits[n])
	return nil
}

// flush closes the open record: its lines become blocks of the file.
func (r *lcovReader) flush() {
	if r.file != "" && len(r.hits) > 0 {
		r.out[r.file] = append(r.out[r.file], blocksOfLines(r.hits)...)
	}
	r.file, r.hits = "", nil
}

// blocksOfLines turns executed lines into blocks: a run of consecutive lines with one count is one
// block. The blocks are in line order.
func blocksOfLines(hits map[int]int) []CoverBlock {
	lines := make([]int, 0, len(hits))
	for n := range hits {
		lines = append(lines, n)
	}
	slices.Sort(lines)
	var blocks []CoverBlock
	for _, n := range lines {
		last := len(blocks) - 1
		if last >= 0 && blocks[last].End == n-1 && blocks[last].Count == hits[n] {
			blocks[last].End = n
			continue
		}
		blocks = append(blocks, CoverBlock{Start: n, End: n, Count: hits[n]})
	}
	return blocks
}
