package repocheck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// rulesSection is a §12.1 section in the shape of docs/SPEC.md: one sentence that lists the
// repository files, with parentheses that explain some of them, then sentences that name what does
// not exist. It uses the stand-in product name of the other fixtures.
func rulesSection() []string {
	return []string{
		"### 12.1 Repository files",
		"`LICENSE` (MIT, Q1) · `README.md` · `CONTRIBUTING.md` · `SECURITY.md` (private vulnerability reporting once public,",
		"L9; the risk of §6.7) · `CODE_OF_CONDUCT.md` (a link to Contributor Covenant 3.0, §17; conduct reports go to the",
		"maintainer on GitHub) · `go.mod`, `go.sum` · `tools/go.mod`, `tools/go.sum` (L4) · `gate.json` · `.golangci.yml`,",
		"`.custom-gcl.yml` · `.goreleaser.yaml` · `.githooks/commit-msg` · `.gitattributes` (LF, DR-18) · `.gitignore`",
		"(`/dist/`) · `.github/dependabot.yml` (`gomod`, `github-actions`) · `.github/workflows/ci.yml` (the only workflow). No",
		"issue form or pull-request template (DR-16); no `CHANGELOG.md`.",
		"",
		"`README.md`, in order: the first paragraph of §1.1 verbatim; `zentra adapters list`.",
	}
}

// rulesSpec is a spec with the §12.1 section and no table: the rules check needs nothing else.
func rulesSpec() string {
	return strings.Join(joinLines(
		[]string{"# zentra — specification", "", "## 12. Open source, versioning and release"},
		rulesSection(),
		[]string{"### 12.2 Gate, tool chain and CI", "", "**Gate**: `gate.json`.", ""},
	), "\n")
}

// oldTablesSpec is rulesSpec plus the tables an earlier rules check read: a §3 table with an empty
// Serves cell and a test whose source exists, a §12.2 step gate.json lacks, and a delivery-plan Files cell.
func oldTablesSpec() string {
	return strings.Join(joinLines(
		[]string{
			"# zentra — specification", "",
			"## 3. Design rules", "",
			"| # | Rule | Serves | Enforcement |", "|---|---|---|---|",
			"| DR-1 | The engine runs unattended. |   | test `engine/lifecycle_test.go` |", "",
			"## 12. Open source, versioning and release",
		},
		rulesSection(),
		[]string{
			"### 12.2 Gate, tool chain and CI", "",
			"| Step | Command | Catches |", "|---|---|---|",
			"| `vuln` | `govulncheck ./...` | known vulnerabilities |", "",
			"## 14. Delivery plan", "",
			"| Unit | Goal | Files | Verify |", "|---|---|---|---|",
			"| 0.5 | Text stays byte-exact. | `core/text.go` | `go test ./internal/core/` |", "",
		},
	), "\n")
}

// joinLines joins the line groups in order.
func joinLines(groups ...[]string) []string {
	var all []string
	for _, g := range groups {
		all = append(all, g...)
	}
	return all
}

// rulesRepoFiles are the §12.1 files of rulesSection.
func rulesRepoFiles() []string {
	return []string{
		"LICENSE", "README.md", "CONTRIBUTING.md", "SECURITY.md", "CODE_OF_CONDUCT.md",
		"go.mod", "go.sum", "tools/go.mod", "tools/go.sum", "gate.json", ".golangci.yml",
		".custom-gcl.yml", ".goreleaser.yaml", ".githooks/commit-msg", ".gitattributes", ".gitignore",
		".github/dependabot.yml", ".github/workflows/ci.yml",
	}
}

// rulesRepoSpec writes a repository with spec as docs/SPEC.md and every §12.1 file except those
// in skip. The four files a standard reads carry the text that standard asks for, so a case that
// tests the file list is not answered by the text rules; the rest carry a one-line body.
func rulesRepoSpec(t *testing.T, spec string, skip map[string]bool) string {
	t.Helper()
	dir := t.TempDir()
	writeRulesFile(t, dir, "docs/SPEC.md", spec)
	for _, f := range rulesRepoFiles() {
		if !skip[f] {
			writeRulesFile(t, dir, f, rulesFileBody(f))
		}
	}
	return dir
}

