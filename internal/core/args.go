package core

import (
	"fmt"
	"path/filepath"
)

// ResolvePath resolves the path parameter p against repo, the value of --repo (SPEC §5.12), which
// the CLI boundary has already made absolute (resolveRepo). An absolute p is kept; a relative p
// needs an absolute repo; ResolvePath never uses the working directory.
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

// resolveRepo makes the --repo value in in absolute, once, at the CLI boundary (SPEC §5.12). It is
// the one place the working directory is used: a relative --repo resolves against it, an absolute
// one is cleaned, and an empty one is a usage error. Every other parameter is left as given.
func resolveRepo(in Input) error {
	repo, ok := in.Params["repo"]
	if !ok {
		return nil
	}
	if repo == "" {
		return &Error{Outcome: OutcomeUsage, Expected: "a repository path for --repo", Found: "an empty value",
			Next: "pass --repo <path>, for example --repo ."}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &Error{Outcome: OutcomeUsage, Expected: "a --repo that resolves against the working directory",
			Found: fmt.Sprintf("%q: %v", repo, err), Next: "pass --repo as an absolute path", Err: err}
	}
	in.Params["repo"] = abs
	return nil
}
