package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// isBatchLauncher reports whether path is a batch file, which cmd.exe runs.
func isBatchLauncher(path string) bool {
	ext := filepath.Ext(path)
	return strings.EqualFold(ext, ".cmd") || strings.EqualFold(ext, ".bat")
}

// batchCommandLine builds the command line that runs script with args through shell (cmd.exe).
// Go quotes argv for CommandLineToArgvW, which cmd.exe does not follow (SPEC §5.20), so every
// word is put in double quotes, where cmd.exe takes & | < > ( ) ^ , ; = literally. An argument
// cmd.exe would still change inside quotes is refused. /d skips AutoRun commands, /v:OFF keeps !
// literal and /s strips exactly the outer pair of quotes.
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
// % expands a variable, and a line break or another control character ends or breaks the line.
func checkBatchWord(word string) error {
	for _, r := range word {
		if r == '"' || r == '%' || (unicode.IsControl(r) && r != '\t') {
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
