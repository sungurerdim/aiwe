package core

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// markerTestRaceFor is how long a race test calls RemoveOwned while the marker is swapped.
const markerTestRaceFor = 2 * time.Second

// markerTestBad is a plain marker of a few bytes that matches no owned_dir record.
const markerTestBad = `{"path":"not-the-owned-dir","owner":"someone-else","pid":1}`

// markerTestOwned makes an owned dir and returns the owner, the dir and its marker's bytes.
func markerTestOwned(t *testing.T) (*Owner, OwnedDir, []byte) {
	t.Helper()
	r := removeTestRoots(t, t.TempDir())
	o := removeTestOpen(t, r)
	d := removeTestCreate(t, o, TempRoot)
	removeTestWrite(t, filepath.Join(d.Path(), "work.txt"), "owned work")
	marker, err := os.ReadFile(filepath.Join(d.Path(), markerFile))
	if err != nil {
		t.Fatalf("read the owned marker: %v", err)
	}
	return o, d, marker
}

// markerTestSwapper puts each source in turn under the marker's name: it hard-links the source to a
// spare name beside the sources and renames that over the marker, so the marker name always exists.
// A source that is a symlink is made afresh with its target instead: a hard link to a symlink
// never showed the open following it in the race on Linux.
// The returned stop ends the loop and reports how many swaps took effect.
func markerTestSwapper(t *testing.T, marker string, srcs []string) func() int64 {
	t.Helper()
	spare := filepath.Join(filepath.Dir(srcs[0]), "next")
	targets := make([]string, len(srcs))
	for i, s := range srcs {
		if fi, err := os.Lstat(s); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			if targets[i], err = os.Readlink(s); err != nil {
				t.Fatalf("readlink %s: %v", s, err)
			}
		}
	}
	var swaps atomic.Int64
	quit := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-quit:
				return
			default:
			}
			_ = os.Remove(spare)
			var mk error
			if tg := targets[i%len(srcs)]; tg != "" {
				mk = os.Symlink(tg, spare)
			} else {
				mk = os.Link(srcs[i%len(srcs)], spare)
			}
			if mk != nil {
				continue
			}
			if os.Rename(spare, marker) == nil {
				swaps.Add(1)
			}
		}
	}()
	var once sync.Once
	stop := func() int64 {
		once.Do(func() { close(quit); wg.Wait() })
		return swaps.Load()
	}
	t.Cleanup(func() { stop() })
	return stop
}

// TestOwnerMarkerSwappedForLarger catches a marker whose type and size are read from its name and
// whose bytes are read in a second step by name (#172, security review of #124). A plain marker of
// a few bytes passes the size check; a valid marker padded past 4096 bytes is then read in its
// place and accepted. Neither marker is acceptable on its own: the short one matches no record and
// the long one is too large. So RemoveOwned must refuse every time; one success means the bytes
// came from a file that was never checked. The check and the read must use one handle.
func TestOwnerMarkerSwappedForLarger(t *testing.T) {
	o, d, marker := markerTestOwned(t)
	src := filepath.Join(o.temp, "swap-src")
	small := filepath.Join(src, "small")
	large := filepath.Join(src, "large")
	removeTestWrite(t, small, markerTestBad)
	removeTestWrite(t, large, string(marker)+strings.Repeat(" ", 2*maxMarkerBytes))
	p := filepath.Join(d.Path(), markerFile)
	removeTestWrite(t, p, markerTestBad)
	stop := markerTestSwapper(t, p, []string{small, large})

	calls := 0
	accepted := false
	for end := time.Now().Add(markerTestRaceFor); time.Now().Before(end); calls++ {
		if err := o.RemoveOwned(context.Background(), d); err == nil {
			accepted = true
			break
		}
	}
	swaps := stop()
	if swaps < 2 {
		t.Fatalf("the marker was swapped %d times in %v; the race never ran", swaps, markerTestRaceFor)
	}
	if accepted {
		t.Errorf("RemoveOwned accepted a marker swapped from %d plain bytes to %d bytes between its check and its read (call %d of %d swaps); want ErrNotOwned every time",
			len(markerTestBad), len(marker)+2*maxMarkerBytes, calls+1, swaps)
	}
}

