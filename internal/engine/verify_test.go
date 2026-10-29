package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// verifyTestStep returns the red unit verify the tests run: this test binary, told by verifyTestEnv
// to record a run in record and exit 4.
func verifyTestStep(record string) gates.GateStep {
	return gates.GateStep{ID: "engine", Command: []string{os.Args[0], record}, Required: true,
		Expect: gates.Expect{ExitCode: 0}}
}

// verifyTestOwner opens an owner over fresh roots, so the verify's check runs in owned directories
// (SPEC §6.4, §7.8). engineTestRoots keeps the git state override out of those roots.
func verifyTestOwner(t *testing.T) *core.Owner {
	t.Helper()
	roots := engineTestRoots(t)
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

// TestVerifyRedDetail catches a red verify missing its step, command, exit code or tail (SPEC §7.3, §7.8).
func TestVerifyRedDetail(t *testing.T) {
	tr := verifyTestTreeOf(t)
	res, err := Verify(context.Background(), tr.runner, tr.req)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if runs := verifyTestRuns(t, tr.record); runs != 1 {
		t.Fatalf("catches a red verify whose command did not run once: %d runs", runs)
	}
	step := tr.req.Step
	if res.Step != step.ID || !slices.Equal(res.Command, step.Command) || res.ExitCode != 4 || res.Result != gates.ResultRed {
		t.Errorf("catches a red verify missing its step, command, exit code or result: %+v", res)
	}
	if !strings.Contains(res.Tail, "verify detail tail") || strings.Contains(res.Tail, "verify line 00") {
		t.Errorf("catches a red verify whose tail is wrong: %q", res.Tail)
	}
}

// TestVerifyRunsOncePerKey catches a second run of one key, or a stored result for a changed tree (DR-8).
func TestVerifyRunsOncePerKey(t *testing.T) {
	tr := verifyTestTreeOf(t)
	first, err := Verify(context.Background(), tr.runner, tr.req)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	second, err := Verify(context.Background(), tr.runner, tr.req)
	if err != nil {
		t.Fatalf("Verify again: %v", err)
	}
	if runs := verifyTestRuns(t, tr.record); runs != 1 {
		t.Fatalf("catches a verify that ran %d times for one key, want once", runs)
	}
	if second.Step != first.Step || second.Result != first.Result || second.ExitCode != first.ExitCode ||
		second.Tail != first.Tail || !slices.Equal(second.Command, first.Command) {
		t.Errorf("catches a second verify that does not return the stored result: %+v", second)
	}
	verifyTestGitCommit(t, tr.dir, map[string]string{
		"internal/gates/cache.go": "package gates\n\nconst cacheSeed = \"round one\"\n",
	}, "change the declared input")
	verifyTestAgain(t, tr, 2, "catches a changed tree served from the stored key")
}

// TestVerifyFreshCloneHitsKey catches a key bound to the directory: the same tree in another
// directory reuses the stored result, and a changed tree does not (DR-8, SPEC §6.5, §7.8).
func TestVerifyFreshCloneHitsKey(t *testing.T) {
	tr := verifyTestTreeOf(t)
	verifyTestAgain(t, tr, 1, "catches a verify that did not run")
	clone := tr
	clone.dir = verifyTestGit(t, verifyTestSeed())
	clone.req.Dir = clone.dir
	second, err := Verify(context.Background(), clone.runner, clone.req)
	if err != nil {
		t.Fatalf("Verify the same tree in a fresh clone: %v", err)
	}
	if runs := verifyTestRuns(t, tr.record); runs != 1 {
		t.Fatalf("catches a verify that ran %d times for one key in a fresh clone, want once", runs)
	}
	if second.ExitCode != 4 || second.Result != gates.ResultRed || !strings.Contains(second.Tail, "verify detail tail") {
		t.Errorf("catches a fresh clone served %+v, want the stored red result", second)
	}
	verifyTestGitCommit(t, clone.dir, map[string]string{
		"internal/gates/cache.go": "package gates\n\nconst cacheSeed = \"clone change\"\n",
	}, "change the clone")
	verifyTestAgain(t, clone, 2, "catches a changed tree served from the stored key in a fresh clone")
}

// TestVerifySharedArgvServesStoredResult catches two steps that share an argv and a tree running
// twice, a report labelled with the stored step, and a key built from the caller's argv instead of
// the command the step runs (DR-8, SPEC §7.3, §7.8).
func TestVerifySharedArgvServesStoredResult(t *testing.T) {
	tr := verifyTestTreeOf(t)
	second := tr.req.Step
	second.ID = "engine-second"
	for i, step := range []gates.GateStep{tr.req.Step, second} {
		res, err := Verify(context.Background(), tr.runner, VerifyRequest{
			Step: step, Dir: tr.dir, Cache: tr.req.Cache,
		})
		if err != nil {
			t.Fatalf("Verify step %d: %v", i+1, err)
		}
		if res.Step != step.ID || res.ExitCode != 4 || res.Result != gates.ResultRed || !strings.Contains(res.Tail, "verify detail tail") {
			t.Errorf("catches a verify of step %q that did not return the stored red result: %+v", step.ID, res)
		}
	}
	if runs := verifyTestRuns(t, tr.record); runs != 1 {
		t.Fatalf("catches %d runs for two steps that share an argv and a tree, want 1 (one key)", runs)
	}
	other := tr
	other.record = filepath.Join(t.TempDir(), "other.txt")
	other.req.Step = verifyTestStep(other.record)
	other.req.Step.Inputs = tr.req.Step.Inputs
	verifyTestAgain(t, other, 1, "catches a step with another command served the first step's stored result")
}

// TestVerifyRefusesInputWithNoBlob catches a declared input with no blob hashed as a digest (DR-6).
func TestVerifyRefusesInputWithNoBlob(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inputs []string
	}{
		{"no declared input matches a blob", []string{"missing.go"}},
		{"one declared input matches no blob", []string{"internal/gates/cache.go", "missing.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := verifyTestTreeOf(t)
			tr.req.Step.Inputs = tc.inputs
			if _, err := Verify(context.Background(), tr.runner, tr.req); err == nil {
				t.Fatalf("catches %s cached as a real digest", tc.name)
			}
			if _, err := os.Stat(tr.record); err == nil {
				t.Fatalf("catches a verify that ran for %s", tc.name)
			}
		})
	}
}

