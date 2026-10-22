package core_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The tests below pin the contract of internal/core/tools.go, the file a later change adds (K7).
// An acquisition has one entry point, core.AcquireTool, which the tests call with the pin, the
// owner approval and the two IO callbacks the core is not allowed to import itself (SPEC §5.6):
// a fetch (network) and a store (cache root). A real caller passes the implementations of an
// *_io.go file, which the unit's file set does not name yet.

// toolTestArtefact is the downloaded body of one pinned tool, as the fetcher returns it. It is a
// function, not a package-level variable: SPEC §5.3 allows no package-level variable here
// (internal/repocheck rule `package_var`).
func toolTestArtefact() []byte {
	return []byte("gitleaks 8.28.0 linux x64, one release artefact\n")
}

// toolTestDigest returns the hex sha256 of b, the form a pin holds.
func toolTestDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// toolTestDoc returns a one-row pinned table that names tool and is complete in every other field,
// so a test varies one field at a time and the parser must refuse it for that field alone.
func toolTestDoc(tool string) string {
	return toolTestFields(tool, "1.0.0", "MIT")
}

// toolTestFields is toolTestDoc with the version and the licence the case is about.
func toolTestFields(tool, version, licence string) string {
	return `{"formatVersion":1,"tools":[{"name":` + strconv.Quote(tool) +
		`,"version":` + strconv.Quote(version) +
		`,"url":"https://example.invalid/t.tar.gz","sha256":"` +
		toolTestDigest(toolTestArtefact()) +
		`","licence":` + strconv.Quote(licence) +
		`,"source":"https://example.invalid/t"}]}`
}

// toolTestTable is a complete three-row table for the parser. It is not the shipped pin table:
// its digest is the sha256 of the test artefact, and the shipped digests are checked against the
// published releases in TestToolsManifestPinsEveryCheckTool.
func toolTestTable() string {
	row := func(name string) string {
		return `{"name":` + strconv.Quote(name) + `,"version":"1.0.0","url":"https://example.invalid/` + name +
			`.tar.gz","sha256":"` + toolTestDigest(toolTestArtefact()) +
			`","licence":"MIT","source":"https://example.invalid/` + name + `"}`
	}
	return `{"formatVersion":1,"tools":[` + row("gremlins") + `,` + row("govulncheck") + `,` + row("gitleaks") + `]}`
}

// toolTestPin returns the pin of toolTestArtefact: the row a matching hash accepts.
func toolTestPin() core.ToolPin {
	return core.ToolPin{
		Name:    "gitleaks",
		Version: "8.28.0",
		URL:     "https://github.com/gitleaks/gitleaks/releases/download/v8.28.0/gitleaks_8.28.0_linux_x64.tar.gz",
		SHA256:  toolTestDigest(toolTestArtefact()),
		Licence: "MIT",
		Source:  "https://github.com/gitleaks/gitleaks/releases/tag/v8.28.0",
	}
}

// toolTestIO is the injected IO of one acquisition: it records every fetch and store it is asked
// for, serves toolTestArtefact, and writes a stored artefact where the core names it.
type toolTestIO struct {
	fetched, stored []string
	artefact        []byte
}

func (io *toolTestIO) fetch(_ context.Context, url string) ([]byte, error) {
	io.fetched = append(io.fetched, url)
	return io.artefact, nil
}

func (io *toolTestIO) store(path string, data []byte) error {
	io.stored = append(io.stored, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// toolTestCacheFiles returns every file under the cache root, so a test proves what landed there.
func toolTestCacheFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the cache root: %v", err)
	}
	return files
}

