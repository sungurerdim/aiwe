package repocheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sungurerdim/aiwe/internal/core"
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

// ciShapeOf runs CIShape on a tree that holds body as the one workflow. The shape fixture has no
// step selection and no gate.json, so gate_ci_drift is dropped: TestCIShapeGateDrift owns that rule.
func ciShapeOf(t *testing.T, body string) CIShapeReport {
	t.Helper()
	rep, present, err := CIShape(fstest.MapFS{ciWorkflowPath: {Data: []byte(body)}})
	if err != nil || !present {
		t.Fatalf("CIShape: present %v, err %v; want the workflow measured", present, err)
	}
	rep.Findings = slices.DeleteFunc(rep.Findings, func(f Finding) bool { return f.Rule == "gate_ci_drift" })
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
// commit SHA, a trigger other than pull_request and workflow_dispatch (a push included: a main push
// starts no run, SPEC §12.2), or a concurrency other than group ci-${{ github.ref }} cancelled for
// pull requests only, that passes repo/ci-shape (DR-22, SPEC §12.2).
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
		{"a quoted on key", "\non:\n", "\n'on':\n"},
		{"an expression without inner spaces",
			"${{ github.event_name == 'pull_request' }}", "${{github.event_name == 'pull_request'}}"},
		{"a double-quoted group", "group: ci-${{ github.ref }}", `group: "ci-${{ github.ref }}"`},
		// A tab before `#` leaves a line with a comment and no node; it must not panic.
		{"a tab-indented why comment line", "      # why: the gate reads the tree\n", "\t# why: the gate reads the tree\n"},
		{"a line of only a vertical tab", "      # why: the gate reads the tree\n", "      # why: the gate reads the tree\n\v\n"},
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
		{"a push trigger", "  pull_request:\n", "  push:\n    branches: [main]\n  pull_request:\n",
			"trigger_shape", "  push:"},
		{"a push trigger without a branch filter", "  pull_request:\n", "  push:\n  pull_request:\n",
			"trigger_shape", "  push:"},
		{"a push trigger after the dispatch block", "\nconcurrency:\n",
			"\n  push:\n    branches: [main]\nconcurrency:\n", "trigger_shape", "  push:"},
		{"pull_request with a paths filter", "  pull_request:\n", "  pull_request:\n    paths-ignore: ['docs/**']\n",
			"trigger_shape", "paths-ignore:"},
		{"an extra trigger", "  pull_request:\n", "  pull_request:\n  schedule:\n    - cron: '0 3 * * *'\n",
			"trigger_shape", "schedule:"},
		{"pull_request_target for pull_request", "  pull_request:\n", "  pull_request_target:\n",
			"trigger_shape", "on:"},
		{"no workflow_dispatch", "  workflow_dispatch:\n    inputs:\n      all-os:\n        description: run the test job on every OS\n        type: boolean\n        default: false\n",
			"", "trigger_shape", "on:"},
		{"a flow list of triggers", "on:\n  pull_request:\n",
			"on: [pull_request]\nx:\n", "trigger_shape", "on:"},
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

// ciGateJSON is the gate the workflows below share. Its step ids are the ones a workflow may never
// write out itself: the selection names them, and the workflow only reads its build output.
const ciGateJSON = `{
  "formatVersion": 1,
  "steps": [
    {"id": "format", "command": ["gofmt", "-l", "."], "required": true,
      "inputs": ["**/*.go"], "when": ["source", "test-only"], "expect": {"exitCode": 0}},
    {"id": "repo", "command": ["go", "run", "./internal/cmd/repocheck", "all", "--repo", "."], "required": true,
      "inputs": ["**"], "when": ["docs", "source", "test-only"], "expect": {"exitCode": 0}},
    {"id": "test", "command": ["go", "test", "-json", "./..."], "required": true,
      "inputs": ["**/*.go"], "when": ["source", "test-only"], "expect": {"exitCode": 0}}
  ]
}
`

// ciGateWorkflow is a valid workflow (#316): the changes job has one output, build, which gatesteps
// derives from gate.json and the build job reads; no job reads a step list, so the job has none. The
// gate run takes no step list: the runner counts a required step left out of --step as not measured,
// so a partial list fails the gate (DR-6). No step id or change class is written in the workflow.
const ciGateWorkflow = `name: ci

on:
  pull_request:
  workflow_dispatch:

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}

jobs:
  changes:
    runs-on: ubuntu-latest
    outputs:
      build: ${{ steps.change.outputs.build }}
    steps:
      # why: the selector reads the diff of the change
      - uses: ` + checkoutRef + ` # v4.2.2
      - id: change
        # why: gatesteps reads gate.json, the one source of the steps
        run: |
          set -eu
          paths="${RUNNER_TEMP}/paths"
          if ! git diff --no-renames --name-only -z "origin/${GITHUB_BASE_REF}" HEAD > "$paths"; then
            : > "$paths"
          fi
          go run ./internal/cmd/gatesteps < "$paths" >> "$GITHUB_OUTPUT"
  build:
    needs: changes
    if: needs.changes.outputs.build == 'true'
    runs-on: ubuntu-latest
    steps:
      - name: test # why: processes and paths differ by OS
        run: go test ./...
  gate:
    runs-on: ubuntu-latest
    steps:
      # why: a step left out of the run is not measured, so the gate runs every step
      - name: gate steps
        run: |
          set -eu
          go run ./cmd/` + core.Product + ` gates gate --repo .
`

// ciShapeWithGate runs CIShape on a tree that holds the workflow and ciGateJSON.
func ciShapeWithGate(t *testing.T, workflow string) CIShapeReport {
	t.Helper()
	rep, present, err := CIShape(fstest.MapFS{
		ciWorkflowPath: {Data: []byte(workflow)},
		"gate.json":    {Data: []byte(ciGateJSON)},
	})
	if err != nil || !present {
		t.Fatalf("CIShape: present %v, err %v; want the workflow measured", present, err)
	}
	return rep
}

// gateDrift reports whether rep contains gate_ci_drift.
func gateDrift(rep CIShapeReport) bool {
	return slices.ContainsFunc(rep.Findings, func(f Finding) bool { return f.Rule == "gate_ci_drift" })
}

// TestCIShapeGateDrift catches CI step selection that is a second copy of gate.json (#316): a
// selection step that writes its output itself instead of running gatesteps, a job output that does
// not come from that step, a selection output no job reads, a diff that follows renames or swallows
// its failure, a gate run that takes the selected list or any output of the selection job, which
// leaves a required step not measured and the gate incomplete, a gate job that needs the selection
// job, even through another job, or whose condition or env reads it in dot or index form, which can
// skip the gate, a gate run that names its own ids or pastes an expression into its script, a
// condition that names a step id, a change class read beside the selection, no gate run, and a
// missing gate.json.
func TestCIShapeGateDrift(t *testing.T) {
	t.Run("accepts a workflow whose gate runs every step", func(t *testing.T) {
		if rep := ciShapeWithGate(t, ciGateWorkflow); len(rep.Findings) != 0 {
			t.Fatalf("findings = %+v, want none", rep.Findings)
		}
	})

	const selector = `          go run ./internal/cmd/gatesteps < "$paths" >> "$GITHUB_OUTPUT"` + "\n"
	const echo = `          echo 'build=true' >> "$GITHUB_OUTPUT"` + "\n"
	const build = "      build: ${{ steps.change.outputs.build }}\n"
	const gateRun = ` gates gate --repo .` + "\n"
	const gateStep = "      - name: gate steps\n"
	const gateJob = "  gate:\n    runs-on: ubuntu-latest\n"
	const relayed = gateStep + "        env:\n          STEPS: ${{ needs.changes.outputs.steps }}\n" +
		"        run: |\n          set -eu\n          steps=$(printf '%s' \"$STEPS\" | tr -d '\"[]')\n" +
		"          go run ./cmd/x gates gate --repo . --step \"$steps\"\n"
	refused := []struct{ name, old, repl string }{
		{"a selection replaced by an echo", selector, echo},
		{"an echo beside the selection", selector, selector + echo},
		{"a build output written out", "build: ${{ steps.change.outputs.build }}", "build: 'true'"},
		{"a selection output no job reads", build, "      steps: ${{ steps.change.outputs.steps }}\n" + build},
		{"an output of another step", "${{ steps.change.outputs.build }}", "${{ steps.other.outputs.build }}"},
		{"a diff that follows renames", "git diff --no-renames --name-only", "git diff --name-only"},
		{"a swallowed failure", selector, "          git fetch --no-tags origin || true\n" + selector},
		{"an expression pasted into the gate script", gateRun, gateRun + "          echo '${{ needs.changes.outputs.steps }}'\n"},
		{"a gate run that relays the selected list", ciGateWorkflow[strings.Index(ciGateWorkflow, gateStep):], relayed},
		{"a gate run that reads the list through env", gateStep,
			gateStep + "        env:\n          STEPS: ${{ needs.changes.outputs.steps }}\n"},
		{"a gate run that reads the build output through env", gateStep,
			gateStep + "        env:\n          BUILD: ${{ needs.changes.outputs.build }}\n"},
		{"a run that names its own step ids", gateRun, " gates gate --repo . --step format,repo,test\n"},
		{"a condition that names a step id", "if: needs.changes.outputs.build == 'true'",
			"if: contains(fromJSON(needs.changes.outputs.steps), 'test')"},
		{"a condition on a change class", "      - name: gate steps\n",
			"      - name: gate steps\n        if: contains(fromJSON(needs.changes.outputs.classes), 'docs')\n"},
		{"a class marker comment", gateStep, gateStep + "        # class: docs\n"},
		{"a second path classifier", gateRun, gateRun + `          case "*_test.go") add test-only ;;` + "\n"},
		{"a second gate run with step ids", "  gate:\n",
			"  again:\n    runs-on: ubuntu-latest\n    steps:\n      - name: some # why: x\n        run: go run ./cmd/x gates gate --repo . --step repo\n  gate:\n"},
		{"no gate run", ciGateWorkflow[strings.Index(ciGateWorkflow, "  gate:\n"):], ""},
		// A gate job that waits on the selection or reads it can be skipped on a docs-only change, and
		// a skipped job passes a required check: the merge signal turns green with no gate run.
		{"a gate job skipped on the build output", gateJob,
			"  gate:\n    needs: changes\n    if: needs.changes.outputs.build == 'true'\n    runs-on: ubuntu-latest\n"},
		{"a gate job that needs the selection job", gateJob, "  gate:\n    needs: changes\n    runs-on: ubuntu-latest\n"},
		{"a gate job that needs it in a flow list", gateJob,
			"  gate:\n    needs: [changes]\n    runs-on: ubuntu-latest\n"},
		{"a gate job that needs it in a block list", gateJob,
			"  gate:\n    needs:\n      - changes\n    runs-on: ubuntu-latest\n"},
		{"a gate job that needs a job that needs it", gateJob, "  gate:\n    needs: build\n    runs-on: ubuntu-latest\n"},
		{"a gate job condition on the build output", gateJob,
			"  gate:\n    if: needs.changes.outputs.build == 'true'\n    runs-on: ubuntu-latest\n"},
		{"a gate job condition that indexes the selection job", gateJob,
			"  gate:\n    if: needs['changes'].outputs.build == 'true'\n    runs-on: ubuntu-latest\n"},
		{"a gate job env that reads the build output", gateJob,
			"  gate:\n    runs-on: ubuntu-latest\n    env:\n      BUILD: ${{ needs.changes.outputs.build }}\n"},
		{"a gate step condition on the build output", gateStep,
			gateStep + "        if: needs.changes.outputs.build == 'true'\n"},
		{"a gate step env that indexes the selection job", gateStep,
			gateStep + "        env:\n          BUILD: ${{ needs[\"changes\"].outputs.build }}\n"},
		{"a gate job that needs it in a multi-line flow list", gateJob,
			"  gate:\n    needs: [\n      build,\n      lint\n      ]\n    runs-on: ubuntu-latest\n"},
	}
	for _, tc := range refused {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			if !strings.Contains(ciGateWorkflow, tc.old) {
				t.Fatalf("workflow has no %q", tc.old)
			}
			body := strings.Replace(ciGateWorkflow, tc.old, tc.repl, 1)
			if rep := ciShapeWithGate(t, body); !gateDrift(rep) {
				t.Fatalf("findings = %+v, want gate_ci_drift", rep.Findings)
			}
		})
	}

	// The allowed side of the needs rule: a gate job may wait on a job that does not depend on the
	// selection, and a job id that only starts with the selection's id is another job.
	const lintJob = "  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - name: vet # why: x\n        run: go vet ./...\n"
	accepted := []struct{ name, old, repl string }{
		{"a gate job that needs a job free of the selection", gateJob,
			lintJob + "  gate:\n    needs: lint\n    runs-on: ubuntu-latest\n"},
		{"a gate env that reads a job whose id only starts with the selection's", gateStep,
			gateStep + "        env:\n          X: ${{ needs.changes_x.outputs.y }}\n"},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			if !strings.Contains(ciGateWorkflow, tc.old) {
				t.Fatalf("workflow has no %q", tc.old)
			}
			body := strings.Replace(ciGateWorkflow, tc.old, tc.repl, 1)
			if rep := ciShapeWithGate(t, body); gateDrift(rep) {
				t.Fatalf("findings = %+v, want no gate_ci_drift", rep.Findings)
			}
		})
	}

	t.Run("a missing gate.json is drift, not a pass", func(t *testing.T) {
		rep, present, err := CIShape(fstest.MapFS{ciWorkflowPath: {Data: []byte(ciGateWorkflow)}})
		if err != nil || !present {
			t.Fatalf("CIShape: present %v, err %v; want the workflow measured", present, err)
		}
		if !gateDrift(rep) {
			t.Fatalf("findings = %+v, want gate_ci_drift", rep.Findings)
		}
	})

	t.Run("a gate.json that is not a regular file is an error, unread", func(t *testing.T) {
		// A FIFO would block the open; the guard of #199 refuses it from its mode.
		_, _, err := CIShape(fstest.MapFS{
			ciWorkflowPath: {Data: []byte(ciGateWorkflow)},
			"gate.json":    {Data: []byte(ciGateJSON), Mode: fs.ModeNamedPipe},
		})
		if err == nil {
			t.Fatalf("CIShape: err nil, want an error for a gate.json that is not a regular file")
		}
	})

	t.Run("accepts this repository's workflow and gate.json", func(t *testing.T) {
		rep, present, err := CIShape(os.DirFS(filepath.Join("..", "..")))
		if err != nil || !present || len(rep.Findings) != 0 {
			t.Fatalf("CIShape(repository): findings %+v, present %v, err %v; want none", rep.Findings, present, err)
		}
	})
}
