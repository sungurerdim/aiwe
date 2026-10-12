package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"github.com/sungurerdim/aiwe/internal/core"
)

func TestLauncherBestFitBackslash(t *testing.T) {
	// #105 fix round, finding 1: Windows maps these characters to \ when an ANSI program reads its
	// command line (U+FF3C on 874 and 1250-1258, U+2216 on 1250/1252/1254, U+00A5 on 932, U+20A9 on
	// 949). At the end of a word they escape the closing quote for that program, and doubling them
	// as ASCII \ would escape it for a Unicode one: no quoting is safe, so the word is refused (SPEC
	// §5.20). In the middle of a word only text follows them, so the word passes intact.
	t.Run("a best-fit form of a backslash at the end of an argument is refused", func(t *testing.T) {
		launcher := writeProcessLauncher(t, t.TempDir())
		// A fixed relative path: no host path literal, and no TMP value (a % in it is refused for
		// another reason) decides the outcome.
		const work = `work\notes`
		chars := []struct{ name, r string }{
			{"fullwidth reverse solidus U+FF3C", "\uff3c"},
			{"set minus U+2216", "\u2216"},
			{"yen sign U+00A5", "\u00a5"},
			{"won sign U+20A9", "\u20a9"},
		}
		for _, c := range chars {
			for _, pos := range []struct {
				name, arg string
				quotable  bool
			}{
				{"at the end", work + c.r, false},
				{"before trailing backslashes", work + c.r + `\\`, false},
				{"in the middle", `C:` + c.r + `work` + c.r + `notes`, true},
			} {
				t.Run(c.name+" "+pos.name, func(t *testing.T) {
					dir := t.TempDir()
					ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
					defer cancel()
					child, err := core.Start(ctx, core.ChildSpec{
						Argv: []string{launcher, pos.arg, "--next"},
						Dir:  dir,
						Env:  processEnv(dir, "argv", false),
					})
					if err != nil {
						if pos.quotable {
							t.Fatalf("Start refused %q: %v; want it quoted and passed", pos.arg, err)
						}
						if _, statErr := os.Stat(filepath.Join(dir, "argv.json")); statErr == nil {
							t.Errorf("Start refused %q with %v, but the launcher ran", pos.arg, err)
						}
						// Any other refusal would pass with the best-fit backslash check gone.
						if want := "before the closing quote reads as \\ in an ANSI program"; !strings.Contains(err.Error(), want) {
							t.Errorf("Start refused %q with %v; want the refusal %q", pos.arg, err, want)
						}
						return
					}
					if waitErr := waitProcessChild(t, child, processBound); waitErr != nil {
						t.Errorf("launcher with %q: Wait = %v", pos.arg, waitErr)
					}
					if !pos.quotable {
						t.Errorf("Start(%q, %q) = nil error, want a refusal: an ANSI program reads the end as \\ and the closing quote as escaped",
							launcher, pos.arg)
						return
					}
					data, err := os.ReadFile(filepath.Join(dir, "argv.json"))
					if err != nil {
						t.Fatalf("launcher with %q did not reach the program: %v", pos.arg, err)
					}
					var got []string
					if err := json.Unmarshal(data, &got); err != nil {
						t.Fatalf("argv record %q: %v", data, err)
					}
					if want := []string{pos.arg, "--next"}; !slices.Equal(got, want) {
						t.Errorf("launcher passed %q, program received %q", want, got)
					}
				})
			}
		}
	})
}

