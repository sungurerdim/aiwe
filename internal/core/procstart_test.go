package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// procTestDeadPID starts a child that exits at once and returns its pid after it is gone.
func procTestDeadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v", err)
	}
	return pid
}

// procTestLinuxID reads the start identity of pid from its sources, without startID: the boot id
// of /proc/sys/kernel/random/boot_id and field 22 of /proc/<pid>/stat.
func procTestLinuxID(t *testing.T, pid int) string {
	t.Helper()
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatalf("boot id: %v", err)
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatalf("stat of process %d: %v", pid, err)
	}
	// Field 22 is the 20th field after the command name, which ends at the last ')'.
	f := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	if len(f) < 20 {
		t.Fatalf("stat of process %d: %q has too few fields", pid, stat)
	}
	return strings.TrimSpace(string(boot)) + "/" + f[19]
}

// TestCurrentProcess catches an identity of the running process that is not its pid, has no start
// time or one in the future, changes between two reads, or on Linux is not the exact boot id and
// start tick of /proc (U13).
func TestCurrentProcess(t *testing.T) {
	self, err := CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	if self.PID != os.Getpid() {
		t.Fatalf("CurrentProcess().PID = %d, want %d", self.PID, os.Getpid())
	}
	if self.Start.IsZero() || self.Start.After(time.Now()) {
		t.Fatalf("CurrentProcess().Start = %v, want the process start time", self.Start)
	}
	again, err := CurrentProcess()
	if err != nil {
		t.Fatalf("second CurrentProcess: %v", err)
	}
	if again.id != self.id {
		t.Errorf("start identity changed between two reads: %q, then %q", self.id, again.id)
	}
	if !exactStartID {
		// The stored start time is the identity here; it is read the same way at every call.
		if !again.Start.Equal(self.Start) {
			t.Errorf("CurrentProcess().Start changed between two reads: %v, then %v", self.Start, again.Start)
		}
		if self.id != "" {
			t.Errorf("CurrentProcess() start identity = %q on %s, want none: this OS keeps none", self.id, runtime.GOOS)
		}
		return
	}
	if want := procTestLinuxID(t, self.PID); self.id != want {
		t.Errorf("CurrentProcess() start identity = %q, want %q (boot_id/start tick)", self.id, want)
	}
}

// TestStartID catches a start identity that is not exact (U13): on Linux, startID of a running pid
// must be its boot id and start tick, and a gone pid must not run; elsewhere startID must fail, so
// a record that carries one counts as a live owner.
func TestStartID(t *testing.T) {
	pid := os.Getpid()
	id, running, err := startID(pid)
	if !exactStartID {
		if err == nil {
			t.Errorf("startID(%d) on %s = %q, %v, nil; want an error: this OS keeps no start identity", pid, runtime.GOOS, id, running)
		}
		return
	}
	if err != nil || !running {
		t.Fatalf("startID(%d) of the running process = %q, %v, %v; want its identity, running", pid, id, running, err)
	}
	if want := procTestLinuxID(t, pid); id != want {
		t.Errorf("startID(%d) = %q, want %q (boot_id/start tick)", pid, id, want)
	}
	dead := procTestDeadPID(t)
	if id, running, err := startID(dead); err != nil || running || id != "" {
		t.Errorf("startID(%d) of a gone process = %q, %v, %v; want \"\", false, nil", dead, id, running, err)
	}
}

// TestProcessStart catches a start time of the running process that is not the OS start time, and a
// gone pid, or a pid that names no process, read as running (U13).
func TestProcessStart(t *testing.T) {
	self, err := CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	start, running, err := processStart(self.PID)
	if err != nil || !running {
		t.Fatalf("processStart(%d) of the running process = %v, %v, %v; want its start, running", self.PID, start, running, err)
	}
	if d := start.Sub(self.Start); d < -time.Second || d > time.Second {
		t.Errorf("processStart(%d) = %v, %v away from CurrentProcess().Start %v", self.PID, start, d, self.Start)
	}
	for _, pid := range []int{procTestDeadPID(t), 0, -1} {
		if _, running, err := processStart(pid); err != nil || running {
			t.Errorf("processStart(%d) of no running process = running %v, %v; want not running, no error", pid, running, err)
		}
	}
}

