package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// trustCase is one home or settings file install must refuse. setup prepares a temporary home and
// returns the home Apply gets, the row it applies and a check that what was there is unchanged.
type trustCase struct {
	name    string
	catches string
	want    error
	setup   func(t *testing.T, home string) (string, Harness, func(t *testing.T))
}

// TestApplyRefusesWhatItCannotTrust proves install fails closed on a home or a settings file it
// cannot read or replace as one plain file, and leaves what was there as it was.
func TestApplyRefusesWhatItCannotTrust(t *testing.T) {
	t.Parallel()
	for _, tc := range trustCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home, h, check := tc.setup(t, t.TempDir())
			changes, err := Apply(home, h)
			if err == nil {
				t.Fatalf("install accepted with changes %v; catches: %s", changes, tc.catches)
			}
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Outcome != core.OutcomeError {
				t.Errorf("refusal = %v, want a *core.Error with outcome %q", err, core.OutcomeError)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("refusal = %v, want %v; catches: %s", err, tc.want, tc.catches)
			}
			check(t)
		})
	}
}

// trustCases returns the rows of TestApplyRefusesWhatItCannotTrust, one per refused error path.
func trustCases() []trustCase {
	return []trustCase{
		{
			name:    "home missing",
			catches: "an install that creates a home directory that does not exist",
			setup: func(t *testing.T, home string) (string, Harness, func(t *testing.T)) {
				absent := filepath.Join(home, "absent")
				return absent, claudeCode(t), func(t *testing.T) {
					if _, err := os.Lstat(absent); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("the missing home %s is there now (%v)", absent, err)
					}
				}
			},
		},
		{
			name:    "settings over 1 MiB",
			catches: "a large settings file read as none and replaced, losing the user's settings",
			setup: func(t *testing.T, home string) (string, Harness, func(t *testing.T)) {
				h := claudeCode(t)
				big := `{"pad":"` + strings.Repeat("x", maxSettingsBytes) + `"}`
				seed(t, home, h, big)
				return home, h, func(t *testing.T) { unchanged(t, settingsPath(home, h), big) }
			},
		},
		{
			name:    "settings a directory",
			catches: "a directory at the settings path read as no file and replaced",
			setup: func(t *testing.T, home string) (string, Harness, func(t *testing.T)) {
				h := claudeCode(t)
				if err := os.MkdirAll(settingsPath(home, h), 0o700); err != nil {
					t.Fatalf("make the settings directory: %v", err)
				}
				return home, h, func(t *testing.T) {
					if info, err := os.Lstat(settingsPath(home, h)); err != nil || !info.IsDir() {
						t.Errorf("the settings directory is gone or changed (%v)", err)
					}
				}
			},
		},
		{
			name:    "settings a symbolic link",
			want:    ErrSettingsLink,
			catches: "a dotfiles link followed and then replaced by a plain file, so the linked copy stops getting updates",
			setup:   linkedSettings,
		},
		{
			name:    "temp file cannot be created",
			catches: "a failed write reported as success, or a temp file left behind",
			setup: func(t *testing.T, home string) (string, Harness, func(t *testing.T)) {
				h := claudeCode(t)
				// The longest name a file system takes: the settings file can be read as missing, but
				// no temp file next to it can be created, so the write fails.
				h.File = strings.Repeat("s", 250) + ".json"
				return home, h, func(t *testing.T) {
					entries, err := os.ReadDir(filepath.Join(home, h.Dir))
					if err != nil && !errors.Is(err, fs.ErrNotExist) {
						t.Fatalf("read the settings directory: %v", err)
					}
					if len(entries) != 0 {
						t.Errorf("a failed write left %d files behind, first %s", len(entries), entries[0].Name())
					}
				}
			},
		},
	}
}