// #105 fix round, finding 2: the record helper exited 0 whenever its variable was set, so
// `AIWE_LAUNCHER_TEST_RECORD=<dir> go test ./internal/core/` passed with no test run (DR-6,
// DR-12). The helper must act only on its own marker and fail on anything else.
func TestLauncherRecordHelper(t *testing.T) {
	t.Run("the record variable without the marker does not pass the package silently", func(t *testing.T) {
		record := t.TempDir()
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcessTreeJob$", "-test.v", "-test.count=1")
		cmd.Env = launcherRecordEnvFor(record)
		out, err := cmd.CombinedOutput()
		if err == nil && !strings.Contains(string(out), "=== RUN") {
			t.Errorf("test binary with %s set exited 0 and ran no test; output %q", launcherRecordEnv(), out)
		}
		if ran, ok := readLauncherRan(t, record); ok {
			t.Errorf("test binary with %s set and no marker recorded a start of %s; want nothing recorded",
				launcherRecordEnv(), ran)
		}
	})
}

// #105 fix round, finding 12: a program with a root and no drive (`\tools\agent.exe`) or a drive
// and no root (`X:agent.exe`) was looked up against the current directory's drive. Go's Start
// resolves it against Dir (syscall joinExeDirAndFName), so the batch decision named another file.
func TestLauncherDirDrive(t *testing.T) {
	t.Run("a rooted program without a drive resolves on Dir's drive", func(t *testing.T) {
		work := t.TempDir()
		vol := filepath.VolumeName(work)
		if len(vol) != 2 || vol[1] != ':' {
			t.Fatalf("temp directory %s has volume %q; the case needs a drive letter", work, vol)
		}
		decided := filepath.Join(work, "tools", "agent.exe")
		if err := os.MkdirAll(filepath.Dir(decided), 0o700); err != nil {
			t.Fatalf("make the tools directory: %v", err)
		}
		copyLauncherProgram(t, decided)
		if other := otherDriveRoot(vol); other != "" {
			t.Chdir(other) // the current drive differs from Dir's, so the two resolutions differ
		} else {
			t.Logf("no second drive: the current directory stays on %s, where both resolutions name %s", vol, decided)
		}
		startsDecidedLauncher(t, strings.TrimPrefix(decided, vol), work, decided)
	})

	// On one drive the difference shows through the directory: Dir holds the decided file and the
	// current directory holds a decoy, and `X:agent.exe` must start the one in Dir.
	t.Run("a drive-relative program resolves in Dir", func(t *testing.T) {
		work := t.TempDir()
		cwd := t.TempDir()
		vol := filepath.VolumeName(work)
		if len(vol) != 2 || vol[1] != ':' || !strings.EqualFold(filepath.VolumeName(cwd), vol) {
			t.Fatalf("temp directories %s and %s; the case needs both on one drive letter", work, cwd)
		}
		decided := filepath.Join(work, "agent.exe")
		copyLauncherProgram(t, decided)
		copyLauncherProgram(t, filepath.Join(cwd, "agent.exe"))
		t.Chdir(cwd)
		startsDecidedLauncher(t, vol+"agent.exe", work, decided)
	})
}

// startsDecidedLauncher starts program in dir and fails unless decided is the file that ran.
func startsDecidedLauncher(t *testing.T, program, dir, decided string) {
	t.Helper()
	record := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
	defer cancel()
	child, err := core.Start(ctx, core.ChildSpec{Argv: []string{program, launcherRecordArg()}, Dir: dir, Env: launcherRecordEnvFor(record)})
	if err != nil {
		t.Fatalf("Start(%q, Dir %q) = %v, want %s started, as Go's Start resolves it", program, dir, err, decided)
	}
	if waitErr := waitProcessChild(t, child, processBound); waitErr != nil {
		t.Errorf("Start(%q, Dir %q): Wait = %v", program, dir, waitErr)
	}
	ran, ok := readLauncherRan(t, record)
	if !ok {
		t.Fatalf("Start(%q, Dir %q) started nothing that recorded, want %s", program, dir, decided)
	}
	if !sameLauncherFile(t, ran, decided) {
		t.Errorf("Start(%q, Dir %q) ran %s, want %s", program, dir, ran, decided)
	}
}

