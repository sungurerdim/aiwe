package repocheck

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// The rule names of check repo/ci-shape (DR-42, SPEC §12.2).
const (
	ruleStepWithoutWhy   = "step_without_why"
	ruleUsesNotPinned    = "uses_not_pinned"
	ruleTriggerShape     = "trigger_shape"
	ruleConcurrencyShape = "concurrency_shape"
	ruleJobsMissing      = "jobs_missing"
	ruleExtraWorkflow    = "extra_workflow"
)

// The one workflow and its directory (SPEC §12.1), and the concurrency SPEC §12.2 prescribes,
// with every space removed.
const (
	workflowDir       = ".github/workflows"
	ciWorkflowPath    = workflowDir + "/ci.yml"
	concurrencyGroup  = "ci-${{github.ref}}"
	concurrencyCancel = "${{github.event_name=='pull_request'}}"
)

// ErrNoWorkflow reports that the tree has no CI workflow, so the shape was not measured (DR-13).
var ErrNoWorkflow = errors.New("no CI workflow")

// pinnedUsesPattern matches an action or reusable workflow named by a full commit SHA.
var pinnedUsesPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$`)

// CIShapeReport is the result of check repo/ci-shape: the counts DR-42 reports and the findings.
type CIShapeReport struct {
	Workflows int
	Jobs      int
	Steps     int
	Findings  []Finding
}

// CIShape checks the CI workflow of the tree fsys against SPEC §12.2: every step carries a
// `# why:` comment, every `uses:` names a full commit SHA, the triggers are push on main,
// pull_request and workflow_dispatch, the concurrency is the prescribed one, and ci.yml is the
// only workflow. A tree without ci.yml is ErrNoWorkflow, never green (DR-13).
func CIShape(fsys fs.FS) (CIShapeReport, error) {
	body, err := fs.ReadFile(fsys, ciWorkflowPath)
	if errors.Is(err, fs.ErrNotExist) {
		return CIShapeReport{}, fmt.Errorf("ci-shape: %s: %w", ciWorkflowPath, ErrNoWorkflow)
	}
	if err != nil {
		return CIShapeReport{}, fmt.Errorf("ci-shape: %w", err)
	}
	entries, err := fs.ReadDir(fsys, workflowDir)
	if err != nil {
		return CIShapeReport{}, fmt.Errorf("ci-shape: %w", err)
	}
	rep := workflowShape(parseYAML(string(body)))
	for _, e := range entries {
		ext := path.Ext(e.Name())
		if e.IsDir() || (ext != ".yml" && ext != ".yaml") {
			continue
		}
		rep.Workflows++
		if p := workflowDir + "/" + e.Name(); p != ciWorkflowPath {
			rep.Findings = append(rep.Findings, Finding{Rule: ruleExtraWorkflow, Path: p})
		}
	}
	sortFindings(rep.Findings)
	return rep, nil
}

// workflowShape checks the parsed top-level keys of ci.yml.
func workflowShape(roots []*yamlNode) CIShapeReport {
	var rep CIShapeReport
	add := func(rule string, line int) {
		rep.Findings = append(rep.Findings, Finding{Rule: rule, Path: ciWorkflowPath, Line: line})
	}
	checkTriggers(child(roots, "on"), add)
	checkConcurrency(child(roots, "concurrency"), add)
	jobs := child(roots, "jobs")
	if jobs == nil || len(jobs.children) == 0 {
		add(ruleJobsMissing, lineOrZero(jobs))
	}
	if jobs != nil {
		for _, job := range jobs.children {
			rep.Jobs++
			rep.Steps += checkSteps(child(job.children, "steps"), add)
		}
	}
	walk(roots, func(n *yamlNode) {
		if n.key == "uses" && !pinnedUsesPattern.MatchString(unquote(n.value)) {
			add(ruleUsesNotPinned, n.line)
		}
	})
	return rep
}

