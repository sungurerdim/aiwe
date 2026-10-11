package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// resolveLauncher returns the absolute path of the file that program names in dir, and refuses a
// relative dir, a name Windows would strip, a name that resolves to no file and a file outside
// PATHEXT.
func resolveLauncher(program, dir string) (string, error) {
	if dir != "" && !filepath.IsAbs(dir) {
		return "", fmt.Errorf("work directory %q is relative: refused", dir)
	}
	if err := checkLauncherPath(program); err != nil {
		return "", err
	}
	path, err := exec.LookPath(launcherCandidate(program, dir))
	if err == nil && !filepath.IsAbs(path) {
		path, err = filepath.Abs(path) // still relative only to the current directory or drive
	}
	if err != nil {
		return "", fmt.Errorf("resolve program %q: %w", program, err)
	}
	if err := checkLauncherPath(path); err != nil { // the batch decision is made on this path
		return "", err
	}
	return path, checkPathExt(path)
}

// checkPathExt refuses a program whose extension is not in PATHEXT, read as Start reads it.
// CreateProcess runs an executable image whatever its extension, and the shell opens a file
// outside PATHEXT through its file association. An entry with no name, such as ".", accepts no
// extension: a path whose extension is empty or "." names no program type.
func checkPathExt(path string) error {
	exts := LookupEnv("PATHEXT")
	if exts == "" {
		exts = ".com;.exe;.bat;.cmd" // the list Start uses when PATHEXT is unset
	}
	ext := filepath.Ext(path)
	for e := range strings.SplitSeq(exts, ";") {
		if name := strings.TrimPrefix(e, "."); name != "" && strings.EqualFold(ext, "."+name) {
			return nil
		}
	}
	return fmt.Errorf("program %q: extension %q is not in PATHEXT %q: refused", path, ext, exts)
}

// launcherCandidate resolves program path against dir as Start does (syscall joinExeDirAndFName),
// so it names the file Start would run. A relative path joins dir. A path with a root and no
// drive (`\x`) takes dir's drive. A path with dir's drive and no root (`X:x`) joins dir. A path
// with a drive and a root, a UNC path and a path on another drive are used as given.
func launcherCandidate(path, dir string) string {
	if dir == "" || path == "" {
		return path
	}
	vol := filepath.VolumeName(path)
	rooted := len(path) > len(vol) && os.IsPathSeparator(path[len(vol)])
	switch {
	case vol == "" && rooted:
		return filepath.VolumeName(dir) + path
	case vol == "":
		return filepath.Join(dir, path)
	case !rooted && len(vol) == 2 && strings.EqualFold(vol, filepath.VolumeName(dir)):
		return filepath.Join(dir, path[len(vol):])
	}
	return path
}

// commandShell returns the path of cmd.exe in the system directory, never one found on PATH.
func (k kernel32) commandShell() (string, error) {
	var buf [systemDirectoryMaxLength]uint16
	n, _, callErr := k.getSystemDirectory.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || n >= uintptr(len(buf)) {
		return "", fmt.Errorf("find the system directory: %w", callErr)
	}
	return filepath.Join(syscall.UTF16ToString(buf[:n]), "cmd.exe"), nil
}

// windowsStrippedTail is what Windows strips from the end of a file name before it opens it.
const windowsStrippedTail = ". "

// checkLauncherPath refuses a program path whose last element is not the file Windows opens. It
// strips a trailing dot or space, so "launch.cmd." runs as the batch file "launch.cmd" while its
// own extension says it is not one. A trailing separator or a "." or ".." element resolves away
// too: LookPath returns `run.cmd\.` for `run.cmd\` and `run.cmd\x\..\`, whose extension is ".".
func checkLauncherPath(path string) error {
	if strings.TrimRight(path, windowsStrippedTail) != path {
		return fmt.Errorf("program %q ends in a dot or a space, which Windows strips: refused", path)
	}
	rest := path[len(filepath.VolumeName(path)):]
	if rest != "" && os.IsPathSeparator(rest[len(rest)-1]) {
		return fmt.Errorf("program %q ends in a path separator: refused", path)
	}
	for _, elem := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		if elem == "." || elem == ".." {
			return fmt.Errorf("program %q holds a %q element: refused", path, elem)
		}
	}
	return nil
}