// TestVerifyConcurrentCallersRunOnce catches two goroutines with one key running the command twice (DR-8).
func TestVerifyConcurrentCallersRunOnce(t *testing.T) {
	tr := verifyTestTreeOf(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := Verify(context.Background(), tr.runner, tr.req); err != nil {
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
	if runs := verifyTestRuns(t, tr.record); runs != 1 {
		t.Fatalf("catches two callers that ran the command %d times, want once", runs)
	}
}

// TestVerifyTwoKeysShareOneRunner catches a verify that writes the shared runner's output field
// while another key runs: two different keys run at the same time (DR-3), so each report must carry
// its own step's output tail, and the runner's own field must stay untouched. Under `go test -race`
// a write to that shared field is a data race too.
func TestVerifyTwoKeysShareOneRunner(t *testing.T) {
	tr := verifyTestTreeOf(t)
	first := tr.req.Step
	secondRecord := filepath.Join(t.TempDir(), "second.txt")
	second := verifyTestStep(secondRecord)
	second.Command = append(slices.Clone(second.Command), "second")
	second.ID = "engine-second"
	second.Inputs = first.Inputs
	if slices.Equal(first.Command, second.Command) {
		t.Fatal("catches two steps that share an argv, so they share one key")
	}
	type outcome struct {
		step gates.GateStep
		res  VerifyReport
		err  error
	}
	results := make(chan outcome, 2)
	for _, step := range []gates.GateStep{first, second} {
		go func(step gates.GateStep) {
			res, err := Verify(context.Background(), tr.runner, VerifyRequest{
				Step: step, Dir: tr.dir, Cache: tr.req.Cache})
			results <- outcome{step: step, res: res, err: err}
		}(step)
	}
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("Verify step %q: %v", got.step.ID, got.err)
		}
		if got.res.Step != got.step.ID || got.res.ExitCode != 4 || got.res.Result != gates.ResultRed ||
			!strings.Contains(got.res.Tail, "verify detail tail") {
			t.Errorf("catches step %q served %+v, want its own red result", got.step.ID, got.res)
		}
	}
	if tr.runner.Output != nil {
		t.Errorf("catches a verify that left the shared runner's output writer set: %v", tr.runner.Output)
	}
}

func verifyTestRawGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// verifyTestOK fails the test on a setup error.
func verifyTestOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// verifyTestWrite writes body to file, making its directory first.
func verifyTestWrite(t *testing.T, file, body string) {
	t.Helper()
	verifyTestOK(t, os.MkdirAll(filepath.Dir(file), 0o700))
	verifyTestOK(t, os.WriteFile(file, []byte(body), 0o600))
}

func verifyTestGitCommit(t *testing.T, dir string, files map[string]string, message string) {
	t.Helper()
	for path, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		verifyTestWrite(t, full, body)
		verifyTestRawGit(t, dir, "add", "--", filepath.ToSlash(path))
	}
	verifyTestRawGit(t, dir, "commit", "-q", "-m", message)
}

func verifyTestGit(t *testing.T, files map[string]string) string {
	t.Helper()
	base := t.TempDir()
	dir, hooks := filepath.Join(base, "repo"), filepath.Join(base, "hooks")
	for _, d := range []string{dir, hooks} {
		verifyTestOK(t, os.MkdirAll(d, 0o700))
	}
	verifyTestRawGit(t, dir, "init", "-q")
	for _, kv := range [][2]string{{"core.hooksPath", hooks}, {"user.name", "Ada Lovelace"},
		{"user.email", "ada.lovelace@example.com"}, {"commit.gpgsign", "false"}, {"core.autocrlf", "false"}} {
		verifyTestRawGit(t, dir, "config", kv[0], kv[1])
	}
	verifyTestGitCommit(t, dir, files, "seed the tree")
	return dir
}

type verifyTestTree struct {
	dir, record string
	runner      *gates.Runner
	req         VerifyRequest
}

func verifyTestSeed() map[string]string {
	sum := "github.com/sungurerdim/" + core.Product + " v0.1.0 h1:Y2tzA4bC5dE7fG8hI9jK0lM1nO2pQ3rS4tU5vW6xY=\n"
	return map[string]string{
		"internal/gates/cache.go": "package gates\n\nconst cacheSeed = \"round zero\"\n",
		"go.sum":                  sum,
		"README.md":               "# " + core.Product + "\n\nCache notes for round zero.\n",
	}
}

func verifyTestTreeOf(t *testing.T) verifyTestTree {
	t.Helper()
	t.Setenv(verifyTestEnv(), "1")
	record := filepath.Join(t.TempDir(), "ran.txt")
	step := verifyTestStep(record)
	step.Inputs = []string{"internal/gates/cache.go"}
	dir := verifyTestGit(t, verifyTestSeed())
	return verifyTestTree{dir: dir, record: record, runner: &gates.Runner{Owner: verifyTestOwner(t)},
		req: VerifyRequest{Step: step, Dir: dir, Cache: gates.NewCache()}}
}

// verifyTestAgain runs tr's verify, wants want recorded runs and returns the report.
func verifyTestAgain(t *testing.T, tr verifyTestTree, want int, catches string) VerifyReport {
	t.Helper()
	res, err := Verify(context.Background(), tr.runner, tr.req)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := verifyTestRuns(t, tr.record); got != want {
		t.Fatalf("%s: %d runs, want %d", catches, got, want)
	}
	return res
}

