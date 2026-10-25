package main

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// decisionWidth is how many characters of a decision's text the index keeps.
	decisionWidth = 120
	// decisionHeading and nextHeading bound the SPEC §16 decision log.
	decisionHeading = "## 16. "
	nextHeading     = "## "
	// decisionCells is the cell count of a row: id, decision, status, enforced by.
	decisionCells = 4
)

// idPattern matches a decision id such as O1 or L41.
var idPattern = regexp.MustCompile(`^[A-Z]+[0-9]+$`)

// decision is one row of the SPEC §16 table.
type decision struct {
	id, text, status string
}

// decisions builds DECISIONS.md, the index of the SPEC §16 table.
func (s *session) decisions() (string, error) {
	raw, ok, err := s.d.read(specPath)
	if err != nil {
		return "", fmt.Errorf("%s: %w", specPath, err)
	}
	if !ok {
		return "", fmt.Errorf("%s: file not found", specPath)
	}
	rows, err := parseDecisions(string(raw))
	if err != nil {
		return "", fmt.Errorf("%s §16: %w", specPath, err)
	}
	var b strings.Builder
	b.WriteString(header(s.d.now(), specPath+" §16 decision log table"))
	fmt.Fprintf(&b, "# DECISIONS\n\n%d decisions. Full text: [%s](%s#16-decision-log). Regenerate: `%s --write decisions`\n\n",
		len(rows), specPath, specPath, recordsCmd)
	for _, r := range rows {
		fmt.Fprintf(&b, "- %s · %s · %s\n", r.id, r.status, clip(r.text, decisionWidth))
	}
	return b.String(), nil
}

// parseDecisions reads the table rows between the §16 heading and the next heading. A table that
// is missing, empty or malformed is an error: an empty index would read as "no decisions" (DR-6).
func parseDecisions(spec string) ([]decision, error) {
	in := false
	var rows []decision
	for _, line := range strings.Split(spec, "\n") {
		switch {
		case strings.HasPrefix(line, decisionHeading):
			in = true
		case in && strings.HasPrefix(line, nextHeading):
			in = false
		case in && strings.HasPrefix(line, "| "):
			row, skip, err := parseRow(line)
			if err != nil {
				return nil, err
			}
			if !skip {
				rows = append(rows, row)
			}
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no decision rows found")
	}
	return rows, nil
}

// parseRow reads one table row; the header row is skipped.
func parseRow(line string) (row decision, skip bool, err error) {
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "| "), " |")
	cells := strings.Split(inner, " | ")
	if len(cells) != decisionCells {
		return decision{}, false, fmt.Errorf("a row has %d cells, want %d: %.40q", len(cells), decisionCells, line)
	}
	if cells[0] == "Id" {
		return decision{}, true, nil
	}
	if !idPattern.MatchString(cells[0]) {
		return decision{}, false, fmt.Errorf("row id %q is not a decision id", cells[0])
	}
	return decision{id: cells[0], text: oneLine(cells[1]), status: shortStatus(cells[2])}, false, nil
}

// shortStatus keeps the status up to its first qualifier: "valid; narrows R8" is "valid".
func shortStatus(cell string) string {
	status, _, _ := strings.Cut(cell, ";")
	status, _, _ = strings.Cut(status, " (")
	if status = strings.TrimSpace(status); status != "" {
		return status
	}
	return "unknown"
}

// clip cuts text to width characters and marks the cut.
func clip(text string, width int) string {
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	return strings.TrimRight(string(runes[:width]), " ") + "..."
}
