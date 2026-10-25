package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// statusFile is the one record file that keeps hand-written text beside its generated section.
	statusFile  = "STATUS.md"
	autoStart   = "<!-- auto:start -->"
	autoEnd     = "<!-- auto:end -->"
	statusTitle = "# Status\n\n"
	// statusSeed is what a new STATUS.md gets after its auto section: the hand-kept headings, empty.
	statusSeed = "\n## Broken\n\n## Live configuration\n"
	// statusSource names the sources in the marker line of the auto section.
	statusSource = "git, gh issue list and gh run list"
	// shaWidth is how many characters of a commit sha the status shows.
	shaWidth = 12
	// ciWorkflow is the workflow file whose last run on main is the CI result.
	ciWorkflow = "ci.yml"
)

// status builds STATUS.md: the auto section is regenerated, and the text around it is kept.
func (s *session) status(ctx context.Context) (string, error) {
	lines, err := s.statusLines(ctx)
	if err != nil {
		return "", err
	}
	auto := autoStart + "\n" + header(s.d.now(), statusSource) + strings.Join(lines, "\n") + "\n" + autoEnd
	old, exists, err := s.d.read(statusFile)
	if err != nil {
		return "", fmt.Errorf("%s: %w", statusFile, err)
	}
	if !exists {
		return statusTitle + auto + "\n" + statusSeed, nil
	}
	return replaceAuto(string(old), auto)
}

// replaceAuto swaps the text between the auto markers, markers included, and keeps the rest byte
// for byte. A file without exactly one ordered pair of markers is refused rather than overwritten.
func replaceAuto(old, auto string) (string, error) {
	start, end := strings.Index(old, autoStart), strings.Index(old, autoEnd)
	if start < 0 || end < start || strings.Count(old, autoStart) != 1 || strings.Count(old, autoEnd) != 1 {
		return "", fmt.Errorf("%s needs exactly one %s line followed by one %s line; the file was left as it is",
			statusFile, autoStart, autoEnd)
	}
	return old[:start] + auto + old[end+len(autoEnd):], nil
}

// statusLines returns the measured lines of the auto section.
func (s *session) statusLines(ctx context.Context) ([]string, error) {
	sha, err := s.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	subject, err := s.git(ctx, "log", "-1", "--format=%s")
	if err != nil {
		return nil, err
	}
	branch, err := s.git(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, err
	}
	open, err := s.issues(ctx, "open")
	if err != nil {
		return nil, err
	}
	closed, err := s.issues(ctx, "closed")
	if err != nil {
		return nil, err
	}
	ci, err := s.ciLine(ctx)
	if err != nil {
		return nil, err
	}
	return []string{
		fmt.Sprintf("- HEAD: %s %s", short(sha), subject),
		fmt.Sprintf("- Branch: %s, %s", branch, s.divergence(ctx)),
		fmt.Sprintf("- Issues: %d open, %d closed", len(open), len(closed)),
		ci,
	}, nil
}

// git returns the trimmed output of one git call.
func (s *session) git(ctx context.Context, args ...string) (string, error) {
	out, err := s.call(ctx, "git", args...)
	return strings.TrimSpace(string(out)), err
}

// divergence says how far HEAD is from its upstream; with no upstream it says unknown, never 0.
func (s *session) divergence(ctx context.Context) string {
	out, err := s.git(ctx, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	var behind, ahead int
	if err != nil {
		return "ahead/behind unknown (no upstream)"
	}
	if n, _ := fmt.Sscan(out, &behind, &ahead); n != 2 {
		return "ahead/behind unknown (unreadable)"
	}
	return fmt.Sprintf("ahead %d, behind %d of the upstream", ahead, behind)
}

// ciLine reports the last CI run on main.
func (s *session) ciLine(ctx context.Context) (string, error) {
	out, err := s.call(ctx, "gh", "run", "list", "--branch", "main", "--workflow", ciWorkflow, "--limit", "1",
		"--json", "conclusion,status,workflowName,createdAt,headSha")
	if err != nil {
		return "", err
	}
	var runs []struct {
		Conclusion   string `json:"conclusion"`
		Status       string `json:"status"`
		WorkflowName string `json:"workflowName"`
		CreatedAt    string `json:"createdAt"`
		HeadSha      string `json:"headSha"`
	}
	if err := json.Unmarshal(out, &runs); err != nil {
		return "", fmt.Errorf("gh run list: read the JSON: %w", err)
	}
	if len(runs) == 0 {
		return "- CI (main): no run found", nil
	}
	r := runs[0]
	result := r.Conclusion
	if result == "" {
		result = r.Status
	}
	return fmt.Sprintf("- CI (main, workflow %s): %s, %s, sha %s", r.WorkflowName, result, r.CreatedAt, short(r.HeadSha)), nil
}

// short cuts a commit sha for display.
func short(sha string) string {
	if len(sha) > shaWidth {
		return sha[:shaWidth]
	}
	return sha
}
