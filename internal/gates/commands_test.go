package gates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// commandsTestEnvelope is the JSON result envelope of SPEC §5.3 with the gate report as its data.
type commandsTestEnvelope struct {
	Status  core.Status  `json:"status"`
	Outcome core.Outcome `json:"outcome"`
	Data    GateReport   `json:"data"`
	Detail  string       `json:"detail"`
}

// commandsTestRepo makes a repository directory whose gate.json declares steps. A nil steps
// leaves gate.json out.
func commandsTestRepo(t *testing.T, steps []GateStep) string {
	t.Helper()
	dir := t.TempDir()
	if steps == nil {
		return dir
	}
	data, err := json.Marshal(Definition{FormatVersion: formatVersion, Steps: steps})
	if err != nil {
		t.Fatalf("encode gate.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gate.json"), data, 0o600); err != nil {
		t.Fatalf("write gate.json: %v", err)
	}
	return dir
}

// commandsTestRoots are the state, cache and temp root overrides of one test (SPEC §6.4).
type commandsTestRoots struct{ state, cache, temp string }

// commandsTestSetRoots points the root overrides at new directories under the test's temp
// directory, so the engine owner and its owner.key never land in the user's real roots.
func commandsTestSetRoots(t *testing.T) commandsTestRoots {
	t.Helper()
	base := t.TempDir()
	r := commandsTestRoots{state: filepath.Join(base, "state"), cache: filepath.Join(base, "cache"),
		temp: filepath.Join(base, "temp")}
	p := strings.ToUpper(core.Product)
	t.Setenv(p+"_HOME", r.state)
	t.Setenv(p+"_CACHE", r.cache)
	t.Setenv(p+"_TMP", r.temp)
	return r
}

// commandsTestRun runs `aiwe gates gate` through the one command registry and the CLI front, as
// the binary does (SPEC §5.7), and returns the exit code and the decoded envelope.
func commandsTestRun(t *testing.T, args ...string) (int, commandsTestEnvelope, string) {
	t.Helper()
	code, env, out, _ := commandsTestRunStreams(t, args...)
	return code, env, out
}

// commandsTestRunStreams is commandsTestRun that also returns what the command wrote to its
// stream writer, stderr in the binary. Stdout must be exactly one JSON envelope.
func commandsTestRunStreams(t *testing.T, args ...string) (int, commandsTestEnvelope, string, string) {
	t.Helper()
	var out, stream bytes.Buffer
	argv := append([]string{"gates", "gate"}, args...)
	argv = append(argv, "--format", "json")
	code := core.RunCLI(context.Background(), Commands(), argv, &out, &stream)
	var env commandsTestEnvelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf(core.Product+" %s printed no JSON envelope: %v\n%s", strings.Join(argv, " "), err, out.String())
	}
	return code, env, out.String(), stream.String()
}

// commandsTestWithoutRepo returns detail with every form of the repo path removed: as given, with
// forward slashes, and Go-quoted, so a check on the rest cannot match text the path supplies.
func commandsTestWithoutRepo(detail, repo string) string {
	quoted := strconv.Quote(repo)
	for _, form := range []string{quoted[1 : len(quoted)-1], repo, filepath.ToSlash(repo)} {
		detail = strings.ReplaceAll(detail, form, "")
	}
	return detail
}

// commandsTestRow returns the outcome table row named o; ok is false when the table has none.
func commandsTestRow(o core.Outcome) (core.OutcomeRow, bool) {
	for _, r := range core.Outcomes() {
		if r.Outcome == o {
			return r, true
		}
	}
	return core.OutcomeRow{}, false
}

