package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// todoSource names the sources in the first line of TODO.md.
const todoSource = "gh issue list --state open, sub-issues and dependencies"

// todo builds TODO.md: the root goal tree first, then the open issues outside it.
func (s *session) todo(ctx context.Context) (string, error) {
	open, err := s.issues(ctx, "open")
	if err != nil {
		return "", err
	}
	goals, err := s.tree(ctx)
	if err != nil {
		return "", err
	}
	blockers, err := s.blockers(ctx, open)
	if err != nil {
		return "", err
	}
	return header(s.d.now(), todoSource) + renderTodo(open, goals, blockers), nil
}

// todoView holds what rendering the TODO needs: titles of the open issues, their open blockers
// and the issues already listed.
type todoView struct {
	titles   map[int]string
	blockers map[int][]int
	placed   map[int]bool
}

// renderTodo lays out the goal tree and then the legacy issues.
func renderTodo(open []issue, goals []goal, blockers map[int][]int) string {
	v := &todoView{titles: map[int]string{}, blockers: blockers, placed: map[int]bool{rootGoal: true}}
	for _, is := range open {
		v.titles[is.Number] = is.Title
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# TODO\n\n%d open issues. Regenerate: `%s --write todo`\n\n", len(open), recordsCmd)
	fmt.Fprintf(&b, "## #%d %s\n", rootGoal, v.name(rootGoal))
	byNum := map[int]goal{}
	var nums []int
	for _, g := range goals {
		byNum[g.num] = g
		nums = append(nums, g.num)
	}
	for _, n := range order(nums, blockers) {
		v.goalSection(&b, byNum[n])
	}
	b.WriteString("\n## Legacy (to triage, tasima/issue-eylem.md)\n\n")
	var legacy []int
	for _, is := range open {
		if !v.placed[is.Number] {
			legacy = append(legacy, is.Number)
		}
	}
	slices.Sort(legacy)
	for _, n := range legacy {
		b.WriteString(v.line(n))
	}
	return b.String()
}

// goalSection writes one goal heading and its open sub-issues; a closed goal with none is skipped.
func (v *todoView) goalSection(b *strings.Builder, g goal) {
	v.placed[g.num] = true
	var members []int
	for _, m := range g.members {
		if _, open := v.titles[m]; open {
			members = append(members, m)
			v.placed[m] = true
		}
	}
	if _, open := v.titles[g.num]; !open && len(members) == 0 {
		return
	}
	fmt.Fprintf(b, "\n### #%d %s%s\n\n", g.num, v.name(g.num), v.mark(g.num))
	for _, m := range order(members, v.blockers) {
		b.WriteString(v.line(m))
	}
}

// name returns the title of an open issue, or says it is closed.
func (v *todoView) name(n int) string {
	if t, ok := v.titles[n]; ok {
		return t
	}
	return "(closed)"
}

// mark returns the blocked marker of n, or nothing.
func (v *todoView) mark(n int) string {
	var refs []string
	for _, b := range v.blockers[n] {
		refs = append(refs, fmt.Sprintf("#%d", b))
	}
	if len(refs) == 0 {
		return ""
	}
	return " (blocked by " + strings.Join(refs, ", ") + ")"
}

// line returns the list line of one open issue.
func (v *todoView) line(n int) string {
	return fmt.Sprintf("- #%d %s%s\n", n, v.titles[n], v.mark(n))
}

// order returns nums with every issue after the blockers that are in nums; among issues that are
// ready the lowest number goes first. A cycle is broken at its lowest number, so the order always
// ends and lists every issue once.
func order(nums []int, blockers map[int][]int) []int {
	in := make(map[int]bool, len(nums))
	for _, n := range nums {
		in[n] = true
	}
	rest := slices.Sorted(slices.Values(nums))
	done := map[int]bool{}
	out := make([]int, 0, len(nums))
	for len(rest) > 0 {
		pick := slices.IndexFunc(rest, func(n int) bool { return unblocked(n, in, done, blockers) })
		if pick < 0 {
			pick = 0
		}
		done[rest[pick]] = true
		out = append(out, rest[pick])
		rest = slices.Delete(rest, pick, pick+1)
	}
	return out
}

// unblocked reports whether every blocker of n that is in the set has been listed.
func unblocked(n int, in, done map[int]bool, blockers map[int][]int) bool {
	for _, b := range blockers[n] {
		if in[b] && !done[b] && b != n {
			return false
		}
	}
	return true
}
