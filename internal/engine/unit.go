// Package engine holds the unit flows, starting with the intake parser of the §8.4 issue body.
package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// MaxUnitBodySize caps the issue body ParseUnit reads (SPEC §5.13).
const MaxUnitBodySize = 64 << 10

// maxScopeSources is the ceiling DR-7 puts on the source files of one file set; a test, lock or
// generated file and `go.mod`/`go.sum` do not count against it (SPEC §8.4).
const maxScopeSources = 5

// The sentinel failures of ParseUnit, which errors.Is sees (SPEC §5.3).
var (
	ErrUnitSectionMissing = errors.New("unit: required section missing")
	ErrUnitBodyTooLarge   = errors.New("unit: body too large")
	ErrUnitPathUnsafe     = errors.New("unit: scope path outside the repository")
	ErrUnitVerifyUnsafe   = errors.New("unit: verify command would run a shell")
	ErrUnitScopeTooWide   = errors.New("unit: scope holds too many source files")
	// ErrUnitVerifyUnresolved is the refusal of a verify whose first token is no plain program name
	// that can resolve on PATH, such as the path-qualified `./missing.sh` (SPEC §8.4).
	ErrUnitVerifyUnresolved = errors.New("unit: verify command does not resolve")
)

// MissingSectionError names the §8.4 section a body must carry but does not.
type MissingSectionError struct {
	Section string
}

// Error renders the missing section.
func (e *MissingSectionError) Error() string {
	return fmt.Sprintf("unit: section %q is missing", e.Section)
}

// Unwrap returns ErrUnitSectionMissing, so errors.Is sees the sentinel (SPEC §5.3).
func (e *MissingSectionError) Unwrap() error { return ErrUnitSectionMissing }

// UnsafePathError names the Scope In entry a unit may not change: one that is no plain repository
// path, one outside the clone, or one under the repository's control data (SPEC §8.4).
type UnsafePathError struct {
	Path string
}

// Error renders the refusing path.
func (e *UnsafePathError) Error() string {
	return fmt.Sprintf("unit: scope path %q is not a safe unit path", e.Path)
}

// Unwrap returns ErrUnitPathUnsafe, so errors.Is sees the sentinel (SPEC §5.3).
func (e *UnsafePathError) Unwrap() error { return ErrUnitPathUnsafe }

// Unit is a parsed unit issue: the file set of Scope In, the verify commands and the test items.
type Unit struct {
	Files  []string
	Verify []string
	Tests  []string
}

// ParseUnit reads a unit issue body: the Scope In file set, the Verification commands and the To
// do test items; every sentinel above and a MissingSectionError name its refusals (§5.13, §8.4).
func ParseUnit(body string) (Unit, error) {
	if len(body) > MaxUnitBodySize {
		return Unit{}, fmt.Errorf("%w: %d bytes", ErrUnitBodyTooLarge, len(body))
	}
	sections := unitSections(body)
	files, err := scopeFiles(sections["Scope"])
	if err != nil {
		return Unit{}, err
	}
	verify, err := verificationCommands(sections["Verification"])
	if err != nil {
		return Unit{}, err
	}
	tests, err := toDoTests(sections["To do"], files)
	if err != nil {
		return Unit{}, err
	}
	switch {
	case len(files) == 0:
		return Unit{}, &MissingSectionError{Section: "Scope"}
	case len(verify) == 0:
		return Unit{}, &MissingSectionError{Section: "Verification"}
	case len(tests) == 0:
		return Unit{}, &MissingSectionError{Section: "To do"}
	}
	return Unit{Files: files, Verify: verify, Tests: tests}, nil
}

// unitSections maps each `## <name>` heading of body to the lines under it, up to the next heading.
func unitSections(body string) map[string][]string {
	sections := map[string][]string{}
	name := ""
	for _, line := range strings.Split(body, "\n") {
		head := strings.TrimRight(line, "\r")
		if !strings.HasPrefix(head, "## ") {
			if name != "" {
				sections[name] = append(sections[name], line)
			}
			continue
		}
		name = strings.TrimSpace(head[3:])
	}
	return sections
}

// scopeFiles returns the paths of the In: list of a Scope section and ignores the Out: list and
// everything after it; an unsafe entry and a set over the DR-7 ceiling are refused, named (§8.4).
func scopeFiles(lines []string) ([]string, error) {
	var files []string
	in, sources := false, 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "In:" || trimmed == "Out:" {
			in = trimmed == "In:"
			continue
		}
		if !in {
			continue
		}
		paths, err := scopeLineFiles(line)
		if err != nil {
			return nil, err
		}
		sources += sourceCount(paths)
		files = append(files, paths...)
	}
	if sources > maxScopeSources {
		return nil, fmt.Errorf("%w: %d source files, the ceiling is %d",
			ErrUnitScopeTooWide, sources, maxScopeSources)
	}
	return files, nil
}

