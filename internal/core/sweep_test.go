package core

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sweepTestRoots names a state root under a fresh t.TempDir and makes the temp, cache, home and
// repository roots owner-only, the same as OpenOwner, which makes the state root itself.
func sweepTestRoots(t *testing.T) OwnedRoots {
	t.Helper()
	base := t.TempDir()
	r := OwnedRoots{
		State:     filepath.Join(base, "state"),
		Temp:      filepath.Join(base, "temp"),
		Cache:     filepath.Join(base, "cache"),
		Home:      filepath.Join(base, "home"),
		Canonical: []string{filepath.Join(base, "repo")},
	}
	for _, d := range []string{r.Temp, r.Cache, r.Home, r.Canonical[0]} {
		if err := mkdirOwnerOnly(d); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func sweepTestOpen(t *testing.T, r OwnedRoots, self ProcessID) *Owner {
	t.Helper()
	o, err := OpenOwner(r, self)
	if err != nil {
		t.Fatalf("OpenOwner: %v", err)
	}
	return o
}

func sweepTestCreate(t *testing.T, o *Owner, root OwnedRoot) OwnedDir {
	t.Helper()
	d, err := o.CreateOwned(context.Background(), root, "run-20261010-0002")
	if err != nil {
		t.Fatalf("CreateOwned: %v", err)
	}
	return d
}

func sweepTestExists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Lstat(p)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	t.Fatalf("lstat %s: %v", p, err)
	return false
}

func sweepTestSelf(t *testing.T) ProcessID {
	t.Helper()
	self, err := CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	return self
}

// sweepTestDeadProcess starts a child that exits at once and returns its identity after it is
// gone: a pid whose owner is dead.
func sweepTestDeadProcess(t *testing.T) ProcessID {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	start := time.Now()
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v", err)
	}
	return ProcessID{PID: pid, Start: start}
}

// sweepTestChildEnv makes the test binary, run by sweepTestLiveChild, wait in TestSweepLiveChild.
const sweepTestChildEnv = "CORE_SWEEP_TEST_LIVE_CHILD"

// TestSweepLiveChild is the body of the child that sweepTestLiveChild starts: it runs until its
// stdin closes. Run without sweepTestChildEnv, it returns at once.
func TestSweepLiveChild(t *testing.T) {
	if os.Getenv(sweepTestChildEnv) != "1" {
		return
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatalf("live child stdin: %v", err)
	}
}

// sweepTestLiveChild starts a child that runs until the test ends and returns its pid.
func sweepTestLiveChild(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSweepLiveChild$")
	cmd.Env = append(os.Environ(), sweepTestChildEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("live child stdin: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start live child: %v", err)
	}
	t.Cleanup(func() {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	})
	return cmd.Process.Pid
}

// sweepTestSplitID returns the boot id and the start tick of the exact start identity of self.
func sweepTestSplitID(t *testing.T, self ProcessID) (string, uint64) {
	t.Helper()
	boot, tick, ok := strings.Cut(self.id, "/")
	n, err := strconv.ParseUint(tick, 10, 64)
	if !ok || boot == "" || err != nil {
		t.Fatalf("start identity of the current process = %q, want <boot_id>/<start tick>", self.id)
	}
	return boot, n
}

// sweepTestOtherBoot is a boot id of another boot than the running one.
const sweepTestOtherBoot = "0f1e2d3c-4b5a-4978-8796-a5b4c3d2e1f0"

