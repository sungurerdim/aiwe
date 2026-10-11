package gates

import (
	"slices"
	"strconv"
	"strings"
)

// withParallelism returns a copy of argv with every `-p` flag removed and one `-p=workers` put
// right after the `test` subcommand, or after the program when there is none.
func withParallelism(argv []string, workers int) []string {
	out := make([]string, 0, len(argv)+1)
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "-p" {
			i++ // the flag's value is the next argument
			continue
		}
		if strings.HasPrefix(a, "-p=") {
			continue
		}
		out = append(out, a)
	}
	at := min(1, len(out))
	if i := slices.Index(out, "test"); i >= 0 {
		at = i + 1
	}
	return slices.Insert(out, at, "-p="+strconv.Itoa(workers))
}