// TestGatesCommand pins that `aiwe gates gate` takes its own repo and step params, runs the gate
// of <repo>/gate.json, and maps each gate result to one named outcome of the table: a red or
// incomplete gate never exits 0 (BC-1, DR-10).
func TestGatesCommand(t *testing.T) {
	commandsTestSetRoots(t)
	green1, green2, red, missing := runnerTestSteps(t)

	t.Run("a command whose repo or step param is not in the dictionary is refused at every call", func(t *testing.T) {
		if err := core.CheckParams(core.Dictionary(), Commands()); err != nil {
			t.Fatalf("the gates commands do not pass the dictionary check: %v", err)
		}
	})

	cases := []struct {
		name string
		// catches names the defect the case fails on.
		catches string
		steps   []GateStep
		args    func(repo string) []string
		// wantCode is the exit code; -1 means any code but 0.
		wantCode    int
		wantStatus  core.Status
		wantResult  Result
		wantPartial bool
	}{
		{
			name:        "green gate exits 0 with every step reported",
			catches:     "a green gate refused for its --repo param, or reported without its steps",
			steps:       []GateStep{green1, green2},
			args:        func(repo string) []string { return []string{"--repo", repo} },
			wantCode:    0,
			wantStatus:  core.StatusOK,
			wantResult:  ResultGreen,
			wantPartial: false,
		},
		{
			name:        "red gate exits with the red code",
			catches:     "a gate with a failing step that exits 0 or is not reported red (DR-10)",
			steps:       []GateStep{green1, red, green2},
			args:        func(repo string) []string { return []string{"--repo", repo} },
			wantCode:    core.StatusRed.ExitCode(),
			wantStatus:  core.StatusRed,
			wantResult:  ResultRed,
			wantPartial: false,
		},
		{
			name:        "gate with a step that cannot start is incomplete and exits non-zero",
			catches:     "an unrun step that lets the gate exit 0 (DR-10, DR-13)",
			steps:       []GateStep{green1, missing},
			args:        func(repo string) []string { return []string{"--repo", repo} },
			wantCode:    -1,
			wantResult:  ResultIncomplete,
			wantPartial: false,
		},
		{
			name:        "gate run for one step is incomplete and exits non-zero",
			catches:     "a --step run refused for its own param, or a partial run that exits 0 (DR-10)",
			steps:       []GateStep{green1, green2},
			args:        func(repo string) []string { return []string{"--repo", repo, "--step", green2.ID} },
			wantCode:    -1,
			wantResult:  ResultIncomplete,
			wantPartial: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := commandsTestRepo(t, tc.steps)
			code, env, raw := commandsTestRun(t, tc.args(repo)...)
			if tc.wantCode == -1 && code == 0 || tc.wantCode != -1 && code != tc.wantCode {
				t.Fatalf("%s: exit code %d, want %d (-1: any but 0)\n%s", tc.catches, code, tc.wantCode, raw)
			}
			if tc.wantStatus != "" && env.Status != tc.wantStatus {
				t.Errorf("%s: status %q, want %q\n%s", tc.catches, env.Status, tc.wantStatus, raw)
			}
			if code != env.Status.ExitCode() {
				t.Errorf("%s: exit code %d does not match status %q\n%s", tc.catches, code, env.Status, raw)
			}
			if env.Data.Result != tc.wantResult || env.Data.Partial != tc.wantPartial {
				t.Errorf("%s: gate result %q partial %v, want %q partial %v\n%s",
					tc.catches, env.Data.Result, env.Data.Partial, tc.wantResult, tc.wantPartial, raw)
			}
			if len(env.Data.Steps) != len(tc.steps) {
				t.Errorf("%s: %d step reports, want one per declared step (%d)\n%s",
					tc.catches, len(env.Data.Steps), len(tc.steps), raw)
			}
			row, ok := commandsTestRow(env.Outcome)
			if !ok || row.Status != env.Status || row.Next == "" {
				t.Errorf("%s: outcome %q is not a table row with status %q and a next action (BC-1)\n%s",
					tc.catches, env.Outcome, env.Status, raw)
			}
		})
	}

	t.Run("red and incomplete gates are two different named outcomes", func(t *testing.T) {
		_, redEnv, redRaw := commandsTestRun(t, "--repo", commandsTestRepo(t, []GateStep{green1, red}))
		_, incEnv, incRaw := commandsTestRun(t, "--repo", commandsTestRepo(t, []GateStep{green1, missing}))
		if redEnv.Outcome == incEnv.Outcome {
			t.Fatalf("a red and an incomplete gate share the outcome %q, so a reader cannot tell them apart (BC-1)\n%s\n%s",
				redEnv.Outcome, redRaw, incRaw)
		}
		for _, o := range []core.Outcome{redEnv.Outcome, incEnv.Outcome} {
			if o == core.OutcomeOK || o == core.OutcomeError || o == core.OutcomeUsage {
				t.Errorf("gate outcome %q is a generic core outcome, not a named gate outcome (BC-1)", o)
			}
		}
	})

	refusals := []struct {
		name    string
		catches string
		steps   []GateStep
		args    func(repo string) []string
		want    core.Status
		// found is text the §5.10 detail must name.
		found string
	}{
		{
			name:    "unknown step name is a usage refusal naming the step",
			catches: "a --step typo that runs nothing and reports green, or is refused as an undeclared param",
			steps:   []GateStep{green1, green2},
			args:    func(repo string) []string { return []string{"--repo", repo, "--step", "tset"} },
			want:    core.StatusUsage,
			found:   "tset",
		},
		{
			name:    "repository without gate.json is an error, never a green gate",
			catches: "a repository with no gate.json that exits 0 as if its gate held (DR-13)",
			steps:   nil,
			args:    func(repo string) []string { return []string{"--repo", repo} },
			want:    core.StatusError,
			found:   "gate.json",
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			repo := commandsTestRepo(t, tc.steps)
			code, env, raw := commandsTestRun(t, tc.args(repo)...)
			if env.Status != tc.want || code != tc.want.ExitCode() {
				t.Fatalf("%s: status %q exit %d, want %q exit %d\n%s", tc.catches, env.Status, code, tc.want, tc.want.ExitCode(), raw)
			}
			if env.Outcome == "param_undeclared" {
				t.Fatalf("%s: refused for an undeclared param\n%s", tc.catches, raw)
			}
			// The repo path holds the subtest name, so tc.found is searched only in the detail
			// without the repo path; a detail that names tc.found only through that path fails.
			prefix := string(env.Outcome) + ": expected "
			if !strings.HasPrefix(env.Detail, prefix) || !strings.Contains(env.Detail, "; found ") ||
				!strings.Contains(env.Detail, "; next: ") || !strings.Contains(commandsTestWithoutRepo(env.Detail, repo), tc.found) {
				t.Errorf("%s: detail %q is not \"<outcome>: expected <x>; found <y>; next: <action>\" naming %q (SPEC §5.10)",
					tc.catches, env.Detail, tc.found)
			}
		})
	}
}

