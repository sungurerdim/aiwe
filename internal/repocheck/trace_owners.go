package repocheck

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// contractsFile owns the D ids of the diff-check table and the sections of the contracts.
const contractsFile = "docs/SPEC-contracts.md"

var (
	diffCheckPattern = regexp.MustCompile(`^D[0-9]+$`)
	registryPattern  = regexp.MustCompile(`^(?:DR|STD)-[0-9]+$`)
	// headingPattern captures the number of a numbered heading of the specification.
	headingPattern = regexp.MustCompile(`^#{2,6} ([0-9]+(?:\.[0-9]+)*)\.?\s`)
)

// owner is where the ids of one kind of card Gate value are written: the files that hold them and
// how to read them from the lines of one file.
type owner struct {
	files []string
	read  func(lines []string, ids map[string]bool)
}

// owners returns the owners of the kinds other than the registry, which Trace already holds.
func owners() map[string]owner {
	return map[string]owner{
		"diff":    {files: []string{contractsFile}, read: diffIDs},
		"outcome": {files: []string{specFile}, read: outcomeIDs},
		"section": {files: []string{specFile, contractsFile}, read: sectionIDs},
	}
}

// unknownGates returns one unknown_rule finding per card Gate value that the owner of its kind does
// not hold. An owner is read once, and only when a card names its kind; a value of no kind is red.
func unknownGates(repo string, gates []ruleMarker, registry map[string]bool) ([]Finding, error) {
	owned := map[string]map[string]bool{"registry": registry}
	var found []Finding
	for _, g := range gates {
		kind := gateKind(g.id)
		if owned[kind] == nil {
			ids, err := ownerIDs(repo, owners()[kind])
			if err != nil {
				return nil, err
			}
			owned[kind] = ids
		}
		found = append(found, unknownMarkers([]ruleMarker{g}, owned[kind])...)
	}
	return found, nil
}

// gateKind names the owner of a Gate value by its shape: a rule id of the registry, a D id of the
// diff-check table, a section sign of a heading, or else an outcome key.
func gateKind(id string) string {
	switch {
	case registryPattern.MatchString(id):
		return "registry"
	case diffCheckPattern.MatchString(id):
		return "diff"
	case strings.HasPrefix(id, "§"):
		return "section"
	}
	return "outcome"
}

// ownerIDs reads the ids one owner writes from its files. An unreadable file is an error, never an
// owner that holds no id (DR-6).
func ownerIDs(repo string, o owner) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, rel := range o.files {
		body, err := readRepoFile(repo, rel)
		if err != nil {
			return nil, fmt.Errorf("trace: %s: %w", rel, err)
		}
		text, err := core.ReadText(body)
		if err != nil {
			return nil, fmt.Errorf("trace: %s: %w", rel, err)
		}
		o.read(strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), ids)
	}
	return ids, nil
}

// diffIDs adds the Id column of the diff-check table.
func diffIDs(lines []string, ids map[string]bool) {
	for _, c := range tableColumn(lines, "Id", "Check", "Enforced by") {
		ids[c] = true
	}
}

// outcomeIDs adds the backticked names of the Outcome column.
func outcomeIDs(lines []string, ids map[string]bool) {
	for _, c := range tableColumn(lines, "Outcome") {
		for _, m := range backtickPattern.FindAllStringSubmatch(c, -1) {
			ids[m[1]] = true
		}
	}
}

// sectionIDs adds the section sign of each numbered heading.
func sectionIDs(lines []string, ids map[string]bool) {
	for _, line := range lines {
		if m := headingPattern.FindStringSubmatch(line); m != nil {
			ids["§"+m[1]] = true
		}
	}
}

// tableColumn returns the column named header[0] of each table whose header row holds every name.
func tableColumn(lines []string, header ...string) []string {
	var col []string
	at := -1
	for _, line := range lines {
		if !strings.HasPrefix(line, "|") {
			at = -1
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for k := range cells {
			cells[k] = strings.TrimSpace(cells[k])
		}
		if at >= 0 && at < len(cells) {
			col = append(col, cells[at])
		} else if at < 0 && !slices.ContainsFunc(header, func(h string) bool { return !slices.Contains(cells, h) }) {
			at = slices.Index(cells, header[0])
		}
	}
	return col
}