// TestOwnerMarkerSwappedForFIFO catches a marker that is opened by name after its type was checked
// by name (#172, security review of #124): a plain marker swapped for a FIFO with no writer makes
// the open block, and RemoveOwned hangs. The marker must be opened without blocking, and its type
// checked on that handle; every call must return within 1 s, never with success.
func TestOwnerMarkerSwappedForFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a FIFO is a Unix file type")
	}
	o, d, _ := markerTestOwned(t)
	src := filepath.Join(o.temp, "swap-src")
	small := filepath.Join(src, "small")
	fifo := filepath.Join(src, "fifo")
	removeTestWrite(t, small, markerTestBad)
	if out, err := exec.Command("mkfifo", "-m", "600", fifo).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo %s: %v: %s", fifo, err, out)
	}
	p := filepath.Join(d.Path(), markerFile)
	removeTestWrite(t, p, markerTestBad)
	stop := markerTestSwapper(t, p, []string{small, fifo})

	calls := 0
	for end := time.Now().Add(markerTestRaceFor); time.Now().Before(end); calls++ {
		done := make(chan error, 1)
		go func() { done <- o.RemoveOwned(context.Background(), d) }()
		select {
		case err := <-done:
			if err == nil {
				stop()
				t.Fatalf("RemoveOwned accepted a marker that was a FIFO or a foreign file (call %d)", calls+1)
			}
			continue
		case <-time.After(time.Second):
		}
		swaps := stop()
		t.Errorf("RemoveOwned still blocked after 1 s on a marker swapped for a FIFO (call %d, %d swaps); want a refusal without blocking", calls+1, swaps)
		// A writer releases the blocked open of the FIFO, so the goroutine ends with the test.
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		<-done
		return
	}
	if swaps := stop(); swaps < 2 {
		t.Fatalf("the marker was swapped %d times in %v; the race never ran", swaps, markerTestRaceFor)
	}
}

// TestOwnerMarkerShape catches a marker check that follows a link, reads a directory or a FIFO, or
// reads more than 4096 bytes, and a fix that refuses the owner's own marker: the marker as made
// loads, and each marker that is not a plain file of at most 4096 bytes is refused with ErrNotOwned
// and nothing is removed.
func TestOwnerMarkerShape(t *testing.T) {
	t.Run("the marker as made loads", func(t *testing.T) {
		o, d, _ := markerTestOwned(t)
		if err := o.RemoveOwned(context.Background(), d); err != nil {
			t.Fatalf("RemoveOwned of an owned dir with its own marker = %v, want nil", err)
		}
		if removeTestExists(t, d.Path()) {
			t.Errorf("RemoveOwned returned nil and left %s", d.Path())
		}
	})
	for _, c := range []struct {
		name string
		make func(t *testing.T, p string, marker []byte)
	}{
		{"a valid marker padded past 4096 bytes", func(t *testing.T, p string, marker []byte) {
			removeTestWrite(t, p, string(marker)+strings.Repeat(" ", maxMarkerBytes))
		}},
		{"a directory", func(t *testing.T, p string, _ []byte) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			removeTestMkdir(t, p)
		}},
		{"a symlink to a copy of the marker", func(t *testing.T, p string, marker []byte) {
			cp := filepath.Join(filepath.Dir(p), "marker-copy")
			removeTestWrite(t, cp, string(marker))
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Base(cp), p); err != nil {
				t.Skipf("symlink %s: %v (no symlink privilege)", p, err)
			}
		}},
		{"a FIFO", func(t *testing.T, p string, _ []byte) {
			if runtime.GOOS == "windows" {
				t.Skip("a FIFO is a Unix file type")
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("mkfifo", "-m", "600", p).CombinedOutput(); err != nil {
				t.Fatalf("mkfifo %s: %v: %s", p, err, out)
			}
		}},
	} {
		t.Run(c.name+" is refused", func(t *testing.T) {
			o, d, marker := markerTestOwned(t)
			p := filepath.Join(d.Path(), markerFile)
			c.make(t, p, marker)
			done := make(chan error, 1)
			go func() { done <- o.RemoveOwned(context.Background(), d) }()
			select {
			case err := <-done:
				if !errors.Is(err, ErrNotOwned) {
					t.Errorf("RemoveOwned with a marker that is %s = %v, want %v", c.name, err, ErrNotOwned)
				}
			case <-time.After(time.Second):
				t.Errorf("RemoveOwned with a marker that is %s still blocked after 1 s; want a refusal", c.name)
				if w, err := os.OpenFile(p, os.O_WRONLY, 0); err == nil {
					_ = w.Close()
				}
				<-done
			}
			if !removeTestExists(t, filepath.Join(d.Path(), "work.txt")) {
				t.Errorf("RemoveOwned removed the work of a dir whose marker is %s", c.name)
			}
		})
	}
}