// TestSweep catches a temp directory with a dead owner left after the sweep, or a reused pid
// taken as a live owner (DR-36, U13).
func TestSweep(t *testing.T) {
	ctx := context.Background()
	self := sweepTestSelf(t)

	r := sweepTestRoots(t)
	live := sweepTestCreate(t, sweepTestOpen(t, r, self), TempRoot)
	dead := sweepTestCreate(t, sweepTestOpen(t, r, sweepTestDeadProcess(t)), TempRoot)
	// Same pid as a running process, another start: the pid was reused, the owner is dead. Where the
	// OS keeps an exact start identity, the record carries the same pid and start tick in another
	// boot; elsewhere the stored start time is that identity, an hour earlier.
	reusedID := ProcessID{PID: self.PID, Start: self.Start.Add(-time.Hour)}
	if exactStartID {
		_, tick := sweepTestSplitID(t, self)
		reusedID = ProcessID{PID: self.PID, Start: self.Start,
			id: sweepTestOtherBoot + "/" + strconv.FormatUint(tick, 10)}
	}
	reused := sweepTestCreate(t, sweepTestOpen(t, r, reusedID), TempRoot)
	// Bare clones in the cache root are evicted by the footprint, never by the sweep.
	cached := sweepTestCreate(t, sweepTestOpen(t, r, sweepTestDeadProcess(t)), CacheRoot)
	// A directory the engine did not create stays, whatever the sweep finds.
	foreign := filepath.Join(r.Temp, "not-ours")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}

	rep, err := sweepTestOpen(t, r, self).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if sweepTestExists(t, dead.Path()) {
		t.Errorf("temp dir %q of a dead owner is left after the sweep", dead.Path())
	}
	if sweepTestExists(t, reused.Path()) {
		t.Errorf("temp dir %q of a reused pid is left after the sweep", reused.Path())
	}
	if !sweepTestExists(t, live.Path()) {
		t.Errorf("temp dir %q of a live owner was swept", live.Path())
	}
	if !sweepTestExists(t, cached.Path()) {
		t.Errorf("cache dir %q was swept", cached.Path())
	}
	if !sweepTestExists(t, foreign) {
		t.Errorf("unowned dir %q was swept", foreign)
	}
	if rep.Removed != 2 || rep.Live != 1 {
		t.Errorf("Sweep report = %+v, want Removed 2, Live 1", rep)
	}

	again, err := sweepTestOpen(t, r, self).Sweep(ctx)
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if again.Removed != 0 || again.Live != 1 {
		t.Errorf("second Sweep report = %+v, want Removed 0, Live 1", again)
	}
}

// TestSweepHandBuiltStart catches the temp dir of a running owner removed by a sweep of another
// owner because the owner's ProcessID was built by hand from its pid and a wall-clock start, not by
// CurrentProcess (DR-25, U13). A start that cannot be compared exactly counts as a live owner.
func TestSweepHandBuiltStart(t *testing.T) {
	ctx := context.Background()
	pid := sweepTestLiveChild(t)
	// The caller saw the child start a moment ago: the hand-built start is later than the OS start
	// by more than one Linux clock tick (10 ms) and less than the stored-time tolerance (1 s).
	time.Sleep(50 * time.Millisecond)
	owner := ProcessID{PID: pid, Start: time.Now()}

	r := sweepTestRoots(t)
	d := sweepTestCreate(t, sweepTestOpen(t, r, owner), TempRoot)
	rep, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !sweepTestExists(t, d.Path()) {
		t.Errorf("temp dir %q of running pid %d, owner %+v built by hand, was swept", d.Path(), pid, owner)
	}
	if rep.Removed != 0 || rep.Live != 1 {
		t.Errorf("Sweep report = %+v, want Removed 0, Live 1 (a running owner with a hand-built start)", rep)
	}
}

// sweepTestUnreadablePID returns a pid other than this process whose OS start time cannot be read,
// such as a process of another user that the OS does not open; it reports false when none is found.
func sweepTestUnreadablePID() (int, bool) {
	for pid := 1; pid < 1<<16; pid++ {
		if pid == os.Getpid() {
			continue
		}
		if _, _, err := processStart(pid); err != nil {
			return pid, true
		}
	}
	return 0, false
}

// TestSweepStartUnreadable catches the temp dir of an owner whose start cannot be read removed by
// the sweep: such an owner counts as alive and its dir stays (U13, fail-closed).
func TestSweepStartUnreadable(t *testing.T) {
	ctx := context.Background()
	self := sweepTestSelf(t)
	check := func(t *testing.T, owner ProcessID, why string) {
		t.Helper()
		r := sweepTestRoots(t)
		d := sweepTestCreate(t, sweepTestOpen(t, r, owner), TempRoot)
		rep, err := sweepTestOpen(t, r, self).Sweep(ctx)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if !sweepTestExists(t, d.Path()) {
			t.Errorf("temp dir %q of owner %+v was swept; %s, so the owner counts as alive", d.Path(), owner, why)
		}
		if rep.Removed != 0 || rep.Live != 1 {
			t.Errorf("Sweep report = %+v, want Removed 0, Live 1 (%s)", rep, why)
		}
	}

	t.Run("start identity this OS does not keep", func(t *testing.T) {
		if exactStartID {
			t.Skip("this OS reads the exact start identity of a running pid")
		}
		// A record written on Linux for a pid that runs here: this OS cannot read its start tick.
		check(t, ProcessID{PID: self.PID, Start: self.Start, id: sweepTestOtherBoot + "/4242"},
			"its start identity is not readable on "+runtime.GOOS)
	})

	t.Run("start time the OS denies", func(t *testing.T) {
		pid, ok := sweepTestUnreadablePID()
		if !ok {
			t.Skip("no process below pid 65536 hides its start time from this test")
		}
		check(t, ProcessID{PID: pid, Start: time.Now()},
			"the start time of pid "+strconv.Itoa(pid)+" is not readable")
	})
}

