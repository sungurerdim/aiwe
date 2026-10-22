package engine

import (
	"github.com/sungurerdim/aiwe/internal/core"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

func CheckReturn(text string, tip map[string]string) error {
	declared, err := declaredNames(tip)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(text, "\n") {
		if err := checkReturnLine(line, tip, declared); err != nil {
			return err
		}
	}
	return nil
}
func checkReturnLine(line string, tip map[string]string, declared map[string]bool) error {
	if token := badPath(line, tip); token != "" {
		return unverified("a path:line that is on the tip", token)
	}
	if name := badIdent(line, declared); name != "" {
		return unverified("an identifier the tip declares", name)
	}
	if reason, number := badNumber(line); number != "" {
		return unverified(reason, number)
	}
	return nil
}
func badPath(line string, tip map[string]string) string {
	for _, loc := range returnPathPattern.FindAllStringSubmatchIndex(line, -1) {
		token := line[loc[2]:loc[3]]
		if pathMisses(tip, token) {
			return token
		}
	}
	return ""
}
func pathMisses(tip map[string]string, token string) bool {
	path, num, _ := strings.Cut(token, ":")
	n, err := strconv.Atoi(num)
	src, found := tip[path]
	if err != nil || !found {
		return true
	}
	return n < 1 || n > tipLines(src)
}
func tipLines(s string) int {
	n := strings.Count(s, "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
func badIdent(line string, declared map[string]bool) string {
	masked := blankSpans(line)
	for _, loc := range returnIdentPattern.FindAllStringSubmatchIndex(masked, -1) {
		name := masked[loc[2]:loc[3]]
		if identSkipped(masked, loc[3], name) || declared[name] {
			continue
		}
		return name
	}
	return ""
}
func identSkipped(line string, end int, name string) bool {
	if j := skipSpace(line, end); allCaps(name) && j < len(line) && line[j] == ':' {
		return true
	}
	if returnWord(name) || sentenceWord(line, end-len(name), name) {
		return true
	}
	const mark = "VERDICT:"
	i := strings.Index(line, mark)
	return allCaps(name) && i >= 0 && end-len(name) == skipSpace(line, i+len(mark))
}
func identByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
func allCaps(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] >= 'a' && name[i] <= 'z' {
			return false
		}
	}
	return true
}

// returnWords returns the words of the SPEC §9.3 return format and the auditor's RETURN section, and
// the name SPEC: a return writes them, and none is a name the tip declares.
func returnWords() []string {
	return []string{"PLAN", "TESTS", "IMPLEMENTED", "UPDATED", "ANSWER", "ROOT", "CAUSE", "VERDICT",
		"GREEN", "RED", "PASS", "FAIL", "RunID", "SPEC"}
}
func returnWord(name string) bool {
	for _, w := range returnWords() {
		if name == w {
			return true
		}
	}
	return false
}

// sentenceWord reports a plain capitalised word that opens a line or a sentence, such as Done, Not
// or Scope: prose, not a name. A capitalised name mid-sentence or after a label is still checked.
func sentenceWord(line string, start int, name string) bool {
	if len(name) < 2 || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		if name[i] < 'a' || name[i] > 'z' {
			return false
		}
	}
	before := strings.TrimRight(line[:start], " \t")
	before = strings.TrimRight(strings.TrimLeft(before, " \t-*"), " \t")
	return before == "" || strings.HasSuffix(before, ".") || strings.HasSuffix(before, "!") ||
		strings.HasSuffix(before, "?")
}

