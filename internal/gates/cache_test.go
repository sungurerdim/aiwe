package gates

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// cacheTestResult is a plausible stored result of the unit verify in keyTestInput. Its tail names
// the key it was built for, so a test can tell which result the cache served.
func cacheTestResult(key string) CheckResult {
	return CheckResult{Result: ResultGreen, ExitCode: 0, Tail: key}
}

// TestCacheServesStoredResult catches a cache that runs a check again for a key it already holds, or
// serves a result for a key it never stored (DR-8, SPEC §7.8).
func TestCacheServesStoredResult(t *testing.T) {
	cache := NewCache()
	key := CheckKey(keyTestInput())
	runs := 0
	got, err := cache.Once(key, func() (CheckResult, error) {
		runs++
		return cacheTestResult(key), nil
	})
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if got.Result != ResultGreen || got.ExitCode != 0 || got.Tail != key {
		t.Errorf("served result %+v, want the stored one", got)
	}
	if _, err := cache.Once(key, func() (CheckResult, error) {
		runs++
		return cacheTestResult(key), nil
	}); err != nil {
		t.Fatalf("Once again: %v", err)
	}
	if runs != 1 {
		t.Fatalf("catches a stored result that is not served for its own key")
	}
	other := keyTestInput()
	other.Argv = []string{"go", "test", "./internal/engine/..."}
	otherRuns := 0
	if _, err := cache.Once(CheckKey(other), func() (CheckResult, error) {
		otherRuns++
		return cacheTestResult(CheckKey(other)), nil
	}); err != nil {
		t.Fatalf("Once other: %v", err)
	}
	if otherRuns != 1 {
		t.Errorf("catches a result served for a key that was never stored")
	}
}

