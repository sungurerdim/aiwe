package gates

import (
	"fmt"
	"regexp"
	"strings"
)

// tsc has no JSON reporter; with `--pretty false` it prints one fixed line per
// diagnostic, which is its machine format: `path(line,col): error TS2322: msg`,
// or `error TS5083: msg` when no file is involved.
var (
	tscLocated = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): (error|warning|message) (TS\d+): (.*)$`)
	tscGlobal  = regexp.MustCompile(`^(error|warning|message) (TS\d+): (.*)$`)
	tscSummary = regexp.MustCompile(`^Found \d+ errors?\b`)
)

// ParseTscText reads the stdout of `tsc --pretty false`. Empty output means no
// errors. Blank lines, indented continuation lines and the "Found N errors"
// summary are skipped; any other line that is not a diagnostic is an error
// (fail closed, DR-6), so a crash message does not read as a clean run. A
// global error has no file and no position.
func ParseTscText(data []byte) ([]StaticFinding, error) {
	var out []StaticFinding
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || line[0] == ' ' || line[0] == '\t' || tscSummary.MatchString(line) {
			continue
		}
		f, ok := tscFinding(line)
		if !ok {
			return nil, fmt.Errorf("tsc output line %d is not a diagnostic: %q", i+1, shortMessage(line))
		}
		out = append(out, f)
	}
	return out, nil
}

func tscFinding(line string) (StaticFinding, bool) {
	var f StaticFinding
	var kind, msg string
	if m := tscLocated.FindStringSubmatch(line); m != nil {
		f.File = m[1]
		f.Line, f.Col = tscNum(m[2]), tscNum(m[3])
		kind, f.Rule, msg = m[4], m[5], m[6]
	} else if m := tscGlobal.FindStringSubmatch(line); m != nil {
		kind, f.Rule, msg = m[1], m[2], m[3]
	} else {
		return f, false
	}
	f.Tool = "tsc"
	f.Level = map[string]string{"error": levelError, "warning": levelWarning, "message": levelNote}[kind]
	f.Message = shortMessage(msg)
	f.Fix = "fix the reported " + f.Rule + " error; tsc --noEmit rechecks"
	return f, true
}

// tscNum is a position, or 0 (not given) when the digits overflow.
func tscNum(s string) int {
	if n, ok := posInt(s); ok {
		return n
	}
	return 0
}
