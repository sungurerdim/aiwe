package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The selections run makes on this repository's gate.json: every step, repo alone, and repo with
// the test step, whose package tests read docs/SPEC-contracts.md, docs/SPEC.md and docs/standards.md.
const (
	everyStep   = `["format","lint","vuln","tidy","deadcode","repo","test"]`
	repoOnly    = `["repo"]`
	repoAndTest = `["repo","test"]`
)

// sourceOnlyGate is a gate whose steps a docs change does not touch, so StepsForChange selects
// none of them. Its go test step is named unit, so build follows the command, not the id test.
const sourceOnlyGate = `{"formatVersion":1,"steps":[` +
	`{"id":"fmt","command":["gofmt","-l","."],"required":true,` +
	`"inputs":["**/*.go"],"when":["source"],"expect":{"exitCode":0}},` +
	`{"id":"unit","command":["go","test","./..."],"required":true,` +
	`"inputs":["**/*.go"],"when":["source"],"expect":{"exitCode":0}}]}`

// TestRun catches a gatesteps whose outputs let CI skip work a change needs, or that writes an
// output no job reads. Each row names the failure it catches; the wants are the exact text, so they
// also pin the shape: $GITHUB_OUTPUT gets one build line with true or false and nothing else, and
// the log gets one line with the JSON list of the selected steps.
func TestRun(t *testing.T) {
	cases := []struct {
		name  string // the failure the row catches
		gate  string // the gate.json body; empty reads this repository's gate.json
		paths string // stdin, as git diff --no-renames --name-only -z prints it
		steps string // the selected step ids the log line names
		build string // the build output
	}{
		{"an empty selection printed as no step instead of every step", sourceOnlyGate, "README.md\x00",
			`["fmt","unit"]`, "true"},
		{"no path, as on a manual run, that skips a step", "", "", everyStep, "true"},
		{"separators alone read as a path", "", "\x00\x00\n", everyStep, "true"},
		{"a build output that is true when no go test step runs", "", "README.md\x00", repoOnly, "false"},
		{"a docs-only change that runs a code step or the build matrix", "",
			"README.md\x00CONTRIBUTING.md\x00docs/guide.mdx\x00notes.txt\x00", repoOnly, "false"},
		{"a newline or CRLF list read as one path", "", "README.md\r\nCONTRIBUTING.md\n", repoOnly, "false"},
		{"a SPEC-contracts.md change that skips the roles test that reads it", "",
			"docs/SPEC-contracts.md\x00", repoAndTest, "true"},
		{"a SPEC.md change that skips the registry test that reads it", "", "docs/SPEC.md\x00", repoAndTest, "true"},
		{"a standards.md change that skips the registry test that reads it", "", "docs/standards.md\x00",
			repoAndTest, "true"},
		{"a ci.yml change that skips a step", "", ".github/workflows/ci.yml\x00", everyStep, "true"},
		{"a gate.json change that skips a step", "", "gate.json\x00", everyStep, "true"},
		{"a source change that skips a step or the build matrix", "", "internal/gates/definition.go\x00",
			everyStep, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join("..", "..", "..")
			if tc.gate != "" {
				dir = t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "gate.json"), []byte(tc.gate), 0o600); err != nil {
					t.Fatalf("write gate.json: %v", err)
				}
			}
			t.Chdir(dir)
			var out, log bytes.Buffer
			if err := run(strings.NewReader(tc.paths), &out, &log); err != nil {
				t.Fatalf("run(%q) error = %v, want nil", tc.paths, err)
			}
			if got, want := out.String(), "build="+tc.build+"\n"; got != want {
				t.Fatalf("run(%q) wrote output %q, want %q", tc.paths, got, want)
			}
			if got, want := log.String(), "gatesteps: selected steps "+tc.steps+"\n"; got != want {
				t.Fatalf("run(%q) logged %q, want %q", tc.paths, got, want)
			}
		})
	}

	t.Run("a missing gate.json printed as a selection", func(t *testing.T) {
		// No gate.json is no source of steps, so run fails and the change job is red (fail closed).
		t.Chdir(t.TempDir())
		var out, log bytes.Buffer
		if err := run(strings.NewReader("README.md\x00"), &out, &log); err == nil || out.Len() != 0 || log.Len() != 0 {
			t.Fatalf("run without gate.json: error %v, output %q, log %q; want an error and nothing written",
				err, out.String(), log.String())
		}
	})
}
