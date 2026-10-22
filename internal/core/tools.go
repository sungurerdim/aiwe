package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The pinned tool table and the one acquisition of a check tool (K7, SPEC §12.2). A check needs a
// mutation tool, a vulnerability scanner and a secret scanner. The table pins a tool only when its
// release publishes a binary asset, the sha256 that asset's checksum file states, a licence the
// LICENSE states and the source line of that release. A release with no binary asset is not a row:
// a module zip is not that binary. Nothing is downloaded that the owner did not approve, and no
// byte enters the cache root that does not hash to its pin. A row enters only when its version is
// the release's MAJOR.MINOR.PATCH and its licence is one the LICENSE reader recognises.

const (
	// toolPinsFormatVersion is the one format of the pinned table this reader knows. A table of
	// another format is refused, never read as if it were this one (fail-closed, K7).
	toolPinsFormatVersion = 1
	// maxToolName caps the name of a pinned tool: it names the tool in a message and, for a later
	// reader, in the cache root.
	maxToolName = 64
	// toolDigestHex is the length of the hex sha256 every pin carries.
	toolDigestHex = 64
)

// The two refusals of one acquisition. The text of a sentinel is the name of the outcome its
// condition maps to (SPEC §5.3): neither refusal names a status word of its own, and neither leaves
// the caller able to decide, so both carry the error-class outcome of the table, as the ledger's
// torn-line sentinel does.
var (
	// ErrToolNotApproved refuses a download the owner did not approve. The approval names the tool
	// and the sha256, so an approval of another tool, or of the release before this one, starts
	// nothing (DR-1, DR-16, K7).
	ErrToolNotApproved = errors.New(string(OutcomeError))
	// ErrToolHashMismatch refuses a download whose bytes do not hash to the pin, and a pin that
	// names no sha256 at all: bytes can never match a hash that is not one (K7).
	ErrToolHashMismatch = errors.New(string(OutcomeError))
)

// ToolPin is one row of the pinned tool table: the tool a check needs, the release it comes from,
// the sha256 its artefact must have, and the licence and the source line a reader checks both
// against (SPEC §12.2, K7).
type ToolPin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Licence string `json:"licence"`
	Source  string `json:"source"`
}

// ToolPins is the whole pinned tool table, the document internal/core/tools.json holds.
type ToolPins struct {
	FormatVersion int       `json:"formatVersion"`
	Tools         []ToolPin `json:"tools"`
}

// ToolApproval is the owner's terminal-only approval of one download (DR-1, SPEC §5.7): the tool and
// the sha256 of the bytes the owner approved. A download this approval does not name never starts.
type ToolApproval struct {
	Tool   string
	SHA256 string
}

// ToolFetch downloads the bytes the url of a pin serves. The core imports no network package, so a
// real caller passes the implementation of an *_io.go file (SPEC §5.6). This unit's file set names
// no such file: internal/core/tools_io.go and the terminal-only command that carries the owner's
// approval (SPEC §5.7) are the production wiring a later unit adds, and AcquireTool is its contract.
type ToolFetch func(ctx context.Context, url string) ([]byte, error)

// ToolStore writes data at path, making the directories above it as it needs them. The core names
// the path and the caller's *_io.go file writes the file (SPEC §5.6).
type ToolStore func(path string, data []byte) error

// ParseToolPins reads the pinned tool table and refuses one that cannot vouch for every tool it
// pins: a row that names no name, version, url, sha256, licence or source line, a tool that is not
// one plain name, one tool pinned twice, a sha256 that is not 64 lower-case hex digits, a table of
// another format and a table that pins nothing at all. Every refusal returns the empty table, so a
// caller never acts on a row the parser did not check (DR-6, K7).
func ParseToolPins(b []byte) (ToolPins, error) {
	var pins ToolPins
	if err := json.Unmarshal(b, &pins); err != nil {
		return ToolPins{}, fmt.Errorf("pinned tool table: %w", err)
	}
	if err := checkToolPins(pins); err != nil {
		return ToolPins{}, err
	}
	return pins, nil
}

// checkToolPins refuses a table the reader cannot vouch for, and reports its first broken rule, so a
// reader fixes one pin at a time.
func checkToolPins(pins ToolPins) error {
	if pins.FormatVersion != toolPinsFormatVersion {
		return fmt.Errorf("pinned tool table: formatVersion %d, want %d", pins.FormatVersion, toolPinsFormatVersion)
	}
	if len(pins.Tools) == 0 {
		return errors.New("pinned tool table: it pins no tool, so no check can measure one")
	}
	named := make(map[string]bool, len(pins.Tools))
	for _, pin := range pins.Tools {
		if err := checkToolPin(pin, named); err != nil {
			return err
		}
	}
	return nil
}