// checkTriggers checks the `on` key: exactly push on main, pull_request and workflow_dispatch.
func checkTriggers(on *yamlNode, add func(string, int)) {
	if on == nil || on.value != "" {
		add(ruleTriggerShape, lineOrZero(on))
		return
	}
	for _, name := range []string{"push", "pull_request", "workflow_dispatch"} {
		if child(on.children, name) == nil {
			add(ruleTriggerShape, on.line)
		}
	}
	for _, t := range on.children {
		switch t.key {
		case "push":
			checkPush(t, add)
		case "pull_request":
			if hasBody(t) {
				add(ruleTriggerShape, firstLine(t))
			}
		case "workflow_dispatch":
		default:
			add(ruleTriggerShape, t.line)
		}
	}
}

// checkPush checks the push trigger: one key, branches, that lists main alone.
func checkPush(push *yamlNode, add func(string, int)) {
	branches := child(push.children, "branches")
	if push.value != "" || branches == nil {
		add(ruleTriggerShape, push.line)
	}
	for _, c := range push.children {
		if c.key != "branches" || !slices.Equal(listValues(c), []string{"main"}) {
			add(ruleTriggerShape, c.line)
		}
	}
}

// checkConcurrency checks the concurrency key: the prescribed group and cancel-in-progress only.
func checkConcurrency(conc *yamlNode, add func(string, int)) {
	if conc == nil || conc.value != "" {
		add(ruleConcurrencyShape, lineOrZero(conc))
		return
	}
	want := map[string]string{"group": concurrencyGroup, "cancel-in-progress": concurrencyCancel}
	for key := range want {
		if child(conc.children, key) == nil {
			add(ruleConcurrencyShape, conc.line)
		}
	}
	for _, c := range conc.children {
		if w, ok := want[c.key]; !ok || strings.ReplaceAll(unquote(c.value), " ", "") != w {
			add(ruleConcurrencyShape, c.line)
		}
	}
}

// checkSteps reports each step of steps without a `# why:` comment and returns the step count.
func checkSteps(steps *yamlNode, add func(string, int)) int {
	if steps == nil {
		return 0
	}
	count := 0
	for _, s := range steps.children {
		if !s.item {
			continue
		}
		count++
		if !hasWhy(s) {
			add(ruleStepWithoutWhy, s.line)
		}
	}
	return count
}

// hasWhy reports whether n or a node below it carries a `# why:` comment with a reason.
func hasWhy(n *yamlNode) bool {
	found := false
	walk([]*yamlNode{n}, func(m *yamlNode) {
		for _, c := range m.comments {
			reason, ok := strings.CutPrefix(strings.TrimSpace(strings.TrimPrefix(c, "#")), "why:")
			found = found || (ok && strings.TrimSpace(reason) != "")
		}
	})
	return found
}

// listValues returns the scalars of a flow list (`[a, b]`) or a block list below n.
func listValues(n *yamlNode) []string {
	var out []string
	if inner, ok := strings.CutPrefix(n.value, "["); ok {
		for v := range strings.SplitSeq(strings.TrimSuffix(inner, "]"), ",") {
			out = append(out, unquote(strings.TrimSpace(v)))
		}
		return out
	}
	if n.value != "" {
		return []string{unquote(n.value)}
	}
	for _, c := range n.children {
		out = append(out, unquote(c.value))
	}
	return out
}

// hasBody reports whether n has an inline value or nodes below it.
func hasBody(n *yamlNode) bool { return n.value != "" || len(n.children) > 0 }

// child returns the first node of nodes with key, or nil.
func child(nodes []*yamlNode, key string) *yamlNode {
	i := slices.IndexFunc(nodes, func(n *yamlNode) bool { return n.key == key })
	if i < 0 {
		return nil
	}
	return nodes[i]
}

// walk calls fn for every node of nodes and every node below them, depth first.
func walk(nodes []*yamlNode, fn func(*yamlNode)) {
	for _, n := range nodes {
		fn(n)
		walk(n.children, fn)
	}
}

// lineOrZero returns the line of n, or 0 when n is absent.
func lineOrZero(n *yamlNode) int {
	if n == nil {
		return 0
	}
	return n.line
}

// firstLine returns the line of the first node below n, or the line of n when it has none.
func firstLine(n *yamlNode) int {
	if len(n.children) > 0 {
		return n.children[0].line
	}
	return n.line
}

// unquote drops one pair of matching single or double quotes around s.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// yamlNode is one key or list item of the block YAML subset a workflow uses: its line, its indent,
// its key and inline value, the comments above and beside it, and the nodes below it.
type yamlNode struct {
	line     int
	indent   int
	item     bool
	key      string
	value    string
	comments []string
	children []*yamlNode
}

