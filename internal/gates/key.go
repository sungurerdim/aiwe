package gates

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Fingerprint is the environment a check ran in (SPEC §7.8): the OS, the architecture, the output
// of the toolchain probe and the digests of the lock files.
type Fingerprint struct {
	OS          string
	Arch        string
	ToolVersion string
	LockDigests []string
}

// KeyInput is everything that identifies one check (SPEC §7.8): the argv the step runs, the digest
// of the step's declared inputs from git objects — the whole tracked tree when the step declares
// none — and the env fingerprint. Neither the step's id nor the directory a check runs in is part
// of it, so the same tree in a fresh clone (§6.5) hits the cache instead of running the check again.
type KeyInput struct {
	Argv         []string
	InputsDigest string
	Fingerprint  Fingerprint
}

// CheckKey returns the sha256 hex digest of in. The same inputs return the same key; a change to
// the argv, the tree digest or any fingerprint field returns another key, so a stored result is
// never served for a changed tree or a changed tool (DR-8, SPEC §7.8). The step's id is not part of
// the key: a caller that shares a key labels the stored result with the step it asked for (§7.3
// step 6). The directory a check runs in is not part of the key either: the same tree in a fresh
// clone maps to the same key (§6.5).
func CheckKey(in KeyInput) string {
	h := sha256.New()
	h.Write([]byte("gates.CheckKey"))
	writeKey(h, "argv", in.Argv)
	writeKey(h, "inputs", []string{in.InputsDigest})
	writeKey(h, "os", []string{in.Fingerprint.OS})
	writeKey(h, "arch", []string{in.Fingerprint.Arch})
	writeKey(h, "tool", []string{in.Fingerprint.ToolVersion})
	writeKey(h, "locks", in.Fingerprint.LockDigests)
	return hex.EncodeToString(h.Sum(nil))
}

// writeKey mixes label and values into h. Each value is length-prefixed, so two different lists
// cannot hash to the same bytes.
func writeKey(h hash.Hash, label string, values []string) {
	h.Write([]byte{0})
	h.Write([]byte(label))
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(values)))
	h.Write(n[:])
	for _, v := range values {
		binary.BigEndian.PutUint64(n[:], uint64(len(v)))
		h.Write(n[:])
		h.Write([]byte(v))
	}
}

// toolchainProbe is the Go row of the stack profile's toolchain probe (SPEC §7.8, §8.2) with
// GOFLAGS added: each name it prints changes what a Go step builds. It does not depend on the
// step's own program, so a program with no `version` subcommand still has a key.
func toolchainProbe() []string {
	return []string{"go", "env", "GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS"}
}

// toolchainError is the refusal of a key whose toolchain was not read from the probe.
func toolchainError(found string) error {
	return &core.Error{Outcome: core.OutcomeError,
		Expected: "the output of `" + strings.Join(toolchainProbe(), " ") + "` in the work tree", Found: found,
		Next: "put the go tool on PATH so the probe prints its values and exits 0, then run the verify again"}
}

// workTreeError is the refusal of a directory whose git objects are not the key.
func workTreeError(found string) error {
	return &core.Error{Outcome: core.OutcomeError,
		Expected: "the top of a git work tree", Found: found,
		Next: "pass the work tree root; a key is computed from its git objects, not from caller strings"}
}

// gitObjectsError is the refusal of a work tree whose git objects or status could not be read.
func gitObjectsError(err error) error {
	return &core.Error{Outcome: core.OutcomeError,
		Expected: "the git objects of the step's declared inputs", Found: err.Error(),
		Next: "commit the declared inputs in the work tree, then run the verify again", Err: err}
}

// gitFormatError is the refusal of git output that is not the record format the key reads, so a
// changed git format is never hashed as an empty or a clean tree (DR-6).
func gitFormatError(expected string) error {
	return &core.Error{Outcome: core.OutcomeError,
		Expected: expected, Found: "a record that is not in that format",
		Next: "run the verify with a git that prints " + expected}
}

// gitObject is one blob in a commit, named by the path git stores and the blob id.
type gitObject struct {
	path string
	blob string
}

// parseTree reads `git ls-tree -r -z` output. Each record ends in NUL and its path is the bytes
// git stores, never quoted or rewritten (DR-18). Submodule commits are skipped.
func parseTree(stdout string) ([]gitObject, error) {
	var out []gitObject
	for _, record := range strings.Split(stdout, "\x00") {
		if record == "" {
			continue
		}
		meta, file, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || file == "" || len(fields) != 3 {
			return nil, gitFormatError("git ls-tree -r -z blob records")
		}
		if fields[1] == "blob" {
			out = append(out, gitObject{path: file, blob: fields[2]})
		}
	}
	return out, nil
}