// TestSweepStartTick catches a reused pid taken as a live owner on Linux: the start is compared by
// the boot id and the clock tick of /proc/<pid>/stat, with no tolerance (U13).
func TestSweepStartTick(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("start ticks and boot_id are Linux sources")
	}
	ctx := context.Background()
	self := sweepTestSelf(t)
	r := sweepTestRoots(t)
	live := sweepTestCreate(t, sweepTestOpen(t, r, self), TempRoot)
	// One clock tick (10 ms) later than the running process in the same boot: another process with
	// the same pid. Start stays that of the running process, so only the start identity differs.
	boot, tick := sweepTestSplitID(t, self)
	reused := sweepTestCreate(t, sweepTestOpen(t, r,
		ProcessID{PID: self.PID, Start: self.Start, id: boot + "/" + strconv.FormatUint(tick+1, 10)}), TempRoot)

	rep, err := sweepTestOpen(t, r, self).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if sweepTestExists(t, reused.Path()) {
		t.Errorf("temp dir %q of a pid reused one tick later is left after the sweep", reused.Path())
	}
	if !sweepTestExists(t, live.Path()) {
		t.Errorf("temp dir %q of the live owner was swept", live.Path())
	}
	if rep.Removed != 1 || rep.Live != 1 {
		t.Errorf("Sweep report = %+v, want Removed 1, Live 1", rep)
	}
}

// sweepTestField returns the int field name of rep, or fails the test when SweepReport has none.
func sweepTestField(t *testing.T, rep SweepReport, name string) int {
	t.Helper()
	v := reflect.ValueOf(rep).FieldByName(name)
	if !v.IsValid() || !v.CanInt() {
		t.Errorf("SweepReport %+v has no int field %s: a registered dir of a dead owner that fails the ownership check is not reported", rep, name)
		return -1
	}
	return int(v.Int())
}

