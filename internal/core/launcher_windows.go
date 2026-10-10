package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// windowsStrippedTail is what Windows strips from the end of a file name before it opens it.
const windowsStrippedTail = ". "

// checkLauncherPath refuses a program path that ends in a dot or a space. Windows strips them, so
// "launch.cmd." runs as the batch file "launch.cmd" while its own extension says it is not one.
func checkLauncherPath(path string) error {
	if strings.TrimRight(path, windowsStrippedTail) != path {
		return fmt.Errorf("program %q ends in a dot or a space, which Windows strips: refused", path)
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

// checkBatchWord refuses what cmd.exe changes even inside quotes: a double quote ends the quotes,
// % expands a variable, ! expands one in a script that turns on delayed expansion, and a line
// break or another control character ends or breaks the line.
func checkBatchWord(word string) error {
	for _, r := range word {
		if r == '"' || r == '%' || r == '!' || (unicode.IsControl(r) && r != '\t') {
			return fmt.Errorf("batch launcher argument %q: %q cannot be passed through cmd.exe safely", word, r)
		}
	}
	return nil
}

// quoteBatchWord quotes word for cmd.exe and for the CommandLineToArgvW parse of the program the
// launcher starts: trailing backslashes are doubled, so they do not escape the closing quote.
func quoteBatchWord(word string) string {
	trailing := len(word) - len(strings.TrimRight(word, `\`))
	return `"` + word + strings.Repeat(`\`, trailing) + `"`
}