// isBatchLauncher reports whether path is a batch file, which cmd.exe runs. It reads the extension
// as Windows does: after the trailing dots and spaces, in any letter case.
func isBatchLauncher(path string) bool {
	ext := filepath.Ext(strings.TrimRight(path, windowsStrippedTail))
	return strings.EqualFold(ext, ".cmd") || strings.EqualFold(ext, ".bat")
}

// batchCommandLine builds the command line that runs script with args through shell (cmd.exe).
// Go quotes argv for CommandLineToArgvW, which cmd.exe does not follow (SPEC §5.20), so every
// word is put in double quotes, where cmd.exe takes & | < > ( ) ^ , ; = literally. An argument
// cmd.exe would still change inside quotes is refused. /d skips AutoRun commands, /v:OFF keeps !
// literal on this command line and /s strips exactly the outer pair of quotes.
func batchCommandLine(shell, script string, args []string) (string, error) {
	var b strings.Builder
	b.WriteString(`"` + shell + `" /d /e:ON /v:OFF /s /c "`)
	for i, word := range append([]string{script}, args...) {
		if err := checkBatchWord(word); err != nil {
			return "", err
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(quoteBatchWord(word))
	}
	b.WriteByte('"')
	return b.String(), nil
}

// batchBestFit holds the characters that a Windows ANSI code page (874, 932, 936, 949, 950,
// 1250-1258) maps to " % or ! by best fit, measured with WideCharToMultiByte; a program that reads
// its command line as ANSI sees them as the characters checkBatchWord refuses.
const batchBestFit = "\u02ba\u030e\u201e\u201f\u2033\u2036\u301d\u301e\uff02" + // to "
	"\u066a\uff05" + // to %
	"\u00a1\u01c3\u203c\uff01" // to !

// batchBestFitBackslash holds the characters that a Windows ANSI code page maps to \ by best fit.
// quoteBatchWord doubles only real trailing backslashes, so one of these at the end of a word, or
// just before its trailing backslashes, escapes the closing quote for a program that reads ANSI.
const batchBestFitBackslash = "\uff3c\u2216\u00a5\u20a9"

// checkBatchWord refuses what cmd.exe changes even inside quotes: a double quote ends the quotes,
// % expands a variable, ! expands one in a script that turns on delayed expansion, and a line
// break or another control character ends or breaks the line. A best-fit form of " % or ! is
// refused too, and so is a best-fit form of \ that quoteBatchWord cannot keep off the closing quote.
func checkBatchWord(word string) error {
	for _, r := range word {
		if strings.ContainsRune(`"%!`+batchBestFit, r) || (unicode.IsControl(r) && r != '\t') {
			return fmt.Errorf("batch launcher argument %q: %q cannot be passed through cmd.exe safely", word, r)
		}
	}
	last, _ := utf8.DecodeLastRuneInString(strings.TrimRight(word, `\`))
	if strings.ContainsRune(batchBestFitBackslash, last) {
		return fmt.Errorf("batch launcher argument %q: %q before the closing quote reads as \\ in an ANSI program: refused",
			word, last)
	}
	return nil
}

// quoteBatchWord quotes word for cmd.exe and for the CommandLineToArgvW parse of the program the
// launcher starts: trailing backslashes are doubled, so they do not escape the closing quote.
func quoteBatchWord(word string) string {
	trailing := len(word) - len(strings.TrimRight(word, `\`))
	return `"` + word + strings.Repeat(`\`, trailing) + `"`
}
