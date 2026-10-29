package gates

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// withParallelism returns a copy of argv that runs with workers parallel packages (DR-3). Only a
// `go test` argv changes: every `-p`, `-p=`, `--p` and `--p=` before `-args` is removed and one
// `-p=workers` goes right after `test`. The test binary's own flags after `-args` stay as declared.
func withParallelism(argv []string, workers int) []string {
	if !isGoTest(argv) {
		return slices.Clone(argv)
	}
	end := slices.IndexFunc(argv, func(a string) bool { return a == "-args" || a == "--args" })
	if end < 0 {
		end = len(argv)
	}
	out := make([]string, 0, len(argv)+1)
	out = append(out, argv[0], argv[1], "-p="+strconv.Itoa(workers))
	for i := 2; i < end; i++ {
		switch a := argv[i]; {
		case a == "-p" || a == "--p":
			i++ // the flag's value is the next argument
		case strings.HasPrefix(a, "-p=") || strings.HasPrefix(a, "--p="):
		default:
			out = append(out, a)
		}
	}
	return append(out, argv[end:]...)
}

// isGoTest reports whether argv runs the go tool's test subcommand. The go tool may be named by a
// path; on Windows its file name may carry the .exe extension.
func isGoTest(argv []string) bool {
	if len(argv) < 2 || argv[1] != "test" {
		return false
	}
	name := filepath.Base(argv[0])
	return name == "go" || strings.EqualFold(name, "go.exe")
}

// withCoverage returns a copy of argv that measures coverage of every package of the module and
// writes the profile to profile (SPEC L66). Only a `go test` argv changes: every `-coverprofile`
// and `-coverpkg` before `-args` is removed and one of each goes right after `test`.
func withCoverage(argv []string, profile string) []string {
	if !isGoTest(argv) {
		return slices.Clone(argv)
	}
	end := argsEnd(argv)
	out := []string{argv[0], argv[1], "-coverpkg=./...", "-coverprofile=" + profile}
	for i := 2; i < end; i++ {
		name, _, hasValue := strings.Cut(strings.TrimLeft(argv[i], "-"), "=")
		if strings.HasPrefix(argv[i], "-") && (name == "coverprofile" || name == "coverpkg") {
			if !hasValue {
				i++ // the flag's value is the next argument
			}
			continue
		}
		out = append(out, argv[i])
	}
	return append(out, argv[end:]...)
}

// isFullGoTest reports whether argv runs `go test` over every package of the module.
func isFullGoTest(argv []string) bool {
	return isGoTest(argv) && slices.Contains(argv[:argsEnd(argv)], "./...")
}

// argsEnd returns the index of the `-args` that ends the go tool's own flags, or len(argv).
func argsEnd(argv []string) int {
	end := slices.IndexFunc(argv, func(a string) bool { return a == "-args" || a == "--args" })
	if end < 0 {
		return len(argv)
	}
	return end
}