// TestGatesCommandWorkers catches `aiwe gates gate` running its test step without the measured
// worker count in the report, or without the source of its concurrent job count (DR-7).
func TestGatesCommandWorkers(t *testing.T) {
	commandsTestSetRoots(t)
	_, test, _, _ := runnerTestSteps(t)
	repo := commandsTestRepo(t, []GateStep{test})
	code, env, out := commandsTestRun(t, "--repo", repo)
	if code != 0 || env.Data.Result != ResultGreen {
		t.Fatalf("exit %d, gate %q, want 0 and green\n%s", code, env.Data.Result, out)
	}
	if len(env.Data.Steps) != 1 || env.Data.Steps[0].ID != test.ID {
		t.Fatalf("steps = %+v, want the one test step", env.Data.Steps)
	}
	detail := env.Data.Steps[0].Detail
	for _, want := range []string{"workers=", "jobs=1", "jobs_from=single-run"} {
		if !strings.Contains(detail, want) {
			t.Errorf("test step detail %q does not contain %q", detail, want)
		}
	}
}

// TestGateCommandStream catches step output missing from stderr, streamed without its
// `--- step <id>` header, or mixed into the JSON envelope on stdout (SPEC §5, DR-38).
func TestGateCommandStream(t *testing.T) {
	commandsTestSetRoots(t)
	release := filepath.Join(t.TempDir(), "release")
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("write the release file: %v", err)
	}
	t.Setenv(streamHelperEnv(), release)
	step := GateStep{ID: "stream", Command: []string{os.Args[0]}, Required: true}
	code, env, out, stderr := commandsTestRunStreams(t, "--repo", commandsTestRepo(t, []GateStep{step}))
	if code != 0 || env.Data.Result != ResultGreen {
		t.Fatalf("exit %d, gate %q, want 0 and green\n%s\nstderr:\n%s", code, env.Data.Result, out, stderr)
	}
	for _, line := range []string{streamOutLine, streamErrLine} {
		if strings.Contains(out, line) {
			t.Errorf("step output %q reached stdout, mixed into the envelope:\n%s", line, out)
		}
		_, after, found := strings.Cut(stderr, "--- step "+step.ID+"\n")
		if !found || !strings.Contains(after, line) {
			t.Errorf("stderr does not hold step output %q after its --- step %s header:\n%s", line, step.ID, stderr)
		}
	}
}