// sourceCount counts the paths of one Scope In line that count against the DR-7 ceiling (SPEC §8.4).
func sourceCount(paths []string) (n int) {
	for _, path := range paths {
		if countsAsSource(path) {
			n++
		}
	}
	return
}

// countsAsSource reports whether one file-set path counts against the ceiling of DR-7: a test, lock
// or generated file and `go.mod`/`go.sum` are no source file of the unit (SPEC §8.4).
func countsAsSource(path string) bool {
	base := path[strings.LastIndex(path, "/")+1:]
	if base == "go.mod" || base == "go.sum" || strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, "_generated.go") || strings.HasSuffix(base, ".pb.go") {
		return false
	}
	switch lower := strings.ToLower(base); lower {
	case "npm-shrinkwrap.json", "packages.lock.json", "gradle.lockfile":
		return false
	default:
		return !strings.HasSuffix(lower, ".lock") && !strings.HasSuffix(lower, ".lockb") &&
			!strings.HasSuffix(lower, ".sum") && !strings.HasSuffix(lower, "-lock.json")
	}
}

// scopeLineFiles returns the paths one Scope In line names: its backticked spans when it carries
// any, else one whitespace-free `- ` token that looks like a repository path; prose and `none` name none (§8.4).
func scopeLineFiles(line string) ([]string, error) {
	if paths := backticked(line); len(paths) > 0 {
		return filePaths(paths)
	}
	item, ok := strings.CutPrefix(strings.TrimSpace(line), "- ")
	if !ok {
		return nil, nil
	}
	item = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(item), "(new)"))
	if item == "" || strings.ContainsAny(item, " \t") || !strings.ContainsAny(item, `/\\.`) {
		return nil, nil
	}
	return filePaths([]string{item})
}

// filePaths returns the paths of the Scope In entries: each entry with its `(new)` marker dropped,
// refused with its name when what stays is no plain repository path (SPEC §8.4).
func filePaths(entries []string) ([]string, error) {
	var out []string
	for _, entry := range entries {
		path := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(entry), "(new)"))
		if path == "" || strings.ContainsAny(path, " \t`*?[]{}()<>|;&$!'\"#") || pathOutside(path) {
			return nil, &UnsafePathError{Path: strings.TrimSpace(entry)}
		}
		out = append(out, path)
	}
	return out, nil
}