// checkToolPin refuses one row: a tool that is not one plain name, a tool the table already pins, a
// version that is not the release's MAJOR.MINOR.PATCH, a licence the LICENSE reader does not
// recognise, a row missing its url or source line, and a sha256 that is not one. named holds the
// tools the rows before this one pinned.
func checkToolPin(pin ToolPin, named map[string]bool) error {
	if !plainName(pin.Name, maxToolName) {
		return fmt.Errorf("pinned tool table: %q is not a tool name of 1 to %d letters, digits, '.', '_' or '-'",
			pin.Name, maxToolName)
	}
	if named[pin.Name] {
		return fmt.Errorf("pinned tool table: it pins %s twice, so a reader cannot tell which release a check runs", pin.Name)
	}
	named[pin.Name] = true
	if !isReleaseVersion(pin.Version) {
		return fmt.Errorf("pinned tool table: %s names version %q, want MAJOR.MINOR.PATCH of the release",
			pin.Name, pin.Version)
	}
	if !knownToolLicence(pin.Licence) {
		return fmt.Errorf("pinned tool table: %s names licence %q, which its LICENSE cannot state",
			pin.Name, pin.Licence)
	}
	for _, f := range []struct{ what, value string }{
		{"url", pin.URL}, {"source line", pin.Source},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("pinned tool table: %s names no %s", pin.Name, f.what)
		}
	}
	if !isToolSHA256(pin.SHA256) {
		return fmt.Errorf("pinned tool table: %s pins %q, want 64 lower-case hex digits of a sha256", pin.Name, pin.SHA256)
	}
	return nil
}

// isReleaseVersion reports whether s is the version a pin may name: MAJOR.MINOR.PATCH, each part a
// decimal number without a leading zero. A tag prefix is not part of the version; it belongs to
// the source line.
func isReleaseVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if !releasePart(p) {
			return false
		}
	}
	return true
}

// releasePart reports whether p is one numeric component of a release version.
func releasePart(p string) bool {
	if p == "" || (len(p) > 1 && p[0] == '0') {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return false
		}
	}
	return true
}

// knownToolLicence reports whether s is an SPDX id the LICENSE reader can confirm. A pin that
// names any other text, including a sentence or a made-up id, is refused: the reader would have
// nothing to match it to (K7).
func knownToolLicence(s string) bool {
	switch s {
	case "Apache-2.0", "MIT":
		return true
	default:
		return false
	}
}

