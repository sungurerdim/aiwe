package repocheck

import "context"

// StagedDiff returns the staged diff of the repository at repo with no context lines; no git, or a
// directory that is not a repository, is an error.
func StagedDiff(ctx context.Context, repo string) (string, error) {
	out, err := runProgram(ctx, repo, "git", "-c", "core.quotePath=false", "diff", "--cached",
		"--no-color", "--no-ext-diff", "--no-renames", "--unified=0")
	return string(out), err
}