// TestOwnerMarkerSwappedForLink catches a marker whose name is checked as a plain file while the
// open follows a link put there after the check (#172, security review of 34b6c7a). The name
// alternates between a plain marker that matches no record and a link to a valid copy of the
// marker in the same dir. The handle must be the file the name check saw; one success means the
// bytes came through a link.
func TestOwnerMarkerSwappedForLink(t *testing.T) {
	o, d, marker := markerTestOwned(t)
	src := filepath.Join(o.temp, "swap-src")
	small := filepath.Join(src, "small")
	link := filepath.Join(src, "link")
	// The link is relative: under the marker name it names the copy beside it, inside the root.
	removeTestWrite(t, small, markerTestBad)
	cp := filepath.Join(d.Path(), "marker-copy")
	removeTestWrite(t, cp, string(marker))
	if err := os.Symlink(filepath.Base(cp), link); err != nil {
		t.Skipf("symlink %s: %v (no symlink privilege)", link, err)
	}
	p := filepath.Join(d.Path(), markerFile)
	removeTestWrite(t, p, markerTestBad)
	stop := markerTestSwapper(t, p, []string{small, link})

	calls := 0
	accepted := false
	for end := time.Now().Add(markerTestRaceFor); time.Now().Before(end); calls++ {
		if err := o.RemoveOwned(context.Background(), d); err == nil {
			accepted = true
			break
		}
	}
	swaps := stop()
	if swaps < 2 {
		t.Fatalf("the marker was swapped %d times in %v; the race never ran", swaps, markerTestRaceFor)
	}
	if accepted {
		t.Errorf("RemoveOwned accepted a marker swapped from a plain file to a link between its check and its open (call %d of %d swaps); want ErrNotOwned every time", calls+1, swaps)
	}
}

// markerTestGrowTo is the size the marker grows to in place in TestOwnerMarkerGrownInPlace.
const markerTestGrowTo = 64 << 20

// TestOwnerMarkerGrownInPlace catches a marker read whole, or read past 4096 bytes and accepted,
// when the file the handle checked grows after the check (#172, security review of 34b6c7a). Before
// each call the marker is short, ends in " x" and matches no record. During the call, after a
// random delay, it grows in place, same inode, to 64 MiB, and only then turns its first 4097 bytes
// into the valid marker and spaces; it never shrinks while a call runs. So no call may succeed,
// and no call may allocate the grown file: the read stops after 4097 bytes and the bytes past 4096
// are refused, not dropped.
func TestOwnerMarkerGrownInPlace(t *testing.T) {
	o, d, marker := markerTestOwned(t)
	p := filepath.Join(d.Path(), markerFile)
	l := int64(len(marker))
	removeTestWrite(t, p, string(marker)+" x")
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open the marker to grow it: %v", err)
	}
	defer f.Close()
	pad := []byte(strings.Repeat(" ", 2*maxMarkerBytes))
	space, x := []byte(" "), []byte("x")
	grow := func(delay time.Duration) error {
		for start := time.Now(); time.Since(start) < delay; {
		}
		_, e1 := f.WriteAt(pad, l+2)
		e2 := f.Truncate(markerTestGrowTo)
		_, e3 := f.WriteAt(space, l+1)
		return errors.Join(e1, e2, e3)
	}

	calls, accepted := 0, false
	var most uint64
	var before, after runtime.MemStats
	took := time.Millisecond
	for end := time.Now().Add(markerTestRaceFor); time.Now().Before(end); calls++ {
		if _, err := f.WriteAt(x, l+1); err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(l + 2); err != nil {
			t.Fatal(err)
		}
		grown := make(chan error, 1)
		delay := rand.N(took)
		runtime.ReadMemStats(&before)
		start := time.Now()
		go func() { grown <- grow(delay) }()
		err := o.RemoveOwned(context.Background(), d)
		took = max(time.Since(start), time.Microsecond)
		runtime.ReadMemStats(&after)
		if gerr := <-grown; gerr != nil {
			t.Fatalf("grow the marker in place: %v", gerr)
		}
		most = max(most, after.TotalAlloc-before.TotalAlloc)
		if err == nil {
			accepted = true
			break
		}
	}
	if accepted {
		t.Errorf("RemoveOwned accepted a marker that grew past 4096 bytes after its check (call %d); want ErrNotOwned every time", calls+1)
	}
	if most > 8<<20 {
		t.Errorf("a RemoveOwned call allocated %d bytes while the marker grew to %d bytes in %d calls; want the read bounded at %d bytes", most, markerTestGrowTo, calls, maxMarkerBytes+1)
	}
}