// isToolSHA256 reports whether s is a sha256 in the form a pin holds it: 64 lower-case hex digits.
func isToolSHA256(s string) bool {
	if len(s) != toolDigestHex {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// toolDigest returns the sha256 of data in the hex form a pin holds.
func toolDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReleaseDocs is what a release publishes for one pin: the checksum file that names the asset, and
// the text of the LICENSE at the same tag. ConfirmToolPin is the check a reader runs on those two
// before the row is trusted (K7).
type ReleaseDocs struct {
	Checksums   string
	LicenceText string
}

// ConfirmToolPin refuses a pin whose asset is not in the release checksum file at the pinned
// sha256, whose LICENSE does not state the pinned licence, or whose source line is not the tag of
// the pinned version. The caller supplies the documents of that tag; this function decides, and it
// does no IO (SPEC §5.6).
func ConfirmToolPin(pin ToolPin, docs ReleaseDocs) error {
	if err := checkToolPin(pin, map[string]bool{}); err != nil {
		return err
	}
	asset, err := toolAsset(pin.URL)
	if err != nil {
		return err
	}
	if !strings.HasSuffix(pin.Source, "/releases/tag/v"+pin.Version) {
		return fmt.Errorf("pinned tool table: %s source %q is not the tag of version %s",
			pin.Name, pin.Source, pin.Version)
	}
	if err := checksumMatches(docs.Checksums, asset, pin.SHA256); err != nil {
		return fmt.Errorf("pinned tool table: %s: %w", pin.Name, err)
	}
	got, ok := licenceOf(docs.LicenceText)
	if !ok || got != pin.Licence {
		return fmt.Errorf("pinned tool table: %s names licence %s, the LICENSE states %q",
			pin.Name, pin.Licence, got)
	}
	return nil
}

// toolAsset returns the file name of an https asset URL. The name is the last path segment, so the
// checksum line can be found without taking a host or a query as part of it.
func toolAsset(raw string) (string, error) {
	if !strings.HasPrefix(raw, "https://") {
		return "", fmt.Errorf("pinned tool table: asset url %q is not https", raw)
	}
	rest := strings.TrimPrefix(raw, "https://")
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	slash := strings.LastIndex(rest, "/")
	if slash <= 0 || slash == len(rest)-1 {
		return "", fmt.Errorf("pinned tool table: asset url %q names no file", raw)
	}
	return rest[slash+1:], nil
}

// checksumMatches refuses unless one checksum line names asset with the digest want. A line is a
// 64-hex digest and a file name, the form a release checksum file uses; a leading * on the name is
// the binary-mode marker and is not part of the name.
func checksumMatches(body, asset, want string) error {
	var got string
	for _, line := range strings.Split(body, "\n") {
		sum, name, ok := checksumField(line)
		if !ok || name != asset {
			continue
		}
		if sum == want {
			return nil
		}
		got = sum
	}
	if got == "" {
		return fmt.Errorf("the checksum file does not name %s", asset)
	}
	return fmt.Errorf("%s hashes to %s, the pin says %s", asset, got, want)
}

// checksumField parses one checksum line into its digest and file name.
func checksumField(line string) (string, string, bool) {
	fields := strings.Fields(strings.TrimPrefix(line, "\uFEFF"))
	if len(fields) != 2 || !isToolSHA256(fields[0]) {
		return "", "", false
	}
	return fields[0], strings.TrimPrefix(fields[1], "*"), true
}

// licenceOf reads the licence a LICENSE states. An SPDX-License-Identifier line decides on its
// own: an id this reader knows is that licence, and an id it does not know is not one it can
// confirm. It does not continue into a grant sentence, or a later sentence of another licence
// would still be reported as MIT or Apache-2.0 (K7). With no SPDX line it reads the Apache 2.0
// title, then the MIT grant.
func licenceOf(text string) (string, bool) {
	text = strings.TrimPrefix(text, "\uFEFF")
	if id, ok := spdxLicence(text); ok {
		if knownToolLicence(id) {
			return id, true
		}
		return "", false
	}
	if apacheTitle(text) {
		return "Apache-2.0", true
	}
	const mitGrant = "Permission is hereby granted, free of charge, to any person obtaining a copy"
	if strings.Contains(text, mitGrant) {
		return "MIT", true
	}
	return "", false
}

// apacheTitle reports whether text carries the Apache License 2.0 title as two lines of its own,
// "Apache License" and "Version 2.0, January 2004", the lines a published LICENSE opens with. A
// note that only mentions those words is not the title, so it cannot confirm an Apache-2.0 pin (K7).
func apacheTitle(text string) bool {
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "Apache License" &&
			strings.TrimSpace(lines[i+1]) == "Version 2.0, January 2004" {
			return true
		}
	}
	return false
}

// spdxLicence returns the identifier of a SPDX-License-Identifier line, when the text has one.
func spdxLicence(text string) (string, bool) {
	const mark = "SPDX-License-Identifier:"
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\uFEFF"))
		if !strings.HasPrefix(line, mark) {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(line, mark))
		if i := strings.IndexAny(id, " \t"); i >= 0 {
			id = id[:i]
		}
		return id, id != ""
	}
	return "", false
}

// AcquireTool downloads the tool the pin names into the cache root once the owner approved that
// exact pin, checks the sha256 of the bytes and stores them, returning the path of the stored
// artefact. The network and the file system are injected, so the core keeps none of them (SPEC
// §5.6); the order is the point (DR-1, K7): no fetch without the approval of this pin, and no store
// before the bytes hash to it. The artefact is named by its sha256, so no text of the table makes a
// path, and a changed pin can never read the artefact of the release before it.
func AcquireTool(ctx context.Context, cacheRoot string, pin ToolPin, approval ToolApproval,
	fetch ToolFetch, store ToolStore) (string, error) {
	if approval.Tool != pin.Name || approval.SHA256 != pin.SHA256 {
		return "", toolRefusal(ErrToolNotApproved,
			"the owner's approval of "+pin.Name+" at "+pin.SHA256,
			"no approval of that tool and that sha256",
			"approve the pinned tool in the terminal, then run the command again")
	}
	if !isToolSHA256(pin.SHA256) {
		return "", toolRefusal(ErrToolHashMismatch,
			"a pin that names the sha256 of "+pin.URL,
			fmt.Sprintf("the sha256 %q, which is not 64 lower-case hex digits", pin.SHA256),
			"update the pin to the sha256 of the release its source line names, then run the command again")
	}
	data, err := fetch(ctx, pin.URL)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", pin.URL, err)
	}
	if got := toolDigest(data); got != pin.SHA256 {
		return "", toolRefusal(ErrToolHashMismatch,
			"the bytes of "+pin.URL+" to hash to the pinned "+pin.SHA256,
			"bytes that hash to "+got,
			"check the source line of the pin; if the release changed, update the pin, then run the command again")
	}
	path := filepath.Join(cacheRoot, pin.SHA256)
	if err := store(path, data); err != nil {
		return "", fmt.Errorf("store the verified tool at %s: %w", path, err)
	}
	return path, nil
}

// toolRefusal returns the refusal of one acquisition: the error-class outcome of the table with the
// condition, and the sentinel the caller matches with errors.Is (SPEC §5.3).
func toolRefusal(sentinel error, expected, found, next string) error {
	return &Error{Outcome: OutcomeError, Expected: expected, Found: found, Next: next, Err: sentinel}
}
