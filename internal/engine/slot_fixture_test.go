package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The fixtures of the slot tests (SPEC §6.5, §6.6): a small Node project as the canonical
// repository, node_modules as the stack profile's KeepDirs and package-lock.json as its lock file.
const (
	slotTestSession       = "sess-ada-01"
	slotTestSecondSession = "sess-grace-02"
	slotTestThirdSession  = "sess-linus-03"

	slotTestKeepDir       = "node_modules"
	slotTestLockFile      = "package-lock.json"
	slotTestInstalled     = "node_modules/left-pad/index.js"
	slotTestInstalledBody = "module.exports = function leftPad(s, n) { return String(s).padStart(n) }\n"
	slotTestReadmeBody    = "# greeter\n\nA small greeting service.\n"
	slotTestPackageBody   = "{\n  \"name\": \"greeter\",\n  \"version\": \"1.0.0\"\n}\n"
	slotTestLockBody      = "{\n  \"name\": \"greeter\",\n  \"lockfileVersion\": 3,\n  \"packages\": {\"node_modules/left-pad\": {\"version\": \"1.3.0\"}}\n}\n"

	slotTestPoll = 10 * time.Millisecond
	slotTestWait = 30 * time.Second
	slotTestDay  = 24 * time.Hour
)

// slotTestEpoch is the injected clock's start.
func slotTestEpoch() time.Time { return time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC) }

// slotTestRestoreLogEnv names the file the restore helper appends one line to per run, so a test
// counts the restores; slotTestRestoreJunkEnv makes the helper also write outside node_modules,
// which no verify may accept. Both carry the product prefix, built at run time (SPEC §5.3).
func slotTestRestoreLogEnv() string { return strings.ToUpper(core.Product) + "_SLOT_TEST_RESTORE_LOG" }
func slotTestRestoreJunkEnv() string {
	return strings.ToUpper(core.Product) + "_SLOT_TEST_RESTORE_JUNK"
}

// slotTestClock is the injected clock of a pool; Advance moves it without a sleep.
type slotTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *slotTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *slotTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// slotTestProcs is the injected liveness check: a holder is alive only while its pid runs under
// the start time it was recorded with, so a reused pid is a dead holder (SPEC §6.5 Sweeper).
type slotTestProcs struct {
	mu   sync.Mutex
	live map[int]time.Time
}

func (p *slotTestProcs) Alive(id core.ProcessID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	start, ok := p.live[id.PID]
	return ok && start.Equal(id.Start)
}

func (p *slotTestProcs) run(pid int, start time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.live[pid] = start
}

func (p *slotTestProcs) kill(pid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.live, pid)
}

// slotTestQueue is a Queue writer several goroutines share; waitFor blocks until a text appears.
type slotTestQueue struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	changed chan struct{}
}

func newSlotTestQueue() *slotTestQueue { return &slotTestQueue{changed: make(chan struct{}, 1)} }

func (q *slotTestQueue) Write(b []byte) (int, error) {
	q.mu.Lock()
	n, err := q.buf.Write(b)
	q.mu.Unlock()
	select {
	case q.changed <- struct{}{}:
	default:
	}
	return n, err
}

func (q *slotTestQueue) String() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.buf.String()
}

func (q *slotTestQueue) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(slotTestWait)
	for !strings.Contains(q.String(), want) {
		select {
		case <-q.changed:
		case <-deadline:
			t.Fatalf("queue output never showed %q; got %q", want, q.String())
		}
	}
}

// slotTestFixture is one test's roots, owner, canonical repository and injected clock and liveness.
type slotTestFixture struct {
	roots      core.OwnedRoots
	owner      *core.Owner
	canonical  string
	base       string
	leaseDir   string
	restoreLog string
	outside    string
	clock      *slotTestClock
	procs      *slotTestProcs
}

