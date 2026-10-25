package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// archiveSource names the source in the first line of ARCHIVE.md.
const archiveSource = "gh issue list --state closed"

// archive builds ARCHIVE.md: one line per closed issue, newest close first.
func (s *session) archive(ctx context.Context) (string, error) {
	closed, err := s.issues(ctx, "closed")
	if err != nil {
		return "", err
	}
	type entry struct {
		at time.Time
		is issue
	}
	entries := make([]entry, 0, len(closed))
	for _, is := range closed {
		at, err := time.Parse(time.RFC3339, is.ClosedAt)
		if err != nil {
			return "", fmt.Errorf("gh issue list --state closed: issue #%d has closedAt %q", is.Number, is.ClosedAt)
		}
		entries = append(entries, entry{at: at.UTC(), is: is})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if c := b.at.Compare(a.at); c != 0 {
			return c
		}
		return b.is.Number - a.is.Number
	})
	var b strings.Builder
	b.WriteString(header(s.d.now(), archiveSource))
	fmt.Fprintf(&b, "# ARCHIVE\n\n%d closed issues, newest first. Regenerate: `%s --write archive`\n\n",
		len(entries), recordsCmd)
	for _, e := range entries {
		fmt.Fprintf(&b, "- %s · #%d %s · %s\n", e.at.Format(time.DateOnly), e.is.Number, e.is.Title, reason(e.is))
	}
	return b.String(), nil
}

// reason returns the close reason in words; a close with no reason says so.
func reason(is issue) string {
	if is.StateReason == "" {
		return "unknown"
	}
	return strings.ToLower(strings.ReplaceAll(is.StateReason, "_", " "))
}