// TestRealPath catches a real path that keeps a link, a letter case or a short name the file
// system does not hold, or that reads a missing path as real (§5.15).
func TestRealPath(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "Workspace")
	if err := os.MkdirAll(filepath.Join(target, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := realPath(target)
	if err != nil {
		t.Fatalf("realPath(%q): %v", target, err)
	}
	if !filepath.IsAbs(want) || filepath.Base(want) != "Workspace" {
		t.Fatalf("realPath(%q) = %q, want an absolute path ending in Workspace", target, want)
	}
	if again, err := realPath(want); err != nil || again != want {
		t.Errorf("realPath(%q) = %q, %v; want the same path back", want, again, err)
	}

	link := filepath.Join(base, "link")
	removeTestLink(t, target, link)
	if got, err := realPath(link); err != nil || got != want {
		t.Errorf("realPath(%q) = %q, %v; want the link target %q", link, got, err, want)
	}
	inner := filepath.Join(link, "src")
	if got, err := realPath(inner); err != nil || got != filepath.Join(want, "src") {
		t.Errorf("realPath(%q) = %q, %v; want %q", inner, got, err, filepath.Join(want, "src"))
	}
	if runtime.GOOS == "windows" {
		upper := strings.ToUpper(target)
		if got, err := realPath(upper); err != nil || got != want {
			t.Errorf("realPath(%q) = %q, %v; want the letter case on disk %q", upper, got, err, want)
		}
	}

	missing := filepath.Join(base, "missing")
	if got, err := realPath(missing); err == nil {
		t.Errorf("realPath(%q) of a missing path = %q, nil; want an error", missing, got)
	}
}

// procTestChildEnv marks the test binary started by procTestChild: it then waits on its stdin.
const procTestChildEnv = "PROCSTART_TEST_CHILD"

// TestProcStartChild is the body of the child that procTestChild starts; in the parent run it skips.
func TestProcStartChild(t *testing.T) {
	if os.Getenv(procTestChildEnv) != "1" {
		t.Skip("child body of procTestChild only")
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// procTestChild starts a child that runs until the test ends and returns its pid together with
// the wall clock just before and just after the start.
func procTestChild(t *testing.T) (pid int, before, after time.Time) {
	t.Helper()
	// The child runs until its stdin closes in the cleanup, after the test context is done.
	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), os.Args[0], "-test.run=^TestProcStartChild$")
	cmd.Env = append(os.Environ(), procTestChildEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("child stdin: %v", err)
	}
	before = time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	after = time.Now()
	t.Cleanup(func() {
		if err := stdin.Close(); err != nil {
			t.Errorf("child stdin: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Errorf("child: %v", err)
		}
	})
	return cmd.Process.Pid, before, after
}

// TestProcessStartOfChild catches a start time that is not the moment the process started: one
// shifted by any amount over a second, or the boot time in place of the start (U13). The start of a
// fresh child must fall between the wall clock read just before and just after it started, within
// 1 s, plus 20 ms for the 10 ms start tick of Linux.
func TestProcessStartOfChild(t *testing.T) {
	const tolerance = time.Second + 20*time.Millisecond
	pid, before, after := procTestChild(t)
	start, running, err := processStart(pid)
	if err != nil || !running {
		t.Fatalf("processStart(%d) of a running child = %v, %v, %v; want its start, running", pid, start, running, err)
	}
	if start.Before(before.Add(-tolerance)) || start.After(after.Add(tolerance)) {
		t.Errorf("processStart(%d) = %v, want between %v and %v within %v", pid, start, before, after, tolerance)
	}
}

// TestRealPathEmpty catches an empty path read as a real path, such as the current directory "."
// (§5.15): an empty path names no file on any OS.
func TestRealPathEmpty(t *testing.T) {
	if got, err := realPath(""); err == nil {
		t.Errorf("realPath(\"\") = %q, nil; want an error", got)
	}
}

// TestOwnedCauseWrapped catches a refusal that drops its cause (§5.15): a root or an owned
// directory that cannot be read fails with its sentinel, and errors.Is must still find the file
// system error under it, so a caller can tell a missing path from another failure.
func TestOwnedCauseWrapped(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "temp")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "missing")
	_, realDirErr := realDir(namedPath{"home", missing})
	_, missingRootErr := (&Owner{}).checkOwnedContent(missing, "run-20261012-0001", ownedRecord{})
	_, missingNameErr := (&Owner{}).checkOwnedContent(dir, "run-20261012-0001", ownedRecord{})
	for _, c := range []struct {
		name     string
		err      error
		sentinel error
	}{
		{"realDir of a missing home", realDirErr, ErrRootUnsafe},
		{"checkRootSafe of a missing root", checkRootSafe(namedPath{"temp root", missing}, nil), ErrRootUnsafe},
		{"checkAncestors of a missing home", checkAncestors(di, namedPath{"temp root", dir}, namedPath{"home", missing}), ErrRootUnsafe},
		{"checkOwnedContent under a missing root", missingRootErr, ErrNotOwned},
		{"checkOwnedContent of a missing name", missingNameErr, ErrNotOwned},
	} {
		if !errors.Is(c.err, c.sentinel) || !errors.Is(c.err, fs.ErrNotExist) {
			t.Errorf("%s = %v; want %v that wraps %v", c.name, c.err, c.sentinel, fs.ErrNotExist)
		}
	}
}