// rulesFileBody is the body the fixture writes for rel: the statement a standard names for the four
// files one reads, and a one-line body for the rest.
func rulesFileBody(rel string) string {
	switch rel {
	case "LICENSE":
		return mitLicence
	case "CODE_OF_CONDUCT.md":
		return conductText
	case "SECURITY.md":
		return securityText
	case "go.mod":
		return goModText
	}
	return "fixture\n"
}

// writeRulesFile writes body to the slash-separated, repository-relative file rel of dir.
func writeRulesFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// rulesSpecWithout returns rulesSpec with its §12.1 section replaced by body.
func rulesSpecWithout(body ...string) string {
	spec := rulesSpec()
	i := strings.Index(spec, "### 12.1")
	j := strings.Index(spec, "### 12.2")
	repl := ""
	if len(body) > 0 {
		repl = strings.Join(body, "\n") + "\n\n"
	}
	return spec[:i] + repl + spec[j:]
}

func TestRules(t *testing.T) {
	t.Run("a SPEC without the old tables passes", func(t *testing.T) {
		// Guards against a rules check that still needs the §3, §12.2 or delivery-plan tables: a spec that
		// has only the §12.1 list, in a repository that holds every listed file, is green.
		got, err := repocheck.Rules(rulesRepoSpec(t, rulesSpec(), nil))
		if err != nil || len(got) != 0 {
			t.Fatalf("Rules = %+v, %v; want no finding and no error", got, err)
		}
	})

	t.Run("the old tables are not read", func(t *testing.T) {
		// Guards against a check that still parses a table: an empty Serves cell, a named test
		// that is absent and a gate step gate.json lacks are not findings of this check.
		got, err := repocheck.Rules(rulesRepoSpec(t, oldTablesSpec(), nil))
		if err != nil || len(got) != 0 {
			t.Fatalf("Rules = %+v, %v; want no finding and no error", got, err)
		}
	})

	t.Run("a repository file of §12.1 missing", func(t *testing.T) {
		skip := map[string]bool{"SECURITY.md": true, "tools/go.sum": true, ".github/workflows/ci.yml": true}
		got, err := repocheck.Rules(rulesRepoSpec(t, rulesSpec(), skip))
		if err != nil {
			t.Fatalf("Rules error = %v, want nil", err)
		}
		if len(got) != len(skip) {
			t.Errorf("Rules = %+v, want exactly %d findings", got, len(skip))
		}
		for f := range skip {
			if !hasFinding(got, "missing_repo_file", f) {
				t.Errorf("Rules = %+v, want a missing_repo_file finding for %s", got, f)
			}
		}
	})

	t.Run("a name outside the file list is not required", func(t *testing.T) {
		// Guards against reading every backticked name as a file: CHANGELOG.md is named only to say
		// it does not exist, /dist/ is an ignore pattern, gomod is an ecosystem name, and the
		// paragraph after the list names a command.
		got, err := repocheck.Rules(rulesRepoSpec(t, rulesSpec(), nil))
		if err != nil {
			t.Fatalf("Rules error = %v, want nil", err)
		}
		for _, name := range []string{"CHANGELOG.md", "/dist/", "gomod", "github-actions", "zentra adapters list"} {
			if hasFinding(got, "missing_repo_file", name) {
				t.Errorf("Rules = %+v, want no finding for %s", got, name)
			}
		}
	})

	t.Run("a full stop inside the list does not end it", func(t *testing.T) {
		// Guards against a list read only up to its first full stop: an abbreviation such as
		// "e.g." ends the first sentence early, yet the files after it in the same paragraph are
		// still required, so a deleted workflow file is a finding, not a pass.
		spec := rulesSpecWithout(
			"### 12.1 Repository files",
			"`LICENSE` e.g. `README.md` · `go.mod`. See also `.github/workflows/ci.yml` (the only",
			"workflow). No issue form; no `CHANGELOG.md`.",
			"",
			"`README.md`, in order: `zentra adapters list`.",
		)
		skip := map[string]bool{"go.mod": true, ".github/workflows/ci.yml": true}
		got, err := repocheck.Rules(rulesRepoSpec(t, spec, skip))
		if err != nil {
			t.Fatalf("Rules error = %v, want nil", err)
		}
		if len(got) != len(skip) {
			t.Errorf("Rules = %+v, want exactly %d findings", got, len(skip))
		}
		for f := range skip {
			if !hasFinding(got, "missing_repo_file", f) {
				t.Errorf("Rules = %+v, want a missing_repo_file finding for %s", got, f)
			}
		}
	})

	t.Run("a spec without a file list is an error, not a pass", func(t *testing.T) {
		// Guards against absence read as evidence (DR-6): a §12.1 that Rules cannot find, or one
		// that names no file, means the file rule was not checked.
		broken := []struct{ name, spec string }{
			{"no section at all", "# spec\n"},
			{"no §12.1", rulesSpecWithout()},
			{"§12.1 names no file", rulesSpecWithout("### 12.1 Repository files", "", "Industry standard files only.")},
		}
		for _, tc := range broken {
			t.Run(tc.name, func(t *testing.T) {
				got, err := repocheck.Rules(rulesRepoSpec(t, tc.spec, nil))
				if err == nil {
					t.Fatalf("Rules(%s) = %+v, nil error; want an error", tc.name, got)
				}
			})
		}
	})

	t.Run("a repository without docs/SPEC.md is an error", func(t *testing.T) {
		// Guards against absence read as evidence (DR-6): no spec means no rule was checked.
		got, err := repocheck.Rules(t.TempDir())
		if err == nil {
			t.Fatalf("Rules(no spec) = %+v, nil error; want an error", got)
		}
	})
}

