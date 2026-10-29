package gates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Resource lanes and CPU tickets (#596, SPEC §7.8, L63, L78). A check queues in the lane of each
// exclusive resource it names and in the CPU lane for as many tickets as its workers. A lane is a
// directory of ticket files served first in, first out; a request waits only in blocking OS locks
// (core.AcquireFileLock), never polling; lane_io.go does the IO (STD-10).
//
// The owner of ticket <seq>.ticket holds two OS locks: <seq>.wait while it queues and <seq>.alive
// until it ends. A request blocks on the wait lock of the ticket just ahead, so none overtakes one
// queued before it, then, while the tickets ahead leave too little capacity, on the alive lock of
// the oldest. The OS frees both locks when an owner dies; a ticket whose owner no longer runs is
// dropped and logged; one that cannot be read holds its place until its alive lock is got (DR-6).
// lane.lock serialises the numbering of new tickets and the sweep of an idle lane's lock files; sweep
// (lane_io.go) states the one rule under which a lock file is removed.

const (
	laneCPUDir       = "cpu"
	laneExclusiveDir = "exclusive"
	laneCPULabel     = "cpu" // distinct: the lane label in records; the dir is the on-disk name
	laneMutexName    = "lane.lock"
	laneTicketExt    = ".ticket"
	laneWaitExt      = ".wait"
	laneAliveExt     = ".alive"
	laneTempExt      = ".tmp"
	laneSeqDigits    = 20
	laneTokenSep     = ":"
	laneTokenParts   = 3
	laneMaxPool      = 1 << 20 // far above any core count; it keeps the ticket sums in range
	laneTicketMax    = 4096    // bytes; a ticket is far smaller
)

// laneNamePattern is the form of a resource name, which becomes a directory name: no separator,
// dot or drive (A2 of security-591).
var laneNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// errLaneReleased is returned by a second Release of one lease.
var errLaneReleased = errors.New("lane lease already released")

// laneSlotEnv and laneTokenEnv name the variables a lease gives its check: the slot the project
// derives its port and test database from, and the token its nested calls show.
func laneSlotEnv() string  { return strings.ToUpper(core.Product) + "_SLOT" }
func laneTokenEnv() string { return strings.ToUpper(core.Product) + "_LANE_TOKEN" }

func laneResourceLabel(name string) string { return "resource " + name }

// laneReservedNames are the Windows device names; no lane directory takes one, on any OS (A2).
func laneReservedNames() []string {
	names := []string{"con", "prn", "aux", "nul"}
	for i := range 10 {
		names = append(names, "com"+strconv.Itoa(i), "lpt"+strconv.Itoa(i))
	}
	return names
}

// LaneRequest is what one check asks of the lanes: the exclusive resources it cannot share and its
// worker count, the number of CPU tickets it takes. Repo and Kind name the job to its waiters. Slot
// is the index of the caller's slot lease, from 1: the lease runs with it as the slot variable (SPEC
// 6.5), the lanes never number slots themselves. A nested call must name its holder's slot.
type LaneRequest struct {
	Repo      string
	Kind      string
	Exclusive []string
	Workers   int
	Slot      int
}

// Lanes admits checks by resource on one machine. Root holds one directory per lane, Pool is the
// measured CPU ticket pool (DR-3) and MaxWait bounds one Acquire (DR-1). Log gets each wait
// position and dropped ticket; Lookup reads the env (core.LookupEnv when nil), Now the clock. A
// process uses one Lanes per Root: its gates keep one request per lane in the OS wait (L78).
type Lanes struct {
	Root    string
	Pool    core.Measured[int64]
	MaxWait time.Duration
	Log     io.Writer
	Lookup  func(string) string
	Now     func() time.Time
	lock    func(context.Context, string) (*core.FileLock, error) // core.AcquireFileLock when nil
	read    func(dir, name string) ([]byte, error)                // readLaneTicket when nil
	mu      sync.Mutex
	gates   map[string]chan struct{}
}

// LaneRecord is the queue record of one lane of a lease: Started minus Asked is its queue wait;
// Ended is zero while it is held.
type LaneRecord struct {
	Lane    string
	Asked   time.Time
	Started time.Time
	Ended   time.Time
}

// LaneLease is the lanes one check holds until Release.
type LaneLease struct {
	lanes    *Lanes
	slot     int
	token    string
	nested   bool
	holds    []*laneHold
	records  []LaneRecord
	released bool
}

// laneSpec is one lane a request queues in: capacity tickets in all, weight of them for the
// request.
type laneSpec struct {
	label            string
	dir              string // relative to Root
	capacity, weight int
}

// laneTicket is the content of one ticket file; seq comes from its file name. unread marks the
// placeholder of a ticket that is there but empty or refused: only its seq is known.
type laneTicket struct {
	PID     int       `json:"pid"`
	Start   time.Time `json:"start"`
	StartID string    `json:"start_id"`
	Repo    string    `json:"repo"`
	Kind    string    `json:"kind"`
	Since   time.Time `json:"since"`
	Weight  int       `json:"weight"`
	seq     uint64
	unread  bool
}

func (t laneTicket) owner() core.ProcessID { return core.ProcessFrom(t.PID, t.Start, t.StartID) }

// laneWait names the lock a request waits on next: the wait or alive lock of ticket seq.
type laneWait struct {
	seq uint64
	ext string
}

// Acquire queues req in each lane it needs, its exclusive resources in name order and the CPU lane
// last, so no two requests each hold a lane the other waits for. It returns once every lane admits
// req, or fails when ctx or MaxWait ends first (DR-1), leaving no ticket. A call whose env carries
// the token of a live holder runs inside that holder's check: it takes no ticket, keeps the slot.
func (l *Lanes) Acquire(ctx context.Context, req LaneRequest) (*LaneLease, error) {
	if lease, ok, err := l.nested(req); ok || err != nil {
		return lease, err
	}
	specs, err := l.plan(req)
	if err != nil {
		return nil, err
	}
	self, err := core.CurrentProcess()
	if err != nil {
		return nil, fmt.Errorf("lane: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, l.MaxWait)
	defer cancel()
	lease := &LaneLease{lanes: l, slot: req.Slot, token: laneToken(self)}
	since := l.now().UTC()
	for _, s := range specs {
		asked := l.now()
		h, err := l.take(ctx, s, laneTicket{PID: self.PID, Start: self.Start.UTC(), StartID: self.StartID(),
			Repo: req.Repo, Kind: req.Kind, Since: since, Weight: s.weight})
		if err != nil {
			return nil, errors.Join(fmt.Errorf("lane %s: %w", s.label, err), lease.Release())
		}
		lease.holds = append(lease.holds, h)
		lease.records = append(lease.records, LaneRecord{Lane: s.label, Asked: asked, Started: l.now()})
	}
	return lease, nil
}

// nested returns the lease of a call inside a holder's check. A token whose owner is gone was left
// in a shell and is ignored; a malformed token or slot is refused, never read as a nested call.
func (l *Lanes) nested(req LaneRequest) (*LaneLease, bool, error) {
	token := l.lookup(laneTokenEnv())
	if token == "" {
		return nil, false, nil
	}
	owner, ok := parseLaneToken(token)
	slot, err := strconv.Atoi(l.lookup(laneSlotEnv()))
	if !ok || err != nil || slot < 1 {
		return nil, false, laneError("a lane token and a slot as a holding check sets them",
			fmt.Sprintf("%s=%q and %s=%q", laneTokenEnv(), token, laneSlotEnv(), l.lookup(laneSlotEnv())),
			"unset both variables, or run the call inside the check that holds the lanes")
	}
	if !core.OwnerAlive(owner) {
		return nil, false, nil
	}
	if req.Slot != slot {
		return nil, false, laneError("a nested call that names the slot of the check it runs in",
			fmt.Sprintf("slot %d in the request and %d in %s", req.Slot, slot, laneSlotEnv()),
			"pass the slot lease index of the running check in LaneRequest.Slot")
	}
	return &LaneLease{lanes: l, slot: slot, token: token, nested: true}, true, nil
}

// plan checks l and req and returns the lanes req queues in, in the order it takes them.
func (l *Lanes) plan(req LaneRequest) ([]laneSpec, error) {
	if err := checkLaneRoot(l.Root); err != nil {
		return nil, err
	}
	if !l.Pool.Known || l.Pool.Value < 1 || l.Pool.Value > laneMaxPool {
		return nil, laneError(fmt.Sprintf("a measured CPU ticket pool of 1 to %d (DR-3)", laneMaxPool),
			fmt.Sprintf("%+v", l.Pool), "measure the pool at run time and pass it to the lanes")
	}
	if l.MaxWait <= 0 || req.Workers < 1 {
		return nil, laneError("a wait bound above 0 (DR-1) and at least 1 worker",
			fmt.Sprintf("MaxWait %v and %d workers", l.MaxWait, req.Workers), "derive both from the measured durations and cores")
	}
	if req.Slot < 1 {
		return nil, laneError("the index of the caller's slot lease, from 1 (SPEC 6.5)", fmt.Sprintf("slot %d", req.Slot),
			"pass the K of the slot lease in LaneRequest.Slot")
	}
	names := slices.Clone(req.Exclusive)
	slices.Sort(names)
	specs := make([]laneSpec, 0, len(names)+1)
	for _, n := range slices.Compact(names) {
		if err := checkLaneName(n); err != nil {
			return nil, err
		}
		specs = append(specs, laneSpec{label: laneResourceLabel(n), dir: filepath.Join(laneExclusiveDir, n), capacity: 1, weight: 1})
	}
	pool := int(l.Pool.Value)
	return append(specs, laneSpec{label: laneCPULabel, dir: laneCPUDir, capacity: pool, weight: min(req.Workers, pool)}), nil
}

// checkLaneRoot refuses a lane root that is empty or relative, climbs with "..", or is a UNC or
// device path (\\host\share, //host/share, \\.\, \\?\): an open of a UNC path sends the user's
// NTLM credentials to that host (A2). It reads only the text, so it runs before any IO.
func checkLaneRoot(root string) error {
	found := ""
	switch {
	case root == "":
		found = "an empty root"
	case strings.HasPrefix(root, `\\`) || strings.HasPrefix(root, "//") || strings.HasPrefix(root, `\/`) ||
		strings.HasPrefix(root, `/\`) || len(filepath.VolumeName(root)) > 2:
		found = "a UNC or device path"
	case !filepath.IsAbs(root):
		found = "a relative path"
	case slices.Contains(strings.FieldsFunc(root, func(r rune) bool { return r == '/' || r == '\\' }), ".."):
		found = `a ".." element`
	}
	if found == "" {
		return nil
	}
	return laneError("an absolute local lane root", fmt.Sprintf("%s: %q", found, root), "resolve the lanes under the local state root")
}

// checkLaneName refuses a resource name that is no plain directory name or is a device name (A2).
func checkLaneName(name string) error {
	if laneNamePattern.MatchString(name) && !slices.Contains(laneReservedNames(), name) {
		return nil
	}
	return laneError("a resource name of lower-case letters, digits, - and _ that is no device name",
		strconv.Quote(name), "rename the exclusive resource in gate.json")
}

func laneError(expected, found, next string) error {
	return &core.Error{Outcome: core.OutcomeError, Expected: expected, Found: found, Next: next}
}

// laneStep returns the lock a ticket of weight waits on next, or false when the lane admits it:
// first the wait lock of the newest ticket ahead not seen to start (tickets start in order, so once
// it started, all ahead did), then, while the tickets ahead leave too little of capacity, the alive
// lock of the oldest, which ends first in FIFO order.
func laneStep(ahead []laneTicket, started map[uint64]bool, weight, capacity int) (laneWait, bool) {
	if len(ahead) == 0 {
		return laneWait{}, false
	}
	if last := ahead[len(ahead)-1]; !started[last.seq] {
		return laneWait{seq: last.seq, ext: laneWaitExt}, true
	}
	used := weight
	for _, t := range ahead {
		used += min(max(t.Weight, 1), capacity) // a foreign weight stays in range
	}
	if used <= capacity {
		return laneWait{}, false
	}
	return laneWait{seq: ahead[0].seq, ext: laneAliveExt}, true
}

// laneWaitLine is a waiter's line: its position (the tickets ahead) and the job just ahead. Repo
// and kind are quoted, so a name cannot fake a second line.
func laneWaitLine(label string, ahead []laneTicket) string {
	t := ahead[len(ahead)-1]
	if t.unread {
		return fmt.Sprintf("lane %s: position %d, behind ticket %d, which cannot be read\n", label, len(ahead), t.seq)
	}
	return fmt.Sprintf("lane %s: position %d, behind repo %q kind %q since %s\n", label, len(ahead), t.Repo, t.Kind,
		t.Since.UTC().Format(time.RFC3339Nano))
}

// laneDropLine is the line logged for the dropped ticket of a dead owner, with its pid and start.
func laneDropLine(label string, t laneTicket) string {
	return fmt.Sprintf("lane %s: dropped ticket %d of a dead owner, pid %d started %s (repo %q kind %q)\n", label, t.seq,
		t.PID, t.Start.UTC().Format(time.RFC3339Nano), t.Repo, t.Kind)
}

// laneSeqName is the file name of ticket seq with ext; the fixed width keeps name and seq order one.
func laneSeqName(seq uint64, ext string) string {
	return fmt.Sprintf("%0*d%s", laneSeqDigits, seq, ext)
}

// laneSeqOf reads the seq and ext of a lane file name; false for a name of another form.
func laneSeqOf(name string) (uint64, string, bool) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if len(stem) != laneSeqDigits {
		return 0, "", false
	}
	seq, err := strconv.ParseUint(stem, 10, 64)
	return seq, ext, err == nil
}

// laneNextSeq returns the seq after every seq a name holds; false when none is left. A seq is
// never reused while a ticket below it is awaited: the waiter's own ticket stays above it.
func laneNextSeq(names []string) (uint64, bool) {
	var top uint64
	for _, n := range names {
		if seq, _, ok := laneSeqOf(n); ok {
			top = max(top, seq)
		}
	}
	return top + 1, top < math.MaxUint64
}

func decodeLaneTicket(seq uint64, b []byte) (laneTicket, error) {
	var t laneTicket
	err := json.Unmarshal(b, &t)
	t.seq = seq
	return t, err
}

// laneToken is the token a nested call shows: the holder's pid, start and start identity.
func laneToken(p core.ProcessID) string {
	return strconv.Itoa(p.PID) + laneTokenSep + strconv.FormatInt(p.Start.UnixNano(), 10) + laneTokenSep + p.StartID()
}

// parseLaneToken reads a token of laneToken; false for any other text.
func parseLaneToken(s string) (core.ProcessID, bool) {
	parts := strings.SplitN(s, laneTokenSep, laneTokenParts)
	if len(parts) != laneTokenParts {
		return core.ProcessID{}, false
	}
	pid, err := strconv.Atoi(parts[0])
	ns, nerr := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || nerr != nil || pid < 1 {
		return core.ProcessID{}, false
	}
	return core.ProcessFrom(pid, time.Unix(0, ns), parts[2]), true
}

// Slot is the slot of the check, from 1: its AIWE_SLOT.
func (lease *LaneLease) Slot() int { return lease.slot }

// Env returns the variables the check runs with: its slot and the token its nested calls show.
func (lease *LaneLease) Env() []string {
	return []string{laneSlotEnv() + "=" + strconv.Itoa(lease.slot), laneTokenEnv() + "=" + lease.token}
}

// Records returns the queue record of each lane of the lease.
func (lease *LaneLease) Records() []LaneRecord { return slices.Clone(lease.records) }

// Release ends the lease, its lanes in reverse order. The end time is taken before any lock is
// freed, so a record never ends after the next holder's starts. A second call returns an error.
func (lease *LaneLease) Release() error {
	if lease.released {
		return errLaneReleased
	}
	lease.released = true
	ended := lease.lanes.now()
	for i := range lease.records {
		lease.records[i].Ended = ended
	}
	var errs []error
	for _, h := range slices.Backward(lease.holds) {
		errs = append(errs, h.end())
	}
	return errors.Join(errs...)
}