// parseStatus reads `git status --porcelain=v1 -z` output into the paths it names, byte-exact. A
// rename or a copy names its source path too.
func parseStatus(stdout string) ([]string, error) {
	var paths []string
	records := strings.Split(stdout, "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		if record == "" {
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			return nil, gitFormatError("git status --porcelain=v1 -z records")
		}
		paths = append(paths, record[3:])
		if strings.ContainsAny(record[:2], "RC") {
			i++
			if i == len(records) || records[i] == "" {
				return nil, gitFormatError("git status --porcelain=v1 -z records")
			}
			paths = append(paths, records[i])
		}
	}
	return paths, nil
}

// digestInputs is the sha256 of the declared blobs, each path and blob id length-prefixed, so a
// path that holds a newline or a space cannot alias another list. An empty inputs list is the
// whole tree. Zero matched blobs, or any declared input that matches none, is an error: hashing
// nothing would look like a real digest (DR-6).
func digestInputs(objects []gitObject, inputs []string) (string, error) {
	if missing := unmatchedInput(objects, inputs); missing != "" {
		return "", noInputBlobsError(missing)
	}
	matched := make([]gitObject, 0, len(objects))
	for _, o := range objects {
		if matchesInputs(o.path, inputs) {
			matched = append(matched, o)
		}
	}
	slices.SortFunc(matched, func(a, b gitObject) int { return strings.Compare(a.path, b.path) })
	h := sha256.New()
	for _, o := range matched {
		writeKey(h, "blob", []string{o.path, o.blob})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// unmatchedInput is the declared input that names no blob, or the whole tree when it holds none.
// An empty string means every declared input has a blob.
func unmatchedInput(objects []gitObject, inputs []string) string {
	if len(inputs) == 0 {
		if len(objects) == 0 {
			return "the whole tracked tree"
		}
		return ""
	}
	for _, in := range inputs {
		if !slices.ContainsFunc(objects, func(o gitObject) bool { return matchesInputs(o.path, []string{in}) }) {
			return in
		}
	}
	return ""
}

// noInputBlobsError is the refusal of a key whose declared inputs are not in the git tree.
func noInputBlobsError(missing string) error {
	return &core.Error{Outcome: core.OutcomeError,
		Expected: "a git blob for each declared input", Found: "no blob matched " + missing,
		Next: "commit the declared inputs in the work tree, then run the verify again"}
}

// digestLocks is one sha256 per tracked go.sum, in path order.
func digestLocks(objects []gitObject) []string {
	lines := make([]string, 0, 1)
	for _, o := range objects {
		if isLockFile(o.path) {
			lines = append(lines, o.path+" "+o.blob)
		}
	}
	sort.Strings(lines)
	out := make([]string, len(lines))
	for i, line := range lines {
		sum := sha256.Sum256([]byte(line))
		out[i] = hex.EncodeToString(sum[:])
	}
	return out
}

// matchesInputs reports whether file is one of the step's declared inputs: a glob equal to the
// path, or a glob that matches it segment by segment. An empty list is the whole tracked tree.
func matchesInputs(file string, globs []string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if g == file || matchGlob(strings.Split(g, "/"), strings.Split(file, "/")) {
			return true
		}
	}
	return false
}

// matchGlob reports whether the path segments name match the glob segments pat. A `**` segment
// matches any number of segments, none included, so `pkg/**` holds every file under pkg at any
// depth (SPEC §7.8). Any other segment is a path.Match pattern of one segment. A malformed pattern
// matches nothing, so its declared input is refused as unmatched (DR-6).
func matchGlob(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		return matchAnyDepth(pat[1:], name)
	}
	return len(name) > 0 && segmentMatches(pat[0], name[0]) && matchGlob(pat[1:], name[1:])
}

// matchAnyDepth reports whether rest matches name after a `**` segment took zero or more of its
// leading segments.
func matchAnyDepth(rest, name []string) bool {
	for i := 0; i <= len(name); i++ {
		if matchGlob(rest, name[i:]) {
			return true
		}
	}
	return false
}

// segmentMatches reports whether one path segment matches one glob segment; a malformed glob
// matches nothing.
func segmentMatches(glob, segment string) bool {
	ok, err := path.Match(glob, segment)
	return err == nil && ok
}

// isLockFile reports whether p is a Go lock file (SPEC §8.2).
func isLockFile(p string) bool {
	return p == "go.sum" || strings.HasSuffix(p, "/go.sum")
}