// mitLicence is the licence text this repository ships (Q1, §12.1): the MIT grant, so a file that
// only exists is not enough.
const mitLicence = `MIT License

Copyright (c) 2026 Fixture Owner

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`

// conductText is a conduct file that meets STD-2: it links Contributor Covenant 3.0.
const conductText = `# Code of conduct

This project follows the
[Contributor Covenant, version 3.0](https://www.contributor-covenant.org/version/3/0/code_of_conduct/).

Report unacceptable behaviour privately to the maintainer on GitHub.
`

// securityText is a security policy that meets STD-29: it names the private reporting channel.
const securityText = `# Security policy

Report a vulnerability privately through this repository's **Security** tab →
**Report a vulnerability**. Do not open a public issue.
`

// goModText is a go.mod that meets STD-44: its module metadata carries the licence identifier.
const goModText = `// SPDX-License-Identifier: MIT

module github.com/fixture/zentra

go 1.27.3

require golang.org/x/sys v0.49.0
`

// rulesRepoContent writes a repository whose four read-checked files carry the text each standard
// names: the licence grant (STD-1), the covenant link (STD-2), the private channel (STD-29) and the
// module's SPDX identifier (STD-44).
func rulesRepoContent(t *testing.T) string {
	t.Helper()
	dir := rulesRepoSpec(t, rulesSpec(), nil)
	writeRulesFile(t, dir, "LICENSE", mitLicence)
	writeRulesFile(t, dir, "CODE_OF_CONDUCT.md", conductText)
	writeRulesFile(t, dir, "SECURITY.md", securityText)
	writeRulesFile(t, dir, "go.mod", goModText)
	return dir
}