// toolTestCheckPin fails when a row does not pin a tool: a name, a version, a URL, a sha256 in the
// 64 hex digits every sha256 has, the licence and the source line its version was checked against.
func toolTestCheckPin(t *testing.T, pin core.ToolPin) {
	t.Helper()
	fields := []struct{ name, value string }{
		{"name", pin.Name}, {"version", pin.Version}, {"url", pin.URL},
		{"sha256", pin.SHA256}, {"licence", pin.Licence}, {"source", pin.Source},
	}
	for _, f := range fields {
		if f.value == "" {
			t.Errorf("the pin of %q is missing its %s", pin.Name, f.name)
		}
	}
	if len(pin.SHA256) != 64 || strings.Trim(pin.SHA256, "0123456789abcdef") != "" {
		t.Errorf("the pin of %q carries the sha256 %q, want 64 lower-case hex digits", pin.Name, pin.SHA256)
	}
}

// TestAcquireToolRefusesHashMismatch catches a download whose bytes do not match the pinned sha256
// and is run anyway: the mismatch must refuse the tool, reach neither the store nor the cache root
// (Done criterion, K7).
func TestAcquireToolRefusesHashMismatch(t *testing.T) {
	ctx := context.Background()
	pin := toolTestPin()
	// The URL now serves bytes other than the ones the pin hashes, as after an upstream rebuild.
	pin.SHA256 = toolTestDigest([]byte("the same URL after the release was rebuilt"))
	io := &toolTestIO{artefact: toolTestArtefact()}
	cache := t.TempDir()

	path, err := core.AcquireTool(ctx, cache, pin, core.ToolApproval{Tool: pin.Name, SHA256: pin.SHA256}, io.fetch, io.store)
	if !errors.Is(err, core.ErrToolHashMismatch) {
		t.Fatalf("AcquireTool with a mismatched sha256 = %v, want %v", err, core.ErrToolHashMismatch)
	}
	if path != "" {
		t.Errorf("AcquireTool returned the path %q with a mismatched sha256, want no path", path)
	}
	if len(io.fetched) != 1 {
		t.Errorf("the fetcher ran %d times, want 1: the download did start", len(io.fetched))
	}
	if len(io.stored) != 0 {
		t.Errorf("the storer ran %d times, want 0: a mismatched binary never enters the cache", len(io.stored))
	}
	if files := toolTestCacheFiles(t, cache); len(files) != 0 {
		t.Errorf("the cache root holds %v, want no file", files)
	}
}

// TestAcquireToolRefusesAPinWithoutASHA256 catches a pin that names a sha256 which is not one: even
// an approval the pin matches must not start a download of bytes that can never match a hash that
// is not one. The mismatch is refused before the fetch, and nothing reaches the store or the cache
// root (K7).
func TestAcquireToolRefusesAPinWithoutASHA256(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		sha256  string
		catches string
	}{
		{"an empty sha256", "", "a pin whose sha256 is empty"},
		{"a sha256 that is too short", "abc123", "a pin whose sha256 is not 64 hex digits"},
		{"an upper-case sha256", strings.Repeat("A", 64), "a pin whose sha256 is not lower-case"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pin := toolTestPin()
			pin.SHA256 = tc.sha256
			io := &toolTestIO{artefact: toolTestArtefact()}
			cache := t.TempDir()

			path, err := core.AcquireTool(ctx, cache, pin, core.ToolApproval{Tool: pin.Name, SHA256: pin.SHA256}, io.fetch, io.store)
			if !errors.Is(err, core.ErrToolHashMismatch) {
				t.Fatalf("%s: AcquireTool = %v, want %v", tc.catches, err, core.ErrToolHashMismatch)
			}
			if path != "" {
				t.Errorf("AcquireTool returned the path %q for %s, want no path", path, tc.catches)
			}
			if len(io.fetched) != 0 {
				t.Errorf("the fetcher ran %d times for %s, want 0: the pin was refused before the download", len(io.fetched), tc.catches)
			}
			if len(io.stored) != 0 {
				t.Errorf("the storer ran %d times for %s, want 0", len(io.stored), tc.catches)
			}
			if files := toolTestCacheFiles(t, cache); len(files) != 0 {
				t.Errorf("the cache root holds %v for %s, want no file", files, tc.catches)
			}
		})
	}
}

