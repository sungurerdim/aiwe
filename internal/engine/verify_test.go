package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/gates"
)

// verifyTestEnv marks a run of this test binary as the red unit verify the tests run: the helper
// appends one line to the record file its first argument names, prints fifty numbered lines and
// exits 4, so a red verify has a step, a command, an exit code and a tail to return.
func verifyTestEnv() string { return strings.ToUpper(core.Product) + "_ENGINE_TEST_VERIFY" }

func init() {
	if os.Getenv(verifyTestEnv()) != "1" || len(os.Args) < 2 {
		return
	}
	record, err := os.OpenFile(os.Args[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(5)
	}
	if _, err := record.WriteString("ran\n"); err != nil {
		os.Exit(6)
	}
	if err := record.Close(); err != nil {
		os.Exit(7)
	}
	for i := 0; i < 50; i++ {
		fmt.Printf("verify line %02d\n", i)
	}
	fmt.Println("verify detail tail")
	os.Exit(4)
}

// verifyTestDigest returns the sha256 hex digest of text, the form a check's declared inputs are
// summarised as (SPEC §7.8).
func verifyTestDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// verifyTestStep returns the red unit verify the tests run: this test binary, told by verifyTestEnv
// to record a run in record and exit 4.
func verifyTestStep(record string) gates.GateStep {
	return gates.GateStep{ID: "engine", Command: []string{os.Args[0], record}, Required: true,
		Expect: gates.Expect{ExitCode: 0}}
}

// verifyTestKey is the key of a check over one tree, argv and tool versions (SPEC §7.8).
func verifyTestKey(step gates.GateStep) gates.KeyInput {
	return gates.KeyInput{
		Argv:         step.Command,
		InputsDigest: verifyTestDigest("tree at the unit's test commit"),
		Fingerprint: gates.Fingerprint{
			OS:          runtime.GOOS,
			Arch:        runtime.GOARCH,
			ToolVersion: "go version go1.24.0",
			LockDigests: []string{verifyTestDigest("go.sum at the unit's test commit")},
		},
	}
}

// verifyTestOwner opens an owner over fresh roots, so the verify's check runs in owned directories
// (SPEC §6.4, §7.8).
func verifyTestOwner(t *testing.T) *core.Owner {
	t.Helper()
	base := t.TempDir()
	roots := core.OwnedRoots{State: filepath.Join(base, "state"), Temp: filepath.Join(base, "temp"),
		Cache: filepath.Join(base, "cache"), Home: filepath.Join(base, "home"),
		Canonical: []string{filepath.Join(base, "repo")}}
	for _, d := range []string{roots.Home, roots.Canonical[0]} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	self, err := core.CurrentProcess()
	if err != nil {
		t.Fatalf("CurrentProcess: %v", err)
	}
	owner, err := core.OpenOwner(roots, self)
	if err != nil {
		t.Fatalf("OpenOwner: %v", err)
	}
	return owner
}

// verifyTestRuns counts the runs the helper recorded in record.
func verifyTestRuns(t *testing.T, record string) int {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("catches a verify that did not run its command: %v", err)
	}
	return strings.Count(string(data), "ran\n")
}