// pathOutside reports whether path is no repository path a unit may change: absolute or Windows
// separated, holding `..`, or under `.git`/`.githooks`, case-blind, trailing dots stripped (§8.4).
func pathOutside(path string) bool {
	if strings.HasPrefix(path, "/") || strings.Contains(path, `\`) || (len(path) > 1 && path[1] == ':') {
		return true
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part = strings.TrimRight(part, ". "); strings.EqualFold(part, ".git") || strings.EqualFold(part, ".githooks") {
			return true
		}
	}
	return slices.Contains(parts, "..")
}

// verificationCommands returns the commands of a Verification section; a line is either a backticked
// `<command>` → <signal> or a plain <command> → <signal>. A command that would run a
// shell, counts from HEAD or starts with no plain program name is refused, named (SPEC §8.4).
func verificationCommands(lines []string) ([]string, error) {
	var cmds []string
	for _, line := range lines {
		cmd, ok := verificationCommand(line)
		if !ok {
			continue
		}
		if shellOperator(cmd) || countsFromHead(cmd) {
			return nil, fmt.Errorf("%w: %q", ErrUnitVerifyUnsafe, cmd)
		}
		if !resolvableFirstToken(cmd) {
			return nil, fmt.Errorf("%w: %q", ErrUnitVerifyUnresolved, cmd)
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

// resolvableFirstToken reports whether the first token of command is a plain program name: the
// engine runs the verify as argv without a shell, so its first token is the program to run. A
// path-qualified, dot-leading or flag-leading token names a file or no program (SPEC §8.4).
func resolvableFirstToken(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return !strings.ContainsAny(fields[0], `/\`) && !strings.HasPrefix(fields[0], "-") &&
		!strings.HasPrefix(fields[0], ".")
}

// verificationCommand returns the command of one Verification line: the backticked span before the
// arrow, or the plain text before it; the span after `→` is the signal, never an argument the
// engine runs, and a list item is never a verify line, backticked or not (SPEC §8.4).
func verificationCommand(line string) (string, bool) {
	if strings.HasPrefix(strings.TrimSpace(line), "- ") {
		return "", false
	}
	head, _, arrow := strings.Cut(line, "→")
	if quoted := backticked(head); len(quoted) > 0 {
		return strings.Join(quoted, " "), true
	}
	if head = strings.TrimSpace(head); head == "" {
		return "", false
	}
	return head, arrow
}

// shellOperator reports whether line carries a shell operator outside quotes: any of `&&`, `||`,
// `|`, `;`, `>`, `<`, `$(` or a backtick (SPEC §8.4).
func shellOperator(line string) bool {
	var quote rune
	for i, r := range line {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if isShellOperator(line, i, r) {
			return true
		}
	}
	return false
}

// isShellOperator reports whether r, the rune of line at byte i, is an unquoted shell operator.
func isShellOperator(line string, i int, r rune) bool {
	return r == '`' || strings.HasPrefix(line[i:], "$(") || strings.ContainsRune("&|;<>", r)
}

// countsFromHead reports whether command counts from the base commit with a `HEAD~<n>` or `HEAD^`
// base, which DR-9 refuses (SPEC §8.4).
func countsFromHead(command string) bool {
	for _, token := range strings.Fields(command) {
		if strings.HasPrefix(token, "HEAD~") || strings.HasPrefix(token, "HEAD^") {
			return true
		}
	}
	return false
}

// toDoTests returns the text of each To do item whose check runs or names a test of the unit's
// file set; a line that is no `task — verify: <check> → <signal>` item is refused, named, as is a
// body whose list holds no item at all (SPEC §8.4).
func toDoTests(lines []string, files []string) ([]string, error) {
	var tests []string
	for _, line := range lines {
		item := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if item == "" {
			continue
		}
		if !completeToDoItem(item) {
			return nil, fmt.Errorf("unit: To do item %q is not `task — verify: <check> → <signal>`", item)
		}
		if isTestItem(item, files) {
			tests = append(tests, item)
		}
	}
	return tests, nil
}

// completeToDoItem reports whether one To do item follows the standard: `task — verify: <check> →
// <signal>` carries the task, the check and the signal, none of the three empty (§8.4).
func completeToDoItem(item string) bool {
	task, rest, ok := strings.Cut(item, "verify:")
	if !ok || strings.TrimSpace(task) == "" {
		return false
	}
	check, signal, ok := strings.Cut(rest, "→")
	return ok && strings.TrimSpace(check) != "" && strings.TrimSpace(signal) != ""
}

// isTestItem reports whether one To do item's check is a test of the unit's file set: a check that
// names test files counts when the set lists them, and a `go test` run counts when one of its
// arguments holds a file of the set; the caller passes an item completeToDoItem accepted (SPEC §8.4).
func isTestItem(item string, files []string) bool {
	_, rest, _ := strings.Cut(item, "verify:")
	check, _, _ := strings.Cut(rest, "→")
	var named []string
	for _, token := range strings.Fields(check) {
		if token = strings.Trim(token, "`\"'(),;:[]."); strings.HasSuffix(token, "_test.go") {
			named = append(named, token)
		}
	}
	for _, name := range named {
		if !slices.Contains(files, name) {
			return false
		}
	}
	return len(named) > 0 || coversFileSet(check, files)
}

// coversFileSet reports whether a check runs `go test` over a package or directory that holds a
// file of the unit's file set; a check without a `go test` run covers nothing (SPEC §8.4).
func coversFileSet(check string, files []string) bool {
	_, rest, ok := strings.Cut(check, "go test")
	if !ok {
		return false
	}
	for _, token := range strings.Fields(rest) {
		token = strings.Trim(token, "`\"'(),;:[]")
		if token == "" || strings.HasPrefix(token, "-") {
			continue
		}
		if targetCovers(token, files) {
			return true
		}
	}
	return false
}

// targetCovers reports whether one test target names a file of the file set: a `_test.go` target
// must be in the set, and any other target is a directory, so the set must hold a file under it (§8.4).
func targetCovers(target string, files []string) bool {
	if strings.HasSuffix(target, "_test.go") {
		return slices.Contains(files, target)
	}
	dir := strings.TrimPrefix(target, "./")
	dir = strings.TrimSuffix(strings.TrimSuffix(dir, "/..."), "/")
	if dir == "." || dir == "..." {
		dir = ""
	}
	for _, file := range files {
		if dir == "" || strings.HasPrefix(file, dir+"/") {
			return true
		}
	}
	return false
}

// backticked returns the text of each backticked span of line, in order.
func backticked(line string) []string {
	parts := strings.Split(line, "`")
	var out []string
	for i := 1; i < len(parts); i += 2 {
		if token := strings.TrimSpace(parts[i]); token != "" {
			out = append(out, token)
		}
	}
	return out
}