// linkedSettings makes the settings file a symbolic link to a file next to it, as a dotfiles
// manager does, and checks that the link and its target stay as they were.
func linkedSettings(t *testing.T, home string) (string, Harness, func(t *testing.T)) {
	t.Helper()
	h := claudeCode(t)
	const before = `{"model":"opus"}`
	dir := filepath.Join(home, h.Dir)
	target := filepath.Join(dir, "real.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}
	if err := os.WriteFile(target, []byte(before), 0o600); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
	if err := os.Symlink("real.json", settingsPath(home, h)); err != nil {
		t.Skipf("this OS cannot create a symbolic link here (%v), so a linked settings file cannot occur; #475", err)
	}
	return home, h, func(t *testing.T) {
		if info, err := os.Lstat(settingsPath(home, h)); err != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("the settings link is gone or replaced by a plain file (%v)", err)
		}
		unchanged(t, target, before)
	}
}

// unchanged checks that the file at path still holds want.
func unchanged(t *testing.T, path, want string) {
	t.Helper()
	if b, err := os.ReadFile(path); err != nil || string(b) != want {
		t.Errorf("%s = %.40q, %v; want it unchanged as %.40q", path, b, err, want)
	}
}

// TestApplyWritesThroughItsOwnTempFile proves the write goes through a temp file of its own: a
// stray or concurrent install's file at a shared temp name neither breaks nor tears it, and no
// temp file is left after the rename.
func TestApplyWritesThroughItsOwnTempFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	h := claudeCode(t)
	stray := settingsPath(home, h) + ".tmp"
	if err := os.MkdirAll(stray, 0o700); err != nil {
		t.Fatalf("make %s: %v", stray, err)
	}
	if _, err := Apply(home, h); err != nil {
		t.Fatalf("install next to a stray %s: %v; catches: one fixed temp name every install shares",
			filepath.Base(stray), err)
	}
	top := decode(t, readBytes(t, home, h))
	for event, command := range wantWires() {
		if own := ownHandlers(t, top, event); len(own) != 1 || own[0]["command"] != command {
			t.Errorf("event %s holds own handlers %v, want exactly %q", event, own, command)
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, h.Dir))
	if err != nil {
		t.Fatalf("read the settings directory: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{h.File, h.File + ".tmp"}; !slices.Equal(names, want) {
		t.Errorf("the settings directory holds %v, want %v: a temp file was left behind", names, want)
	}
}

// FuzzMerge catches a merge of the untrusted settings file that panics, that writes text which is
// no JSON, or that is not idempotent: merging its own output again changes nothing (STD-19, SPEC
// §5.13).
func FuzzMerge(f *testing.F) {
	own := core.Product + " hook "
	f.Add([]byte(""))
	f.Add([]byte("null"))
	f.Add([]byte(`{"model":"opus","count":12345678901234567890,"env":{"A":"1&2<3"}}`))
	f.Add([]byte(`{"hooks":{"Notification":[{"matcher":"idle","hooks":[{"type":"command","command":"notify-send done"}]}]}}`))
	f.Add([]byte(`{"hooks":{"Stop":[{"matcher":7,"hooks":[{"type":"command","command":"` + own +
		`stop --off"},{"command":"echo x"}]}]}}`))
	f.Add([]byte(`{"hooks":{"PreToolUse":[null,{"hooks":null},{"matcher":"Bash","hooks":[{"type":"command",` +
		`"command":"` + own + `pre-tool","timeout":3}]}]}}`))
	entries := Harnesses()[0].Entries()
	f.Fuzz(func(t *testing.T, settings []byte) {
		merged, changes, err := Merge(settings, entries)
		if err != nil {
			return
		}
		if len(changes) > 0 && !json.Valid(merged) {
			t.Fatalf("Merge(%q) wrote text that is no JSON: %q", settings, merged)
		}
		again, more, err := Merge(merged, entries)
		if err != nil || len(more) != 0 || !bytes.Equal(again, merged) {
			t.Fatalf("a second merge of the output of %q changed it: %v, %v\n%s\n%s", settings, more, err, merged, again)
		}
	})
}