// TestGateCommandOwner catches a command run that creates owner.key outside its root overrides,
// or runs a check in roots that are not owned directories under the temp root (SPEC §6.4, §7.8).
// The OS conventions point at a test directory, so a key made by convention is seen there.
func TestGateCommandOwner(t *testing.T) {
	roots := commandsTestSetRoots(t)
	convention := t.TempDir()
	for _, v := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, convention)
	}
	t.Setenv(rootsHelperEnv(), "1")
	step, record := rootsTestStep(t.TempDir(), "owned", "")
	code, env, out := commandsTestRun(t, "--repo", commandsTestRepo(t, []GateStep{step}))
	if code != 0 || env.Data.Result != ResultGreen {
		t.Fatalf("exit %d, gate %q, want 0 and green\n%s", code, env.Data.Result, out)
	}
	if _, err := os.Stat(filepath.Join(roots.state, "owner.key")); err != nil {
		t.Errorf("catches a gate run that opened no engine owner under the state root override: %v", err)
	}
	err := filepath.WalkDir(convention, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Name() == "owner.key" {
			t.Errorf("catches a gate run that created owner.key outside its root overrides: %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk the convention directory: %v", err)
	}
	temp, err := filepath.EvalSymlinks(roots.temp)
	if err != nil {
		t.Fatalf("resolve the temp root override: %v", err)
	}
	checkTemp := rootsTestEnv(t, record)[strings.ToUpper(core.Product)+"_TMP"]
	if rel, err := filepath.Rel(temp, checkTemp); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		t.Errorf("catches a check whose roots are not an owned directory under the temp root %q: %q", temp, checkTemp)
	}
}

// TestGateCommandNoOwner catches a gate that runs its checks outside owned roots when no engine
// owner opens: the command refuses with a named outcome and runs no step (SPEC §6.4, §7.8).
func TestGateCommandNoOwner(t *testing.T) {
	commandsTestSetRoots(t)
	t.Setenv(rootsHelperEnv(), "1")
	step, record := rootsTestStep(t.TempDir(), "unowned", "")
	repo := commandsTestRepo(t, []GateStep{step})
	// A temp root that is the repository itself is unsafe, so no owner opens.
	t.Setenv(strings.ToUpper(core.Product)+"_TMP", repo)
	code, env, out := commandsTestRun(t, "--repo", repo)
	if env.Outcome != "root_unsafe" || env.Status != core.StatusError || code != core.StatusError.ExitCode() {
		t.Fatalf("outcome %q status %q exit %d, want the named refusal root_unsafe\n%s", env.Outcome, env.Status, code, out)
	}
	if row, ok := commandsTestRow(env.Outcome); !ok || row.Next == "" {
		t.Errorf("outcome %q is not a table row with a next action (BC-1)\n%s", env.Outcome, out)
	}
	if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("catches a step run with no engine owner: stat %q: %v", record, err)
	}
	if len(env.Data.Steps) != 0 {
		t.Errorf("catches a refused gate that reports steps: %+v", env.Data.Steps)
	}
}

// TestGateCommandRepoInTemp catches an engine owner opened without the repository as a canonical
// path: a temp root that holds the repository refuses as root_unsafe even when it is owner-only,
// so no check's roots can land inside the repository (SPEC §6.4).
func TestGateCommandRepoInTemp(t *testing.T) {
	roots := commandsTestSetRoots(t)
	t.Setenv(rootsHelperEnv(), "1")
	first, _ := rootsTestStep(t.TempDir(), "first", "")
	// The first run makes the temp root owner-only, so only the repository rule can refuse later.
	if code, env, out := commandsTestRun(t, "--repo", commandsTestRepo(t, []GateStep{first})); code != 0 {
		t.Fatalf("the first run, with the repository outside the temp root: exit %d, outcome %q\n%s", code, env.Outcome, out)
	}
	repo := filepath.Join(roots.temp, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatalf("make the repository in the temp root: %v", err)
	}
	step, record := rootsTestStep(t.TempDir(), "inside", "")
	data, err := json.Marshal(Definition{FormatVersion: formatVersion, Steps: []GateStep{step}})
	if err != nil {
		t.Fatalf("encode gate.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "gate.json"), data, 0o600); err != nil {
		t.Fatalf("write gate.json: %v", err)
	}
	code, env, out := commandsTestRun(t, "--repo", repo)
	if env.Outcome != "root_unsafe" || code != core.StatusError.ExitCode() {
		t.Errorf("catches an owner that does not keep the repository out of its temp root: outcome %q exit %d\n%s", env.Outcome, code, out)
	}
	if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("catches a step run with its repository inside the temp root: stat %q: %v", record, err)
	}
}

