# Phase 0 test baseline (2026-10-27)
Scope: the repository root (main, HEAD 2f6c205), bridge `../aiwe-wt` (HEAD 91bc64f), batch clone
`../aiwe-wt/batch` (HEAD c854ccc, 17 commits over origin/main). Window: 2026-10-06..2026-10-27.
Read-only measurement. No test, gate, lint or build was run by the author (DR-8); the one test run
in section 3.4 was made by the lead. Anything unmeasured says `not measured`. `tahmin` marks the only
estimates. Percentiles: linear interpolation over the observed values. Python helpers lived in the
session scratchpad, not in the repo; each number below names its source command or file.

## 1. Test inventory
### 1.1 aiwe (Go module `go.mod`; `tools/go.mod` is a tool module with 0 test files)
| Kind | Files | Test funcs | Command |
|---|---|---|---|
| All `_test.go` | 131 | 548 `Test` + 8 `Fuzz` + 0 `Benchmark` | `find . -name '*_test.go' -not -path './.local/*'`; `grep -c '^func Test'` over them |
| Unit/table (any `Test` using `t.Run`) | 111 files use `t.Run` | 277 funcs hold subtests; 650 `t.Run` calls | per-func scan for `t.Run(` inside each `^func Test` body |
| Contract suite (port) | 2 | 2 | `internal/tracker/trackertest/{adapter,contract}_test.go`; also consumed by `engine/lifecycle_test.go`, `engine/unit_test.go` |
| Smoke build tag | 1 | 1 | `internal/core/tools_smoke_test.go:1` `//go:build smoke`, live release read, off the default path |
| OS build tags (not smoke) | 7 | n/a | `grep '^//go:build'`: windows 3, !windows 2, linux 1, unix&&!darwin 1 (all in `internal/core`) |
| Spawns git/processes (`exec.Command*`) | 35 | 169 (file level); 19 call `exec.Command` directly | `grep -l 'exec\.Command'`; `awk` on func bodies. 14 files name `"git"` |
| Builds and runs the binary (e2e-like) | 1 | 3 in file; `TestVersion` runs `go build` | `cmd/aiwe/main_test.go:20-48` |
| `t.Parallel` | 17 | 161 calls | `grep -c 't\.Parallel()'` |

Per package Test counts: core 142, engine 131, gates 49, adapters 49, item 35, repocheck 31, ledger 30, roles 25, tracker 19, install 14, others 1 to 7. No `TestMain` (`grep TestMain`: 0).

The lead's `go test -json` run (3.4) saw 537 top-level tests, the grep sees 548. The gap of 11 is
consistent with tag-excluded files (linux, unix, smoke) on a Windows host (`tahmin`, not verified).
### 1.2 aiwe-wt (pytest; `pytest.ini`: `norecursedirs = u* batch evidence runs ...`)
| Kind | Files | Test funcs | Command |
|---|---|---|---|
| All `test_*.py` (root) | 26 | 801 (`def test_`) | `git ls-files 'test_*.py'`; `cat test_*.py \| grep -c '^\s*def test_'` |
| pytest classes `class Test*` | n/a | 10 classes (rest are unittest-style classes) | `grep -c '^class Test'` |
| Parametrized | 0 | 0 | `grep -c parametrize` = 0 |
| Spawn processes (`subprocess`) | 19 | 71 `subprocess.run/Popen` calls | `grep -l subprocess`; `grep -c 'subprocess.run\|Popen'` |
| Real git (`git` argv) | 16 | n/a | `grep -l '"git"\|git init\|\["git'` |
| Temp dirs (`tempfile/mkdtemp`) | 22 | n/a | `grep -l 'tempfile\|mkdtemp\|TemporaryDirectory'` |
| `conftest.py` | 0 | n/a | `ls conftest.py` |

