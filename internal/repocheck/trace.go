package repocheck

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The rule names of check repo/trace: a rule row whose enforcement does not resolve, and a
// `// rule:` marker that names no rule row (the-gate-fails-a-rule-id-without-a-live-enforcement).
const (
	ruleNoLiveEnforcement = "no_live_enforcement"
	ruleUnknownRule       = "unknown_rule"
)

// decisionOnlyCell is the cell of a rule that a decision covers and no check can (#307).
const decisionOnlyCell = "decision-only"

// How this repository declares the checks its gate runs (SPEC §8.3, §12.1).
const (
	gateFile = "gate.json"
	repoStep = "repo"
	hooksDir = ".githooks"
)

// The patterns of one enforcement cell of docs/standards.md, in the grammar its header declares,
// and of one `// rule: <id>` marker. A cell may end with a parenthesis that explains it (DR-13).
var (
	testCellPattern      = regexp.MustCompile("^test `([^`]+)` ([A-Za-z_][A-Za-z0-9_]*)($| \\()")
	checkCellPattern     = regexp.MustCompile("^check `([^`]+)`($| \\()")
	plannedIssuePattern  = regexp.MustCompile(`^planned #[0-9]+$`)
	plannedStepPattern   = regexp.MustCompile("^planned: (?:CI job|release step) `[^`]+`")
	judgementCellPattern = regexp.MustCompile(`^judgement: ([a-z][a-z-]*)$`)
	ruleMarkerPattern    = regexp.MustCompile(`^rule:\s*(\S*)\s*$`)
)

// enforcementKind is the grammar kind of one enforcement cell of the one registry.
type enforcementKind int

const (
	kindTest enforcementKind = iota
	kindCheck
	kindPlanned
	kindJudgement
	kindDecisionOnly
)

// enforcement is one parsed enforcement cell: its kind, the test file and function, or the check id
// or judging role.
type enforcement struct {
	kind enforcementKind
	file string
	test string
	id   string
}

// parseEnforcement reads one enforcement cell of docs/standards.md; a cell of no known kind is not
// read.
func parseEnforcement(cell string) (enforcement, bool) {
	cell = strings.TrimSpace(cell)
	if m := testCellPattern.FindStringSubmatch(cell); m != nil {
		return enforcement{kind: kindTest, file: m[1], test: m[2]}, true
	}
	if m := checkCellPattern.FindStringSubmatch(cell); m != nil {
		return enforcement{kind: kindCheck, id: m[1]}, true
	}
	if m := judgementCellPattern.FindStringSubmatch(cell); m != nil {
		return enforcement{kind: kindJudgement, id: m[1]}, true
	}
	switch {
	case plannedIssuePattern.MatchString(cell), plannedStepPattern.MatchString(cell):
		return enforcement{kind: kindPlanned}, true
	case cell == decisionOnlyCell:
		return enforcement{kind: kindDecisionOnly}, true
	}
	return enforcement{}, false
}

// resolves reports whether e names a live test, check or card role, or a counted kind of no check.
func (e enforcement) resolves(src goSources, live, roles map[string]bool) bool {
	switch e.kind {
	case kindTest:
		return src.declares(e.file, e.test)
	case kindCheck:
		return live[e.id]
	case kindJudgement:
		return roles[e.id]
	case kindPlanned, kindDecisionOnly:
		return true
	}
	return false
}

// TraceReport is the result of check repo/trace: the counts of the enforcement kinds and findings.
type TraceReport struct {
	Total        int
	Live         int
	Planned      int
	Judgement    int
	DecisionOnly int
	Findings     []Finding
}

// Trace checks the one registry docs/standards.md, every `// rule: <id>` marker of the Go files
// and every Gate value of the role cards of the repository at repo: each rule row names a
// resolving enforcement cell, each marker names a rule row and each Gate value an id its owner
// holds (unknownGates). live holds the ids
// of the checks the gate really runs (LiveChecks). The reads are the capped regular-file reads of
// readRepoFile and GoFiles, so a FIFO is an error, never a blocked read (#199). An unreadable
// registry, one with no rule row, and a tree with no Go file are errors (DR-6).
func Trace(repo string, live []string) (TraceReport, error) {
	rows, err := tableRowsOf(repo, registryFile)
	if err != nil {
		return TraceReport{}, fmt.Errorf("trace: %w", err)
	}
	if len(rows) == 0 {
		return TraceReport{}, fmt.Errorf("trace: %s: no rule row; the rule trace is unchecked", registryFile)
	}
	src, err := parseSources(repo)
	if err != nil {
		return TraceReport{}, err
	}
	cards, err := src.cards()
	if err != nil {
		return TraceReport{}, err
	}
	rep := traceRowsCount(src, rows, live, cards.roles)
	ids := ruleIDs(rows)
	gated, err := unknownGates(repo, cards.gates, ids)
	if err != nil {
		return TraceReport{}, err
	}
	rep.Findings = append(rep.Findings, unknownMarkers(src.markers(), ids)...)
	rep.Findings = append(rep.Findings, gated...)
	sortFindings(rep.Findings)
	return rep, nil
}

// traceRowsCount classifies each rule row against the Go files, the live check ids and the roles
// of the shipped cards; each unresolved cell is no_live_enforcement.
func traceRowsCount(src goSources, rows []tableRow, live []string, roles map[string]bool) TraceReport {
	liveSet := make(map[string]bool, len(live))
	for _, id := range live {
		liveSet[id] = true
	}
	rep := TraceReport{Total: len(rows)}
	for _, row := range rows {
		e, known := parseEnforcement(cellAt(row, 2))
		if !known || !e.resolves(src, liveSet, roles) {
			rep.Findings = append(rep.Findings, Finding{Rule: ruleNoLiveEnforcement, Path: registryFile, Line: row.line})
			continue
		}
		rep.countKind(e.kind)
	}
	return rep
}