// TestCacheNeverServesChangedTree catches a stored result served for a check whose tree content
// changed, so a changed tree is never reported with the result of the tree before it (DR-8, SPEC
// §7.8).
func TestCacheNeverServesChangedTree(t *testing.T) {
	cache := NewCache()
	before := keyTestInput()
	after := keyTestInput()
	after.InputsDigest = keyTestDigest("tree after the fix round")
	first, last := CheckKey(before), CheckKey(after)
	if first == last {
		t.Fatalf("catches a key that does not change with the tree content, so a changed tree reuses a stored result")
	}
	if _, err := cache.Once(first, func() (CheckResult, error) {
		return cacheTestResult(first), nil
	}); err != nil {
		t.Fatal(err)
	}
	runs := 0
	got, err := cache.Once(last, func() (CheckResult, error) {
		runs++
		return cacheTestResult(last), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 || got.Tail != last {
		t.Errorf("catches a stored result served for a changed tree: key %q served for key %q", first, last)
	}
}

// TestCacheTwoKeysRunTogether catches a cache that holds its lock while a check runs: two calls
// for two different keys must run at the same time (DR-3, DR-8, SPEC §7.8). Under a cache that
// runs one key at a time the second call waits for the first and never returns, so the test fails
// on the wait instead of hanging.
func TestCacheTwoKeysRunTogether(t *testing.T) {
	cache := NewCache()
	other := keyTestInput()
	other.Argv = []string{"go", "test", "./internal/engine/..."}
	firstKey, secondKey := CheckKey(keyTestInput()), CheckKey(other)
	if firstKey == secondKey {
		t.Fatal("catches a key that does not change with the argv, so two checks share one key")
	}
	type outcome struct {
		result CheckResult
		err    error
	}
	holding := make(chan struct{})
	release := make(chan struct{})
	first := make(chan outcome, 1)
	go func() {
		r, err := cache.Once(firstKey, func() (CheckResult, error) {
			close(holding)
			<-release
			return cacheTestResult(firstKey), nil
		})
		first <- outcome{r, err}
	}()
	<-holding
	second := make(chan outcome, 1)
	go func() {
		r, err := cache.Once(secondKey, func() (CheckResult, error) {
			return cacheTestResult(secondKey), nil
		})
		second <- outcome{r, err}
	}()
	select {
	case got := <-second:
		if got.err != nil {
			t.Fatalf("second key: %v", got.err)
		}
		if got.result.Tail != secondKey {
			t.Errorf("second key stored %+v, want key %q", got.result, secondKey)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("catches a cache that runs one key at a time: the second key's check waited for the first")
	}
	close(release)
	if got := <-first; got.err != nil || got.result.Tail != firstKey {
		t.Errorf("first key: %+v, %v", got.result, got.err)
	}
}

// TestNilCacheRefusesTheKey catches a nil cache that runs a key and returns the result. A nil
// cache cannot keep the once-per-key rule, so both calls are refused and the check does not run
// (DR-8, SPEC §7.8).
func TestNilCacheRefusesTheKey(t *testing.T) {
	var cache *Cache
	key := CheckKey(keyTestInput())
	runs := 0
	build := func() (CheckResult, error) {
		runs++
		return cacheTestResult(key), nil
	}
	_, err1 := cache.Once(key, build)
	_, err2 := cache.Once(key, build)
	if err1 == nil || err2 == nil {
		t.Fatalf("catches a nil cache that returned a result instead of refusing the key: %v, %v", err1, err2)
	}
	if runs != 0 {
		t.Fatalf("catches a nil cache that ran a key %d times; a refused nil cache runs nothing", runs)
	}
}

// TestCacheStoresNoFailedBuild catches a failed check stored or served: its error reaches the
// caller, a later caller of the same key runs the check again, and only a result is stored (DR-6,
// DR-8). Uncached runs its check every time and never fills the key.
func TestCacheStoresNoFailedBuild(t *testing.T) {
	cache := NewCache()
	key := CheckKey(keyTestInput())
	runs := 0
	failing := func() (CheckResult, error) {
		runs++
		return CheckResult{}, errors.New("the check did not exit")
	}
	if _, err := cache.Once(key, failing); err == nil {
		t.Fatal("catches a failed check that returned no error")
	}
	for i := 0; i < 2; i++ {
		got, err := cache.Uncached(func() (CheckResult, error) { runs++; return cacheTestResult("uncached"), nil })
		if err != nil || got.Tail != "uncached" {
			t.Fatalf("catches an uncached check that did not return its own result: %+v, %v", got, err)
		}
	}
	for i := 0; i < 2; i++ {
		got, err := cache.Once(key, func() (CheckResult, error) { runs++; return cacheTestResult(key), nil })
		if err != nil || got.Tail != key {
			t.Fatalf("catches a key that served %+v, %v after a failed check, want its result", got, err)
		}
	}
	if runs != 4 {
		t.Fatalf("catches %d check runs, want 4: the failure, two uncached runs and one stored run", runs)
	}
}

// cacheTestGit runs git in dir with stdin and returns its trimmed stdout; a failure fails the test.
func cacheTestGit(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// cacheTestRepo makes a git work tree whose first commit holds files.
func cacheTestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	commandsTestSetRoots(t) // core.Git keeps its hooks directory under the state override, not a fresh root
	dir, hooks := t.TempDir(), t.TempDir()
	cacheTestGit(t, dir, "", "init", "-q")
	for _, kv := range [][2]string{{"core.hooksPath", hooks}, {"user.name", "Ada Lovelace"},
		{"user.email", "ada.lovelace@example.com"}, {"commit.gpgsign", "false"}, {"core.autocrlf", "false"}} {
		cacheTestGit(t, dir, "", "config", kv[0], kv[1])
	}
	cacheTestCommit(t, dir, files)
	return dir
}

// cacheTestCommit writes and commits files in dir.
func cacheTestCommit(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cacheTestGit(t, dir, "", "add", "--", name)
	}
	cacheTestGit(t, dir, "", "commit", "-q", "-m", "change the declared inputs")
}

// cacheTestDigest is the inputs digest of the declared inputs in dir's HEAD.
func cacheTestDigest(t *testing.T, dir string, inputs ...string) string {
	t.Helper()
	digest, _, err := gitKeyParts(t.Context(), dir, inputs)
	if err != nil {
		t.Fatalf("catches declared inputs %q refused in a tree that holds them: %v", inputs, err)
	}
	return digest
}

// TestKeyInputsMatchAnyDepth catches `**` read as one path level: a commit to a nested file under
// `pkg/**` changes the inputs digest, and a commit outside it does not (DR-6, SPEC §7.8).
func TestKeyInputsMatchAnyDepth(t *testing.T) {
	dir := cacheTestRepo(t, map[string]string{"pkg/a.go": "package pkg\n",
		"pkg/sub/deep/b.go": "package deep\n", "other/c.go": "package other\n"})
	all, nested := cacheTestDigest(t, dir, "pkg/**"), cacheTestDigest(t, dir, "pkg/**/b.go")
	cacheTestCommit(t, dir, map[string]string{"other/c.go": "package other // changed\n"})
	if cacheTestDigest(t, dir, "pkg/**") != all {
		t.Errorf("catches a commit outside pkg/** that changed its digest")
	}
	cacheTestCommit(t, dir, map[string]string{"pkg/sub/deep/b.go": "package deep // changed\n"})
	if cacheTestDigest(t, dir, "pkg/**") == all {
		t.Errorf("catches a committed change to pkg/sub/deep/b.go that left the digest of pkg/** unchanged")
	}
	if cacheTestDigest(t, dir, "pkg/**/b.go") == nested {
		t.Errorf("catches a committed change to pkg/sub/deep/b.go that left the digest of pkg/**/b.go unchanged")
	}
}

// TestKeyInputsReadPathsByteExact catches a path git quotes, or a name rewritten, before it is
// matched: a commit to docs/ü.md changes the digest of docs/*.md, and a name that holds a backslash
// is that name, not a path with a slash (DR-6, DR-18, SPEC §7.8).
func TestKeyInputsReadPathsByteExact(t *testing.T) {
	dir := cacheTestRepo(t, map[string]string{"docs/a.md": "# a\n", "docs/ü.md": "# ü\n"})
	before := cacheTestDigest(t, dir, "docs/*.md")
	cacheTestCommit(t, dir, map[string]string{"docs/ü.md": "# ü, changed\n"})
	if cacheTestDigest(t, dir, "docs/*.md") == before {
		t.Errorf("catches a committed change to docs/ü.md that left the digest of docs/*.md unchanged")
	}
	blob := cacheTestGit(t, dir, "a name with a backslash\n", "hash-object", "-w", "--stdin")
	tree := cacheTestGit(t, dir, "100644 blob "+blob+"\t"+`lit\name.txt`+"\n", "mktree")
	commit := cacheTestGit(t, dir, "", "commit-tree", "-m", "a name with a backslash", tree)
	cacheTestGit(t, dir, "", "update-ref", "HEAD", commit)
	cacheTestDigest(t, dir, `lit\name.txt`)
	if _, _, err := gitKeyParts(t.Context(), dir, []string{"lit/name.txt"}); err == nil {
		t.Errorf(`catches the name lit\name.txt read as the path lit/name.txt`)
	}
}

func init() { cacheTestFakeGoExit() }

// cacheTestFakeGo puts a copy of this test binary first on PATH as the go tool. Asked for
// `go env`, the copy prints stdout and exits code, so a test sets the toolchain probe (SPEC §7.8).
func cacheTestFakeGo(t *testing.T, code int, stdout string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir, name := t.TempDir(), "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for file, body := range map[string][]byte{name: data, "probe.txt": fmt.Appendf(nil, "%d\n%s", code, stdout)} {
		if err := os.WriteFile(filepath.Join(dir, file), body, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// cacheTestFakeGoExit is the go tool cacheTestFakeGo planted: when this binary is that copy and is
// asked for `go env`, it prints the probe file's body and exits its code.
func cacheTestFakeGoExit() {
	self, err := os.Executable()
	if err != nil || strings.TrimSuffix(filepath.Base(self), ".exe") != "go" || len(os.Args) < 2 || os.Args[1] != "env" {
		return
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(self), "probe.txt"))
	if err != nil {
		os.Exit(8)
	}
	head, body, _ := strings.Cut(string(data), "\n")
	code, err := strconv.Atoi(head)
	if err != nil {
		os.Exit(9)
	}
	if _, err := os.Stdout.WriteString(body); err != nil {
		os.Exit(10)
	}
	os.Exit(code)
}

// cacheTestKeyInput resolves the key of a `git status` step in dir, the work tree root.
func cacheTestKeyInput(t *testing.T, dir string) (KeyInput, error) {
	t.Helper()
	in, _, err := ResolveKeyInput(t.Context(), dir, GateStep{ID: "unit", Command: []string{"git", "status"}})
	return in, err
}

// TestKeyFollowsTheToolchainProbe catches a key that ignores the toolchain probe of SPEC §7.8
// (`go env GOVERSION GOOS GOARCH CGO_ENABLED`) or GOFLAGS, the env that changes a Go build (DR-8).
func TestKeyFollowsTheToolchainProbe(t *testing.T) {
	dir := cacheTestRepo(t, map[string]string{"a.go": "package a\n"})
	for _, kv := range [][2]string{{"CGO_ENABLED", "0"}, {"GOOS", "linux"}, {"GOARCH", "amd64"}, {"GOFLAGS", "-mod=mod"}} {
		t.Setenv(kv[0], kv[1])
	}
	in, err := cacheTestKeyInput(t, dir)
	if err != nil {
		t.Fatalf("catches a measurable key refused: %v", err)
	}
	prev := CheckKey(in)
	for _, kv := range [][2]string{{"CGO_ENABLED", "1"}, {"GOOS", "windows"}, {"GOARCH", "arm64"}, {"GOFLAGS", "-mod=readonly"}} {
		t.Setenv(kv[0], kv[1])
		in, err := cacheTestKeyInput(t, dir)
		if err != nil {
			t.Fatalf("catches a measurable key refused after %s changed: %v", kv[0], err)
		}
		if key := CheckKey(in); key == prev {
			t.Errorf("catches a key that ignored %s=%s", kv[0], kv[1])
		} else {
			prev = key
		}
	}
}

// TestKeyRefusesAnUnmeasuredToolchain catches a toolchain probe that exits non-zero or prints
// nothing kept as a measurement: the key is refused, whatever the step's own program is (DR-6).
func TestKeyRefusesAnUnmeasuredToolchain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   int
		stdout string
	}{
		{"a probe that prints and exits non-zero", 3, "go1.24.0\nlinux\namd64\n0\n-mod=mod\n"},
		{"a probe that prints nothing", 0, "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := cacheTestRepo(t, map[string]string{"a.go": "package a\n"})
			cacheTestFakeGo(t, tc.code, tc.stdout)
			if _, err := cacheTestKeyInput(t, dir); err == nil {
				t.Fatalf("catches %s kept as a measured toolchain", tc.name)
			}
		})
	}
}