func TestRulesContent(t *testing.T) {
	t.Run("the fixture's own texts pass", func(t *testing.T) {
		// Guards the cases below: they fail the text checks, not the shape of the fixture.
		got, err := repocheck.Rules(rulesRepoContent(t))
		if err != nil || len(got) != 0 {
			t.Fatalf("Rules(full text) = %+v, %v; want no finding and no error", got, err)
		}
	})

	t.Run("a replaced licence text is red", func(t *testing.T) {
		// Guards STD-1: the file exists and says something else, so the project is not MIT and a
		// reader of the repository is misled about what they may do with it.
		for _, tc := range []struct{ name, body string }{
			{"the Apache 2.0 heading", "Apache License\nVersion 2.0, January 2004\n"},
			{"a bare title", "MIT License\n"},
			{"an empty file", ""},
			{"the grant with the disclaimer removed", strings.Split(mitLicence, "THE SOFTWARE IS PROVIDED")[0]},
		} {
			dir := rulesRepoContent(t)
			writeRulesFile(t, dir, "LICENSE", tc.body)
			got, err := repocheck.Rules(dir)
			if err != nil {
				t.Fatalf("Rules(%s): %v", tc.name, err)
			}
			if !hasFinding(got, "wrong_licence_text", "LICENSE") {
				t.Errorf("Rules(%s) = %+v, want a wrong_licence_text finding on LICENSE", tc.name, got)
			}
		}
	})

	t.Run("a conduct file without the covenant link is red", func(t *testing.T) {
		// Guards STD-2: a conduct file that names no version leaves a reporter with no text to read.
		for _, tc := range []struct{ name, body string }{
			{"no link at all", "# Code of conduct\n\nBe nice to each other.\n"},
			{"the 2.0 covenant", "# Code of conduct\n\n[Contributor Covenant 2.0](https://www.contributor-covenant.org/version/2/0/code_of_conduct/)\n"},
		} {
			dir := rulesRepoContent(t)
			writeRulesFile(t, dir, "CODE_OF_CONDUCT.md", tc.body)
			got, err := repocheck.Rules(dir)
			if err != nil {
				t.Fatalf("Rules(%s): %v", tc.name, err)
			}
			if !hasFinding(got, "unstated_standard", "CODE_OF_CONDUCT.md") {
				t.Errorf("Rules(%s) = %+v, want an unstated_standard finding", tc.name, got)
			}
		}
	})

	t.Run("a security policy without the private channel is red", func(t *testing.T) {
		// Guards STD-29: a policy that only sends the reporter to a public issue publishes the
		// vulnerability it was meant to keep private.
		for _, tc := range []struct{ name, body string }{
			{"a public issue only", "# Security policy\n\nOpen an issue and we will look at it.\n"},
			{"an empty file", ""},
		} {
			dir := rulesRepoContent(t)
			writeRulesFile(t, dir, "SECURITY.md", tc.body)
			got, err := repocheck.Rules(dir)
			if err != nil {
				t.Fatalf("Rules(%s): %v", tc.name, err)
			}
			if !hasFinding(got, "unstated_standard", "SECURITY.md") {
				t.Errorf("Rules(%s) = %+v, want an unstated_standard finding", tc.name, got)
			}
		}
	})

	t.Run("a licence that names another SPDX identifier is red", func(t *testing.T) {
		// Guards STD-44: an identifier next to the grant that names another licence tells a consumer
		// a permission the licence text does not give.
		for _, tc := range []struct{ name, body string }{
			{"the Apache identifier", strings.Replace(mitLicence, "MIT License\n", "MIT License\n\nSPDX-License-Identifier: Apache-2.0\n", 1)},
			{"a dual licence", strings.Replace(mitLicence, "MIT License\n", "MIT License\n\nSPDX-License-Identifier: MIT OR Apache-2.0\n", 1)},
			{"the BSD identifier", strings.Replace(mitLicence, "MIT License\n", "MIT License\n\nSPDX-License-Identifier: BSD-3-Clause\n", 1)},
		} {
			dir := rulesRepoContent(t)
			writeRulesFile(t, dir, "LICENSE", tc.body)
			got, err := repocheck.Rules(dir)
			if err != nil {
				t.Fatalf("Rules(%s): %v", tc.name, err)
			}
			if !hasFinding(got, "wrong_spdx_identifier", "LICENSE") {
				t.Errorf("Rules(%s) = %+v, want a wrong_spdx_identifier finding on LICENSE", tc.name, got)
			}
		}
	})

	t.Run("the identifier matching the licence passes", func(t *testing.T) {
		// Guards the other half of STD-44: the line this repository will carry is accepted, so the
		// rule does not redden the remedy it asks for.
		dir := rulesRepoContent(t)
		writeRulesFile(t, dir, "LICENSE",
			strings.Replace(mitLicence, "MIT License\n", "MIT License\n\nSPDX-License-Identifier: MIT\n", 1))
		got, err := repocheck.Rules(dir)
		if err != nil || len(got) != 0 {
			t.Fatalf("Rules(SPDX-License-Identifier: MIT) = %+v, %v; want no finding", got, err)
		}
	})

	t.Run("a missing file stays red with its old finding", func(t *testing.T) {
		// Guards the check this unit keeps: the text rules never replace the existence rule.
		dir := rulesRepoContent(t)
		if err := os.Remove(filepath.Join(dir, "LICENSE")); err != nil {
			t.Fatalf("remove LICENSE: %v", err)
		}
		got, err := repocheck.Rules(dir)
		if err != nil {
			t.Fatalf("Rules(no LICENSE): %v", err)
		}
		if !hasFinding(got, "missing_repo_file", "LICENSE") {
			t.Fatalf("Rules(no LICENSE) = %+v, want a missing_repo_file finding", got)
		}
		for _, f := range got {
			if f.Rule != "missing_repo_file" {
				t.Errorf("Rules(no LICENSE) also reported %+v; an absent file has no text to judge", f)
			}
		}
	})
}
