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
	second, err := Verify(context.Background(), runner, req)
	if err != nil {
		t.Fatalf("Verify again: %v", err)
	}
	if runs := verifyTestRuns(t, record); runs != 1 {
		t.Fatalf("catches a verify that ran %d times for one key, want once", runs)
	}
	if second.Step != first.Step || second.Result != first.Result || second.ExitCode != first.ExitCode ||
		second.Tail != first.Tail || !slices.Equal(second.Command, first.Command) {
		t.Errorf("catches a second verify that does not return the stored result: %+v, want %+v", second, first)
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