// TestVerifyNilCacheDoesNotRunTheKey catches a nil cache that runs a unit verify (DR-8, SPEC §7.8).
func TestVerifyNilCacheDoesNotRunTheKey(t *testing.T) {
	tr := verifyTestTreeOf(t)
	tr.req.Cache = nil
	_, err1 := Verify(context.Background(), tr.runner, tr.req)
	_, err2 := Verify(context.Background(), tr.runner, tr.req)
	if err1 == nil || err2 == nil {
		t.Fatalf("catches a nil cache that returned a result: %v, %v", err1, err2)
	}
	if _, err := os.Stat(tr.record); err == nil {
		t.Fatal("catches a nil cache that ran the command")
	}
}

// TestVerifyKeyFollowsGitObjectsNotCallerDigest catches a caller digest reused after a declared
// input or go.sum is committed, a key that moves for any other tracked file, and a work tree that
// differs from HEAD under the declared inputs served or stored under the HEAD key (DR-8, SPEC §7.8).
func TestVerifyKeyFollowsGitObjectsNotCallerDigest(t *testing.T) {
	tr := verifyTestTreeOf(t)
	tr.req.Step.Inputs = []string{"internal/gates/*.go"}
	verifyTestAgain(t, tr, 1, "catches a verify that did not run")
	verifyTestWrite(t, filepath.Join(tr.dir, "internal", "gates", "cache.go"), "package gates\n\nconst cacheSeed = \"dirty\"\n")
	if !verifyTestAgain(t, tr, 2, "catches a dirty declared input served the stored result of HEAD").Uncached {
		t.Error("catches a dirty declared input whose run was not reported uncached")
	}
	verifyTestAgain(t, tr, 3, "catches a dirty declared input whose result was stored")
	verifyTestRawGit(t, tr.dir, "checkout", "--", "internal/gates/cache.go")
	untracked := filepath.Join(tr.dir, "internal", "gates", "new.go")
	verifyTestWrite(t, untracked, "package gates\n")
	verifyTestAgain(t, tr, 4, "catches an untracked file under the declared inputs served the stored result of HEAD")
	verifyTestOK(t, os.Remove(untracked))
	verifyTestWrite(t, filepath.Join(tr.dir, "README.md"), "# dirty notes outside the declared inputs\n")
	if verifyTestAgain(t, tr, 4, "catches a tree clean under the declared inputs that did not reuse the stored result").Uncached {
		t.Error("catches a stored result reported uncached")
	}
	verifyTestGitCommit(t, tr.dir, map[string]string{
		"internal/gates/cache.go": "package gates\n\nconst cacheSeed = \"round one\"\n",
	}, "change the declared input")
	verifyTestAgain(t, tr, 5, "catches a caller-supplied InputsDigest reused after the git object changed")
	verifyTestGitCommit(t, tr.dir, map[string]string{
		"README.md": "# " + core.Product + "\n\nNotes that are not a declared input.\n",
	}, "change a file outside the declared inputs")
	verifyTestAgain(t, tr, 5, "catches a key that changed for a file that is not a declared input or a lock file")
	next := "github.com/sungurerdim/" + core.Product + " v0.2.0 h1:Z3uaB5cD6eF8gH9iJ0kL2mN3oP4qR5sT6uV7wX8y=\n"
	verifyTestGitCommit(t, tr.dir, map[string]string{"go.sum": next}, "change the lock file")
	verifyTestAgain(t, tr, 6, "catches a caller-supplied LockDigests reused after go.sum changed")
}