// parseYAML reads the block structure of body by indent. The lines of a block scalar (`run: |`)
// are skipped, so a shell comment in a script is never read as a YAML comment.
func parseYAML(body string) []*yamlNode {
	root := &yamlNode{indent: -1}
	stack := []*yamlNode{root}
	var pending []string
	block := -1
	for i, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		text := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimLeft(text, " ")
		indent := len(text) - len(trimmed)
		if inBlock(block, indent, trimmed) {
			continue
		}
		block = -1
		if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "#") {
			pending = appendComment(pending, trimmed)
			continue
		}
		nodes := lineNodes(i+1, indent, trimmed)
		nodes[0].comments = append(pending, nodes[0].comments...)
		pending = nil
		for _, n := range nodes {
			stack = attach(stack, n)
		}
		if last := nodes[len(nodes)-1]; isBlockScalar(last.value) {
			block = last.indent
		}
	}
	return root.children
}

// inBlock reports whether a line of the given indent and trimmed text belongs to the block scalar
// opened at indent block; block is -1 when none is open.
func inBlock(block, indent int, trimmed string) bool {
	return block >= 0 && (trimmed == "" || indent > block)
}

// isBlockScalar reports whether value opens a literal or folded block scalar.
func isBlockScalar(value string) bool {
	return strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">")
}

// appendComment appends line to pending when it is a comment.
func appendComment(pending []string, line string) []string {
	if strings.HasPrefix(line, "#") {
		return append(pending, line)
	}
	return pending
}

// lineNodes returns the nodes of one non-blank line: a list item, a key, or a list item and the
// key that follows its dash. The inline comment goes to the last node.
func lineNodes(line, indent int, text string) []*yamlNode {
	content, comment := splitComment(text)
	var nodes []*yamlNode
	var item *yamlNode
	if rest, ok := strings.CutPrefix(content, "- "); ok || content == "-" {
		item = &yamlNode{line: line, indent: indent, item: true}
		nodes = append(nodes, item)
		rest = strings.TrimLeft(rest, " ")
		indent += len(content) - len(rest)
		content = rest
	}
	key, value := splitKey(content)
	switch {
	case item != nil && key == "":
		item.value = value
	case content != "":
		nodes = append(nodes, &yamlNode{line: line, indent: indent, key: key, value: value})
	}
	if comment != "" {
		last := nodes[len(nodes)-1]
		last.comments = append(last.comments, comment)
	}
	return nodes
}

// splitComment splits text into its content and its inline comment: a `#` outside quotes that
// starts the text or follows a space.
func splitComment(text string) (content, comment string) {
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || text[i-1] == ' ' || text[i-1] == '\t'):
			return strings.TrimSpace(text[:i]), text[i:]
		}
	}
	return strings.TrimSpace(text), ""
}

// splitKey splits `key: value` content into its unquoted key and its value; content without a
// key is a scalar, returned as the value.
func splitKey(content string) (key, value string) {
	if content != "" && (content[0] == '\'' || content[0] == '"') {
		end := strings.IndexByte(content[1:], content[0])
		if end < 0 {
			return "", content
		}
		after, ok := strings.CutPrefix(content[end+2:], ":")
		if !ok || (after != "" && after[0] != ' ') {
			return "", content
		}
		return content[1 : end+1], strings.TrimSpace(after)
	}
	if i := strings.Index(content, ": "); i >= 0 {
		return content[:i], strings.TrimSpace(content[i+2:])
	}
	if k, ok := strings.CutSuffix(content, ":"); ok {
		return k, ""
	}
	return "", content
}

// attach adds n below the nearest open node with a smaller indent and returns the new stack. A
// list item at the indent of a key without a value belongs to that key, as YAML allows.
func attach(stack []*yamlNode, n *yamlNode) []*yamlNode {
	for len(stack) > 1 {
		top := stack[len(stack)-1]
		if top.indent < n.indent || (n.item && !top.item && top.indent == n.indent && top.key != "" && top.value == "") {
			break
		}
		stack = stack[:len(stack)-1]
	}
	parent := stack[len(stack)-1]
	parent.children = append(parent.children, n)
	return append(stack, n)
}
