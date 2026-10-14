package gates

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"testing"
)

// keyTestDigest returns the sha256 hex digest of text, the form a check's declared inputs are
// summarised as (SPEC §7.8).
func keyTestDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// keyTestInput is a plausible check identity: the unit verify over the gates and engine packages at
// one tree, with the version the go tool reports.
func keyTestInput() KeyInput {
	return KeyInput{
		Argv:         []string{"go", "test", "./internal/gates/...", "./internal/engine/..."},
		InputsDigest: keyTestDigest("tree at 78daea7"),
		Fingerprint: Fingerprint{
			OS:          "linux",
			Arch:        "amd64",
			ToolVersion: "go version go1.24.0 linux/amd64",
			LockDigests: []string{keyTestDigest("go.sum at 78daea7")},
		},
	}
}

// keyTestHex is the shape of a sha256 hex digest, the key SPEC §7.8 names.
var keyTestHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestCheckKey catches a key that is not a deterministic sha256 of the tree content, the argv and
// the env fingerprint (OS, architecture, the version output of the first token, lock file digests),
// so a check of another tree or tool silently reuses a stored result (DR-8, SPEC §7.8).
func TestCheckKey(t *testing.T) {
	first := CheckKey(keyTestInput())
	if !keyTestHex.MatchString(first) {
		t.Fatalf("catches a key that is not a sha256 hex digest: %q", first)
	}
	cases := []struct {
		name    string
		catches string
		change  func(in *KeyInput)
		// keep is whether the changed input must keep the key.
		keep bool
	}{
		{
			name:    "same argv, tree and tool versions keep the key",
			catches: "a key that is not a deterministic function of its inputs",
			change:  func(*KeyInput) {},
			keep:    true,
		},
		{
			name:    "another tree content changes the key",
			catches: "a key that ignores tree content, so a changed tree reuses a stored result",
			change:  func(in *KeyInput) { in.InputsDigest = keyTestDigest("tree at HEAD") },
			keep:    false,
		},
		{
			name:    "another argv changes the key",
			catches: "a key that ignores argv, so another verify reuses a stored result",
			change:  func(in *KeyInput) { in.Argv = []string{"go", "test", "./internal/gates/..."} },
			keep:    false,
		},
		{
			name:    "another operating system changes the key",
			catches: "a key that ignores the OS of the env fingerprint (SPEC §7.8)",
			change:  func(in *KeyInput) { in.Fingerprint.OS = "windows" },
			keep:    false,
		},
		{
			name:    "another architecture changes the key",
			catches: "a key that ignores the architecture of the env fingerprint (SPEC §7.8)",
			change:  func(in *KeyInput) { in.Fingerprint.Arch = "arm64" },
			keep:    false,
		},
		{
			name:    "another tool version changes the key",
			catches: "a key that ignores the version output of the first token (SPEC §7.8)",
			change:  func(in *KeyInput) { in.Fingerprint.ToolVersion = "go version go1.25.0 linux/amd64" },
			keep:    false,
		},
		{
			name:    "another lock digest changes the key",
			catches: "a key that ignores the lock file digests of SPEC §7.8",
			change:  func(in *KeyInput) { in.Fingerprint.LockDigests = []string{keyTestDigest("go.sum at HEAD")} },
			keep:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := keyTestInput()
			tc.change(&in)
			got := CheckKey(in)
			if tc.keep && got != first {
				t.Errorf("%s: key %q, want %q", tc.catches, got, first)
			}
			if !tc.keep && got == first {
				t.Errorf("%s: key %q did not change", tc.catches, got)
			}
		})
	}
}
