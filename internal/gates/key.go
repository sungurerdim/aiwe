package gates

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

// Fingerprint is the environment a check ran in (SPEC §7.8): the OS, the architecture, the
// version output of the first argv token and the digests of the lock files.
type Fingerprint struct {
	OS          string
	Arch        string
	ToolVersion string
	LockDigests []string
}

// KeyInput is everything that identifies one check (SPEC §7.8): the argv, the digest of the step's
// declared inputs from git objects — the whole tracked tree when the step declares none — and the
// env fingerprint. Neither the step's id nor the directory a check runs in is part of it, so the
// same tree in a fresh clone (§6.5) hits the cache instead of running the check again.
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
