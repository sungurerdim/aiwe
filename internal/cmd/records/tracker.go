package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const (
	// issueLimit is the --limit of `gh issue list`; a list that reaches it may be cut, so it is an error.
	issueLimit = 1000
	// workers bounds the parallel gh api calls.
	workers = 8
	// jsonFence opens the JSON record block of an issue body.
	jsonFence = "```json\n"
)

// issue is the part of a `gh issue list --json` entry that the records read.
type issue struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	ClosedAt    string `json:"closedAt"`
	StateReason string `json:"stateReason"`
}

// session holds one generation: its inputs, the issue lists read once and the warnings so far.
type session struct {
	d          deps
	open       []issue
	closed     []issue
	haveOpen   bool
	haveClosed bool
	warnings   []string
}

// call runs a command; an error names the command (its program and first two arguments).
func (s *session) call(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := s.d.run(ctx, name, args...)
	if err != nil {
		label := append([]string{name}, args[:min(2, len(args))]...)
		return nil, fmt.Errorf("%s: %w", strings.Join(label, " "), err)
	}
	return out, nil
}

// issues returns the issues in the given state, read once per generation.
func (s *session) issues(ctx context.Context, state string) ([]issue, error) {
	have, list := &s.haveOpen, &s.open
	if state == "closed" {
		have, list = &s.haveClosed, &s.closed
	}
	if *have {
		return *list, nil
	}
	out, err := s.call(ctx, "gh", "issue", "list", "--state", state, "--limit", strconv.Itoa(issueLimit),
		"--json", "number,title,labels,body,closedAt,stateReason")
	if err != nil {
		return nil, err
	}
	var got []issue
	if err := json.Unmarshal(out, &got); err != nil {
		return nil, fmt.Errorf("gh issue list --state %s: read the JSON: %w", state, err)
	}
	if len(got) >= issueLimit {
		return nil, fmt.Errorf("gh issue list --state %s: %d issues reach the limit and may be cut", state, len(got))
	}
	for i := range got {
		got[i].Title = oneLine(got[i].Title)
	}
	*have, *list = true, got
	return got, nil
}

// apiNumbers reads the issue numbers one gh api path lists (sub-issues or blockers).
func (s *session) apiNumbers(ctx context.Context, n int, what string) ([]int, error) {
	path := fmt.Sprintf("repos/{owner}/{repo}/issues/%d/%s?per_page=100", n, what)
	out, err := s.call(ctx, "gh", "api", path, "--paginate", "--jq", ".[].number")
	if err != nil {
		return nil, err
	}
	var nums []int
	for _, f := range strings.Fields(string(out)) {
		v, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("gh api %s: read %q as an issue number", path, f)
		}
		nums = append(nums, v)
	}
	return nums, nil
}

// goal is one child goal of the root with every open or closed descendant, flattened.
type goal struct {
	num     int
	members []int
}

// tree reads the sub-issue tree under the root goal, level by level: the root's children are the
// goals, and every descendant belongs to the goal it hangs under.
func (s *session) tree(ctx context.Context) ([]goal, error) {
	w := &walker{owner: map[int]int{rootGoal: rootGoal}}
	frontier := []int{rootGoal}
	for len(frontier) > 0 {
		subs, err := s.level(ctx, frontier)
		if err != nil {
			return nil, err
		}
		var next []int
		for i, parent := range frontier {
			next = append(next, w.adopt(parent, subs[i])...)
		}
		frontier = next
	}
	return groupByGoal(w.goals, w.owner), nil
}

// level reads the sub-issues of every issue of one tree level.
func (s *session) level(ctx context.Context, frontier []int) ([][]int, error) {
	subs := make([][]int, len(frontier))
	err := parallel(len(frontier), func(i int) (err error) {
		subs[i], err = s.apiNumbers(ctx, frontier[i], "sub_issues")
		return err
	})
	return subs, err
}

// walker records which goal each issue of the tree hangs under.
type walker struct {
	owner map[int]int
	goals []int
}

// adopt files the children of parent under its goal (a child of the root is a goal itself) and
// returns the children not seen before.
func (w *walker) adopt(parent int, children []int) []int {
	var fresh []int
	for _, c := range children {
		if _, seen := w.owner[c]; seen {
			continue
		}
		w.owner[c] = w.owner[parent]
		if parent == rootGoal {
			w.owner[c] = c
			w.goals = append(w.goals, c)
		}
		fresh = append(fresh, c)
	}
	return fresh
}

// groupByGoal collects, for each goal, the issues that hang under it (the goal itself excluded).
func groupByGoal(goals []int, owner map[int]int) []goal {
	var out []goal
	for _, g := range goals {
		node := goal{num: g}
		for n, o := range owner {
			if o == g && n != g {
				node.members = append(node.members, n)
			}
		}
		slices.Sort(node.members)
		out = append(out, node)
	}
	return out
}

// blockers returns, for each open issue, the open issues that block it. A blocker is read from the
// dependencies API and from links.blockedBy of the body, and both count: the API is the native
// record and the body block is the plan, so a blocker named in either one is a blocker. When the
// endpoint fails the body alone answers; an issue with no body block either is named in a warning.
func (s *session) blockers(ctx context.Context, open []issue) (map[int][]int, error) {
	isOpen := make(map[int]bool, len(open))
	for _, is := range open {
		isOpen[is.Number] = true
	}
	found := make([][]int, len(open))
	unreadable := make([]bool, len(open))
	err := parallel(len(open), func(i int) error {
		api, apiErr := s.apiNumbers(ctx, open[i].Number, "dependencies/blocked_by")
		links, hasBlock := bodyBlockers(open[i].Body)
		unreadable[i] = apiErr != nil && !hasBlock
		found[i] = append(api, links...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := map[int][]int{}
	var lost []string
	for i, is := range open {
		for _, b := range found[i] {
			if isOpen[b] && b != is.Number {
				out[is.Number] = append(out[is.Number], b)
			}
		}
		slices.Sort(out[is.Number])
		out[is.Number] = slices.Compact(out[is.Number])
		if unreadable[i] {
			lost = append(lost, "#"+strconv.Itoa(is.Number))
		}
	}
	if len(lost) > 0 {
		s.warnings = append(s.warnings, "blockedBy could not be read for "+strings.Join(lost, ", ")+
			" (the dependencies endpoint failed and the body has no record block)")
	}
	return out, nil
}

// bodyBlockers reads links.blockedBy from the first JSON block of an issue body; the bool is false
// when the body has no readable block.
func bodyBlockers(text string) ([]int, bool) {
	_, rest, ok := strings.Cut(text, jsonFence)
	if !ok {
		return nil, false
	}
	block, _, _ := strings.Cut(rest, "\n```")
	var rec struct {
		Links struct {
			BlockedBy []int `json:"blockedBy"`
		} `json:"links"`
	}
	if err := json.Unmarshal([]byte(block), &rec); err != nil {
		return nil, false
	}
	return rec.Links.BlockedBy, true
}

// parallel runs fn for 0..n-1 on at most workers goroutines and waits for all of them; it returns
// the error of the lowest index that failed.
func parallel(n int, fn func(i int) error) error {
	errs := make([]error, n)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			errs[i] = fn(i)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
