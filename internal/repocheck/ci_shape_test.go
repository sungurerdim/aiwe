package repocheck

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// The pinned actions of the fixture: full commit SHAs with the version in a comment (SPEC §12.2).
const (
	checkoutRef = "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"
	setupGoRef  = "actions/setup-go@d35c59abb061a4a6fb18e82ac0862c26744d6ab5"
)

// ciFixture is a workflow of the shape SPEC §12.2 prescribes: two jobs and four steps, each with a
// `# why:` comment in one of the three places a step may carry it.
const ciFixture = `name: ci

on:
  push:
    branches: [main]
  pull_request:
  workflow_dispatch:
    inputs:
      all-os:
        description: run the test job on every OS
        type: boolean
        default: false

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}

permissions:
  contents: read

jobs:
  static:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      # why: the gate reads the tree
      - uses: ` + checkoutRef + ` # v4.2.2
      - name: setup-go # why: go tool runs every gate step
        uses: ` + setupGoRef + ` # v5.5.0
      - name: gate
        # why: these results do not depend on the OS
        run: |
          # why: a shell comment inside the script, never a step reason
          go run ./internal/cmd/repocheck all --repo .
  test:
    needs: static
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - name: test # why: processes and paths differ by OS
        run: go test ./...
`

// ciShapeOf runs CIShape on a tree that holds body as the one workflow.
func ciShapeOf(t *testing.T, body string) CIShapeReport {
	t.Helper()
	rep, present, err := CIShape(fstest.MapFS{ciWorkflowPath: {Data: []byte(body)}})
	if err != nil || !present {
		t.Fatalf("CIShape: present %v, err %v; want the workflow measured", present, err)
	}
	return rep
}

// replaced returns ciFixture with old replaced by repl; an old that is absent fails the test, so a
// fixture edit cannot silently turn a case into a copy of the valid workflow.
func replaced(t *testing.T, old, repl string) string {
	t.Helper()
	if !strings.Contains(ciFixture, old) {
		t.Fatalf("fixture has no %q", old)
	}
	return strings.Replace(ciFixture, old, repl, 1)
}

// lineOf returns the 1-based line of the first line of body that contains text.
func lineOf(t *testing.T, body, text string) int {
	t.Helper()
	for i, l := range strings.Split(body, "\n") {
		if strings.Contains(l, text) {
			return i + 1
		}
	}
	t.Fatalf("body has no line with %q", text)
	return 0
}