func blankSpans(line string) string {
	b := []byte(line)
	for _, pattern := range []*regexp.Regexp{returnCommandPattern, returnRefPattern} {
		for _, loc := range pattern.FindAllStringIndex(line, -1) {
			for i := loc[0]; i < loc[1]; i++ {
				b[i] = ' '
			}
		}
	}
	for _, loc := range returnPathPattern.FindAllStringSubmatchIndex(line, -1) {
		for i := loc[2]; i < loc[3]; i++ {
			b[i] = ' '
		}
	}
	return string(b)
}
func badNumber(line string) (reason, number string) {
	masked := blankSpans(line)
	hasCmd := commandOn(line)
	for from := 0; from < len(masked); {
		start, end, ok := nextDigits(masked, from)
		if !ok {
			return "", ""
		}
		reason, number, next, hit := judgeDigits(masked, start, end, hasCmd)
		if hit {
			return reason, number
		}
		from = next
	}
	return "", ""
}
func nextDigits(s string, from int) (start, end int, ok bool) {
	i := from
	for i < len(s) && !isDigit(s[i]) {
		i++
	}
	start = i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return start, i, start < len(s)
}
func judgeDigits(s string, start, end int, hasCmd bool) (reason, number string, next int, hit bool) {
	if numberGlued(s, start, end) {
		return "", "", end, false
	}
	digits := s[start:end]
	if end < len(s) && s[end] == '%' {
		return "a ratio that carries its denominator", digits, end, true
	}
	if stop, ok := ratioEnd(s, end); ok {
		if hasCmd {
			return "", "", stop, false
		}
		return "a number from the command that produced it", digits, end, true
	}
	if hasCmd {
		return "", "", end, false
	}
	return "a number from the command that produced it", digits, end, true
}
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func numberGlued(s string, start, end int) bool {
	return (start > 0 && identByte(s[start-1])) || (end < len(s) && identByte(s[end]))
}
func ratioEnd(s string, i int) (int, bool) {
	j, ok := ofAt(s, i)
	if !ok {
		return 0, false
	}
	k := skipSpace(s, j)
	if k == j || k >= len(s) || !isDigit(s[k]) {
		return 0, false
	}
	for k < len(s) && isDigit(s[k]) {
		k++
	}
	if k < len(s) && identByte(s[k]) {
		return 0, false
	}
	return k, true
}
func ofAt(s string, i int) (int, bool) {
	j := skipSpace(s, i)
	end := j + len("of")
	if j == i || end > len(s) || s[j:end] != "of" || (end < len(s) && identByte(s[end])) {
		return 0, false
	}
	return end, true
}
func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}
func commandOn(line string) bool {
	for _, quoted := range returnCommandPattern.FindAllString(line, -1) {
		if strings.Trim(quoted, "` \t") != "" {
			return true
		}
	}
	return false
}
func declaredNames(tip map[string]string) (map[string]bool, error) {
	names := make(map[string]bool)
	for path, src := range tip {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		if err := collectDecls(names, path, src); err != nil {
			return nil, err
		}
	}
	return names, nil
}
func collectDecls(names map[string]bool, path, src string) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return unverified("a tip file that parses", path+": "+err.Error())
	}
	ast.Inspect(file, func(n ast.Node) bool { return takeDecl(names, n) })
	return nil
}
func takeDecl(names map[string]bool, n ast.Node) bool {
	switch x := n.(type) {
	case *ast.FuncDecl:
		noteName(names, x.Name)
		return false
	case *ast.TypeSpec:
		noteName(names, x.Name)
	case *ast.ValueSpec:
		for _, id := range x.Names {
			noteName(names, id)
		}
	case *ast.InterfaceType:
		noteFields(names, x.Methods)
		return false
	case *ast.StructType:
		noteFields(names, x.Fields)
	}
	return true
}
func noteFields(names map[string]bool, list *ast.FieldList) {
	if list == nil {
		return
	}
	for _, f := range list.List {
		for _, id := range f.Names {
			noteName(names, id)
		}
	}
}
func noteName(names map[string]bool, id *ast.Ident) {
	if id != nil {
		names[id.Name] = true
	}
}
func unverified(expected, found string) error {
	return &core.Error{
		Outcome:  "unverified_finding",
		Expected: expected,
		Found:    found,
		Next:     "cite only a path, an identifier and a number the tip and its command resolve",
	}
}
