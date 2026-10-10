package core

import (
	"fmt"
	"path/filepath"
)

// ResolvePath resolves the path parameter p against repo, the value of --repo (SPEC §5.12). An
// absolute p is kept; a relative p needs an absolute repo; the working directory is never used.
func ResolvePath(repo, p string) (string, error) {
	if p == "" {
		return "", &Error{Outcome: OutcomeUsage, Expected: "a path", Found: "an empty value",
			Next: "pass a path relative to --repo"}
	}
	if repo != "" && !filepath.IsAbs(repo) {
		return "", &Error{Outcome: OutcomeUsage, Expected: "an absolute --repo",
			Found: fmt.Sprintf("%q", repo), Next: "pass --repo as an absolute path"}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	if repo == "" {
		return "", &Error{Outcome: OutcomeUsage, Expected: "--repo for the relative path " + fmt.Sprintf("%q", p),
			Found: "no --repo", Next: "pass --repo"}
	}
	return filepath.Join(repo, p), nil
}