// TestCIShape catches a CI step without a `# why:` comment, a `uses:` reference that is not a full
// commit SHA, a trigger other than pull_request, push with branches [main] and workflow_dispatch,
// or a concurrency other than group ci-${{ github.ref }} cancelled for pull requests only, that
// passes repo/ci-shape (DR-42, SPEC §12.2).
func TestCIShape(t *testing.T) {
	t.Run("accepts the prescribed shape and counts its jobs and steps", func(t *testing.T) {
		rep := ciShapeOf(t, ciFixture)
		if len(rep.Findings) != 0 {
			t.Fatalf("findings = %+v, want none", rep.Findings)
		}
		if rep.Workflows != 1 || rep.Jobs != 2 || rep.Steps != 4 {
			t.Fatalf("counts = %d workflows, %d jobs, %d steps; want 1, 2, 4", rep.Workflows, rep.Jobs, rep.Steps)
		}
	})

	accepted := []struct{ name, old, repl string }{
		{"a quoted pinned reference", "- uses: " + checkoutRef, "- uses: '" + checkoutRef + "'"},
		{"a block list of branches", "branches: [main]", "branches:\n      - main"},
		{"a quoted on key", "\non:\n", "\n'on':\n"},
		{"an expression without inner spaces",
			"${{ github.event_name == 'pull_request' }}", "${{github.event_name == 'pull_request'}}"},
		{"a double-quoted group", "group: ci-${{ github.ref }}", `group: "ci-${{ github.ref }}"`},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			if got := ciShapeOf(t, replaced(t, tc.old, tc.repl)).Findings; len(got) != 0 {
				t.Fatalf("findings = %+v, want none", got)
			}
		})
	}

	refused := []struct {
		name, old, repl, rule, line string
	}{
		{"a step without any why comment", "      # why: the gate reads the tree\n", "", "step_without_why", "- uses: " + checkoutRef},
		{"a step whose why is only a shell comment in its script",
			"        # why: these results do not depend on the OS\n", "", "step_without_why", "- name: gate"},
		{"a why comment without a reason", "# why: processes and paths differ by OS", "# why:", "step_without_why", "- name: test"},
		{"a why comment spelt otherwise", "# why: go tool runs", "# reason: go tool runs", "step_without_why", "- name: setup-go"},
		{"a tag reference", checkoutRef, "actions/checkout@v4", "uses_not_pinned", "actions/checkout@v4"},
		{"a short SHA", checkoutRef, "actions/checkout@11bd719", "uses_not_pinned", "actions/checkout@11bd719"},
		{"a branch reference", setupGoRef, "actions/setup-go@main", "uses_not_pinned", "actions/setup-go@main"},
		{"a reference without a ref", checkoutRef, "actions/checkout", "uses_not_pinned", "uses: actions/checkout "},
		{"an upper-case SHA", checkoutRef, strings.ToUpper(checkoutRef), "uses_not_pinned", "ACTIONS/CHECKOUT"},
		{"a 41-character SHA", checkoutRef, checkoutRef + "0", "uses_not_pinned", checkoutRef + "0"},
		{"a local action", checkoutRef, "./.github/actions/setup", "uses_not_pinned", "./.github/actions/setup"},
		{"a docker action", checkoutRef, "docker://alpine:3.20", "uses_not_pinned", "docker://alpine"},
		{"a job-level reusable workflow on a tag", "    needs: static\n",
			"    needs: static\n    uses: octo-org/ci/.github/workflows/test.yml@v1\n",
			"uses_not_pinned", "octo-org/ci/.github/workflows/test.yml@v1"},
		{"push on another branch too", "branches: [main]", "branches: [main, 'unit/**']", "trigger_shape", "branches:"},
		{"push on another branch only", "branches: [main]", "branches: [develop]", "trigger_shape", "branches:"},
		{"push without a branch filter", "  push:\n    branches: [main]\n", "  push:\n", "trigger_shape", "  push:"},
		{"push on tags", "    branches: [main]\n", "    branches: [main]\n    tags: ['v*']\n", "trigger_shape", "tags:"},
		{"push with a paths filter", "    branches: [main]\n", "    branches: [main]\n    paths: ['**.go']\n",
			"trigger_shape", "paths:"},
		{"pull_request with a paths filter", "  pull_request:\n", "  pull_request:\n    paths-ignore: ['docs/**']\n",
			"trigger_shape", "paths-ignore:"},
		{"an extra trigger", "  pull_request:\n", "  pull_request:\n  schedule:\n    - cron: '0 3 * * *'\n",
			"trigger_shape", "schedule:"},
		{"pull_request_target for pull_request", "  pull_request:\n", "  pull_request_target:\n",
			"trigger_shape", "on:"},
		{"no workflow_dispatch", "  workflow_dispatch:\n    inputs:\n      all-os:\n        description: run the test job on every OS\n        type: boolean\n        default: false\n",
			"", "trigger_shape", "on:"},
		{"a flow list of triggers", "on:\n  push:\n    branches: [main]\n  pull_request:\n",
			"on: [push, pull_request]\nx:\n", "trigger_shape", "on:"},
		{"another concurrency group", "group: ci-${{ github.ref }}", "group: ci-${{ github.workflow }}",
			"concurrency_shape", "group:"},
		{"a group without the ci prefix", "group: ci-${{ github.ref }}", "group: ${{ github.ref }}",
			"concurrency_shape", "group:"},
		{"cancel-in-progress always on", "${{ github.event_name == 'pull_request' }}", "true",
			"concurrency_shape", "cancel-in-progress:"},
		{"cancel-in-progress for pushes", "${{ github.event_name == 'pull_request' }}",
			"${{ github.event_name == 'push' }}", "concurrency_shape", "cancel-in-progress:"},
		{"no cancel-in-progress", "  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n", "",
			"concurrency_shape", "concurrency:"},
		{"a scalar concurrency", "concurrency:\n  group: ci-${{ github.ref }}\n  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n",
			"concurrency: ci-${{ github.ref }}\n", "concurrency_shape", "concurrency:"},
		{"no concurrency", "concurrency:\n  group: ci-${{ github.ref }}\n  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n",
			"", "concurrency_shape", ""},
		{"an extra concurrency key", "  group: ci-${{ github.ref }}\n", "  group: ci-${{ github.ref }}\n  queue: max\n",
			"concurrency_shape", "queue:"},
	}
	for _, tc := range refused {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			body := replaced(t, tc.old, tc.repl)
			want := 0
			if tc.line != "" {
				want = lineOf(t, body, tc.line)
			}
			got := ciShapeOf(t, body).Findings
			if !slices.ContainsFunc(got, func(f Finding) bool {
				return f.Rule == tc.rule && f.Line == want && f.Path == ciWorkflowPath
			}) {
				t.Fatalf("findings = %+v, want %s at %s:%d", got, tc.rule, ciWorkflowPath, want)
			}
		})
	}

	t.Run("refuses a workflow without jobs", func(t *testing.T) {
		body := ciFixture[:strings.Index(ciFixture, "jobs:")]
		got := ciShapeOf(t, body).Findings
		if !slices.ContainsFunc(got, func(f Finding) bool { return f.Rule == "jobs_missing" }) {
			t.Fatalf("findings = %+v, want jobs_missing", got)
		}
	})

	t.Run("refuses a second workflow file", func(t *testing.T) {
		rep, present, err := CIShape(fstest.MapFS{
			ciWorkflowPath:                   {Data: []byte(ciFixture)},
			".github/workflows/release.yaml": {Data: []byte(ciFixture)},
		})
		if err != nil || !present {
			t.Fatalf("CIShape: present %v, err %v; want the workflow measured", present, err)
		}
		if rep.Workflows != 2 || !slices.ContainsFunc(rep.Findings, func(f Finding) bool {
			return f.Rule == "extra_workflow" && f.Path == ".github/workflows/release.yaml"
		}) {
			t.Fatalf("report = %+v, want 2 workflows and extra_workflow for release.yaml", rep)
		}
	})

	t.Run("reports an absent workflow as unmeasured, never green", func(t *testing.T) {
		rep, present, err := CIShape(fstest.MapFS{"go.mod": {Data: []byte("module example.com/x\n")}})
		if err != nil || present || rep.Workflows != 0 || len(rep.Findings) != 0 {
			t.Fatalf("CIShape without %s: report %+v, present %v, err %v; want not present and no error",
				ciWorkflowPath, rep, present, err)
		}
	})
}