// TestSweepUnownedOrphan catches a registered temp dir of a dead owner that fails the ownership
// check and is then neither removed nor reported (DR-36, DR-13): a crash before the marker, a
// dangling link inside, a link out of the root.
func TestSweepUnownedOrphan(t *testing.T) {
	ctx := context.Background()
	self := sweepTestSelf(t)
	r := sweepTestRoots(t)
	base := filepath.Dir(r.Temp)

	// A crash between Mkdir and the marker leaves an empty dir with no .owner.
	unmarked := sweepTestCreate(t, sweepTestOpen(t, r, sweepTestDeadProcess(t)), TempRoot)
	if err := os.Remove(filepath.Join(unmarked.Path(), ".owner")); err != nil {
		t.Fatal(err)
	}
	dangling := sweepTestCreate(t, sweepTestOpen(t, r, sweepTestDeadProcess(t)), TempRoot)
	gone := filepath.Join(base, "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	removeTestLink(t, gone, filepath.Join(dangling.Path(), "cache-link"))
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	escaping := sweepTestCreate(t, sweepTestOpen(t, r, sweepTestDeadProcess(t)), TempRoot)
	keep := filepath.Join(r.Home, "documents", "thesis.txt")
	if err := os.MkdirAll(filepath.Dir(keep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeTestLink(t, filepath.Dir(keep), filepath.Join(escaping.Path(), "documents"))

	rep, err := sweepTestOpen(t, r, self).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if sweepTestExists(t, unmarked.Path()) {
		t.Errorf("empty unmarked temp dir %q of a dead owner is left after the sweep", unmarked.Path())
	}
	if !sweepTestExists(t, keep) {
		t.Fatalf("user file %q removed through a link", keep)
	}
	if rep.Removed != 1 || rep.Live != 0 {
		t.Errorf("Sweep report = %+v, want Removed 1 (the unmarked dir), Live 0", rep)
	}
	if got := sweepTestField(t, rep, "Refused"); got != 2 {
		t.Errorf("Sweep report Refused = %d, want 2 (dangling link, link out of the root)", got)
	}
}

// TestSweepRecordEscape catches a record signed under the owner key whose Path leaves the temp
// root, which makes the sweep remove an empty dir outside it (security review of #6, low).
func TestSweepRecordEscape(t *testing.T) {
	r := sweepTestRoots(t)
	outside := filepath.Join(filepath.Dir(r.Temp), "outside", "empty")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	o := sweepTestOpen(t, r, sweepTestDeadProcess(t))
	dead := o.self
	rec := ownedRecord{
		FormatVersion: ownedFormatVersion,
		Kind:          kindOwnedDir,
		At:            time.Now().UTC(),
		RepoID:        noneField,
		Goal:          noneField,
		Unit:          noneField,
		RunID:         noneField,
		Root:          "temp",
		Path:          filepath.Join("..", "outside", "empty"),
		Owner:         "run-20261011-0003",
		PID:           dead.PID,
		Start:         dead.Start.UTC(),
		StartID:       noneField,
	}
	rec.HMAC = o.mac(kindOwnedDir, o.temp, rec)
	if err := o.appendRecord(rec); err != nil {
		t.Fatalf("append the signed record: %v", err)
	}

	rep, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !sweepTestExists(t, outside) {
		t.Errorf("empty dir %q outside the temp root was removed by a record with Path %q", outside, rec.Path)
	}
	if rep != (SweepReport{}) {
		t.Errorf("Sweep report = %+v, want zero: a record whose Path is not a plain name is no record", rep)
	}
}

// TestSweepRecordTrailingDot catches a signed record whose Path ends in a dot: Windows strips the
// dot, so the sweep removes the unregistered dir "keep" (#109, low). Path must be a plain name
// with no trailing dot on every OS; on Unix the literal dir is made too, so the case bites there.
func TestSweepRecordTrailingDot(t *testing.T) {
	for _, name := range []string{"keep.", "keep.."} {
		t.Run(name, func(t *testing.T) {
			r := sweepTestRoots(t)
			keep := filepath.Join(r.Temp, "keep")
			for _, d := range []string{keep, filepath.Join(r.Temp, name)} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			o := sweepTestOpen(t, r, sweepTestDeadProcess(t))
			rec := ownedRecord{FormatVersion: ownedFormatVersion, Kind: kindOwnedDir, At: time.Now().UTC(),
				RepoID: noneField, Goal: noneField, Unit: noneField, RunID: noneField, Root: "temp", Path: name,
				Owner: "run-20261011-0004", PID: o.self.PID, Start: o.self.Start.UTC(), StartID: noneField}
			rec.HMAC = o.mac(kindOwnedDir, o.temp, rec)
			if err := o.appendRecord(rec); err != nil {
				t.Fatalf("append the signed record: %v", err)
			}
			rep, err := sweepTestOpen(t, r, sweepTestSelf(t)).Sweep(context.Background())
			if err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if !sweepTestExists(t, keep) || !sweepTestExists(t, filepath.Join(r.Temp, name)) {
				t.Errorf("unregistered dir under %s removed by a record with Path %q", r.Temp, name)
			}
			if rep != (SweepReport{}) {
				t.Errorf("Sweep report = %+v, want zero: a Path with a trailing dot is no record", rep)
			}
		})
	}
}

// TestOwnerStateJunction catches a state path that is a junction to another owner-only dir and
// that OpenOwner follows, writing the owner key at the target (#109, low).
func TestOwnerStateJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows-only")
	}
	r := removeTestRoots(t, t.TempDir())
	target := filepath.Join(filepath.Dir(r.State), "elsewhere")
	if err := mkdirOwnerOnly(target); err != nil {
		t.Fatalf("make owner-only target %s: %v", target, err)
	}
	JunctionForTest(t, target, r.State)
	self, err := CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	if o, err := OpenOwner(r, self); !errors.Is(err, ErrRootUnsafe) {
		t.Errorf("OpenOwner of state junction %s -> %s: owner %t, error %v; want %v", r.State, target, o != nil, err, ErrRootUnsafe)
	}
	if removeTestExists(t, filepath.Join(target, ownerKeyFile)) {
		t.Errorf("OpenOwner wrote a key at the junction target %s", target)
	}
}
