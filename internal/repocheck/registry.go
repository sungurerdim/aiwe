package repocheck

import (
	"fmt"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// ruleRegistryDrift is the one rule of check repo/registry: a row of the §3 or §3.1 table of
// docs/SPEC.md that the registry docs/standards.md does not carry, or carries differently (K41).
const ruleRegistryDrift = "registry_drift"

// registryFile is the one registry of aiwe's rules: every design rule and every standard (K41).
const registryFile = "docs/standards.md"

// Registry checks the design-rule and standards tables of docs/SPEC.md against docs/standards.md,
// the one registry (K41): each table row has the same id, statement and enforcement as its registry
// row, and no row is in one and not the other. A registry or spec that cannot be read, or that holds
// no rule row, is an error: no rule was checked (DR-6).
func Registry(repo string) ([]Finding, error) {
	spec, err := tableRowsOf(repo, specFile)
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	if len(spec) == 0 {
		return nil, fmt.Errorf("registry: %s: §3 names no rule row; the tables are unchecked", specFile)
	}
	registry, err := tableRowsOf(repo, registryFile)
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	if len(registry) == 0 {
		return nil, fmt.Errorf("registry: %s: no rule row; the tables are unchecked", registryFile)
	}
	found := drift(registry, spec)
	sortFindings(found)
	return found, nil
}

// tableRow is one rule row of a markdown table: its 1-based line in the file and its cells.
type tableRow struct {
	line  int
	cells []string
}

// tableRowsOf returns the rule rows of the markdown tables in rel: a row whose first cell is a DR or
// STD id.
func tableRowsOf(repo, rel string) ([]tableRow, error) {
	body, err := readRepoFile(repo, rel)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	text, err := core.ReadText(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	return tableRows(strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")), nil
}

// tableRows returns the rule rows of lines. A table row is a line whose first cell is a DR or STD
// id; the pipes between the cells are its separators, and the cells are trimmed.
func tableRows(lines []string) []tableRow {
	var rows []tableRow
	for i, line := range lines {
		if !strings.HasPrefix(line, "| DR-") && !strings.HasPrefix(line, "| STD-") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for k := range cells {
			cells[k] = strings.TrimSpace(cells[k])
		}
		rows = append(rows, tableRow{line: i + 1, cells: cells})
	}
	return rows
}

// drift returns one finding per row the two tables do not share. The rows are walked in file order,
// so the same files give the same findings in the same order (K39). A row the registry carries and
// the spec does not points at the spec, and the other way round at the registry; a row both carry
// but with a different statement or enforcement points at the spec row that must change.
func drift(registry, spec []tableRow) []Finding {
	specByID := rowsByID(spec)
	regByID := rowsByID(registry)
	var found []Finding
	for _, row := range registry {
		other, ok := specByID[row.cells[0]]
		switch {
		case !ok:
			found = append(found, Finding{Rule: ruleRegistryDrift, Path: specFile})
		case !sameRow(row, other):
			found = append(found, Finding{Rule: ruleRegistryDrift, Path: specFile, Line: other.line})
		}
	}
	for _, row := range spec {
		if _, ok := regByID[row.cells[0]]; !ok {
			found = append(found, Finding{Rule: ruleRegistryDrift, Path: registryFile})
		}
	}
	return found
}

// rowsByID indexes rows by their id cell.
func rowsByID(rows []tableRow) map[string]tableRow {
	byID := make(map[string]tableRow, len(rows))
	for _, row := range rows {
		if len(row.cells) > 0 {
			byID[row.cells[0]] = row
		}
	}
	return byID
}

// sameRow reports whether the registry row and the spec row carry the same statement and the same
// enforcement. The standards table of the spec carries a source column the registry does not, so
// there the enforcement is the fourth cell; the design-rule table has none, so there it is the third.
// A row too short to hold an enforcement differs from every row.
func sameRow(registry, spec tableRow) bool {
	if len(registry.cells) < 3 || len(spec.cells) < 3 {
		return false
	}
	enforcement := spec.cells[2]
	if strings.HasPrefix(spec.cells[0], "STD-") && len(spec.cells) > 3 {
		enforcement = spec.cells[3]
	}
	return registry.cells[1] == spec.cells[1] && registry.cells[2] == enforcement
}