// TestGateDetailReason catches a text output that drops the reason a gate has no result: a gate
// that ran no required step reads not_measured, and its detail line must say why.
func TestGateDetailReason(t *testing.T) {
	report := GateReport{Result: ResultNotMeasured, Detail: "no required step ran, so the gate has no evidence",
		Steps: []StepReport{{ID: "vuln", Result: ResultGreen, Detail: "not required"}}}
	want := "gate not_measured (no required step ran, so the gate has no evidence)\n  vuln: green (not required)"
	if got := gateDetail(report); got != want {
		t.Errorf("gateDetail = %q, want %q", got, want)
	}
}

// TestGateDefinitionLinkOut catches a gate that reads a gate.json link leading out of the
// repository: the definition read stays inside --repo, and a link out of it is refused as an error.
func TestGateDefinitionLinkOut(t *testing.T) {
	outside := commandsTestRepo(t, []GateStep{{ID: "outside", Command: []string{"go", "version"}, Required: true}})
	repo := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "gate.json"), filepath.Join(repo, "gate.json")); err != nil {
		t.Skipf("file symlink: %v; a junction links only a directory", err)
	}
	def, err := readDefinition(repo)
	var cerr *core.Error
	if !errors.As(err, &cerr) || cerr.Outcome != core.OutcomeError || !strings.Contains(cerr.Expected, "inside the repository") {
		t.Fatalf("readDefinition = %+v, %v; want a *core.Error with outcome %q that names a gate.json inside the repository",
			def, err, core.OutcomeError)
	}
}

// gateDefinitionTestWithin runs readDefinition on repo and fails when it still blocks after 1 s;
// a writer opened on gate.json then releases a blocked FIFO open.
func gateDefinitionTestWithin(t *testing.T, repo string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := readDefinition(repo)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		if w, err := os.OpenFile(filepath.Join(repo, "gate.json"), os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatal("readDefinition still blocked after 1 s; want a named error")
		return nil
	}
}

// TestGateDefinitionNotRegular catches a gate.json that hangs the gate or is read without a cap
// (#199): a FIFO blocks a plain open forever on Unix, and a 1 MiB cap bounds the read.
func TestGateDefinitionNotRegular(t *testing.T) {
	const gateCap = 1 << 20
	padded := func(t *testing.T, repo string, size int) {
		data, err := json.Marshal(Definition{FormatVersion: formatVersion,
			Steps: []GateStep{{ID: "build", Command: []string{"go", "build", "./..."}, Required: true}}})
		if err != nil {
			t.Fatal(err)
		}
		body := append(bytes.Repeat([]byte(" "), size-len(data)), data...)
		if err := os.WriteFile(filepath.Join(repo, "gate.json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name    string
		unix    bool
		make    func(t *testing.T, repo string)
		wantErr bool
	}{
		{name: "gate.json at the cap refused", make: func(t *testing.T, repo string) { padded(t, repo, gateCap) }},
		{name: "gate.json one byte over the cap read", wantErr: true,
			make: func(t *testing.T, repo string) { padded(t, repo, gateCap+1) }},
		{name: "FIFO gate.json blocks the gate", unix: true, wantErr: true,
			make: func(t *testing.T, repo string) {
				if out, err := exec.CommandContext(t.Context(), "mkfifo", "-m", "600", filepath.Join(repo, "gate.json")).CombinedOutput(); err != nil {
					t.Fatalf("mkfifo: %v: %s", err, out)
				}
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unix && runtime.GOOS == "windows" {
				t.Skip("a FIFO is a Unix file type")
			}
			repo := t.TempDir()
			tc.make(t, repo)
			err := gateDefinitionTestWithin(t, repo)
			if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "gate.json")) {
				t.Fatalf("readDefinition = %v; want an error that names gate.json", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("readDefinition: %v; want a gate.json at the cap read", err)
			}
		})
	}
}