// countKind counts one resolved enforcement cell.
func (r *TraceReport) countKind(kind enforcementKind) {
	switch kind {
	case kindTest, kindCheck:
		r.Live++
	case kindPlanned:
		r.Planned++
	case kindJudgement:
		r.Judgement++
	case kindDecisionOnly:
		r.DecisionOnly++
	}
}

// cellAt returns cell i of the row; a row too short to hold it has an empty cell.
func cellAt(row tableRow, i int) string {
	if i >= len(row.cells) {
		return ""
	}
	return row.cells[i]
}

// ruleIDs returns the ids of the rule rows.
func ruleIDs(rows []tableRow) map[string]bool {
	ids := make(map[string]bool, len(rows))
	for _, row := range rows {
		ids[row.cells[0]] = true
	}
	return ids
}

// goSources holds the parsed Go files, by repository-relative path, on one file set.
type goSources struct {
	fset  *token.FileSet
	files map[string]*ast.File
}

// parseSources parses each Go file of repo once; a file that is not UTF-8 text or not Go is an
// error, never a file without markers (DR-6).
func parseSources(repo string) (goSources, error) {
	files, err := GoFiles(repo)
	if err != nil {
		return goSources{}, fmt.Errorf("trace: %w", err)
	}
	src := goSources{fset: token.NewFileSet(), files: make(map[string]*ast.File, len(files))}
	for _, f := range files {
		text, err := core.ReadText(f.Body)
		if err != nil {
			return goSources{}, fmt.Errorf("trace: %s: %w", f.Path, err)
		}
		file, err := parser.ParseFile(src.fset, f.Path, text, parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			return goSources{}, fmt.Errorf("trace: %s: %w", f.Path, err)
		}
		src.files[f.Path] = file
	}
	return src, nil
}

// declares reports whether the Go file internal/<file> declares the function name; an absent file
// is a test that does not exist.
func (s goSources) declares(file, name string) bool {
	f, ok := s.files["internal/"+file]
	if !ok {
		return false
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
			return true
		}
	}
	return false
}

// ruleMarker is one `// rule: <id>` marker: its repository-relative path, its 1-based line and its id.
type ruleMarker struct {
	path string
	line int
	id   string
}

// markers returns every `// rule: <id>` marker of the Go files; the Go parser reads the comments,
// so a marker quoted in a string literal is no marker.
func (s goSources) markers() []ruleMarker {
	var marks []ruleMarker
	for p, f := range s.files {
		for _, group := range f.Comments {
			for _, c := range group.List {
				if m := markerID(c.Text); m != nil {
					marks = append(marks, ruleMarker{path: p, line: s.fset.Position(c.Pos()).Line, id: m[1]})
				}
			}
		}
	}
	return marks
}

// cardsFile is the Go file of the shipped role cards (SPEC §9.1): an `ID:` key names a role, a
// `Gate:` key the id that bounds it.
const cardsFile = "internal/roles/cards.go"

// cardKeys is what the cards file holds: the role ids, and each Gate value as a marker to resolve.
type cardKeys struct {
	fset  *token.FileSet
	roles map[string]bool
	gates []ruleMarker
	err   error
}

// cards returns the keys of the cards file; a tree without it ships no role. A key whose value is
// no string literal is an error, never a card left unchecked (DR-6).
func (s goSources) cards() (cardKeys, error) {
	keys := cardKeys{fset: s.fset, roles: map[string]bool{}}
	if f, ok := s.files[cardsFile]; ok {
		ast.Inspect(f, keys.visit)
	}
	return keys, keys.err
}

// visit records one `ID:` or `Gate:` key of a card literal and walks on, until an error.
func (k *cardKeys) visit(n ast.Node) bool {
	kv, ok := n.(*ast.KeyValueExpr)
	if !ok || k.err != nil {
		return k.err == nil
	}
	key, ok := kv.Key.(*ast.Ident)
	if !ok || (key.Name != "ID" && key.Name != "Gate") {
		return true
	}
	line := k.fset.Position(kv.Pos()).Line
	value, err := stringLiteral(kv.Value)
	if err != nil {
		k.err = fmt.Errorf("trace: %s:%d: %s: %w", cardsFile, line, key.Name, err)
		return false
	}
	if key.Name == "ID" {
		k.roles[value] = true
	} else {
		k.gates = append(k.gates, ruleMarker{path: cardsFile, line: line, id: value})
	}
	return true
}

// stringLiteral returns the value of a Go string literal; any other expression is an error.
func stringLiteral(e ast.Expr) (string, error) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", errors.New("not a string literal; the card is unchecked")
	}
	return strconv.Unquote(lit.Value)
}

// markerID returns the id a `// rule: <id>` line comment names, or nil when the comment is no marker.
func markerID(comment string) []string {
	text, ok := strings.CutPrefix(comment, "//")
	if !ok {
		return nil
	}
	return ruleMarkerPattern.FindStringSubmatch(strings.TrimSpace(text))
}

// unknownMarkers returns one unknown_rule finding per marker that names no rule row of the registry.
func unknownMarkers(marks []ruleMarker, ids map[string]bool) []Finding {
	var found []Finding
	for _, m := range marks {
		if !ids[m.id] {
			found = append(found, Finding{Rule: ruleUnknownRule, Path: m.path, Line: m.line})
		}
	}
	return found
}