// #138: cmd.exe runs nothing from a command line longer than 8191 UTF-16 units and exits 1, so a
// long argument made a child that failed as the agent's own failure would. Start must refuse that
// line and name the limit, and must still start a line of exactly 8191 units (SPEC §5.20).
func TestLauncherCommandLineLimit(t *testing.T) {
	const limit = 8191
	launcher := writeProcessLauncher(t, t.TempDir())
	// The system directory as Start finds it (GetSystemDirectoryW), not from SystemRoot, which may differ.
	sysDir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatalf("find the system directory: %v", err)
	}
	shell := filepath.Join(sysDir, "cmd.exe")
	// The line Start builds for the launcher with the arguments filler and "--next", without filler.
	fixed := utf16Len(`"` + shell + `" /d /e:ON /v:OFF /s /c ""` + launcher + `" "" "--next""`)
	units := []struct{ name, r string }{
		{"ASCII", "a"},
		{"one UTF-16 unit, two UTF-8 bytes", "ü"},
		{"a surrogate pair, four UTF-8 bytes", "\U0001F600"},
	}
	for _, u := range units {
		for _, tc := range []struct {
			name string
			size int
			runs bool
		}{
			{"at the limit", limit, true},
			{"one unit over the limit", limit + 1, false},
		} {
			t.Run(u.name+" "+tc.name, func(t *testing.T) {
				n := tc.size - fixed
				w := utf16Len(u.r)
				arg := strings.Repeat("b", n%w) + strings.Repeat(u.r, n/w)
				if got := fixed + utf16Len(arg); got != tc.size {
					t.Fatalf("built a line of %d units, want %d", got, tc.size)
				}
				dir := t.TempDir()
				ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
				defer cancel()
				child, err := core.Start(ctx, core.ChildSpec{
					Argv: []string{launcher, arg, "--next"},
					Dir:  dir,
					Env:  processEnv(dir, "argv", false),
				})
				_, statErr := os.Stat(filepath.Join(dir, "argv.json"))
				if !tc.runs {
					if err == nil {
						waitErr := waitProcessChild(t, child, processBound)
						t.Fatalf("Start with a %d-unit cmd.exe line = nil error (Wait = %v), want a refusal naming %d",
							tc.size, waitErr, limit)
					}
					// The temp path in the error may hold any digits, so each match names what it is.
					for _, want := range []string{fmt.Sprintf("limit of %d", limit), fmt.Sprintf("%d UTF-16 units", tc.size)} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("Start refused a %d-unit line with %v; want the error to hold %q", tc.size, err, want)
						}
					}
					if statErr == nil {
						t.Errorf("Start refused a %d-unit line with %v, but the launcher ran", tc.size, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("Start with a %d-unit cmd.exe line = %v, want it started: cmd.exe runs it", tc.size, err)
				}
				if waitErr := waitProcessChild(t, child, processBound); waitErr != nil {
					t.Fatalf("launcher with a %d-unit line: Wait = %v, want nil", tc.size, waitErr)
				}
				data, err := os.ReadFile(filepath.Join(dir, "argv.json"))
				if err != nil {
					t.Fatalf("launcher with a %d-unit line did not reach the program: %v", tc.size, err)
				}
				var got []string
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatalf("argv record of %d bytes: %v", len(data), err)
				}
				if !slices.Equal(got, []string{arg, "--next"}) {
					t.Errorf("launcher with a %d-unit line: the program received other arguments", tc.size)
				}
			})
		}
	}
}

// utf16Len returns the length of s in UTF-16 units, the unit of a Windows command line.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// otherDriveRoot returns the root of an existing drive other than vol, or "" if there is none.
func otherDriveRoot(vol string) string {
	for l := 'C'; l <= 'Z'; l++ {
		drive := string(l) + ":"
		if strings.EqualFold(drive, vol) {
			continue
		}
		if info, err := os.Stat(drive + `\`); err == nil && info.IsDir() {
			return drive + `\`
		}
	}
	return ""
}
