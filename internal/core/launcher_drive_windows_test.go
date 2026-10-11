package core_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
				{"at the end", `C:\work` + c.r, false},
				{"before trailing backslashes", `C:\work` + c.r + `\\`, false},
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
// `AIWE_LAUNCHER_TEST_RECORD=<dir> go test ./internal/core/` passed with no test run (DR-13,
// DR-28). The helper must act only on its own marker and fail on anything else.
func TestLauncherRecordHelper(t *testing.T) {
	t.Run("the record variable without the marker does not pass the package silently", func(t *testing.T) {
		record := t.TempDir()
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcessTreeJob$", "-test.v", "-test.count=1")
		cmd.Env = launcherRecordEnvFor(record)
		out, err := cmd.CombinedOutput()
		if err == nil && !strings.Contains(string(out), "=== RUN") {
			t.Errorf("test binary with %s set exited 0 and ran no test; output %q", launcherRecordEnv, out)
		}
		if ran, ok := readLauncherRan(t, record); ok {
			t.Errorf("test binary with %s set and no marker recorded a start of %s; want nothing recorded",
				launcherRecordEnv, ran)
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
	child, err := core.Start(ctx, core.ChildSpec{Argv: []string{program, launcherRecordArg}, Dir: dir, Env: launcherRecordEnvFor(record)})
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