// TestVerifyDirtyKeyPartIsNotServed catches a work tree that differs from HEAD in a part of the key
// yet is served the stored result of the HEAD key, or a result an earlier dirty run stored (DR-8,
// SPEC §7.8).
// Each row makes one change after a clean run and names the fault it catches.
func TestVerifyDirtyKeyPartIsNotServed(t *testing.T) {
	for _, tc := range []struct {
		name, catches string
		// inputs are the step's declared inputs; none is the whole tracked tree.
		inputs []string
		file   string
	}{
		{
			name:    "a dirty go.sum outside the declared inputs",
			catches: "a dirty lock file served the stored result of the HEAD lock digest",
			inputs:  []string{"internal/gates/*.go"},
			file:    "go.sum",
		},
		{
			name:    "a dirty file of the whole tracked tree",
			catches: "a step with no declared inputs whose key does not cover the whole tracked tree",
			file:    "README.md",
		},
		{
			name:    "an untracked file in a new directory under the declared inputs",
			catches: "a git status that folds a new directory into one record no declared input matches",
			inputs:  []string{"internal/**/*.go"},
			file:    "internal/newpkg/new.go",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := verifyTestTreeOf(t)
			tr.req.Step.Inputs = tc.inputs
			verifyTestAgain(t, tr, 1, "catches a verify that did not run")
			verifyTestWrite(t, filepath.Join(tr.dir, filepath.FromSlash(tc.file)), "package gates\n\n// dirty\n")
			if !verifyTestAgain(t, tr, 2, "catches "+tc.catches).Uncached {
				t.Errorf("catches %s: the run was not reported uncached", tc.catches)
			}
			verifyTestAgain(t, tr, 3, "catches a dirty run whose result was stored under any key and served to the next dirty run")
		})
	}
}

// TestVerifyRefusesCallerDigestOutsideWorkTree catches a caller InputsDigest kept for an empty
// directory, a non-repository, or a subdirectory (DR-6). internal/gates/key.go requireWorkTreeRoot.
func TestVerifyRefusesCallerDigestOutsideWorkTree(t *testing.T) {
	tr := verifyTestTreeOf(t)
	outside := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(outside))
	sub := filepath.Join(tr.dir, "internal")
	for _, tc := range []struct{ name, dir string }{
		{"an empty directory", ""},
		{"a directory that is not a git work tree", outside},
		{"a subdirectory of a git work tree", sub},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := tr.req
			req.Dir = tc.dir
			if _, err := Verify(context.Background(), tr.runner, req); err == nil {
				t.Fatalf("catches a caller InputsDigest kept for %s", tc.name)
			}
			if _, err := os.Stat(tr.record); err == nil {
				t.Fatalf("catches a verify that ran for %s", tc.name)
			}
		})
	}
}

// TestVerifyRunsAProgramWithNoVersionCommand catches a step refused because its own program, this
// test binary, has no `version` subcommand: the toolchain probe is the stack profile's, not
// argv0's (SPEC §7.8).
func TestVerifyRunsAProgramWithNoVersionCommand(t *testing.T) {
	tr := verifyTestTreeOf(t)
	verifyTestAgain(t, tr, 1, "catches a step refused because its program has no version subcommand")
}

// verifyTestSink keeps the check records a verify writes; WriteCheck keeps rec.
type verifyTestSink struct{ recs []gates.CheckRecord }

func (s *verifyTestSink) WriteCheck(_ context.Context, rec gates.CheckRecord) error {
	s.recs = append(s.recs, rec)
	return nil
}

// TestVerifyWritesOneCheckRecord catches a verify that runs and writes no record, or one without its
// key and facts, and a stored result served from the cache that writes one (SPEC-contracts §8.5).
func TestVerifyWritesOneCheckRecord(t *testing.T) {
	tr := verifyTestTreeOf(t)
	sink := &verifyTestSink{}
	tr.runner.Records = sink
	in, _, err := gates.ResolveKeyInput(context.Background(), tr.dir, tr.req.Step)
	verifyTestOK(t, err)
	verifyTestAgain(t, tr, 1, "catches a first verify that does not run")
	verifyTestAgain(t, tr, 1, "catches a cached verify that runs again")
	if len(sink.recs) != 1 {
		t.Fatalf("catches a run that writes no record, or a cached call that writes one: %d records", len(sink.recs))
	}
	if c := sink.recs[0].Check; c.Key != gates.CheckKey(in) || !c.TreeHash.Known || !c.DurationMS.Known || c.ExitCode.Value != 4 {
		t.Errorf("catches a verify record without its check key, tree, duration or exit code: %+v", c)
	}
}