// TestAcquireToolRefusesWithoutOwnerApproval catches a download that starts before the owner
// approved it, or one an approval for another tool or another sha256 waves through: without the
// approval of this pin the fetcher and the store must never run (DR-1, DR-16, K7).
func TestAcquireToolRefusesWithoutOwnerApproval(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		approve func(pin core.ToolPin) core.ToolApproval
		catches string
	}{
		{"no approval", func(core.ToolPin) core.ToolApproval { return core.ToolApproval{} },
			"a download that starts with no owner approval"},
		{"another tool approved", func(pin core.ToolPin) core.ToolApproval {
			return core.ToolApproval{Tool: "gremlins", SHA256: pin.SHA256}
		}, "a download an approval for another tool starts"},
		{"another sha256 approved", func(pin core.ToolPin) core.ToolApproval {
			return core.ToolApproval{Tool: pin.Name, SHA256: toolTestDigest([]byte("an older release"))}
		}, "a download an approval for another sha256 starts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pin := toolTestPin()
			io := &toolTestIO{artefact: toolTestArtefact()}
			cache := t.TempDir()

			path, err := core.AcquireTool(ctx, cache, pin, tc.approve(pin), io.fetch, io.store)
			if !errors.Is(err, core.ErrToolNotApproved) {
				t.Fatalf("%s: AcquireTool = %v, want %v", tc.catches, err, core.ErrToolNotApproved)
			}
			if path != "" {
				t.Errorf("AcquireTool returned the path %q without the owner approval, want no path", path)
			}
			if len(io.fetched) != 0 {
				t.Errorf("the fetcher ran %d times without the owner approval, want 0: no download starts", len(io.fetched))
			}
			if len(io.stored) != 0 {
				t.Errorf("the storer ran %d times without the owner approval, want 0", len(io.stored))
			}
			if files := toolTestCacheFiles(t, cache); len(files) != 0 {
				t.Errorf("the cache root holds %v, want no file", files)
			}
		})
	}
}

// TestAcquireToolStoresVerifiedArtefact catches a verified download that is dropped, stored under a
// path outside the cache root, or stored with bytes other than the fetched ones (Done criterion,
// K7).
func TestAcquireToolStoresVerifiedArtefact(t *testing.T) {
	ctx := context.Background()
	pin := toolTestPin()
	io := &toolTestIO{artefact: toolTestArtefact()}
	cache := t.TempDir()

	path, err := core.AcquireTool(ctx, cache, pin, core.ToolApproval{Tool: pin.Name, SHA256: pin.SHA256}, io.fetch, io.store)
	if err != nil {
		t.Fatalf("AcquireTool = %v, want nil", err)
	}
	if len(io.fetched) != 1 || io.fetched[0] != pin.URL {
		t.Errorf("the fetcher ran for %v, want exactly the pinned URL %q", io.fetched, pin.URL)
	}
	if len(io.stored) != 1 || io.stored[0] != path {
		t.Fatalf("the storer ran for %v while the path is %q, want the same one path", io.stored, path)
	}
	if !strings.HasPrefix(path, cache+string(filepath.Separator)) {
		t.Fatalf("the artefact path %q is outside the cache root %q", path, cache)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the stored artefact: %v", err)
	}
	if string(got) != string(toolTestArtefact()) {
		t.Errorf("the stored artefact = %q, want the fetched %q", got, toolTestArtefact())
	}
	if toolTestDigest(got) != pin.SHA256 {
		t.Errorf("the stored artefact hashes to %s, want the pinned %s", toolTestDigest(got), pin.SHA256)
	}
}