// slotTestSetup makes the canonical Node project (README, package.json, package-lock.json,
// .gitignore with node_modules/ and dist/, src/index.js) and the lease directory
// <state>/projects/<repo-id>/slots of SPEC §6.5.
func slotTestSetup(t *testing.T) *slotTestFixture {
	t.Helper()
	roots := engineTestRoots(t)
	fx := &slotTestFixture{
		roots:     roots,
		owner:     engineTestOwner(t, roots),
		canonical: roots.Canonical[0],
		outside:   t.TempDir(),
		clock:     &slotTestClock{now: slotTestEpoch()},
		procs:     &slotTestProcs{live: map[int]time.Time{}},
	}
	fx.restoreLog = filepath.Join(fx.outside, "restore.log")
	t.Setenv(slotTestRestoreLogEnv(), fx.restoreLog)
	fx.leaseDir = filepath.Join(roots.State, "projects", "github.com", "sungurerdim", "greeter", "slots")
	if err := os.MkdirAll(fx.leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := fx.canonical
	engineTestGit(t, dir, "init", "-q")
	engineTestGit(t, dir, "config", "user.name", "Ada Lovelace")
	engineTestGit(t, dir, "config", "user.email", "ada.lovelace@example.com")
	engineTestGit(t, dir, "config", "commit.gpgsign", "false")
	engineTestGit(t, dir, "config", "core.autocrlf", "false")
	for name, body := range map[string]string{
		"README.md":      slotTestReadmeBody,
		"package.json":   slotTestPackageBody,
		slotTestLockFile: slotTestLockBody,
		".gitignore":     "node_modules/\ndist/\n",
		"src/index.js":   "module.exports = () => 'hello'\n",
	} {
		engineTestWrite(t, filepath.Join(dir, filepath.FromSlash(name)), body)
	}
	engineTestGit(t, dir, "add", "-A")
	engineTestGit(t, dir, "commit", "-q", "-m", "feat: greeter service")
	fx.base = engineTestGit(t, dir, "rev-parse", "HEAD")
	return fx
}

// commit writes body to name in the canonical repository, commits it and returns the new commit.
func (fx *slotTestFixture) commit(t *testing.T, name, body, msg string) string {
	t.Helper()
	engineTestWrite(t, filepath.Join(fx.canonical, filepath.FromSlash(name)), body)
	engineTestGit(t, fx.canonical, "add", "-A")
	engineTestGit(t, fx.canonical, "commit", "-q", "-m", msg)
	return engineTestGit(t, fx.canonical, "rev-parse", "HEAD")
}

// spec is the pool spec of n slots with the injected clock and liveness check.
func (fx *slotTestFixture) spec(n int, queue *slotTestQueue) SlotPoolSpec {
	return SlotPoolSpec{
		Owner:     fx.owner,
		Canonical: fx.canonical,
		LeaseDir:  fx.leaseDir,
		Size:      n,
		KeepDirs:  []string{slotTestKeepDir},
		LockFiles: []string{slotTestLockFile},
		Restore:   []string{os.Args[0], "-test.run=^TestSlotRestoreHelper$"},
		Now:       fx.clock.Now,
		Alive:     fx.procs.Alive,
		Queue:     queue,
		Poll:      slotTestPoll,
	}
}

// open opens a pool of n slots; each call is a separate pool, as two sessions are two processes.
func (fx *slotTestFixture) open(t *testing.T, n int, queue *slotTestQueue) *SlotPool {
	t.Helper()
	p, err := OpenSlotPool(fx.spec(n, queue))
	if err != nil {
		t.Fatalf("OpenSlotPool: %v", err)
	}
	return p
}

// holder returns a running holder process for session.
func (fx *slotTestFixture) holder(session string, pid int) SlotHolder {
	h := SlotHolder{Session: session, Process: core.ProcessID{PID: pid, Start: slotTestEpoch().Add(-time.Hour)}}
	fx.procs.run(pid, h.Process.Start)
	return h
}

// lease leases a slot at base and fails the test on an error.
func (fx *slotTestFixture) lease(t *testing.T, p *SlotPool, h SlotHolder, base string) *Slot {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
	defer cancel()
	s, err := p.Lease(ctx, h, base)
	if err != nil {
		t.Fatalf("Lease(%s, %s): %v", h.Session, base, err)
	}
	if s == nil {
		t.Fatalf("Lease(%s, %s): no slot and no error", h.Session, base)
	}
	return s
}

func (fx *slotTestFixture) release(t *testing.T, p *SlotPool, s *Slot) {
	t.Helper()
	if err := p.Release(t.Context(), s); err != nil {
		t.Fatalf("Release(slot %d): %v", s.K, err)
	}
}

// restores counts the restore runs so far.
func (fx *slotTestFixture) restores(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(fx.restoreLog)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func (fx *slotTestFixture) leaseFile(k int) string {
	return filepath.Join(fx.leaseDir, "s"+strconv.Itoa(k)+".lease")
}

// slotTestExists reports whether path exists, failing the test on any other error.
func slotTestExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}

// slotTestSentinel plants a file in the slot's git directory, which no reset touches and only a
// fresh clone drops, and returns its path.
func slotTestSentinel(t *testing.T, s *Slot) string {
	t.Helper()
	p := filepath.Join(engineTestGit(t, s.Dir, "rev-parse", "--absolute-git-dir"), core.Product+"-slot-sentinel")
	engineTestWrite(t, p, "planted by the test\n")
	return p
}

// slotTestAssertClean fails unless the slot is the clean slot of SPEC §6.5 verify: HEAD detached
// at base, `git status --porcelain --ignored` shows only the KeepDirs line and
// `git worktree list --porcelain` names the slot alone.
func slotTestAssertClean(t *testing.T, s *Slot, base string) {
	t.Helper()
	if head := engineTestGit(t, s.Dir, "rev-parse", "HEAD"); head != base {
		t.Errorf("slot %d HEAD = %s, want the base %s", s.K, head, base)
	}
	if ref := engineTestGit(t, s.Dir, "rev-parse", "--abbrev-ref", "HEAD"); ref != "HEAD" {
		t.Errorf("slot %d is on branch %q, want a detached HEAD", s.K, ref)
	}
	if out := engineTestGit(t, s.Dir, "status", "--porcelain", "--ignored"); out != "" {
		for _, line := range strings.Split(out, "\n") {
			if line != "!! "+slotTestKeepDir+"/" {
				t.Errorf("slot %d handed out dirty: status line %q is outside the allow-list", s.K, line)
			}
		}
	}
	wts := engineTestGit(t, s.Dir, "worktree", "list", "--porcelain")
	if n := strings.Count("\n"+wts, "\nworktree "); n != 1 {
		t.Errorf("slot %d has %d worktrees, want the slot alone:\n%s", s.K, n, wts)
	}
}

// slotTestResult is what a background lease returned.
type slotTestResult struct {
	p    *SlotPool
	s    *Slot
	err  error
	name string
}

// leaseAsync leases in a goroutine and sends the result on out.
func slotTestLeaseAsync(ctx context.Context, p *SlotPool, h SlotHolder, base string, out chan<- slotTestResult) {
	go func() {
		s, err := p.Lease(ctx, h, base)
		out <- slotTestResult{p: p, s: s, err: err, name: h.Session}
	}()
}

func slotTestReceive(t *testing.T, ch <-chan slotTestResult, what string) slotTestResult {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil || r.s == nil {
			t.Fatalf("%s: lease of %s: slot %v, err %v", what, r.name, r.s, r.err)
		}
		return r
	case <-time.After(slotTestWait):
		t.Fatalf("%s: no lease within %v", what, slotTestWait)
	}
	return slotTestResult{}
}