Largest files: test_issue 114, test_lead_parse 92, test_seed 70, test_accept 69, test_pipe_flow 65.
### 1.3 Batch clone delta
Go test files 151 (main 131), `Test` funcs 655 (main 548, +107), `Fuzz` 12 (main 8). The batch changes
`.github/workflows/ci.yml` by +132/-1 lines (`git diff --stat origin/main..HEAD -- gate.json .github`);
`gate.json` unchanged. Anything below about CI refers to main's ci.yml (HEAD 2f6c205).

## 2. Current runners
| # | Runner | What it runs | file:line |
|---|---|---|---|
| 1 | CI `changes` job | `go run ./internal/cmd/gatesteps` picks the build flag from `gate.json` | `.github/workflows/ci.yml:18-47` |
| 2 | CI `build` matrix (ubuntu, windows) | `go build ./...`, `go vet ./...`, `go test ./...` | `ci.yml:48-71` |
| 3 | CI `gate` job | `go run ./cmd/aiwe gates gate --repo .` (all steps) | `ci.yml:72-87` |
| 4 | Step registry | 7 steps: format, lint, vuln, tidy, deadcode (optional), repo, test (`go test -json ./...`, 900 s) | `gate.json:3-22` |
| 5 | gates runner | per-step fresh roots; `-p=N` rewrite of `go test` | `internal/gates/roots.go:16-52`, `workers.go:11-35`, `runner.go:28-35`; cache `cache.go:16-90`; durations parser `durations.go:27-52`; limits `limits.go` |
| 6 | Local one-step | `gates gate --repo . --step <id>` (documented) | `AGENTS.md:69`, `CONTRIBUTING.md:13` |
| 8 | bridge `lead.py verify` | unit verify command, evidence record, `-p N` + `GOMAXPROCS` per step, optional docker Linux run | `lead.py:910-985`, `:479-506`, docker `:434-450` |
| 9 | bridge `lead.py integrate` | squash then build, vet, `go test ./...`, gofmt, tidy, gocognit | `lead.py:1136-1141`, run with `timeout=1200` at `:1204` |
| 10 | `implcommit.sh` | `go test $pkgs` on touched packages | `implcommit.sh:31` |
| 12 | `mutate.sh` | mutates one file in a clone, runs caller's test command, judges killed/survived by `--- FAIL` | `mutate.sh:4-19` |
| 13 | `land.sh` / `queue.sh` / `ship.sh` / `landbatch.sh` | require green verify evidence; `gh pr checks --watch` | `land.sh:15,44`; `queue.sh:36`; `ship.sh:44-50`; `landbatch.sh:12` |
| 14 | `pipe.py` | `sh()` default timeout 3600 s; 4 h external step; shells `lead.py` with 180 s | `pipe.py:221`, `:646`, `:1114` |
| 16 | `~/.agents/araclar/dar-test.py` | narrow-by-diff runner; reads `gate.json` formatVersion 2, else manifestless mode (aiwe's gate.json is formatVersion 1); writes `dar-test-kanit/` and `dar-test-defter.jsonl` via `kapi_defteri.py` | `dar-test.py:7`, `kapi_defteri.py:1-30` |
| 17 | `agir-is-kuyrugu.py` | machine-wide slot queue (N slots, load-gated) wrapping heavy commands | `agir-is-kuyrugu.py:1-30` |

Makefile: none. Release workflow: none (only `ci.yml`). `.cursor/` holds `cli.json`, `sandbox.json` (no runner).

Logic duplicated across runners (observed, not judged):

| Concern | Where it exists |
|---|---|
| Which tests run (selection) | `gate.json:22` (`./...`), `ci.yml:71` (`./...`), `lead.py:1138` (`./...`), `implcommit.sh:31` (touched pkgs), `dar-test.py` (changed files), verify commands in issue records |
| The Go step list build/vet/test/format/tidy | `ci.yml:67-71`, `lead.py:1136-1141`, `probe.sh:27-28`; gate.json carries format/tidy/test too |
| Timeouts | `gate.json` 900 s (test only), `ci.yml` 15/20/30 min, `lead.py:1204` 1200, `pipe.py:221` 3600, `ext.sh:26` 1800 |
| Worker count (`-p`) | `internal/gates/workers.go`, `lead.py:479`, `workers.py`, `probe.sh` |
| Cache | in-process `gates.Cache`; fresh per-check roots (`roots.go`); docker volumes `aiwe-gocache`, `aiwe-gomod` (`lead.py:445-447`); no cross-run result cache for a test result |
| Queueing | `agir-is-kuyrugu.py` (machine), `.batch.lock` (`batch_lock.sh`), slot pool in `internal/engine` |
| Evidence writing | `gates` runner (ledger), `lead.py` evidence records (`aiwe-wt/evidence`), `kapi_defteri.py` (`dar-test-kanit`, `dar-test-defter.jsonl`), `ci` run logs |

Files that must change to add something today (grep counts):
- New repo: 9 bridge files carry the literal `sungurerdim/aiwe` (`ext.py findings.py issue.py lead.py mktask.py pipe.py land.sh queue.sh ship.sh`; `lead.py:37 REPO`), plus `lead.py:1136` Go-only step list: about 10.
- New test kind (e.g. Python tests for aiwe): `gate.json`, `ci.yml`, `lead.py` step list and `go_index`/`with_workers`, `implcommit.sh`, `probe.sh`, `dar-test.py`: 6 to 7 (the `go test`/`pytest` string appears in 9 bridge files: accept, criteria_trace, ext, findings, lead, mktask, redlist, workers .py and implcommit.sh).

## 3. Durations (window 2026-10-06..2026-10-27)
### 3.1 CI (`gh run list --workflow ci.yml --limit 100 --json ...`; `gh run view <id> --json jobs`)
The workflow returned 31 runs (limit 100 not reached), created 2026-10-12T15:56Z..2026-10-26T15:01Z;
22 success, 9 failure, 0 cancelled. Events: pull_request 25, push 5, workflow_dispatch 1.
Wall = `updatedAt - startedAt`; seconds.

| Level | n | p50 | p90 | max |
|---|---|---|---|---|
| Run total, all | 31 | 214 | 282 | 801 |
| Run total, success | 22 | 209 | 268 | 735 |
| Run total, failure | 9 | 259 | 409 | 801 |
| Job `build (ubuntu-latest)` | 31 | 93 | 116 | 118 |
| Job `build (windows-latest)` | 31 | 211 | 278 | 761 |
| Job `gate` | 14 | 212 | 226 | 236 |

| Step | n | p50 | p90 | max |
|---|---|---|---|---|
| ubuntu `go test ./...` | 31 | 54 | 58 | 71 |
| windows `go test ./...` | 31 | 139 | 176 | 644 |
| gate `gate` / `gate steps` | 11 / 2 | 193 / 179 | 210 / 186 | 213 / 188 |

The `gate` job did not exist before about 2026-10-21 (14 of 31 runs). Per-gate-step (format, lint, ...)
CI times are not in the run steps (one step runs all): `not measured`.
### 3.2 Local verify records (`aiwe-wt/evidence/*.txt`, mtime 2026-10-10..2026-10-27)
785 records carry `exit: N  duration_s: S` (integer seconds). `repo=` in these records is the
GitHub repo name, so `sungurerdim/aiwe` rows include bridge pytest verifies. Kind from `command:`.

| Kind | n | p50 s | p90 s | max s |
|---|---|---|---|---|
| `go test <pkgs>` (repo label sungurerdim/aiwe) | 503 | 4 | 46 | 602 |
| `go test <pkgs>` (label aiwe, early) | 32 | 6 | 19.9 | 40 |
| `pytest` (bridge) | 197 | 22 | 180.8 | 828 |
| `gates gate` (full local gate) | 9 | 128 | 140 | 176 |
| other (repocheck, gofmt, ...) | 41 | 1 | 5 to 15.7 | 206 |

Limits: no per-step gate split (the record has one duration), no per-test times, durations are integer
seconds, machine load varies (see 3.4). `evidence/int-*.txt`: 141 integrate records carry step exit
codes but no step duration: `not measured`.
### 3.3 Package times from `ok  pkg  X.XXXs` lines in the same evidence (1642 lines, not deduplicated)
p50 / p90 / max seconds (n): core 29.5/84.1/218.8 (392); engine 26.1/307.4/2110.1 (166); gates 7.4/19.1/61.4 (211); cmd/aiwe 7.2/16.3/31.4 (123); adapters 2.8/4.3/8.8 (73); cmd/repocheck 2.4/6.8/26.5 (141); tracker 1.8/3.2/5.0 (53); repocheck 1.0/4.6/15.4 (221); item 1.0/17.2/33.0 (38); ledger, roles, hook, install, trackertest, filestore, gatesteps, records: p50 0.4 to 1.5 (n 3 to 62).

The engine max 2110 s is one line; a load stall (agent queue waits, 3.5) or a different tree cannot be
excluded: cause unknown. Lines mix trees from many units.
### 3.4 Lead run (lead run, 2026-10-27, main 2f6c205, `go test -json -count=1 ./...` via the machine job queue, exit 0; machine NOT idle: cpu p90 35 %, two analysts running, so durations are indicative, not a clean baseline)
Wall 303 s; 16 packages; 537 top-level tests; 0 fails. Raw: a scratch file of the lead session, not kept.

Package elapsed s: engine 291.0, core 77.6, gates 18.8, cmd/aiwe 9.7, repocheck 7.4, cmd/repocheck 7.0, adapters 4.7, tracker 2.9, trackertest 2.4, ledger 1.5.

Slowest tests s: engine TestSlotSafetyStaysInCacheRoot 25.9, core TestProcessTree 25.0, engine TestSlotSafetyNoRepoProgram 18.9, TestSlotSafetyUnusableSkipped 18.7, TestSlotLeaseQueue 18.3, TestSlotDeadHolderAndShrink 17.4, TestSlotCollectHiddenRefs 15.3, TestDirtySlotRecloned 14.2, TestSlotUntrustedState 13.3, TestSlotResetKeepsDeps 13.2, TestSlotCollectBeforeReclone 12.8, core TestProcessTreeBatchLauncher 11.6, engine TestSlotSafetyGoneCreator 9.4, cmd/aiwe TestVersion 8.2, engine TestSlotLeaseRefusals 8.1.

The slowest 10 % of tests take 87 % of the summed test time (lead figure). CI wall for the same
`go test ./...` is 54 s (ubuntu) / 139 s (windows) at p50 (3.1), against 303 s here on a busy machine.
### 3.5 Sources that hold nothing
`<git dir>/dar-test-defter.jsonl` (kapi_defteri ledger) is absent in aiwe, aiwe-wt and batch (aiwe-wt has only `.git/dar-test-kanit/testler.txt`, 967 B); the aiwe state root `.local/state` holds `gh/device-id` only; `aiwe-wt/runs/*.state` (89 files) have time-of-day log lines, no dates or durations; no `go test -json` file exists in evidence. All `not measured`.

## 4. Reds in the window
Rules (applied by script, then checked on the non-matching rows):
tdd-red = record file name carries `-red` (a deliberate test-first run, not a defect);
environment = tail has `go: go.mod file not found` (wrong cwd) or CI runner tool/config failure;
flaky = same command and same tree sha has both a green and a red record; real-bug = tail has
`--- FAIL`, `FAILED`, `AssertionError`, `undefined:`, `panic:`, `[build failed]` or `[setup failed]`;
stale = cache or build staleness; unknown = none of these. The keyword path was refined once after
a first pass mislabelled `No module named`, `(cached)` and `timeout` text inside test source as
environment/stale.
### 4.1 CI (9 failures of 31 runs = 29.0 %)
| Run | Date | Branch | Failed step | Class | Source |
|---|---|---|---|---|---|
| 40339407668 | 10-26 | batch/20261026-r2 | gate: deadcode red (`adapters/all.go:10 unreachable func: All`) | real-bug | `gh run view 40339407668 --log-failed` |
| 40240205124 | 10-25 | batch/20261026-main | ubuntu `go test`: `TestSweepHandBuiltStart` | unknown | log-failed |
| 40207713529 | 10-25 | docs/spec-owner-decisions | gate deadcode; `TestRepoIONotRegular/file_at_the_cap_refused` on both OS | real-bug | log-failed |
| 39729286371 | 10-22 | batch/20261022-083935 | gate: deadcode `adapters/all.go:10` | real-bug | log-failed |
| 39721852757 | 10-22 | batch/20261022-072038 | gate: deadcode `core/capacity.go:70` | real-bug | log-failed |
| 39643657453 | 10-21 | batch/20261021-202519 | gate `custom-gcl`: `configuration file not found`, exit 3 | environment | log-failed |
| 38561545545 | 10-14 | batch/20261014-150756 | both OS `TestOutcomes/outcome_string_used_in_code_missing...` | real-bug | log-failed |
| 38274445790 | 10-12 | docs/245-replan | windows `TestOwnerKeyACL/*` | environment (Windows ACL read, `exit status 1`) | log-failed |
| 38273803274 | 10-12 | docs/245-replan | windows `TestOwnedRootOwnerOnly/*`, "read the ACL of C:\Users\RUNNER~1..." | environment | log-failed |

CI totals: real-bug 5 (55.6 %), environment 3 (33.3 %), unknown 1 (11.1 %), flaky 0, stale 0. Whether
the two Windows ACL runs were a runner quirk or a test defect was not established (class kept as environment from the log text alone).
### 4.2 Bridge verify records (192 non-zero of 785 = 24.5 %; exit 1: 189, exit 2: 3)
| Class | n | share of 192 | share of non-TDD (68) |
|---|---|---|---|
| tdd-red (intended test-first red) | 124 | 64.6 % | n/a |
| real-bug | 52 | 27.1 % | 76.5 % |
| unknown | 12 | 6.3 % | 17.6 % |
| flaky (same tree and command also green) | 3 | 1.6 % | 4.4 % |
| environment | 1 | 0.5 % | 1.5 % |
| stale | 0 | 0 | 0 |

Sources: records `aiwe-wt/evidence/*.txt` with `exit:` non-zero. environment = `u1-60d0d87.txt` (`go: go.mod file not found`).
flaky = `u134-fac0bf4-a8eeb6.txt`, `u316-57920de-823d78-...-dirty.txt`, `u7-83bdb4c-90c506.txt`
(6 of 706 command+tree groups have both outcomes; 3 of those red rows are tdd-named). unknown =
gate or repocheck runs ending `exit status 1/3` without a test FAIL line: `u116-6a45ffe`, `u116-731c358`, `u278-a39fc0b` (gates gate),
`u130-4b42890`, `u16/u17/u190/u201-*` (repocheck rules, `missing_repo_file ci.yml` in early trees), `u352-e88eff4`, `u366-288a587` (pytest collection errors).
The `real-bug` row is an upper bound: some unnamed reds are probably also test-first runs (`tahmin`).
Non-TDD reds by kind: go test 121 of the 192 total, pytest 60, other 7, gates 4 (all 192, TDD included).
### 4.3 Lead and integrate records
- `evidence/*lead-red*.txt`: 80 files, all dated 2026-10-26, all `kind=red` or mutant runs (test-first proof, `mutate.sh` kills). Class: tdd-red 80 of 80. Many show queue waits ("sırada bekleniyor", slots held by other repos' node/vitest jobs).
- `evidence/int-*.txt` integrate results (141): 118 green, 23 red (16.3 %): merge 7, test 6, format 6, linux 2, build 1, fetch 1. Causes are not classified (log bodies not read): `unknown` 23.

Combined (CI 9 + verify 192 + integrate 23 = 224): tdd-red 124 (55.4 %), real-bug 57 (25.4 %), unknown 36 (16.1 %), environment 4 (1.8 %), flaky 3 (1.3 %), stale 0. Excluding the 124 intended reds (100): real-bug 57 %, unknown 36 %, environment 4 %, flaky 3 %. The flaky detector (same tree and command) is narrow; a test that flips on retry in another tree is invisible to it.

## 5. Boundary vs internal-detail tests
Method: all 545 `Test` funcs under `internal/*` (grep-visible, `cmd/aiwe` excluded) listed in path order;
33 taken at the centre of 33 equal-width strata (index `int(n*S + S/2)`, S = 545/33), so every package of size >= 1/33 is hit. Rule, applied mechanically then
read for the doubtful ones: Detail = calls an unexported production function (defined in a non-test file of the package), or fakes an internal collaborator and asserts its call counts, or both. Boundary = everything else (public API, CLI in/out, port contract, real git/fs, invariant, fuzz).
In-package (`package x`, not `x_test`) is a risk marker only: 91 of 130 internal test files are in-package, 39 external.

Samples (B boundary, D detail; all paths under `internal/`):
- B: adapters/deepseek_test.go:190, adapters/openai_chat_test.go:363, adapters/tools_test.go:227 (real git dir), core/cli_test.go:64, core/errors_test.go:25, core/log_test.go:313, core/owner_key_test.go:73 (real fs), core/remove_close_test.go:84, core/remove_swap_test.go:365, core/sweep_test.go:246, engine/acceptance_test.go:222, engine/contract_return_test.go:47, engine/integrate_test.go:194 (real git), engine/slot_order_test.go:170, engine/slot_sweep_test.go:383, engine/unit_test.go:462.
- B: gates/cache_test.go:146, gates/definition_test.go:205, gates/runner_test.go:332, hook/commands_test.go:189, item/filter_test.go:62, item/validate_test.go:87, item/validate_test.go:440, ledger/record_test.go:79, repocheck/ci_shape_test.go:321, repocheck/rules_test.go:324, roles/schema_test.go:283, tracker/github_test.go:396 (httptest).
- D: cmd/repocheck/main_test.go:28 (calls unexported `commands`), core/procstart_test.go:113 (`processStart`), engine/dispatch_test.go:156 (fakeRunner, asserts `len(first.runs)`), engine/lock_test.go:244 (`claimName`, `readClaimFile`, `removeClaimFileIf`), roles/roles_test.go:222 (`methodBodies`, judged by reading).

Result: **28 B / 5 D = 84.8 % / 15.2 %** (strict mechanical rule alone: 29 / 4). Sample error: n = 33 of 545, so about plus or minus 12 points at 95 %
(`tahmin`, normal approximation). Not looked for: assertions on unexported struct fields (the script cannot see them), so the detail share is a lower bound. Detail clusters seen: engine lock/claim helpers, core process-start, repocheck command table, role parsers.

## 6. Concurrency
| Resource | Two runs at once: can they collide? | Evidence |
|---|---|---|
| Ports | No fixed port: `httptest.NewServer/NewTLSServer` (loopback, OS-assigned) | `adapters/loop_test.go:344`, `openai_chat_test.go:145`, `tracker/github_test.go:88,123` |
| Shared OS temp | Collisions possible on fixed names: `filepath.Join(os.TempDir(), "owner-key-test-state")` (windows only); `slot_safety_test.go:328` canary in `os.TempDir()`; `gates/roots_test.go:186` lists the whole `os.TempDir()` (can see another run's dirs) | file:line given |
| State root / HOME | Tests set `t.Setenv` (112 calls), no `TestMain`; `t.Setenv` forbids `t.Parallel` in that test; user state root only via env (`core/platform.go:126-143`); never read in this audit whether a test touches the real user state root | `grep -c t.Setenv` |
| Git config | Tests spawn git in 14 files; whether they set `GIT_CONFIG_GLOBAL`/`HOME` per call was not traced; one grep for `GIT_CONFIG` found no hits in test files | `grep 'GIT_CONFIG\|--global'` over `*_test.go` |
| Go build cache | Shared user `GOCACHE` for plain `go test`; fresh cache root inside a gate check; docker runs use volume `aiwe-gocache` | `lead.py:445-447`, `roots.go` |
| `.batch.lock` | Directory lock at `$root/.batch.lock` held by `ship.sh` during CI; `queue.sh` fails fast instead of waiting | `batch_lock.sh:2-3,27-32,79-81` |
| Slot clones | Engine slot pool leases clones with holder and liveness; tests for it are the slowest in the suite (3.4) | `internal/engine/slot_*_test.go` |
| Machine queue | `agir-is-kuyrugu.py` N slots (`<lock>.0..N-1`), load-gated, shared with other repos (livvmonitor jobs hold slots in lead-red headers) | `agir-is-kuyrugu.py:12-29` |
| Bridge pytest | `pytest.ini` skips unit clones and `evidence/runs`; tests use `tempfile` in 22 files; no `conftest.py`; no xdist or lock found | `pytest.ini`, grep |

Answer: aiwe `go test` runs concurrently in practice (the lead run and two analysts ran at once on
2026-10-27, and the queue admits N heavy jobs at once), with no collision recorded in the evidence.
That is evidence of tolerance, not proof of isolation: the shared-`os.TempDir()` tests above and the unread git-config state remain open.
Bridge: concurrent pytest verifies run (u341 had 20+ lead-red runs on 2026-10-26 under the queue) but two `ship.sh` or `queue.sh`
runs are serialized by `.batch.lock`.

## 7. Open issues on the same topic
Source: `gh issue list --state open --limit 300 --json number,title,labels,body` returned 258 open issues (2026-10-27).
Keyword scan of titles and bodies; the most relevant:

| # | Title | Relevance |
|---|---|---|
| 522 | aiwe check runs verify, narrow and gate through one ticket queue | the runner unification asked here |
| 474 | Machine tools move into aiwe, and CPU admission uses tickets (decisions 8, 10) | queue moves into aiwe |
| 150 | The gate stores green test durations and feeds them to the step limit | duration ledger |
| 149 | Every gate step runs under a time limit, not only the test step | timeouts (only `test` has one in gate.json) |
| 443 | Every gate step reports its duration against a derived budget, and one register says where each | duration register |
| 467 | A slow step prints a red line, and the week prints its three slowest steps | weekly slow-step report |
| 328 | A slow gate step opens a finding with its slowest tests | slowest tests (section 3.4) |
| 66 | Mutation results on changed lines become evidence | mutation (`mutate.sh`) |
| 278 | Unreachable funcs keep the gate lint step red | cause of 3 CI reds in 4.1 |

No open issue has a title naming `coverage`, `quarantine`, `check cache` or `test selection` (titles scanned:
0 hits). `narrow` appears in #522 only. Issue bodies other than those matched by keyword were not read.

## 8. Not measured (reason; command for the lead)
- Per-test times on a quiet machine (lead run was under load): `go test -json -count=1 ./... > t.jsonl` on an idle host, sum `Elapsed` per test.
- Per-gate-step CI times (one CI step runs all): `go run ./cmd/aiwe gates gate --repo .`, read the per-step report.
- Integrate step durations (records hold exit codes only): time `go build/vet/test` in a batch clone.
- Engine 2110 s package line: `grep -rn '2110' ../aiwe-wt/evidence`, open the record.
- Real user state or git config read by tests: run `go test ./...` with `HOME`, `LOCALAPPDATA`, `GIT_CONFIG_GLOBAL` at empty dirs and compare.
- Flake rate with retries: `go test -count=5 -json ./internal/core ./internal/engine`.
- Coverage: `go test -cover ./...`.
- Causes of the 23 red integrate records: `grep -l 'result: red' ../aiwe-wt/evidence/int-*.txt`.
- kapi and aiwe state ledgers: files do not exist.