// TestParseToolPins catches a pinned table that is accepted with a row missing its name, version,
// URL, sha256, licence or source line, with a digest that is not a sha256, with one tool pinned
// twice, with no formatVersion, with no tool at all, or with a document that is not the JSON of a
// table: the parser is fail-closed and returns no partial table (DR-6, K7).
func TestParseToolPins(t *testing.T) {
	t.Run("reads the pinned table", func(t *testing.T) {
		pins, err := core.ParseToolPins([]byte(toolTestTable()))
		if err != nil {
			t.Fatalf("ParseToolPins = %v, want nil", err)
		}
		if pins.FormatVersion != 1 {
			t.Errorf("FormatVersion = %d, want 1", pins.FormatVersion)
		}
		if len(pins.Tools) != 3 {
			t.Fatalf("the table holds %d rows, want 3: a mutation tool, a vulnerability scanner and a secret scanner", len(pins.Tools))
		}
		for _, pin := range pins.Tools {
			toolTestCheckPin(t, pin)
		}
	})

	sum := toolTestDigest(toolTestArtefact())
	other := toolTestDigest([]byte("another release artefact"))
	broken := []struct {
		name    string
		doc     string
		catches string
	}{
		{"row without a sha256",
			`{"formatVersion":1,"tools":[{"name":"gitleaks","version":"8.28.0","url":"https://example.invalid/gitleaks.tar.gz","licence":"MIT","source":"https://example.invalid/gitleaks"}]}`,
			"a row that names no sha256"},
		{"sha256 that is not a sha256",
			`{"formatVersion":1,"tools":[{"name":"gitleaks","version":"8.28.0","url":"https://example.invalid/gitleaks.tar.gz","sha256":"abc123","licence":"MIT","source":"https://example.invalid/gitleaks"}]}`,
			"a row whose sha256 is not 64 hex digits"},
		{"row without a source line",
			`{"formatVersion":1,"tools":[{"name":"gitleaks","version":"8.28.0","url":"https://example.invalid/gitleaks.tar.gz","sha256":"` + sum + `","licence":"MIT"}]}`,
			"a pin with no source line"},
		{"two rows with one name",
			`{"formatVersion":1,"tools":[{"name":"gitleaks","version":"8.28.0","url":"https://example.invalid/a.tar.gz","sha256":"` + sum + `","licence":"MIT","source":"https://example.invalid/a"},{"name":"gitleaks","version":"8.27.0","url":"https://example.invalid/b.tar.gz","sha256":"` + other + `","licence":"MIT","source":"https://example.invalid/b"}]}`,
			"a table that pins one tool twice"},
		{"document without a formatVersion",
			`{"tools":[{"name":"gitleaks","version":"8.28.0","url":"https://example.invalid/gitleaks.tar.gz","sha256":"` + sum + `","licence":"MIT","source":"https://example.invalid/gitleaks"}]}`,
			"a table with no formatVersion"},
		{"table that pins no tool",
			`{"formatVersion":1,"tools":[]}`,
			"a table that pins no tool, so no check can measure one"},
		{"document that is not the JSON of a table",
			`{"formatVersion":1,"tools":`,
			"a document that is not the JSON of a table"},
		{"tool name that is empty", toolTestDoc(""), "a tool with no name"},
		{"tool name over the cap", toolTestDoc(strings.Repeat("a", 65)), "a tool name over the cap"},
		{"tool name with a non-ASCII byte", toolTestDoc("gremlins\u00e9"), "a tool name with a non-ASCII byte"},
		{"tool name Windows strips to another", toolTestDoc("gremlins."), "a tool name that ends in a dot"},
		{"licence that is not one", toolTestFields("gitleaks", "1.0.0", "not-a-licence"),
			"a licence such as not-a-licence"},
		{"version that is not a release", toolTestFields("gitleaks", "not-a-version", "MIT"),
			"a version that is not MAJOR.MINOR.PATCH"},
	}
	for _, tc := range broken {
		t.Run(tc.name, func(t *testing.T) {
			pins, err := core.ParseToolPins([]byte(tc.doc))
			if err == nil {
				t.Fatalf("%s is accepted, want a refusal", tc.catches)
			}
			if len(pins.Tools) != 0 || pins.FormatVersion != 0 {
				t.Errorf("%s returned the partial table %+v, want the empty one (fail-closed)", tc.catches, pins)
			}
		})
	}
}