func slotTestNotYet(t *testing.T, ch <-chan slotTestResult, what string) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("%s: lease of %s returned (slot %v, err %v) while every slot was held", what, r.name, r.s, r.err)
	default:
	}
}

// slotTestBrokenQueue is a queue output whose every write fails.
type slotTestBrokenQueue struct{}

func (slotTestBrokenQueue) Write([]byte) (int, error) { return 0, errors.New("queue output closed") }

// slotTestVictim makes a user repository outside the slot pool, <base>/victim/clone, with a tracked
// and an untracked file, and returns the victim directory, the repository and its HEAD.
func slotTestVictim(t *testing.T, fx *slotTestFixture) (victim, repo, head string) {
	t.Helper()
	victim = filepath.Join(filepath.Dir(fx.roots.Cache), "victim")
	repo = filepath.Join(victim, cloneDirName)
	engineTestWrite(t, filepath.Join(repo, "tracked.txt"), "tracked by the user\n")
	engineTestWrite(t, filepath.Join(repo, "precious.txt"), "untracked work of the user\n")
	engineTestGit(t, repo, "init", "-q")
	engineTestGit(t, repo, "add", "tracked.txt")
	engineTestGit(t, repo, "-c", "user.name=Ada Lovelace", "-c", "user.email=ada.lovelace@example.com",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "docs: the user's work")
	return victim, repo, engineTestGit(t, repo, "rev-parse", "HEAD")
}

// slotTestVictimIntact fails unless the victim repository kept its HEAD and both of its files.
func slotTestVictimIntact(t *testing.T, repo, head string) {
	t.Helper()
	if got := engineTestGit(t, repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("the user repository moved from %s to %s", head, got)
	}
	for _, name := range []string{"tracked.txt", "precious.txt"} {
		if !slotTestExists(t, filepath.Join(repo, name)) {
			t.Errorf("%s of the user repository was removed", name)
		}
	}
}

// TestSlotRestoreHelper is the stack profile's restore command of the slot tests, run by the pool
// as a child: it installs left-pad into node_modules of its working directory and logs one line.
// It does nothing in a normal test run.
func TestSlotRestoreHelper(t *testing.T) {
	logPath := os.Getenv(slotTestRestoreLogEnv())
	if logPath == "" {
		return
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	engineTestWrite(t, filepath.Join(wd, filepath.FromSlash(slotTestInstalled)), slotTestInstalledBody)
	if os.Getenv(slotTestRestoreJunkEnv()) != "" {
		engineTestWrite(t, filepath.Join(wd, ".restore-cache", "left-pad-1.3.0.tgz"), "tarball\n")
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(wd + "\n"); err != nil {
		t.Fatal(errors.Join(err, f.Close()))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// goneSpec is the spec of n slots whose owner is a process that has exited: a gone creator.
func (fx *slotTestFixture) goneSpec(t *testing.T, n int) SlotPoolSpec {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	o, err := core.OpenOwner(fx.roots, core.ProcessID{PID: cmd.ProcessState.Pid(), Start: slotTestEpoch()})
	if err != nil {
		t.Fatal(err)
	}
	spec := fx.spec(n, newSlotTestQueue())
	spec.Owner = o
	return spec
}

// setRecord rewrites the record of slot k with edit.
func (fx *slotTestFixture) setRecord(t *testing.T, k int, edit func(r *slotRecord)) {
	t.Helper()
	path := filepath.Join(fx.leaseDir, "s"+strconv.Itoa(k)+".slot")
	var rec slotRecord
	if err := json.Unmarshal([]byte(engineTestRead(t, path)), &rec); err != nil {
		t.Fatal(err)
	}
	edit(&rec)
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	engineTestWrite(t, path, string(b))
}