// TestVerifyRedDetail catches a red unit verify that reaches the fix round without the step, the
// command, the exit code or the tail of its output (SPEC §7.3 step 6, §7.8).
func TestVerifyRedDetail(t *testing.T) {
	t.Setenv(verifyTestEnv(), "1")
	record := filepath.Join(t.TempDir(), "ran.txt")
	step := verifyTestStep(record)
	res, err := Verify(context.Background(), &gates.Runner{Owner: verifyTestOwner(t)}, VerifyRequest{
		Step: step, Dir: t.TempDir(), Key: verifyTestKey(step), Cache: gates.NewCache(),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if runs := verifyTestRuns(t, record); runs != 1 {
		t.Fatalf("catches a red verify whose command did not run once: %d runs", runs)
	}
	if res.Step != step.ID {
		t.Errorf("catches a red verify without its step: %q, want %q", res.Step, step.ID)
	}
	if !slices.Equal(res.Command, step.Command) {
		t.Errorf("catches a red verify without its command: %v, want %v", res.Command, step.Command)
	}
	if res.ExitCode != 4 {
		t.Errorf("catches a red verify without its exit code: %d, want 4", res.ExitCode)
	}
	if res.Result != gates.ResultRed {
		t.Errorf("catches a verify that did not report red: %q", res.Result)
	}
	if !strings.Contains(res.Tail, "verify detail tail") {
		t.Errorf("catches a red verify whose output tail is missing: %q", res.Tail)
	}
	if strings.Contains(res.Tail, "verify line 00") {
		t.Errorf("catches a verify that returns all its output, not its tail: %q", res.Tail)
	}
}

// TestVerifyRunsOncePerKey catches a unit verify that runs the check again for a key it already
// holds, or serves the stored result for a changed tree (DR-8, SPEC §7.3 step 6, §7.8).
func TestVerifyRunsOncePerKey(t *testing.T) {
	t.Setenv(verifyTestEnv(), "1")
	record := filepath.Join(t.TempDir(), "ran.txt")
	step := verifyTestStep(record)
	runner := &gates.Runner{Owner: verifyTestOwner(t)}
	cache := gates.NewCache()
	req := VerifyRequest{Step: step, Dir: t.TempDir(), Key: verifyTestKey(step), Cache: cache}
	first, err := Verify(context.Background(), runner, req)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if first.Step != step.ID || first.ExitCode != 4 || first.Result != gates.ResultRed {
		t.Fatalf("catches a verify that did not run the step: %+v", first)
	}
	second, err := Verify(context.Background(), runner, req)
	if err != nil {
		t.Fatalf("Verify again: %v", err)
	}
	if runs := verifyTestRuns(t, record); runs != 1 {
		t.Fatalf("catches a verify that ran %d times for one key, want once", runs)
	}
	if second.Step != step.ID || second.Result != gates.ResultRed || second.ExitCode != 4 ||
		!strings.Contains(second.Tail, "verify detail tail") || !slices.Equal(second.Command, step.Command) {
		t.Errorf("catches a second verify that does not return the stored result: %+v", second)
	}
	changed := req
	changed.Key.InputsDigest = verifyTestDigest("tree after the fix round")
	if _, err := Verify(context.Background(), runner, changed); err != nil {
		t.Fatalf("Verify a changed tree: %v", err)
	}
	if runs := verifyTestRuns(t, record); runs != 2 {
		t.Fatalf("catches a changed tree served from the stored key: %d runs, want 2", runs)
	}
}

// TestVerifyFreshCloneHitsKey catches a key bound to the directory a check ran in: a run works in
// its own fresh clone (SPEC §6.5), so the same tree content, argv and tool versions in another
// directory must return the stored result instead of running the step again (DR-8, SPEC §7.8).
// The key changes with the tree, never with its path.
func TestVerifyFreshCloneHitsKey(t *testing.T) {
	t.Setenv(verifyTestEnv(), "1")
	record := filepath.Join(t.TempDir(), "ran.txt")
	step := verifyTestStep(record)
	runner := &gates.Runner{Owner: verifyTestOwner(t)}
	cache := gates.NewCache()
	key := verifyTestKey(step)
	first := VerifyRequest{Step: step, Dir: t.TempDir(), Key: key, Cache: cache}
	if _, err := Verify(context.Background(), runner, first); err != nil {
		t.Fatalf("Verify the tree: %v", err)
	}
	clone := VerifyRequest{Step: step, Dir: t.TempDir(), Key: key, Cache: cache}
	second, err := Verify(context.Background(), runner, clone)
	if err != nil {
		t.Fatalf("Verify the same tree in a fresh clone: %v", err)
	}
	if runs := verifyTestRuns(t, record); runs != 1 {
		t.Fatalf("catches a verify that ran %d times for one key in a fresh clone, want once", runs)
	}
	if second.ExitCode != 4 || second.Result != gates.ResultRed || !strings.Contains(second.Tail, "verify detail tail") {
		t.Errorf("catches a fresh clone served %+v, want the stored red result", second)
	}
	changed := clone
	changed.Key.InputsDigest = verifyTestDigest("tree after the fix round")
	if _, err := Verify(context.Background(), runner, changed); err != nil {
		t.Fatalf("Verify the changed tree in the fresh clone: %v", err)
	}
	if runs := verifyTestRuns(t, record); runs != 2 {
		t.Fatalf("catches a changed tree served from the stored key in a fresh clone: %d runs, want 2", runs)
	}
}

// TestVerifySharedArgvServesStoredResult catches two defects of a shared key: a key that includes
// the step's id, so two steps that share an argv and a tree run one check twice (SPEC §7.8 keys a
// check by its argv, its input digest and its fingerprint), and a report labelled with the stored
// step instead of the step the caller asked for (DR-8, SPEC §7.3 step 6).
func TestVerifySharedArgvServesStoredResult(t *testing.T) {
	t.Setenv(verifyTestEnv(), "1")
	record := filepath.Join(t.TempDir(), "ran.txt")
	first := verifyTestStep(record)
	second := first
	second.ID = "engine-second"
	runner := &gates.Runner{Owner: verifyTestOwner(t)}
	cache := gates.NewCache()
	for i, step := range []gates.GateStep{first, second} {
		res, err := Verify(context.Background(), runner, VerifyRequest{
			Step: step, Dir: t.TempDir(), Key: verifyTestKey(step), Cache: cache,
		})
		if err != nil {
			t.Fatalf("Verify step %d: %v", i+1, err)
		}
		if res.Step != step.ID {
			t.Errorf("catches a verify labelled %q, want the step it asked for: %q", res.Step, step.ID)
		}
		if res.ExitCode != 4 || res.Result != gates.ResultRed || !strings.Contains(res.Tail, "verify detail tail") {
			t.Errorf("catches a verify of step %q that did not return the stored red result: %+v", step.ID, res)
		}
	}
	if runs := verifyTestRuns(t, record); runs != 1 {
		t.Fatalf("catches %d runs for two steps that share an argv and a tree, want 1 (one key)", runs)
	}
}

// TestVerifyRefusesEmptyInputsDigest catches a unit verify that stores a result under a key that
// covers no tree: an empty InputsDigest is refused before the step runs or a result is stored (DR-8,
// SPEC §7.8).
func TestVerifyRefusesEmptyInputsDigest(t *testing.T) {
	t.Setenv(verifyTestEnv(), "1")
	record := filepath.Join(t.TempDir(), "ran.txt")
	step := verifyTestStep(record)
	cache := gates.NewCache()
	key := verifyTestKey(step)
	key.InputsDigest = ""
	_, err := Verify(context.Background(), &gates.Runner{Owner: verifyTestOwner(t)}, VerifyRequest{
		Step: step, Dir: t.TempDir(), Key: key, Cache: cache,
	})
	if err == nil {
		t.Fatalf("catches a verify that accepts an empty InputsDigest")
	}
	if _, ok := cache.Lookup(gates.CheckKey(key)); ok {
		t.Errorf("catches an empty InputsDigest stored as a successful key")
	}
	if _, err := os.Stat(record); err == nil {
		t.Errorf("catches a verify that ran its command for an empty InputsDigest")
	}
}

// TestVerifyConcurrentCallersRunOnce catches a cache that two callers share: two goroutines with the
// same cache and key run the command once, never twice and never a concurrent map write (DR-8, SPEC
// §7.8).
func TestVerifyConcurrentCallersRunOnce(t *testing.T) {
	t.Setenv(verifyTestEnv(), "1")
	record := filepath.Join(t.TempDir(), "ran.txt")
	step := verifyTestStep(record)
	runner := &gates.Runner{Owner: verifyTestOwner(t)}
	cache := gates.NewCache()
	req := VerifyRequest{Step: step, Dir: t.TempDir(), Key: verifyTestKey(step), Cache: cache}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := Verify(context.Background(), runner, req); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Verify: %v", err)
	}
	if runs := verifyTestRuns(t, record); runs != 1 {
		t.Fatalf("catches two callers that ran the command %d times, want once", runs)
	}
}

// TestVerifyTwoKeysShareOneRunner catches a verify that writes the shared runner's output field
// while another key runs: two different keys run at the same time (DR-3), so each report must carry
// its own step's output tail, and the runner's own field must stay untouched. Under `go test -race`
// a write to that shared field is a data race too.
func TestVerifyTwoKeysShareOneRunner(t *testing.T) {
	t.Setenv(verifyTestEnv(), "1")
	first := verifyTestStep(filepath.Join(t.TempDir(), "first.txt"))
	second := verifyTestStep(filepath.Join(t.TempDir(), "second.txt"))
	second.Command = append(slices.Clone(second.Command), "second")
	second.ID = "engine-second"
	if slices.Equal(first.Command, second.Command) {
		t.Fatalf("catches two steps that share an argv, so they share one key")
	}
	runner := &gates.Runner{Owner: verifyTestOwner(t)}
	cache := gates.NewCache()
	dirs := []string{t.TempDir(), t.TempDir()}
	type outcome struct {
		step gates.GateStep
		res  VerifyReport
		err  error
	}
	results := make(chan outcome, 2)
	for i, step := range []gates.GateStep{first, second} {
		go func(step gates.GateStep, dir string) {
			res, err := Verify(context.Background(), runner, VerifyRequest{
				Step: step, Dir: dir, Key: verifyTestKey(step), Cache: cache})
			results <- outcome{step: step, res: res, err: err}
		}(step, dirs[i])
	}
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("Verify step %q: %v", got.step.ID, got.err)
		}
		if got.res.Step != got.step.ID || got.res.ExitCode != 4 || got.res.Result != gates.ResultRed {
			t.Errorf("catches step %q served %+v, want its own red result", got.step.ID, got.res)
		}
		if !strings.Contains(got.res.Tail, "verify detail tail") {
			t.Errorf("catches step %q served another run's tail: %q", got.step.ID, got.res.Tail)
		}
	}
	if runner.Output != nil {
		t.Errorf("catches a verify that left the shared runner's output writer set: %v", runner.Output)
	}
}