// toolTestReleaseDocs is the checksum file and the LICENSE text of one local release.
func toolTestReleaseDocs(pin core.ToolPin, sum, licence string) core.ReleaseDocs {
	asset := pin.URL[strings.LastIndex(pin.URL, "/")+1:]
	return core.ReleaseDocs{Checksums: sum + "  " + asset + "\n", LicenceText: licence}
}

// TestConfirmToolPinRefusesAReleaseThatDisagrees catches a pin the checksum file or the LICENSE
// does not state (K7). The documents are local, so the case does not copy a shipped pin.
func TestConfirmToolPinRefusesAReleaseThatDisagrees(t *testing.T) {
	pin := toolTestPin()
	match := toolTestDigest(toolTestArtefact())
	mit := "Permission is hereby granted, free of charge, to any person obtaining a copy of this software.\n"
	apache := "Apache License\nVersion 2.0, January 2004\n"
	rebuilt := toolTestReleaseDocs(pin, toolTestDigest([]byte("the release was rebuilt")), mit)
	if err := core.ConfirmToolPin(pin, rebuilt); err == nil {
		t.Fatal("a checksum of another artefact is accepted, want a refusal")
	}
	if err := core.ConfirmToolPin(pin, toolTestReleaseDocs(pin, match, apache)); err == nil {
		t.Fatal("a licence the LICENSE does not state is accepted, want a refusal")
	}
	// The two title words in another sentence are not the Apache title, so they must not confirm
	// an Apache-2.0 pin (K7). The header above is that title; this note only mentions it.
	mention := "These notes mention the Apache License and say the draft is Version 2.0.\n"
	apachePin := pin
	apachePin.Licence = "Apache-2.0"
	if err := core.ConfirmToolPin(apachePin, toolTestReleaseDocs(apachePin, match, mention)); err == nil {
		t.Fatal("a note that only mentions Apache License and Version 2.0 is accepted, want a refusal")
	}
	if err := core.ConfirmToolPin(apachePin, toolTestReleaseDocs(apachePin, match, apache)); err != nil {
		t.Fatalf("ConfirmToolPin on the Apache title = %v, want nil", err)
	}
	// An unknown SPDX id must not fall through to a later MIT or Apache grant (K7).
	refused := "SPDX-License-Identifier: GPL-3.0-only\n" + mit + "\n" + apache
	if err := core.ConfirmToolPin(pin, toolTestReleaseDocs(pin, match, refused)); err == nil {
		t.Fatal("an unknown SPDX id followed by a known grant is accepted, want a refusal")
	}
	if err := core.ConfirmToolPin(pin, toolTestReleaseDocs(pin, match, mit)); err != nil {
		t.Fatalf("ConfirmToolPin on the release's own documents = %v, want nil", err)
	}
}

// TestToolsManifestPinsEveryCheckTool catches a pin table that omits a check tool or that the
// parser refuses: a mutation tool, a vulnerability scanner and a secret scanner (DR-6, K7).
// The live checksum and LICENSE read sits in tools_smoke_test.go (SPEC §5.13).
func TestToolsManifestPinsEveryCheckTool(t *testing.T) {
	data, err := os.ReadFile("tools.json")
	if err != nil {
		t.Fatalf("read the pinned tool table: %v", err)
	}
	pins, err := core.ParseToolPins(data)
	if err != nil {
		t.Fatalf("ParseToolPins(tools.json) = %v, want nil", err)
	}
	seen := map[string]bool{}
	for _, pin := range pins.Tools {
		seen[pin.Name] = true
		toolTestCheckPin(t, pin)
	}
	for _, name := range []string{"gremlins", "grype", "gitleaks"} {
		if !seen[name] {
			t.Errorf("tools.json does not pin %s", name)
		}
	}
}