// errTestClose is the error closeFailHook adds at the close a test names.
var errTestClose = errors.New("close failed (test)")

// testFailClose makes the close named what on the removal path fail with errTestClose until the
// test ends.
func testFailClose(t *testing.T, what string) {
	t.Helper()
	closeFailHook = func(w string) error {
		if w == what {
			return errTestClose
		}
		return nil
	}
	t.Cleanup(func() { closeFailHook = nil })
}

// TestOwnedRemoveCloseError catches a failed close on the removal path lost before the caller
// (§5.15). A close that fails during the ownership check refuses the removal and keeps the
// directory; one that fails after the removal is reported with the directory gone.
func TestOwnedRemoveCloseError(t *testing.T) {
	for _, c := range []struct {
		what string
		kept bool
	}{{"owned root", true}, {"owned directory", true}, {"removal root", false}} {
		t.Run(c.what, func(t *testing.T) {
			o := removeTestOpen(t, removeTestRoots(t, t.TempDir()))
			d := removeTestCreate(t, o, TempRoot)
			testFailClose(t, c.what)
			if err := o.RemoveOwned(t.Context(), d); !errors.Is(err, errTestClose) {
				t.Errorf("RemoveOwned with a failing close of the %s = %v, want %v", c.what, err, errTestClose)
			}
			if got := removeTestExists(t, d.Path()); got != c.kept {
				t.Errorf("after a failing close of the %s, %q exists = %v, want %v", c.what, d.Path(), got, c.kept)
			}
		})
	}
}

// TestRemoveDirEntryCloseError catches a failed close of the held directory lost before the caller
// of removeDirEntry (§5.15); the empty directory still goes.
func TestRemoveDirEntryCloseError(t *testing.T) {
	root := t.TempDir()
	name := "run-20261012-0002"
	if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
		t.Fatal(err)
	}
	rt, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Errorf("close root: %v", err)
		}
	})
	fi, err := rt.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	testFailClose(t, "held directory")
	if err := removeDirEntry(rt, name, fi); !errors.Is(err, errTestClose) {
		t.Errorf("removeDirEntry with a failing close of the held directory = %v, want %v", err, errTestClose)
	}
	if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("empty dir %q left after removeDirEntry: %v", name, err)
	}
}
