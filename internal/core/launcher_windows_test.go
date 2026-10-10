package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Batch launchers exist only on Windows (SPEC §5.20).
func TestProcessTreeBatchLauncher(t *testing.T) {
	t.Run("a time limit stops a tree started by a batch launcher", func(t *testing.T) {
		launcher := writeProcessLauncher(t, t.TempDir())
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		child, dir := startProcessTree(t, ctx, []string{launcher}, true)
		if err := waitProcessChild(t, child, processOuterBound+processBound); err == nil {
			t.Error("Wait after the time limit = nil, want the error of a launcher stopped by its limit")
		}
		requireProcessTreeGone(t, dir)
	})

	t.Run("a batch launcher argument arrives intact or is refused", func(t *testing.T) {
		launcherDir := t.TempDir()
		launcher := writeProcessLauncher(t, launcherDir)
		injected := filepath.Join(launcherDir, "injected")
		tests := []struct {
			name string
			arg  string
			// quotable: cmd.exe takes the argument literally inside quotes, so refusing it
			// turns every run of a harness red.
			quotable bool
		}{
			{"plain word", "unit", true},
			{"space", "two words", true},
			{"empty", "", true},
			{"ampersand", "build&test", true},
			{"pipe", "a|b", true},
			{"redirect", "a<b>c", true},
			{"parentheses", "(group)", true},
			{"caret", "a^b", true},
			{"comma semicolon equals", "a,b;c=d", true},
			{"directory with trailing backslash", `build\out dir\`, true},
			{"command injection", "x & type nul > " + injected, true},
			{"percent expansion", "%PATH%", false},
			{"double quote", `say "hi"`, false},
			{"newline", "line one\nline two", false},
			{"unicode", "çalışma ağacı", false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				dir := t.TempDir()
				ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
				defer cancel()
				child, err := core.Start(ctx, core.ChildSpec{
					Argv: []string{launcher, tt.arg},
					Dir:  dir,
					Env:  processEnv(dir, "argv", false),
				})
				if err != nil {
					if tt.quotable {
						t.Fatalf("Start refused %q: %v; want it quoted and passed", tt.arg, err)
					}
					if _, statErr := os.Stat(filepath.Join(dir, "argv.json")); statErr == nil {
						t.Errorf("Start refused %q with %v, but the launcher ran", tt.arg, err)
					}
					return
				}
				if err := waitProcessChild(t, child, processBound); err != nil {
					t.Fatalf("launcher with %q: Wait = %v, want nil", tt.arg, err)
				}
				data, err := os.ReadFile(filepath.Join(dir, "argv.json"))
				if err != nil {
					t.Fatalf("launcher with %q did not reach the program: %v", tt.arg, err)
				}
				var got []string
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatalf("argv record %q: %v", data, err)
				}
				if want := []string{tt.arg}; !slices.Equal(got, want) {
					t.Errorf("launcher passed %q, program received %q", want, got)
				}
				if _, err := os.Stat(injected); err == nil {
					t.Errorf("argument %q ran as a command: %s exists", tt.arg, injected)
				}
			})
		}
	})

	// Windows drops trailing dots and spaces from a file name, so these names open launch.cmd and
	// cmd.exe runs it; a check of the extension alone takes them for a program and quotes the
	// arguments for CommandLineToArgvW, which cmd.exe does not honour.
	t.Run("a disguised batch launcher argument arrives intact or is refused", func(t *testing.T) {
		launcherDir := t.TempDir()
		launcher := writeProcessLauncher(t, launcherDir)
		base := strings.TrimSuffix(launcher, ".cmd")
		tests := []struct {
			name, argv0, arg string
		}{
			{"trailing dot runs no injected command", base + ".cmd.", `x" & type nul > injected & "`},
			{"trailing dots in upper case run no injected command", base + ".CMD..", `x" & type nul > injected & "`},
			{"trailing space runs no injected command", base + ".cmd ", `x" & type nul > injected & "`},
			{"trailing space adds no flag", base + ".cmd ", `prompt" --evil-flag "`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				dir := t.TempDir()
				ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
				defer cancel()
				injected := filepath.Join(dir, "injected") // cmd.exe runs in dir
				child, err := core.Start(ctx, core.ChildSpec{
					Argv: []string{tt.argv0, tt.arg},
					Dir:  dir,
					Env:  processEnv(dir, "argv", false),
				})
				if err != nil {
					if _, statErr := os.Stat(filepath.Join(dir, "argv.json")); statErr == nil {
						t.Errorf("Start(%q) refused %q with %v, but the launcher ran", tt.argv0, tt.arg, err)
					}
					return
				}
				waitErr := waitProcessChild(t, child, processBound)
				if _, err := os.Stat(injected); err == nil {
					t.Errorf("Start(%q): argument %q ran as a command: %s exists", tt.argv0, tt.arg, injected)
				}
				if waitErr != nil {
					t.Fatalf("launcher %q with %q: Wait = %v, want nil", tt.argv0, tt.arg, waitErr)
				}
				data, err := os.ReadFile(filepath.Join(dir, "argv.json"))
				if err != nil {
					t.Fatalf("launcher %q with %q did not reach the program: %v", tt.argv0, tt.arg, err)
				}
				var got []string
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatalf("argv record %q: %v", data, err)
				}
				if want := []string{tt.arg}; !slices.Equal(got, want) {
					t.Errorf("launcher %q passed %q, program received %q", tt.argv0, want, got)
				}
			})
		}
	})

	// Delayed expansion turns !NAME! into the value of NAME even inside quotes, and the launcher,
	// not the caller, decides whether it is on; so "!" in a batch argument is refused.
	t.Run("an exclamation mark in a batch launcher argument is refused", func(t *testing.T) {
		const secretName, secret = "AIWE_PROBE_SECRET", "s3cr3t-value-from-env"
		launcher := filepath.Join(t.TempDir(), "delayed.cmd")
		body := "@setlocal EnableDelayedExpansion\r\n@\"" + os.Args[0] + "\" %*\r\n"
		if err := os.WriteFile(launcher, []byte(body), 0o600); err != nil {
			t.Fatalf("write launcher: %v", err)
		}
		dir := t.TempDir()
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		arg := "!" + secretName + "!"
		child, err := core.Start(ctx, core.ChildSpec{
			Argv: []string{launcher, arg},
			Dir:  dir,
			Env:  append(processEnv(dir, "argv", false), secretName+"="+secret),
		})
		if err != nil {
			if _, statErr := os.Stat(filepath.Join(dir, "argv.json")); statErr == nil {
				t.Errorf("Start refused %q with %v, but the launcher ran", arg, err)
			}
			return
		}
		t.Errorf("Start(%q, %q) = nil error, want a refusal: %q is not safe in a batch argument", launcher, arg, "!")
		if err := waitProcessChild(t, child, processBound); err != nil {
			t.Fatalf("launcher with %q: Wait = %v", arg, err)
		}
		if data, err := os.ReadFile(filepath.Join(dir, "argv.json")); err == nil && strings.Contains(string(data), secret) {
			t.Errorf("launcher expanded %q to the environment value: program received %s", arg, data)
		}
	})

	// Go finds a program path without an extension through PATHEXT, so "solo" runs solo.cmd through
	// cmd.exe; a check of the path as given takes it for a program and quotes the arguments for
	// CommandLineToArgvW, which cmd.exe does not honour.
	t.Run("an extensionless batch launcher argument arrives intact or is refused", func(t *testing.T) {
		const arg = `x" & type nul > MARK & "`
		tests := []struct {
			name  string
			argv0 func(dir string) string
		}{
			{"absolute path", func(dir string) string { return filepath.Join(dir, "solo") }},
			{"relative path", func(string) string { return `.\solo` }},
			{"extended-length path", func(dir string) string { return `\\?\` + filepath.Join(dir, "solo") }},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				dir := t.TempDir()
				body := "@\"" + os.Args[0] + "\" %*\r\n"
				if err := os.WriteFile(filepath.Join(dir, "solo.cmd"), []byte(body), 0o600); err != nil {
					t.Fatalf("write launcher: %v", err)
				}
				argv0 := tt.argv0(dir)
				mark := filepath.Join(dir, "MARK") // cmd.exe runs in dir
				ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
				defer cancel()
				child, err := core.Start(ctx, core.ChildSpec{
					Argv: []string{argv0, arg},
					Dir:  dir,
					Env:  processEnv(dir, "argv", false),
				})
				if err != nil {
					if _, statErr := os.Stat(filepath.Join(dir, "argv.json")); statErr == nil {
						t.Errorf("Start(%q) refused %q with %v, but the launcher ran", argv0, arg, err)
					}
					return
				}
				t.Cleanup(func() { _ = child.StopTree(200 * time.Millisecond) }) // best effort
				waitErr := waitProcessChild(t, child, processBound)
				if _, err := os.Stat(mark); err == nil {
					t.Errorf("Start(%q): argument %q ran as a command: %s exists", argv0, arg, mark)
				}
				if waitErr != nil {
					t.Fatalf("launcher %q with %q: Wait = %v, want nil", argv0, arg, waitErr)
				}
				data, err := os.ReadFile(filepath.Join(dir, "argv.json"))
				if err != nil {
					t.Fatalf("launcher %q with %q did not reach the program: %v", argv0, arg, err)
				}
				var got []string
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatalf("argv record %q: %v", data, err)
				}
				if want := []string{arg}; !slices.Equal(got, want) {
					t.Errorf("launcher %q passed %q, program received %q", argv0, want, got)
				}
			})
		}
	})

	// #105, security review of #7: Start resolved the program a second time after the batch
	// decision, joining a relative Dir once more, so `sub\sub\agent.exe.exe` ran where the decision
	// was made on `sub\agent.exe`. The decoy is a copy of the test binary, so a failure starts no
	// batch file and opens no window.
	t.Run("a relative Dir starts only the file the batch decision was made on", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		if err := os.MkdirAll(filepath.Join(root, "sub", "sub"), 0o700); err != nil {
			t.Fatalf("make the work tree: %v", err)
		}
		copyLauncherProgram(t, filepath.Join(root, "sub", "agent.exe"))
		copyLauncherProgram(t, filepath.Join(root, "sub", "sub", "agent.exe.exe"))
		record := t.TempDir()
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		argv0 := `.\agent.exe`
		child, err := core.Start(ctx, core.ChildSpec{Argv: []string{argv0}, Dir: "sub", Env: launcherRecordEnvFor(record)})
		if err == nil {
			if waitErr := waitProcessChild(t, child, processBound); waitErr != nil {
				t.Errorf("Start(%q, Dir %q): Wait = %v", argv0, "sub", waitErr)
			}
		}
		if ran, ok := readLauncherRan(t, record); ok && !strings.EqualFold(ran, "agent.exe") {
			t.Errorf("Start(%q, Dir %q) ran %q, want only %q (the file the decision was made on) or a refusal",
				argv0, "sub", ran, "agent.exe")
		}
	})

	// #105: CreateProcess runs any executable image whatever its extension, and the shell opens a
	// file outside PATHEXT through its file association; a launcher must name a PATHEXT program.
	t.Run("a launcher whose extension is not in PATHEXT is refused", func(t *testing.T) {
		const ext = ".dat"
		for _, known := range strings.Split(os.Getenv("PATHEXT"), ";") {
			if strings.EqualFold(known, ext) {
				t.Fatalf("PATHEXT %q holds %s; the case needs an extension outside it", os.Getenv("PATHEXT"), ext)
			}
		}
		dir := t.TempDir()
		program := filepath.Join(dir, "agent"+ext)
		copyLauncherProgram(t, program)
		record := t.TempDir()
		ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
		defer cancel()
		child, err := core.Start(ctx, core.ChildSpec{Argv: []string{program}, Dir: dir, Env: launcherRecordEnvFor(record)})
		if err == nil {
			t.Errorf("Start(%q) = nil error, want a refusal: %s is not in PATHEXT", program, ext)
			if waitErr := waitProcessChild(t, child, processBound); waitErr != nil {
				t.Errorf("Start(%q): Wait = %v", program, waitErr)
			}
		}
		if ran, ok := readLauncherRan(t, record); ok {
			t.Errorf("Start(%q) ran %q, want nothing started", program, ran)
		}
	})

	// #105, security review of #7: Windows maps these characters to " % ! when an ANSI program reads
	// its command line (best fit, measured with code pages 1252 and 1254), so they act as the
	// characters checkBatchWord refuses.
	t.Run("a best-fit form of a refused character is refused", func(t *testing.T) {
		launcher := writeProcessLauncher(t, t.TempDir())
		tests := []struct {
			name, arg string
		}{
			{"fullwidth quotation mark U+FF02", "prompt＂ --evil-flag ＂"},
			{"modifier letter double prime U+02BA", "promptʺ --evil-flag ʺ"},
			{"combining double vertical line above U+030E", "prompt̎ --evil-flag ̎"},
			{"fullwidth percent sign U+FF05", "％PATH％"},
			{"fullwidth exclamation mark U+FF01", "！AIWE_PROBE_SECRET！"},
			{"latin letter retroflex click U+01C3", "ǃAIWE_PROBE_SECRETǃ"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				dir := t.TempDir()
				ctx, cancel := context.WithTimeout(t.Context(), processOuterBound)
				defer cancel()
				child, err := core.Start(ctx, core.ChildSpec{
					Argv: []string{launcher, tt.arg},
					Dir:  dir,
					Env:  processEnv(dir, "argv", false),
				})
				if err == nil {
					t.Errorf("Start(%q, %q) = nil error, want a refusal of a best-fit form of \" %% or !", launcher, tt.arg)
					if waitErr := waitProcessChild(t, child, processBound); waitErr != nil {
						t.Errorf("launcher with %q: Wait = %v", tt.arg, waitErr)
					}
					return
				}
				if _, statErr := os.Stat(filepath.Join(dir, "argv.json")); statErr == nil {
					t.Errorf("Start refused %q with %v, but the launcher ran", tt.arg, err)
				}
			})
		}
	})
}

func TestProcessTreeJob(t *testing.T) {
	// U22: the child is created suspended, joins its job, and only then runs; any other order lets
	// a grandchild started first thing run outside the job.
	t.Run("the child joins its job while suspended and resumes after", func(t *testing.T) {
		dir := t.TempDir()
		child := startProcessChild(t, t.Context(), dir, "argv")
		if err := waitProcessChild(t, child, processBound); err != nil {
			t.Fatalf("Wait = %v, want nil", err)
		}
		const createSuspended = 0x00000004 // CREATE_SUSPENDED
		if flags, ok := core.ChildCreationFlags(child); !ok || flags&createSuspended == 0 {
			t.Errorf("creation flags = %#x (known %v), want CREATE_SUSPENDED set", flags, ok)
		}
		want := []string{"create_suspended", "assign_job", "resume"}
		if got := core.TreeSteps(child); !slices.Equal(got, want) {
			t.Errorf("start steps = %q, want %q", got, want)
		}
	})
}

// writeProcessLauncher writes a batch launcher that hands every argument to the test binary.
func writeProcessLauncher(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "launch.cmd")
	body := "@\"" + os.Args[0] + "\" %*\r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write launcher: %v", err)
	}
	return path
}

// launcherRecordEnv is set to a directory when the test binary runs as a copy whose start a
// launcher case checks; the copy writes the base name of its own file there and exits.
const launcherRecordEnv = "AIWE_LAUNCHER_TEST_RECORD"

func init() {
	dir := os.Getenv(launcherRecordEnv)
	if dir == "" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		os.Exit(21)
	}
	if err := os.WriteFile(filepath.Join(dir, "ran"), []byte(filepath.Base(self)), 0o600); err != nil {
		os.Exit(22)
	}
	os.Exit(0)
}

// launcherRecordEnvFor returns the environment that makes a copy of the test binary record its
// start in dir.
func launcherRecordEnvFor(dir string) []string {
	return append(os.Environ(), launcherRecordEnv+"="+dir)
}

// copyLauncherProgram copies the test binary to path, so a case can tell which file Start ran.
func copyLauncherProgram(t *testing.T, path string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("find the test binary: %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read the test binary: %v", err)
	}
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatalf("copy the test binary to %s: %v", path, err)
	}
}

// readLauncherRan returns the base name of the file a started copy recorded in dir, and whether
// one ran.
func readLauncherRan(t *testing.T, dir string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "ran"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read the start record: %v", err)
	}
	return string(data), true
}
