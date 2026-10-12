# aiwe — Product Specification

Status: target design, revision 8, 2026-10-12. This file holds product, architecture and contracts only (L1). The
delivery plan lives in the tracker: one goal issue per milestone, units as its sub-issues (§13). Decisions: §16.
Facts: §17.
Labels: **fact** = traced to §17 · **default** = chosen here; the owner may reverse it. An unmeasured value is
`unknown` (DR-6). Each fact lives in one place; other places cite it by id (DR-n, §n, Sn, Mn, On, Ln, Qn).
Decision order: owner decisions, then the value order (§1.4), then necessity (DR-22).
## 1. Purpose, users, problems, value, metrics
### 1.1 Purpose
aiwe turns a goal issue into verified, merged code, using the AI subscriptions and API keys you already have. It runs
on Windows, macOS and Linux with zero configuration, through each provider's own CLI or API, never through a proxy.
Its primary interface is an MCP server that the user's harness session starts. It is a public open-source product.

Pillars, in value order:
1. **Verified delivery**: small units; only the engine runs checks; an independent auditor judges the done criteria;
   only the audited tip merges; every unit is measured (§1.5). This is the differentiator (§17).
2. **Work tracking**: one issue standard (§8.4, O5); a goal splits into sub-issue units with native dependencies;
   `aiwe issue` and `aiwe report` work across the user's repositories (O8).
3. **Source organization**: subscriptions and API keys are sources; each run gets its model from measured data
   (§8.1, O9) with ordered fallback (O3); quota classes and resets are tracked (§7.6); spend comes from the ledger
   with an optional cap; at most one consumer subscription per provider (O7); aiwe holds no credential (DR-19).

**Users**: solo developers and small teams who run several projects with coding agents; they act only on product decisions.
### 1.3 Problems and how each is solved
| Id | Problem | Mechanism |
|---|---|---|
| S1 | Extra subscriptions and API keys are not usable in a standard way. | One global source registry (§8.1). |
| S2 | Cost, time, errors and waste are minimised by hand. | Model choice and routing (§8.1), measured capacity (DR-3), quota reaction (§7.6), metrics (§1.5). |
| S3 | No standard role or task template. | Role cards and the issue standard ship with the engine (§8.4, §9). |
| S4 | The writer approves its own work, checks are skipped, scope is vague. | DR-5, DR-6, DR-7, DR-9. |
| S5 | Context bloat on long work causes wrong or missing work. | Small units (DR-7), a clean context per run, state only in tracker and ledger (DR-20). |
| S6 | Agents run broad checks outside their scope; a 5 s check becomes 5 min. | DR-8, §7.8. |
| S7 | Agents do not work the same way. | One prompt contract, card format and return format (§9). |
### 1.4 Value order
When goals conflict: **1 time** (the user gives a goal and sees a result) · **2 trust** (every merged change passed a
mechanical gate and an independent audit; escaped defects 0) · **3 cost** (lowest measured quota, money, disk, CPU,
memory and network).
### 1.5 Success metrics
`aiwe metrics [--repo R] [--since D] [--by goal|model]` computes them from the ledger (§8.5) and the git history of
the working branch (L10).

| Id | Metric | Target | Exact definition |
|---|---|---|---|
| M1 | First-try green rate | ≥ 70 % | `A / B`. B = units closed `completed` in the window. A = units in B whose first engineer run merged with `fixRounds = 0` and no second engineer run. |
| M2 | Interventions per unit | 0 | Ledger records with `kind = owner_action` per closed unit. The engine writes one when it stops for the owner, or when it sees a non-engine commit, label or state change on a claimed unit. |
| M3 | Escaped defects | 0 | Merged units U for which a later commit C on the working branch, within W after U's merge commit M, is a `git revert` of M, or is typed `fix` and changes lines that `git blame C^` attributes to M. W = 30 days (default). |
| M4 | Cost per green unit | measured | Sum of `costUsd` over all runs of units merged in the window ÷ number of those units. One unknown cost → `unknown` plus the measured lower bound. |
| M5 | Unit lead time | measured | Median of `ciGreenAt − claimedAt` over units merged in the window. |
### 1.6 Glossary
**Goal**: an issue with native sub-issues, its units. **Unit**: an issue in the standard (§8.4) with a file set and a
verify; identity = the issue number. **Run**: one role (§9.1) on one unit by one source (§8.1); id = ULID.
**Harness**: a CLI program that runs an agent; an **adapter** drives one harness or API wire format (§10.2).
**Check**: one mechanical command with a recorded result; a **gate** is an ordered list of checks. **Evidence**:
engine-recorded command, exit code, last lines and key; agent text is never evidence. **Unattended**: no human waits;
every gate still runs.
## 2. Scope and non-goals
In scope: onboarding a git repository without setup (DR-4); planning a goal into sub-issue units (§7.2); the unit
lifecycle to the merge (§7.3); sources, model choice, routing and quota reaction; ledger and metrics; issue tooling
and reports across repositories (O8); an MCP server as the primary interface, with the CLI from
the same registry, and self-update (O10).

**End of job** for a unit: its squash commit is on the working branch, the remote CI run for that commit is green, and
the issue is closed `completed` by the commit's `Closes` trailer (L7).

Non-goals (permanent): a web, desktop or mobile UI, terminal session manager or chat bridge; a proxy or gateway for
model traffic (DR-19); a learned router whose choice can change without a change in its inputs (O9); storing
credentials (DR-19); reading logs, sessions or bills of tools aiwe did not run (O6); releasing managed projects;
general pull-request review; a network MCP transport; deleting a directory aiwe did not create (DR-11); rotating
consumer accounts of one provider (O7); quota forecasts; benchmark harnesses. Deferred with a trigger: §15.
## 3. Design rules
One enforcement each (L2). "Gate step" = a step of `gate.json` (§12.2); `repo/<check>` = a check in
gate step `repo`. Test paths are relative to `internal/`.

| # | Rule | Enforcement |
|---|---|---|
| DR-1 | The engine runs unattended. Every wait has a time limit derived from measured durations (§7.4, §7.8), and every stop has a named outcome. An irreversible step waits for the owner's terminal-only approval. | test `engine/lifecycle_test.go` |
| DR-2 | Work whose answer is determined by its input is code, never a model call. Anything else comes from the adaptation ladder (§7.7); its stored artefact is reused while its input hash is unchanged. | test `engine/synthesis_test.go` |
| DR-3 | No OS, device, shell, path, core count, memory, harness, model, provider, stack or tracker is assumed. Each difference lives in one adapter file or data row, and capacity is measured at run time (§6.4, §6.6). | test `core/capacity_test.go` |
| DR-4 | A managed project needs no file, setting or setup step. The product needs nothing installed except git: one binary per OS and architecture (§6.2). A project `gate.json`, if present, wins over the derived gate. | CI step `clean-os-smoke` (§12.2) |
| DR-5 | A judging run is separate from the writing run. It starts with a clean context on the most independent source available (§8.1 point 5). Its verdict counts only from its own result bound to its RunID. | test `engine/judge_test.go` |
| DR-6 | Absence is not evidence: a skipped step makes the gate `incomplete`. An unknown stack, empty output, missing tool or zero collected tests is `not_measured` or red. Prices, quotas, scores and metrics are measured or `unknown` with a reason (`no_price`, `no_usage_data`, `subscription`), never `0`. | test `gates/runner_test.go` |
| DR-7 | A unit is dispatched only when its issue parses (§8.4) with a verify and a file set. The file set holds at most 5 source files; test, lock, generated files and `go.mod`/`go.sum` do not count (Q25). A behaviour unit has a To do line whose verify names a test. | test `engine/unit_test.go` |
| DR-8 | Only the engine runs checks: an agent requests its unit verify through `aiwe verify`, the auditor reads recorded results, and a check runs at most once per key (§7.8). | test `gates/cache_test.go` |
| DR-9 | Every engine diff is `git diff <base>...<tip>` from the run's base commit. It is compared with the declared file set (`scope_mismatch`). A flagged line byte-identical on the base is `pre_existing`. A verify that counts from `HEAD~<n>` or `HEAD^` is refused. | test `engine/coverage_test.go` |
| DR-10 | Every run works in its own fresh shallow clone with scratch `HOME` and temp and a secret-free environment. A sandbox adapter records its level. Every engine git call runs with repository-configured programs off. A run whose isolation fails does not start (§5.21, §6.5, §6.7). | test `engine/clone_test.go` |
| DR-11 | The engine owns what it starts: a unit has at most one open run, and a stop ends the whole process tree. Committed work is collected into a run ref before the clone goes. Temporary data is removed on every exit path within §6.4. Recursive removal runs only in `core/remove.go`, on a directory the engine created (§5.15). | test `core/remove_test.go` |
| DR-12 | Tests are never weakened, skipped or mocked away. Base tests run against the new code, so behaviour loss is declared or red. A fixed error class gets the earliest gate that would have caught it, in the same change. | judgement: auditor |
| DR-13 | An agent commits only on its run branch; push, merge and tracker writes belong to the engine. | test `adapters/env_test.go` (no tracker token in the run environment) |
| DR-14 | Only an audited tip merges: one squash commit per unit pull request, after green CI, while its head equals `AuditedTip` (else `unaudited_commits`). The commit carries the trailer `Run-Id: <ULID>`. A red `main` CI is undone by a revert pull request, never a direct push. | test `engine/integrate_test.go` |
| DR-15 | Every run carries one role card, resolved mechanically from the unit (§9.1; none → `role_unresolved`). It also carries one (source, model, effort) entry of the session assignment (§8.1). The observed model is recorded, and a mismatch is red. | test `engine/role_test.go` |
| DR-16 | Every capability is one command. CLI help and MCP tools come from one registry. Templates are served by the tool, never copied into a project. A command that approves an irreversible action or changes trust is terminal-only (§5.7). | test `core/command_test.go` |
| DR-17 | Source passes the repository's static rules. Module edges go only through root packages declared in `core/modules.go`. No source holds a machine-specific path, a product name outside its one constant, an ignored error or dead code. | the gate (§12.2) |
| DR-18 | All text I/O is UTF-8, byte-exact end to end and independent of the locale; machine-parsed markers are fixed English keys; this repository's source, docs and commit messages are in English. | test `core/text_test.go` |
| DR-19 | aiwe holds no credential. It reaches a provider only through the provider's own CLI login made by the user, or through an API key read from an environment variable. It passes a key only to the child that needs it. It never stores, caches, prompts for, proxies or alters a credential or subscription traffic (§6.5, §10). | contract suite `adapters/providertest` |
| DR-20 | The tracker is the single source of work state: every piece of work is an issue in the §8.4 standard before it starts. Only built-in tracker elements are used. Intake reads only text by authors with write permission; HTML comments are removed and bidirectional controls refused. An issue closes mechanically (L7). | test `tracker/intake_test.go` |
| DR-21 | Shipped code links only the Go standard library, `golang.org/x/*` and the official MCP Go SDK, plus the modules these require; any other module needs an owner decision. | check `repo/deps` |
| DR-22 | Every element (module, rule, workflow, job, step, document section, code path) exists only if it is necessary; each CI and release step states its reason in a `# why:` comment. | check `repo/ci-shape` |
| DR-23 | aiwe is built with aiwe: every standard it applies to managed projects applies to its own development. Before the self-hosting point (§13, M-Skeleton) the lead applies the standard under L5. After it, a unit worked outside aiwe is a defect. | CI `repocheck run-id` (§12.2, Q17) |
## 4. Engineering principles
KISS, YAGNI, DRY and SSOT (DR-17, DR-22) · open/closed: a provider is one adapter file plus one line in
`adapters/all.go` · dependency inversion: engine code depends on ports, never on an adapter id (§5.1) · pure core
(§5.6) · append-only ledger (test `ledger/store_test.go`) · idempotency: a "run twice" test per command · accessible
output: status words, no colour, `--format json` (test `core/output_test.go`).
## 5. Coding rules and patterns
Paths are under `internal/`.
### 5.1 Ports and adapters
Ports: provider (§10.2), tracker (§10.1), stack profile (§8.2), sandbox (§6.7); each embeds `core.Identified`. The
engine holds `[]adapters.Provider`, never a vendor type; auth, quota detection, resume and streaming live in the
adapter. `adapters.All()` is the one list of compiled adapters (test `adapters/all_test.go`); `Available(ctx, all,
env)` filters it at run time. An API source needs no code: its row names `openai-chat`. Stack profiles, capabilities,
outcomes, checks, quota classes and role fit are data tables: a new stack is a row.
### 5.3 Result and error model
```go
type Status string // "ok" | "error" | "usage" | "red" | "no_change" → exit 0 1 2 3 4
type Result[T any] struct { Status Status; Outcome Outcome; Data T; Detail string }
type Error struct { Outcome Outcome; Expected, Found, Next string; Err error } // Unwrap() returns Err
```
A red gate or `no_change` is a `Result`; an `error` means the command could not decide. Callers wrap with `%w` and
test with `errors.Is`/`errors.As`, never by text. `core/outcomes.go` maps outcomes to exit codes. `panic` only for
programmer errors, through `core/assert.go`; entry points recover once, write a crash record and exit 1. Errors render
as `<outcome>: expected <x>; found <y>; next: <one action>`. Logs are one JSON object per line on stderr through
`log/slog` (`{ts, level, runId?, unit?, event, detail}`), no colour. Dependencies are struct fields or parameters. No package-level mutable variable except sentinel errors, compiled
patterns and `var version string` in `cmd/aiwe/main.go`. The environment is read only in `core/env.go`.
### 5.6 Pure core, imperative shell
Decisions take and return data; process, git, HTTP and file IO live in `*_io.go` files or adapters. Only those,
`cmd/aiwe/main.go`, `internal/cmd/repocheck/main.go`, `repocheck/source_rules.go` and the `core` files that
`ioCoreFiles` in `repocheck/source_rules_layout.go` lists import `os`, `os/exec`, `io/ioutil`, `net` or `net/http`. A
file under a repository or state root is read through an `*os.Root`, because a path read follows a link out of the
root; only the files `rootReadFiles` lists read by path (rule `root_read`; `os.ReadDir` and nil-source parser reads are
not covered, #214). The root refuses an absolute link target; it cannot see a hardlink.
### 5.7 One command registry
`core.Command{Module, Object, Verb, Params, Run}`; commands read `<object> <verb>` (`aiwe unit check`), or one word for
a single action (`aiwe version`). `Terminal: true` marks `sources enable`, `artefact submit` and
`unit approve-breaking`: never an MCP tool; confirmation from the terminal; no terminal → `not_a_terminal`.
### 5.12 Config precedence
flags > environment > settings files (`<state>/settings.json`, `<state>/settings.*.json`) > defaults. A settings file
is opened once without blocking and must be a regular file of at most 64 KiB, else `config_invalid`.
`aiwe config show` prints each value and its origin. Paths resolve against `--repo`; a relative path without it is
`usage`; only a relative `--repo` resolves against the working directory, once, at the CLI boundary.
### 5.13 Test structure
- `x.go` has its behaviour test in `x_test.go`, or the covering test names it. Tests are table-driven; each row names
  the failure it catches. Every check has a red case on a known-bad input.
- One contract suite per port, run against every implementation and fake (`providertest.Run`, `trackertest.Run`);
  the provider suite checks recorded harness flags, no credential leaving env, DR-15, DR-19 and quota classes (§7.6).
- Parsers of untrusted text (issue body, intake, `gate.json`, sources, catalogue) have a `Fuzz` test. Fixtures use
  realistic values, `t.TempDir()` and `core.CreateOwned`. Live calls sit behind the build tag `smoke`.
- Source file ≤ 400 lines; test file ≤ 500; non-test function ≤ 60 lines (Q23) and cognitive complexity ≤ 12 (Q12);
  a root package holds its public surface only. Enforced: `repo/size`; `gocognit`.
- The test step's limit is at least 2 × the longest recorded package, never divided by workers; a package over half
  the limit turns the gate red. `-p` comes from DR-3.
### 5.14 Text and locale
`core.ReadText` accepts UTF-8 only (`invalid_utf8`); `core.PassText` sends long or non-ASCII text by stdin or a UTF-8
file. Case mapping only on ASCII markers; byte comparison and order; ISO numbers and dates; no console code page is
read or set; Windows argv lives only in `core/process_windows.go` (§17).
### 5.15 Owned removal
`core.Owner.CreateOwned(ctx, root, owner)` makes a directory and its `.owner` marker under `TempRoot` or `CacheRoot`,
the only `OwnedRoot` values; only `CreateOwned` makes an `OwnedDir`. `RemoveOwned` refuses (`not_owned`) unless all
hold (L6): the real path is under the temp or cache root; no link leaves that root; the marker is present; the walk
never descends into a mount point. The state root and user directories stay outside (Q14). A temp or cache root that
is or contains the home, the state root, a canonical repository or a file-system root, that is a link or junction, or
that is not owner-only is `root_unsafe`; so is any root another user can read, write or owns. The fix never names a
directory for delete. A state root is `root_unsafe` (L18) when it is a file-system root, is or contains the home or a
repository, or lies inside a repository work tree.
### 5.17 Static analysis
`go vet` alone caught none of five tested classes (§17). Gate step `lint` runs golangci-lint with `exhaustive`,
`nilaway` (a module plugin that may report false positives), `errcheck`, `errorlint`, `contextcheck`, `containedctx`,
`noctx`, `bodyclose`, `forbidigo` (`os.RemoveAll`, `os.Getenv`, `panic`, `context.Background` outside their one file),
`gocognit` (`min-complexity: 12`; a whole `switch` adds 1, so tables stay cheap), `gosec`, `staticcheck`, `unused` and
`govet`. Data races: CI step `race`. Blocked goroutines: `testing/synctest`. Index range: table tests, fuzz seeds.
### 5.18 Context and goroutines
Every function that blocks, does IO or starts a process takes `ctx` first; a time limit is a `context.WithTimeout`
where it is owned; `context.Background()` only in `cmd/aiwe` and tests; no struct stores a context. Goroutines run
under `errgroup.WithContext` with `SetLimit(core.Workers(jobs))`; the first error cancels the rest (§17). Every
goroutine has a waiting owner; only the sender closes a channel.
### 5.20 Subprocesses and process trees
`core.Start(ctx, ChildSpec{Argv, Dir, Env, Stdin})` starts a child in its own process group (Unix `Setpgid`) or Job
Object (Windows); `StopTree(grace)` asks, waits, then kills the tree. `Cmd.Cancel` = `StopTree` and `Cmd.WaitDelay` is
set, so held pipes cannot hang `Wait`. For `.cmd` and `.bat` launchers the code builds `SysProcAttr.CmdLine` and
refuses an argument it cannot quote, because `cmd.exe` does not follow `CommandLineToArgvW` (§17). Lookup follows
`exec.ErrDot`. Enforced: test `core/process_test.go` on three OS.
### 5.21 Git calls
Every git call of the engine and `repocheck` goes through `core.Git` (DR-10) with `-c core.fsmonitor=false
-c core.hooksPath=<empty owned dir> -c protocol.ext.allow=never -c submodule.recurse=false`, and `--no-ext-diff
--no-textconv` on `diff`, `log` and `show` (§17). The `file` protocol serves only the engine's own clone. A hook or
`diff.external` written into a clone's `.git/config` cannot make the engine run it (test `core/git_test.go`).
## 6. Architecture
### 6.1 Modules and packages
Private packages sit under `internal/<m>/internal/`. Edges live only in `core/modules.go`; `repo/boundaries` compares
them with `go list -deps -json ./...` (DR-17).

| Package | Responsibility | May import |
|---|---|---|
| `internal/core` | Commands, results, params, output, env, config, log, text, roots, capacity, processes, git, owned removal, sweep, footprint, measured values, sandbox port. | — |
| `internal/gates` | Gate runner, check cache, diff checks D1–D18, payload scan. | core |
| `internal/tracker` | Tracker port, GitHub adapter over HTTPS (L3), intake, issue parsing. | core |
| `internal/adapters` | Provider and harness adapters, adapter list, model discovery, catalogue reader, API agent loop (O2). | core |
| `internal/roles` | Embedded role cards and role fit data, card schema, prompt assembly. | core |
| `internal/ledger` | Append-only records, rotation, metrics. | core |
| `internal/engine` | Flows (§7), isolation, claims, model choice and routing, synthesis. | all above |
| `internal/stacks` | Stack profiles, detection, gate derivation (after the skeleton). | core |
| `internal/mcp` | MCP server from the command registry (M-Usable, O10). | core, ledger |
| `internal/repocheck` | This repository's own checks; never shipped. | core |
| `internal/e2e` | Smoke tests (tag `smoke`) that drive the built binary; never shipped. | core |
| `cmd/aiwe` | CLI and the `mcp` subcommand; wires the registry. | root packages, except `repocheck` |
| `internal/cmd/repocheck` | Entry point of `repocheck`. | core, repocheck |
### 6.2 Runtime and distribution
Decision (owner, 2026-10-10): Go, one statically linked binary for windows, darwin and linux on amd64 and arm64.
Basis (§17): the Go binary is the smallest, answers MCP `initialize` fastest, idles with the least memory and
cross-compiles without an extra download. Rejected: Node single executable, Bun, a Node runtime through `npx` (breaks
DR-4). GoReleaser builds with `CGO_ENABLED=0` and `-trimpath`; the version comes from the tag only. No source file is
architecture-specific (`repo/source-rules`). Channels: GitHub Releases (six archives, `checksums.txt`, `checksums.txt.sig`,
one attestation; a browser or `curl` is enough); the MCP Registry as package type `mcpb` (§17); winget (§17); Scoop
(owner decision); a Homebrew tap as a cask, since formulas are deprecated there (§17).

git is the only prerequisite. At start the binary runs `git --version`: missing → `git_missing`; older than the oldest
version that passes the git tests → `git_too_old`; both exit 1 with the next action. Reason: go-git has no rebase and
no patch apply (§17). The tracker needs no extra program: gh is optional (§10.1, L3). Harnesses are optional sources;
development tools (§12.2) are never user prerequisites. The MCP layer is the official Go SDK (§17, DR-21).
### 6.3 MCP and CLI
- The primary interface is an MCP server (`aiwe mcp`, stdio only) that the user's harness session starts; the session's
  lead agent organizes work through its tools: plan, work start, stop and status, models, sources, issue, report
  (O10). One registry feeds MCP and CLI (DR-16). The skeleton is driven through the CLI (L3).
- MCP server start = session start = the model assignment refresh point (§8.1).
- Long work runs in a detached **work session**: an engine process that outlives the harness session. Starting it
  returns a work session id. MCP tools and `aiwe runs list` query it. `aiwe runs stop --run <id>` stops one run's tree, records
  `stopped_by_owner`, requeues the unit and charges nothing.
- Self-update (L14), from `v0.1.0`, only for a binary installed from a GitHub Release. The binary decides its install
  source from its own path, matched against per-OS data rows of package-manager directories (Scoop, WinGet,
  Homebrew). A package-manager install prints the manager's update command instead. A Release install updates at MCP
  start, at most once a day, with one checker at a time (a lease, §6.6). The checker downloads the latest release and
  verifies the Ed25519 signature `checksums.txt.sig` of `checksums.txt` with the public key embedded in the binary
  (`crypto/ed25519`), checks the archive digest and switches on the next start. Before `v0.1.0`: build from source.
- `aiwe version` prints the injected version without the `v` prefix (§17), else `unknown`. A command that targets a
  repository without a stored gate runs onboarding first (§7.1).
- MCP surface: `aiwe mcp snippet` prints a config entry that calls the binary by name; `instructions` (≤ 40 lines,
  with the resolved roots), `prompts` (`plan-goal`, `open-unit`, `audit-unit`), `resources` (`issue-template`,
  `roles/<id>`, `projects/<repo-id>/gate`), `tools` (every non-terminal command).
### 6.4 Disk layout
`core/platform.go` resolves three roots at run time; first hit wins (DR-3).

| Root | Holds | Windows | macOS | Linux and other POSIX | Override |
|---|---|---|---|---|---|
| state | `sources.json`, `sources.state.json`, `models.cache.json`, `assignments.json`, `ledger.jsonl` and rotations, `projects/<repo-id>/`, lease files (§6.6) | `%LOCALAPPDATA%\aiwe\state` | `~/Library/Application Support/aiwe` | `$XDG_STATE_HOME/aiwe`, else `~/.local/state/aiwe` | `AIWE_HOME` |
| cache | bare clones of repositories onboarded by URL | `%LOCALAPPDATA%\aiwe\cache` | `~/Library/Caches/aiwe` | `$XDG_CACHE_HOME/aiwe`, else `~/.cache/aiwe` | `AIWE_CACHE` |
| temp | run clones, scratch, child result files | `%TEMP%\aiwe` | `$TMPDIR/aiwe` | `$TMPDIR/aiwe`, else `/tmp/aiwe` | `AIWE_TMP` |

- Env names derive from the product constant. `repo-id` = normalised remote `host/owner/repo`, never a local path.
  Stored paths are relative and `/`-separated.
- Ceilings (default), enforced by the sweep: state ≤ 64 MB and ≤ 1 000 files; cache ≤ 2 GB and ≤ 2 000 files; temp: 0
  directories without a live owner. Free disk before a clone ≥ 2 × the last measured clone size, else `no_disk`.
  Cleanup: ledger rotation, then bare clones least recently used first; still over → `footprint_exceeded`.
- OS rules: short temp names (`<temp>/r/<pid>-<time>`) and git's long-path option on Windows; file sets compare exact
  case (paths differing only by case → `invalid`); clones set `core.autocrlf=false`; removal retries 5 times, 200 ms
  apart (default), then `residue_locked` for the next sweep.
### 6.5 Run isolation
Serves DR-10 and DR-11.
- **Canonical repository**: the onboarded local path, or a bare clone in the cache root; a fetch writes only
  remote-tracking refs and objects, never the user's checkout.
- **Clone**: `git clone --depth 1 file://<canonical>` into `core.CreateOwned` at the base commit; no worktree. `TEMP`,
  `TMP`, `TMPDIR` and `HOME` point into the run's scratch directory.
- **Restore**: the stack profile's restore command (§8.2) runs inside the clone with the package manager's own
  user-level cache; no junction, hardlink or shared dependency directory.
- **Environment**: the parent environment minus secret-shaped variables (every `credentialEnv`, tracker tokens, names
  containing `TOKEN`, `SECRET`, `KEY` or `PASSWORD`, names ending `_PAT` or `_PASS`, `NPM_CONFIG__AUTH`,
  `GOOGLE_APPLICATION_CREDENTIALS`, and a `_URL` value with `:` after `://` and `@` after that `:`; case-blind) and
  minus the shell's identity variables. The adapter adds back only its own source's key. The harness starts with
  user-level rules, memory, hooks and plugins off through its documented flag or config-dir variable, which keeps its
  login. aiwe never copies or links a login file. `GIT_CONFIG_NOSYSTEM=1`, an empty env-set `credential.helper`,
  `GIT_TERMINAL_PROMPT=0` and `GIT_CONFIG_GLOBAL` at the null device cut git from user settings and helpers (§17).
  Every check process, the restore command included, gets the same environment.
- **Collect**: fetch the run branch into the canonical repository as `refs/aiwe/runs/<run-id>`, record residue
  (uncommitted paths, diff stat), remove the clone; path lists are read from git output, never passed as argv.
- **Sweeper**: at start and after every run, collect each dead run's clone, then remove temp directories whose owner
  process is dead (§5.15).
- Measured (§17): shallow `file://` clone 1.27–2.42 s and 7.9 MB; removal 0.35–0.74 s. Rejected: worktrees (shared
  refs, config and hooks); `--shared` (breaks when the source prunes).
### 6.6 Concurrent sessions (O11)
5 to 10 or more sessions work at once on the same or different projects without conflict, and with no daemon.
- **Per project**: gate, stack, unit queue, the one work session, metrics and state under `<state>/projects/<repo-id>/`.
  `models.json` there holds optional pins and exclusions for this project, in the shape of `overrides` (§8.1). An MCP
  session binds to the project of its working directory and may target another by parameter.
- **Machine-wide**: sources and their quotas, the catalogue and the model assignment, CPU capacity.
- **Coordination**: the project lock `work.lock` allows one work session per project; a second MCP session attaches to it;
  units are claimed atomically on the remote, which prevents double work across machines; per-source run slots and CPU
  admission slots are lease files in the state root, released on exit and reclaimed from dead owners; ledger appends
  run under a lock; `assignments.json` is written atomically; one refresher at a time reads catalogue and discovery.
### 6.7 Sandbox adapter
`Sandbox{Level(); Wrap(ctx, ChildSpec)}` with three levels (Q19): `env-scrub` (only §6.5; the agent runs with the
user's rights) · `harness-sandbox` (the harness's own documented sandbox, switched on by the adapter) · `os-sandbox`
(writes outside the clone, network beyond policy and reach into other processes blocked by the OS without elevated
rights). The skeleton ships `env-scrub`; every run record names its level; stronger levels: §15. An external tool may
implement the adapter if its level holds on three OS and the contract tests prove it. Open risk until then: an agent
can read and change what the user can; README and SECURITY say so (§12.1).
## 7. Core flows
`[M]` mechanical · `[J]` agent judgement · `[O]` owner.
### 7.1 Onboarding — automatic, or `aiwe onboard --repo <path-or-url>`
1. `[M]` Resolve `repo-id`, canonical repository, working branch (remote default or `--branch`) and CI presence.
2. `[M]` A project `gate.json` → use it (DR-4) and go to step 5.
3. `[M]` Detect stacks (§8.2): `stack_detected`, `multi_stack` (§15), `unknown_stack` (§7.7) or `detect_error`.
4. `[M]` Derive gate steps whose tool exists and whose config is present.
5. `[M]` Prove the gate on the base commit in a clean clone: each step `green`, `red_at_base` (stored `known_red`,
   reported, not blocking) or `not_measured`, with a measured duration.
6. `[M]` Store gate and proof under `projects/<repo-id>/` with the input hash; an unchanged rerun is `no_change`.
### 7.2 Planning — `aiwe plan --repo R --goal <issue>`, or the user's own session
Both paths use the `issue-template` and the §8.4 parser. **Architect path**: `[J]` an architect run returns unit texts
· `[M]` each is parsed; a failure returns once · `[J]` an auditor run (DR-5) audits the split against the goal's
Problem and Done criteria and the file-set overlaps · `[M]` a second failure stops with `plan_invalid` and an
`owner-action` issue · `[M]` valid units become sub-issues with native blocked-by links (L8); an unchanged rerun is
`no_change`. **Session path**: the user's session writes each unit from the template and runs `aiwe unit check`;
intake parses it again.
### 7.3 Unit lifecycle — `aiwe work --repo R [--unit N | --goal G]`, skeleton path (M-Skeleton)
| Step | Kind | What happens |
|---|---|---|
| 1 intake | M | Read the issue through the tracker port and the intake filter (DR-20); parse it (§8.4). Skip `owner-action` and blocked issues. |
| 2 claim | M | One open run per unit: a local lock in the skeleton, an atomic remote ref `refs/work/claim/<issue>` later (§6.6). The base commit is the head of the remote working branch at claim. Units with intersecting file sets never run together. |
| 3 isolate | M | Make the run clone (§6.5); failure → `isolation_failed`. |
| 4 route | M | Resolve roles (§9.1); take each role's first available entry of the session assignment (§8.1). |
| 5 write | J | A behaviour unit's test comes first, as its own commit: test-author writes it for a risky unit (L17), the engineer for any other unit. The engine runs the unit verify on that test commit over the base; it must be red (red proof). Then the engineer or doc-writer writes the change on the run branch, with the role prompt (§9.2). |
| 6 verify | M | The engine runs the unit verify once per key when the agent asks through `aiwe verify` and returns red output to it. |
| 7 collect | M | Run branch to a run ref; residue recorded; clone removed. |
| 8 scope check | M | `<base>...<tip>` against the file set (DR-9). |
| 9 audit | J | An auditor on a separate source (DR-5) compares the done criteria with the diff using engine evidence only. Every `path:line` and identifier a RED verdict cites must resolve on the tip, else `unverified_finding` and a new auditor run. |
| 10 fix loop | J | Returned or red → an engineer fix round, at most 2 (§7.4), then `quarantined`. A second red moves the next run up the assignment (§8.1 point 9). |
| 11 integrate | M | Push the run branch; open one pull request; wait for its CI (red → a fix round, step 10); squash-merge only while head = `AuditedTip` (DR-14). Title: `<type>(<scope>): ` of the first non-test commit, then the issue title with a lower-case first letter; the whole title is cut to 50 characters. Message per §12.3: `Effect: <before> → <after>`, `Closes <owner>/<repo>#N`, the same for the goal when this is its last open sub-issue, and `Run-Id` (L7). A breaking marker waits for the owner (§12.3). |
| 12 main CI | M | Wait for the squash commit's `main` CI. Red → a revert pull request, merged once its CI is green; the unit issue reopens and requeues. Red revert CI → `revert_red`; no run in time → `ci_not_observed`. A milestone whose issues are all closed is closed. |
| 13 closeout | M | Append run and check records (§8.5); delete the claim ref and the remote run branch. |

After the skeleton, these join (§13): an **already-done** check before step 3 (base verify; ambiguous → scout); the
stored **gate** on a clean clone of the tip after step 8, reusing recorded keys; **D-checks** and **mutation** before
the audit (§11); **security-reviewer** and **ux-reviewer** beside the auditor (a `not_measured` UI review does not
block, Q10); **batches** of unit pull requests gated together on the working-branch tip, a red batch split in halves
until each red pull request is isolated (a removed one stays open and returns as a fix round, Q21; a second refusal
because `main` moved → `push_rejected`); the **debugger** on a repeated red; the **ladder** (§7.7).
### 7.4 Failure paths
Red signature = step id + sorted failing test ids + exit code + first error line, with digits, hashes and temp paths
normalised.

| Event | Outcome | Engine action |
|---|---|---|
| Two or more stacks; no known stack; detection fails | `multi_stack`, `unknown_stack`, `detect_error` | Stop (§15); ladder (§7.7); red. |
| No adapter can run ladder rung 2 | `artefact_requested` | The owner answers with `aiwe artefact submit`. |
| Unit text invalid; no role resolves; prompt over its cap | `unit_invalid`, `role_unresolved`, `prompt_too_large` | Label `invalid`; findings in the ledger and `aiwe unit check`. |
| Product decision; plan fails parse or audit twice; first 5 units of a goal (default) have M1 < 70 %; revert pull request CI red | `owner_decision`, `plan_invalid`, `goal_paused`, `revert_red` | Stop the unit, goal or queue; an `owner-action` issue that blocks it; ledger `owner_action`. |
| Gate or verify red, agent fault; zero tests collected | `red`, `no_tests_collected` | Fix round. |
| Same red signature in two consecutive rounds | `repeated_red` | A debugger run; repeated again → stop, label `help wanted`, `owner-action` issue. |
| Red from the environment (tool, network, timeout) | `environment` | Retry once in a new clone with a `rerunReason`; still red → `not_measured`; not charged. |
| Isolation failed or disk short | `isolation_failed`, `no_disk` | Not started; queued; a second failure → `environment`. |
| Over the run limit, or silent past the silence limit | `timed_out`, `stalled` | Stop the tree; kill evidence (§7.8); an attempt. |
| Observed model differs; account restricted | `model_mismatch`, `account_restricted` | Not charged; next candidate (§8.1); a restricted source is set `restricted` (§7.6) with an `owner-action` issue. |
| No candidate for a role | `no_capacity` | The unit waits; other roles continue. |
| None of `GH_TOKEN`, `GITHUB_TOKEN` and `gh auth token` yields a token | `tracker_unauthenticated` | No dispatch; exit 1 with the next action. |
| Conflict at integration | `conflict` | A resolution run on the rebased run branch; two failures → `skipped`, requeued (Q11). |
| Three failures on one unit; no CI run in the wait limit | `quarantined`, `ci_not_observed` | Not offered again, label `help wanted`; never green. |
| Run process lost | `lost` | Requeued; the record closes with the time since `StartedAt` as an upper bound, never 0 ms. |
| Ledger not writable; footprint over after cleanup | `ledger_unavailable`, `footprint_exceeded` | No dispatch. |
| Unanswered harness permission request; owner stops a run | `permission_blocked`, `stopped_by_owner` | Stopped, requeued, not charged; an `owner-action` issue for the first. |
| API reply cut, or over its token limit | `answer_truncated`, `token_limit` | An attempt, never a finished run. |
| Tip differs from `AuditedTip`; `VERDICT:` in a commit; RED cites what does not resolve | `unaudited_commits`, `verdict_in_branch`, `unverified_finding` | Refused; new auditor run for the last. |
| A check red, then green on rerun | `flaky` | An `owner-action` issue blocks the unit; closing it clears the flag (L15). |
| Breaking commit | `breaking_change` | Not merged; `owner-action` issue; `aiwe unit approve-breaking` (§12.3). |
| Unsafe root; failed removal check; locked file | `root_unsafe`, `not_owned`, `residue_locked` | Nothing removed (§5.15); next sweep retries a lock. |
| Invalid UTF-8; terminal-only command without a terminal; git missing or too old | `invalid_utf8`, `not_a_terminal`, `git_missing`, `git_too_old` | Refused or exit 1 with the next action; `invalid_utf8` names the first invalid byte. |

**Limits from the ledger** (DR-1), each printed with its basis; defaults hold until the sample exists:
- Run limit per role, source and model: max(30 min, 1.5 × p90 of its green durations) once 20 greens exist; before
  that the role's p90 over all sources once it has 20; else 60 min.
- Silence limit per source: max(5 min, 2 × p99 of the longest output gap in its greens) once 20 exist; else 10 min.
  Time inside `aiwe verify` counts as output.
- Fix rounds per role: 2 until 50 merged units; then the smallest r for which round r + 1 added under 10 % of the
  role's greens over its last 50 units, minimum 1.
- Token limit per API run: 2 × the largest input token count among its greens once 20 exist; none before.
### 7.5 Resume
State lives in refs, the tracker and the ledger. After a crash `work` marks open runs `lost`, releases claims with no
live run, and collects then removes temp directories of dead owners. A run ref goes once its unit's issue is closed.
### 7.6 Quota exhaustion and account restriction
The adapter classifies an error from structured fields, never from message text alone (a text-only harness gets a
per-adapter rule under its contract suite). A quota class sets the source `passive` with its reset time;
`account_restricted` sets it `restricted`, never probed or routed until the owner runs the terminal-only
`aiwe sources enable <id>`. The run requeues without an attempt and takes the next candidate (§8.1), never a second
account of the same provider (O7). A provider balance endpoint is read with the source's own key at route time and
kept 300 s (default); a zero balance sets `balance`; no endpoint → `unknown`. Probe interval P = 30 min (default).

| Class | Detected by (§17) | Passive until | Probe |
|---|---|---|---|
| `rate_window` | HTTP 429 with `retry-after` or a reset header | the stated time | none |
| `provider_spend_limit` | a spend-limit error code | the stated reset, else `unknown` | every P when unknown |
| `balance` | HTTP 402 or a balance error with a limit source | a successful probe | every P |
| `subscription_window` | the harness's usage-limit result | `unknown` | every P |
| `account_restricted` | HTTP 401 or 403, or a documented suspension code | the owner enables it | never: a probe could deepen a ban (§17) |

A single text match never sets `restricted`. An unclassified error is `unknown_error`, an environment fault.
### 7.7 Adaptation ladder (DR-2, after the skeleton)
For gate steps, unknown-stack profiles, adapter config, quota rules and unit decomposition: a stored artefact with the
same input hash is reused · rung 1: a mechanical derivation is proved and stored · rung 2: a synthesis run (architect
or engineer), verified by an auditor run and proved on the base commit or by an adapter dry run, stored as
`{artefact, inputHash, producerRunId, verifierRunId, evidence}`; no adapter → `artefact_requested` and the owner's
terminal-only `aiwe artefact submit`, then the same proof · rung 3: a product decision → an `owner-action` issue.
### 7.8 Check execution (DR-8)
- **Key** (`gates.CheckKey`, sha256): argv + digest of the step's declared inputs from git objects (default the whole
  tracked tree) + env fingerprint (OS, architecture, version output of the first token, lock file digests).
- **Rerun** of a key: only with a recorded `rerunReason`, once, after an environment-class failure. A check red, then
  green on rerun is `flaky` (§7.4, L15).
- **Time limit** per step: max(60 s, 3 × p90 of its last 20 green durations), from the base proof until then (default).
- **Kill evidence**: the running step and, where reported, its test files or packages; machine load; the process
  table. Output streams to the evidence file during the run.
- **Partial run**: `gates gate --step a,b` prints "partial", never "gate green" (DR-6).
- **Zero tests**: a verify whose runner reports zero tests is `no_tests_collected` at any exit code; the profile names
  the signal (`NoTests`); Go prints `[no tests to run]` and exits 0 (§17).
- **Early diff checks** (after the skeleton): each `aiwe verify` also runs D1, D3, D4, D7, D10, D11 and D16.
- **Roots**: each check runs with all three roots in one fresh owned directory; an entry left there is red.
- When the last recorded verify's tree digest equals the final tree, it is the evidence. The
  full gate runs once per batch and in CI, only for changed keys. Adapters add a harness command restriction where
  the harness supports one. Concurrent gates follow DR-3; slot waits are recorded apart.
## 8. Data contracts
Every engine file carries `formatVersion`; parsers are fail-closed (DR-6); JSON keys are `camelCase`.
### 8.1 Sources and model choice — `<state>/sources.json`
```json
{ "formatVersion": 1,
  "sources": [
    { "id": "sub-a", "adapter": "claude-code", "kind": "subscription", "provider": "<provider-id>" },
    { "id": "api-b", "adapter": "openai-chat", "kind": "api", "provider": "<provider-id>",
      "credentialEnv": "B_API_KEY", "baseUrlEnv": "B_BASE_URL", "spendCap": { "usd": 50, "since": "<date>" } } ],
  "overrides": { "pin": { "engineer": [ { "source": "sub-a", "model": "<model-id>", "effort": "<level>" } ] },
                 "exclude": [ { "source": "api-b", "model": "<model-id>" } ], "tier": { "auditor": "top" } } }
```
- Row fields: `id`, `adapter`, `kind` (`subscription` or `api`), `provider` (its data file, §10.2), `models` (optional;
  limits the source's candidates), `credentialEnv`, `baseUrlEnv`, `maxConcurrent`, `spendCap`, `price`. A second
  `subscription` row with the same `provider` is refused (O7); API rows are unlimited.
- `credentialEnv` may not name `GH_TOKEN`, `GITHUB_TOKEN` or another tracker token. An `api` source needs a base URL
  (`baseUrlEnv`, else the provider data `baseUrl`).
- **Spend cap** `{usd, since}`: the ledger's `CostUSD` since `since` against `usd`; at the cap → `capped`. A cap on a
  source whose cost is always `unknown` is refused; any `unknown` cost since `since` reads `not_measured` and caps
  the source. Runs in flight may pass it.
- **Concurrent-run ceiling** (DR-3, Q13): starts at 1, +1 after 10 consecutive clean runs (default) up to
  `maxConcurrent`, −1 (minimum 1) on a `rate_window` or concurrency error.
- `price {inputPerM, cachedInputPerM, outputPerM, currency, url, checkedOn}`: used for 30 days (default), else the
  catalogue price, else `unknown` (`no_price`).
- Runtime state `sources.state.json`: `active | passive | restricted | capped`, quota class, `resetAt` or `unknown`,
  `passiveSince`, `lastProbe`, `ceiling`, `cleanRuns`, `balance`, and each source's discovered models with the read
  date. `aiwe sources show` prints them (`unknown` when unread).
- `aiwe sources discover` writes `sources.json` rows from detected harness logins (`Detect`, §10.2) and API keys
  present in the environment (provider data `keyEnv`); the owner may exclude any row.

**Model choice** (O9) is automatic and follows fixed rules: the same inputs give the same choice. The owner keeps no
list; `overrides` are optional. A **candidate** is (source, model, effort), because effort changes quality and cost as
much as the model does.

**Assignment per session**: aiwe refreshes discovery and the catalogue at each session start: MCP server start (O10)
or `aiwe work` start. A long-lived engine refreshes at most once a day. It computes an ordered candidate list per role.
It prints the difference from the saved `assignments.json` and saves the new list as the next session's default. Every
run of a role takes the first available entry of its list. A quota, ceiling or cap moves to the next entry (O3).
Escalation moves up the list. No run recomputes the list.
1. **Discovery**: each source reports the models it offers now (API `GET /models`; a harness's model listing where one
   exists; else its provider data), refreshed by `aiwe sources discover` and at most daily, cached with the read date.
   An unoffered model is never a candidate. The effort levels a source accepts per model come from its provider data
   and harness capabilities (a CLI effort flag, an API reasoning parameter); a model without effort control has one.
2. **Identity**: one canonical model id per model; each source keeps its wire id as an alias. A catalogue row that
   measures one effort level (a `(high)` qualifier, for example) maps to that (model, effort) only; a level without a
   row has an `unknown` prior, never a neighbour's figure. One normalisation matches names; an ambiguous or unmatched
   name stays unmatched, with scores `unknown`.
3. **Public prior**: the Artificial Analysis catalogue, read at most once a day with the key in
   `ARTIFICIAL_ANALYSIS_API_KEY` and cached in `models.cache.json` with its read date: coding index, intelligence
   index, price per M input and output tokens, cost per task, output speed. Every output that uses it prints "Data:
   Artificial Analysis, https://artificialanalysis.ai/". A failed read or an age over one day is `unknown`, never
   served as current. `artificialanalysis.ai` is an allowed outbound host of the contract suite (DR-19).
4. **Own evidence**: the ledger per (role, canonical model, effort, adapter): runs, first-try green rate, cost per green unit,
   median duration; for judging roles also verdicts later contradicted by an escaped defect (M3).
5. **Candidates**: discovered models on `active` sources below their ceiling and spend cap, minus `exclude`, minus
   models that miss a required capability. A judging run excludes the writer's provider when another provider has a
   candidate; otherwise it takes the same provider's other model, then a new instance (L13). One source still works
   (Q6); the run records the level it got.
6. **Role fit**: shipped data per role (`roles/`) names the measured fields its quality uses and their weights
   (writing roles: coding index first; planning: intelligence index first; judging: both), its required capabilities
   (tool use, context window) and a floor score. A writing role never takes a candidate below its floor.
7. **Quality**: the candidate's own first-try green rate once it has ≥ 10 runs in that role (default); before that its
   role-fit score from the public prior. **Tiers** are mechanical (L16): `base` = the cheapest candidate above the
   role floor; `raised` = the upper half by role-fit score; `top` = the best score. A role starts at its card tier
   (§9.1); a risky unit raises it one step. Tiers filter the session list: entries below the tier floor are skipped.
8. **Objective**: writing roles take the lowest expected cost per green unit (run cost ÷ quality; an active
   subscription counts as prepaid and ranks before a metered API at equal quality); judging and planning roles take
   the highest quality, with cost breaking ties.
9. **Escalation**: a second red on one unit moves the next run up the list: first a higher effort of the same model
   where the list has one, then the next model; never the same (model, effort) again.
10. **New and demoted models**: a newly reported model competes at the next session start on its public prior; a
    demoted model returns when its catalogue entry changes or 30 days pass. No random exploration.
11. **Owner overrides**: `pin` (always first; a pin wins), `exclude`, `tier` per role. The skeleton runs on pins.
12. **Explanation**: `aiwe models` prints the current assignment per role, its inputs and every rejected candidate with
    its reason; `aiwe models --refresh` recomputes it outside a session; each run record stores the assignment version
    it used. Without catalogue or evidence the order is pins, then own evidence, then source order, and the output
    names the unknown inputs.
### 8.2 Stack profiles — `stacks/profiles/<stack>.json`
One embedded file per stack: `ID, Detect, Restore, Gate, TestFiles, SourceExt, LockFiles, TestCase, Assertion,
NoTests, Mutation, Complexity, Registry, SecurityPaths, UIPaths, UIRunner`. The skeleton ships the Go row: detect
`go.mod`, restore `go mod download`, gate `go vet ./...`, `go test ./...`, `gofmt -l .` (empty output), test files
`*_test.go`, lock file `go.sum`, complexity tool `gocognit`. Other stacks come with M-Trust (§13). A stack missing
cells, or an unknown stack, reads them `not_measured` and takes the ladder (§7.7); every derived command is proved per
repository before use (§7.1 step 5). Role path tables (default, DR-15): documents `*.md`, `*.mdx`, `*.rst`, `*.adoc`,
`*.txt`, `docs/**`; security: a path segment containing `auth`, `login`, `session`, `token`, `secret`, `credential`,
`password`, `crypto`, `permission`, `oauth` or `webhook`, a D16 hit, or a stack's input entry point (Go
`*handler*.go`); UI: the stack's UI patterns and `UIRunner`, the project's own browser tool found by its config file
(none → `not_measured`).
### 8.3 Stored gate — `projects/<repo-id>/gate.json` (a project `gate.json` uses the same format)
```json
{ "formatVersion": 1, "origin": "derived", "inputHash": "<sha256>",
  "steps": [ { "id": "test", "command": ["go", "test", "./..."], "required": true, "inputs": ["**"], "timeoutSeconds": 124, "expect": { "exitCode": 0 } } ],
  "proof": { "baseCommit": "<sha>", "steps": [ { "id": "test", "result": "green", "exitCode": 0, "durationMs": 41230 } ] } }
```
`command` is argv, never a shell string. `origin`: `project | derived | synthesized`. `expect` also allows
`outputMatches` and `outputRejects`. A project `gate.json` omits `origin`, `inputHash` and `proof`.
### 8.4 The issue standard
Every goal, unit, finding and owner action is an issue in this standard (O5, DR-20). The `issue-template` resource,
`aiwe unit check`, `aiwe issue` (O8), the intake parser and the architect card follow this section. Headings and the
`Severity:`/`Affected:` keys are fixed English markers; other text may be in any language.

**Title**: ≤ 72 characters; no id, phase, type or other prefix. **Body**, in this order, nothing else:

| Section | Rule |
|---|---|
| `## Problem` | First line `Severity: <critical\|high\|medium\|low> · Affected: <customer, owner, admin, developer, system>` (one or more). Then what is wrong or missing and why it matters. |
| `## Scope` | An `In:` list and an `Out:` list. A unit that changes behaviour on purpose adds a `Behaviour change:` line: what changes and what must stay the same (DR-12). |
| `## To do` | A plain list, no checkboxes, the plan and not the status: `task — verify: <check> → <signal>`. |
| `## Verification` | For a unit: one or more lines `` `<command>` → <expected signal> ``. |
| `## Done criteria` | The observable end state, one item per line. |
| `## Links` | Related issues, pull requests and sources. |

How aiwe reads it:
- **Goal or unit**: an issue with native sub-issues is a goal (§17). An issue with a file set and a Verification line
  is a unit. Nothing else is dispatched.
- **File set** (DR-7): each backticked repository path in `Scope` `In:`; `(new)` marks a file the unit creates. Plain
  paths must exist; `(new)` paths must not. A tracked test file outside the file set that contains a backticked
  identifier of `To do` → `scope_outside_files`.
- **Verify**: each Verification command splits into argv by shell-style quoting and runs without a shell; a shell
  operator outside quotes (`&&`, `\|\|`, `\|`, `;`, `>`, `<`, backtick, `$(`) or a `HEAD~<n>`/`HEAD^` base is refused;
  the first token must resolve. It is checked, never run, before review.
- **Test items**: To do lines whose check runs or names a test file of the file set. A **behaviour unit** (a file set
  path with a `SourceExt` of its stack) has at least one; its test commit is red on base and green on the tip (§7.3 step 5).
- **Dependencies**: native blocked-by links (§17); a unit waits while a blocker is open (L8).
- **Owner work**: label `owner-action`, never dispatched. A stop for the owner opens an `owner-action` issue in this
  standard that blocks the unit: Problem holds the decision with the recommendation first, To do holds each option
  and its consequence, Links holds engine evidence (gate tail, diff stat, Run-Id). Text from a run's `decision:` lines
  passes intake, masking and the payload scan first.
- **Built-in elements only** (DR-20): type via GraphQL `repository.issueTypes` (Q4): null → label `bug`,
  `enhancement` or `documentation`; else type `Bug`, `Feature` or `Task`. Labels `invalid` (refused text) and
  `help wanted` (quarantined). One milestone per roadmap milestone (§13). Close reasons `completed`, `not_planned`,
  `duplicate` (§17). No status label, no field, no comment: findings go into the body or a new issue; evidence goes
  into the commit and the pull request. Closing follows L7 (§7.3 step 11).
- **Intake filter** (DR-20): only the body by an author with write permission; HTML comments removed; bidirectional
  embedding, override or isolate controls → `invalid`. The text enters prompts only in `UNIT`.
### 8.5 Ledger — `<state>/ledger.jsonl`
Append-only JSONL, one schema with `kind`: `run`, `check` or `owner_action`. `core.Measured[T]{Value; Known}`
serialises as a number or `"unknown"` (DR-6). Every record: `formatVersion, kind, at, repoId, goal, unit, runId`
(`"none"` when absent).
- `run`: `role, source, adapter (harness or API wire format), model, modelReported, effort, tier, assignmentVersion, attempt, fixRounds, startedAt, finishedAt,
  outcome, gate (green|red|incomplete|not_measured), audit (approved|returned|none), tokensIn, tokensOut,
  tokensCachedIn, longestGapMs, costUsd, unknownReason (no_price|no_usage_data|subscription), residue,
  auditIndependence (provider|model|instance|none), sandbox, tip, auditedTip, exit, stderrTail (40 lines, masked),
  claimedAt, mergedAt, ciGreenAt, mergeCommit, pullRequest`.
- `check`: `key, stepId, commit (or "uncommitted"), exitCode, killed, durationMs, tail (masked), signature,
  rerunReason, flaky`.
- Each run start gets exactly one end record, or `lost` at the next start; a `run` without a unit is refused; each fix
  round is a new run. At 16 MB (default) the file rotates to `ledger.<n>.jsonl.gz`; `metrics` reads rotations; the
  oldest goes first under the state ceiling. An unwritable ledger stops dispatch.
### 8.6 Run result envelope
A run's `Result` data (§5.3) is `RunReport{RunID, Unit, Role, Source, Adapter, Model, ModelReported, Outcome, Branch, Verdict
(GREEN|RED|none, parsed from the last message), Tip, Verify{Command, ExitCode (nil = not_run), Tail}, TokensIn,
TokensOut, CostUSD, DurationMS}`. The child writes it to its run directory; no file and no live process = `lost`. A
harness that reports only session totals converts them per run or reports `unknown`.
## 9. Roles and the standard agent contract
### 9.1 Role cards
A role exists only with its own kind of work and a gated boundary (DR-22). Skeleton: engineer, auditor, test-author.
Next: architect, security-reviewer, doc-writer. Later: scout, debugger, ux-reviewer. Operator: deferred with operation
units (O4).

**Resolution** (DR-15), from the unit's type and labels, its file set, its diff paths after collect and §8.2: `aiwe
plan` or a plan artefact → architect, auditor · another artefact → engineer, auditor · type `documentation` or only
document paths → doc-writer, auditor (a non-document path → `role_unresolved`) · any other unit → engineer, auditor,
with test-author first for a risky unit (L17) · a diff path in the security or UI table adds security-reviewer or
ux-reviewer · the same red signature twice adds debugger · an ambiguous already-done check runs scout. A **risky
unit** (L17) has Severity `critical` or `high`, or a file-set path that is in the security table (§8.2) or has a
segment containing `identity`, `authz`, `delete`, `purge`, `migrat`, `routing`, `router` or `matching` (default).

| Role | Purpose | May not (gate) | Tier |
|---|---|---|---|
| architect | Splits a goal into unit texts (§8.4). | write files (read-only clone); name a worker model or a missing plain path (parser) | raised |
| test-author | Writes the unit's tests before code, one commit. | write production code; add skipped, focused or empty tests (D1, D10, D11) | base |
| engineer | Implements the unit inside the file set. | touch the test-author's files or leave the file set (D1, DR-9); weaken tests, ignore errors, add TODOs, secrets or unregistered dependencies (D2, D4, D7, D9, D15, D16, D17) | base |
| doc-writer | Updates documents; creates one only when listed `(new)`. | write a non-document path (D1) | base |
| auditor | Judges work it did not write. | share a run or context with the writer, or use a less independent source (DR-5, D14); write `VERDICT:` into a branch commit (`verdict_in_branch`) | raised |
| security-reviewer | Attacks the change; proves each finding. | fix; run a reproduction itself (DR-8) | raised |
| ux-reviewer | Judges changed screens per width. | fix | base |
| scout | Answers "already done on base?". | write files | base |
| debugger | Proves the root cause of a repeated red. | change code; run an experiment itself (DR-8) | raised |

Method per role (the card body): **architect**: each unit has one problem, its scope, Done criteria, To do lines with
verify and dependencies; outcome items taken = items placed; omissions are named. **test-author**: tests check the
Done criteria through the production entry point and fail on base for the missing behaviour, never for an import,
syntax or fixture error. **engineer**: "red before" only from a recorded result; a structural change removes no check
(DR-12); a deliberate behaviour change goes under Scope deviation; three reds with no new cause → stop.
**doc-writer**: every fact is checked against code or a recorded result; an unverified line goes under Not done.
**auditor**: the writer's return is a claim; each Done criterion gets PASS or FAIL with one evidence line; it looks for
silent scope change, weakened assertion, removed check, compatibility shim, scratch file and fail-open reasoning; a
test that only matches source text proves nothing; it never fixes. **security-reviewer**: a one-line threat model; a
finding = location · reproduction the engine runs · result · severity; unreproduced → `not_measured`.
**ux-reviewer**: the project's UI rules (none → `not_measured`); widths 375, 768, 1440 and 1920 px (default); empty,
loading, error, filled and unauthorised states; targets ≥ 44 px; contrast, keyboard, focus. **scout**: facts carry
file and line. **debugger**: one hypothesis, one experiment through `aiwe verify` that changes one variable.

**Card format**: `id, purpose, trigger, needs, output, tier (the starting tier, §8.1 point 7), may, may_not[{text,
gate}]` and a body without shell commands; no harness, model, tool or path names; embedded and served as `roles/<id>`.
### 9.2 Prompt contract
Same sections for every harness, fixed English headings: `ROLE` (the card) · `UNIT` (the filtered body) · `SCOPE`
(repository, run branch, file set, line cap) · `RULES` (the project's commit convention, default Conventional Commits;
DR-12; contract limits override project rules, which override card defaults; user-level harness rules are off) ·
`VERIFY` (the exact verify and how to request it) · `EVIDENCE` (judging roles and debugger: recorded results, full
diff, diff stat, D-check and mutation findings) · `RETURN` (§9.3). ≤ 32 KB (default), else `prompt_too_large`.
### 9.3 Return format
Last message, ≤ 12 lines: **Done** (verify command, exit code, last lines) · **Not done + why** (may carry `decision:`
lines) · **Scope deviation**. No file listing. Done opens with `PLAN:`, `TESTS:`, `IMPLEMENTED:`, `UPDATED:`,
`ANSWER:`, `ROOT CAUSE:` or `VERDICT: GREEN|RED`; the architect's unit texts follow as a separate block. The text is
never evidence; the engine re-reads branch, diff and results.
### 9.4 What an agent may run
May: read, search, edit inside the file set, call `aiwe verify`, commit on the run branch. May not: run test, lint or
gate commands (DR-8); anything DR-13 reserves; publish a package; reach the network beyond the adapter's policy;
delete recursively (DR-11).
## 10. Interfaces
### 10.1 Tracker port (GitHub first)
```go
type Tracker interface {
    Identified
    ReadUnits(ctx context.Context, repo RepoID) ([]Unit, error)
    Write(ctx context.Context, op Op) error // create | edit | label | close | reopen | milestone | pr-create | pr-merge
    Permission(ctx context.Context, repo RepoID, login string) (Permission, error) // write | read | none
    CIStatus(ctx context.Context, repo RepoID, sha string) (CIStatus, error)       // pending | green | red | none
    Capabilities() TrackerCaps
}
```
- The adapter calls REST over HTTPS with the standard library; GraphQL only where REST lacks the call (L3). The token
  comes from `GH_TOKEN`, then `GITHUB_TOKEN`, then `gh auth token` when gh is installed; gh is never required. No token →
  `tracker_unauthenticated` (§7.4). The token is held in memory for the call only (DR-19). Least permissions (default): Contents, Issues and Pull requests
  read and write; Actions and Metadata read. `git push` uses git's own credential helper.
- Every write passes masking and the payload scan (§11).
- `pr-create` = `POST /repos/{owner}/{repo}/pulls`; `pr-merge` = `PUT .../pulls/{pull_number}/merge` with
  `merge_method: squash`, the §7.3 step 11 title and message, and `sha` = the audited head (§17).
- Sub-issues and blocked-by use GitHub's native relations (§17, L8). Volume stays under GitHub's limits (§17); a `403`
  or `429` limit follows `rate_window`. Only `tracker/` knows GitHub; `tracker/trackertest` holds the fake.
### 10.2 Provider port (harness and API)
```go
type Provider interface {
    Identified
    Detect(ctx context.Context, env core.Env) Detection // Present; Version, Reason
    Capabilities() Capabilities // Roles, Models, Effort; Resume, ReportsModel, RestrictsCommands, Images
    Run(ctx context.Context, req RunRequest) (core.Result[RunReport], error)
}
```
- Two kinds: CLI harness (a process in the run clone) and API channel (an engine agent loop over HTTPS with file tools
  confined to the clone, O2). Each uses only the provider's official interface (DR-19). `Capabilities().Models` is
  the source's discovered model list (§8.1).
- `Detect` checks PATH, then the documented per-user install location: `harness_missing`, `harness_incompatible`,
  `harness_unreadable`. A harness runs non-interactively under a written permission policy (`permission_blocked`,
  never a wait). A session resumes only in its own directory (`session_directory_mismatch`).
- API channel: streams `data:` lines when allowed (each chunk resets the silence limit); sends and carries the
  provider's documented reasoning fields; a cut reply → `answer_truncated`. File tools: line-window read, search,
  single exact-match edit, the profile's syntax check after each edit.
- **Provider data** `adapters/providers/<provider>.json` (embedded, fail-closed): `id`, `adapter`, `baseUrl` (https),
  `models` (fallback list when discovery has none), `reasoning {request, carry}`, `errors [{status, code, class}]`,
  `balance {path, field, currency}`, `keyEnv` (the API key's usual variable name), and for a harness `cli {binary, flags, loginEnv, limitText}`. A new compatible API
  or known harness is one file and one row (DR-3); each value cites a §17 fact or the issue that measured it.
- Skeleton: `claude-code` (L3). M-Usable (§13): `opencode`, `cursor`, `codex` and `gemini` (O12), and `openai-chat`
  with one provider data file per API provider.
## 11. Quality system applied to managed projects
Mechanical: stack detection, gate derivation and base proof (§7.1); intake and issue parse (§8.4); tests red on base
before code for every behaviour unit (§7.3 step 5); unit verify once per key (§7.8); scope check (DR-9); payload scan before text leaves the
machine; CI after merge and revert on red (§7.3); red signature and goal pause (§7.4). After
the skeleton, on `<base>...<tip>` lines only: D1 forbidden paths per role · D2 test weakening · D3 suppression growth
· D4 silent catch or ignored error · D5 untested code · D6 size · D7 TODO markers · D8 git hygiene · D9 assertion count
· D10 disabled or focused tests · D11 empty tests · D12 vacuous mocks (flag) · D13 test literals copied into
production lines (flag; never a unit-text identifier) · D14 separation of powers · D15 test count · D16 secrets · D17
unregistered or deprecated dependency · D18 changed function over complexity 12 (Q12); mutation on changed lines with
a 300 s budget (default), tool missing → `not_measured`. Judgement (§9.1): done criteria vs diff, D12 and D13 flags,
mutation survivors, security, UI and root cause.
## 12. Open source, versioning and release
### 12.1 Repository files
`LICENSE` (MIT, Q1) · `README.md` · `CONTRIBUTING.md` · `SECURITY.md` (private vulnerability reporting once public,
L9; the risk of §6.7) · `CODE_OF_CONDUCT.md` (a link to Contributor Covenant 3.0, §17; conduct reports go to the
maintainer on GitHub) · `go.mod`, `go.sum` · `tools/go.mod`, `tools/go.sum` (L4) · `gate.json` · `.golangci.yml`,
`.custom-gcl.yml` · `.goreleaser.yaml` · `.githooks/commit-msg` · `.gitattributes` (LF, DR-18) · `.gitignore`
(`/dist/`) · `.github/dependabot.yml` (`gomod`, `github-actions`) · `.github/workflows/ci.yml` (the only workflow). No
issue form or pull-request template (DR-16); no `CHANGELOG.md`.

`README.md`, in order: the first paragraph of §1.1 verbatim; **Features** (small clean context per
agent, nothing left on disk, every run measured, every unit validated before work); **Supported providers**
(`aiwe adapters list`, plus DR-19 in one paragraph); **Risk** (§6.7); install, first run and MCP registration.
`CONTRIBUTING.md` **Adding a provider**: one adapter file, one line in `adapters/all.go`, `providertest.Run` green; no
leaked or unlicensed harness source is read, quoted or copied.
### 12.2 Gate, tool chain and CI
**Gate**: `gate.json`, run by `aiwe gates gate --repo .` locally and in CI.

| Step | Command | Catches |
|---|---|---|
| `format` | `gofmt -l .` (empty output) | unformatted files |
| `lint` | `custom-gcl run ./...` (golangci-lint with the nilaway plugin) | §5.17; `go vet` runs inside it |
| `vuln` | `go tool -modfile=tools/go.mod govulncheck ./...` | known vulnerabilities (§17) |
| `tidy` | `go mod tidy -diff` | `go.mod`/`go.sum` drift (§17) |
| `deadcode` | `go tool -modfile=tools/go.mod deadcode ./cmd/... ./internal/cmd/...` (empty output) | unreachable functions (§17) |
| `repo` | `go run ./internal/cmd/repocheck all --repo .` | `rules`, `size`, `boundaries`, `source-rules`, `deps`, `ci-shape` |
| `test` | `go test -json -p <workers> ./...` | behaviour (§5.13) |

`deadcode` is `"required": false` until M-Skeleton closes (#186). `repo/rules` checks only that the §12.1 files exist
(L2). The Go toolchain is pinned by the `go` line of `go.mod`, which
`actions/setup-go` reads (§17). govulncheck, deadcode, svu, golangci-lint and GoReleaser are `tool` lines of
`tools/go.mod` (L4), so the main module requires only what the binary links. `.custom-gcl.yml` pins nilaway;
golangci-lint's `custom` command builds `custom-gcl`. A missing tool makes its step `not_measured`. `repocheck doctor`
names each missing development prerequisite with its install command.

**CI**: `.github/workflows/ci.yml`; every step has a `# why:` comment (DR-22). Path filtering uses job conditions, never
workflow `paths`, because a path-skipped workflow leaves required checks pending (§17). Triggers: `push` to `main` (the
evidence of §7.3 step 12), `pull_request` (Q21), `workflow_dispatch` with `all-os` (a milestone close runs every OS
once, Q20). Superseded pull-request runs are cancelled; `main` runs never are. A new Linux check is a step in an
existing job, as GitHub rounds each job up to a whole minute (§17). Each job has `timeout-minutes`. Permissions are
read-only except in `release`. Every `uses:` pins a full commit SHA with the version in a comment.

| Job | Runs on | Runs when | Steps |
|---|---|---|---|
| `changes` | Linux | always | `repocheck changes` → `code`, `release`, `docs`, `green-tree`, `os-variant` (on when an OS-specific file, `go.mod` or `ci.yml` changed, on `all-os`, before a live release; Q20) |
| `static` | Linux | `code`, `docs` or `release` | `gates gate --step format,lint,vuln,tidy,deadcode,repo` (docs only: `repo`); `repocheck commit-msg` on a pull request; `repocheck run-id` on `main` once M-Skeleton closes (DR-23, Q17); `svu next` and release notes on a pull request (Q15); a GoReleaser snapshot when `release` |
| `test` | Linux; Windows and macOS when `os-variant` | `code` | `gates gate --step test`; `race` on Linux (§17); `go build ./cmd/aiwe` and `clean-os-smoke` with `PATH` reduced to git and the binary (DR-4) |
| `required` | Linux | always, after all (`needs`, `if: always()`) | `repocheck ci-report`: fails when a needed job failed or was cancelled; writes durations, step count, cache hits and minutes per OS against the plan's included minutes (§17) |
| `release` | Linux | push to `main`, `required` green, `code` or `release` | §12.3 |
### 12.3 Release
**svu + GoReleaser OSS** (§17): the next version comes from Conventional Commits with no pull request, file or human
step; GoReleaser alone creates no tags, and release-please needs a release pull request and a personal token.

Release job: (1) checkout with tags; (2) `svu next`; equal to the current tag → `no_change`; (3) dry mode until
M-Usable closes (L9): print the version and stop; (4) live: `git tag`, `goreleaser release --clean` (six targets,
archives, `checksums.txt`, the GitHub Release with notes, Scoop, winget and Homebrew manifests); (5) `actions/attest` on
`checksums.txt` (`contents: write`, `id-token: write`, `attestations: write`), then
`gh attestation verify dist/checksums.txt --repo "$GITHUB_REPOSITORY"`; (6) sign `checksums.txt` with the Ed25519
private key held as a CI secret and publish the signature as the asset `checksums.txt.sig`, for self-update (L14); (7) publish the `mcpb` registry entry.

**Commit messages** (here and in managed projects): the Conventional Commits header is ≤ 50 characters, `type(scope): `
included. Body lines are ≤ 72 characters; URLs, trailers and quoted command or output lines are exempt. Every commit
carries `Refs <owner>/<repo>#N` or `Closes <owner>/<repo>#N`; `Closes` names only an issue in the same repository.
The closing commit carries `Effect: <before> → <after>` with measured values. Engine commits carry `Run-Id: <ULID>`.
`external` commits (Dependabot, outside contributors; Q17) are exempt from the trailer and `Run-Id` rules.
`repocheck commit-msg` checks this repository's commits; the engine composes its own messages to the same rules.

Rules: versions follow L9 (`v0.1.0` at M-Usable, `1.0.0` at M-Trust); bumps (§17): breaking → major, `feat` → minor,
`fix` → patch, others → none. A breaking marker is a red line (DR-1): `breaking_change` until the owner runs
`aiwe unit approve-breaking`. No version in any file; GoReleaser injects the tag; `repo/source-rules` rejects version
literals. Release notes (Q15): Keep a Changelog layout via `changelog.groups` (`feat` → Added, `fix` → Fixed, others →
Changed); CI never commits to `main`. Provenance: one attestation, verified with `gh attestation verify` or
`checksums.txt`; cosign rejected. Code signing: Q7. The job runs only on `main` after `required` is green.
## 13. Roadmap
Each milestone has one goal issue; the tracker holds the units as its sub-issues (L1).
- **M-Skeleton**: CI runs on `main`, and `aiwe work --repo . --unit N` takes one aiwe issue to a merged pull request
  with zero owner actions, running on pinned models; this is the self-hosting point (O1, DR-23).
- **M-Usable**: the MCP server, the work session and self-update (O10), concurrent sessions (O11) and model choice
  (O9) first; sources with fallback and quota reaction; the opencode, cursor, codex and
  gemini CLI adapters (O12) and the API adapter with its agent loop (O2); `aiwe plan`; unit
  validation; core D-checks; the security-reviewer; a sandbox-repository end-to-end run; the repository turns public
  and `v0.1.0` ships (L9).
- **M-Tracking**: `aiwe issue` and `aiwe report` across repositories (O8).
- **M-Trust**: `aiwe metrics` M1–M5, invariance, escaped defects, mutation, the debugger, stack profiles beyond Go and
  the adaptation ladder; `1.0.0` ships (L9).
- **M-Hardening**: the frozen hardening findings, which aiwe works itself after M-Skeleton (O1).
- **Deferred**: §15.
## 15. Deferred — the trigger that brings each back
| Item | Trigger |
|---|---|
| Operation units: one non-code change to a server or service, planned by an operator role and executed by the engine after a terminal-only owner approval bound to the plan digest (O4) | The owner names a resource to change, and `os-sandbox` exists (Q19). |
| Percentage shares per source (O3) | Model choice (§8.1) cannot express a split the owner asks for. |
| Duplicate-check counts and gate-slowdown reports (L10) | A measured slow or repeated check. |
| Other API wire formats; remote cloud agents | A source needs one; the provider documents dispatch and concurrency. |
| Non-GitHub trackers | A user asks, with a repository to test on. |
| `harness-sandbox` and `os-sandbox` | An adapter or external tool enforces one on three OS without elevated rights (§6.7). |
| Several stacks in one repository | A target repository has two; until then `multi_stack` stops. |
| One unit on two providers | Lead time p90 > 3× median over 50 units, or `repeated_red` on 3 of 50 units. |
| Branch archive beyond deleting merged refs; session usage report; loop counters inside a run | Run refs exceed 500 in one remote; `lost` runs > 5 % in the ledger; `stalled` plus `timed_out` > 5 % of runs over 50 runs. |
| Dependency cache across runs; per-test dependency map | Restore time > 50 % of run time on a stack; full-gate time > 50 % of M5 over 50 units. |
| Process-tree end on engine crash | The sweeper finds a live process of a dead engine. |
| Opt-in copy of ignored files (for example `.env`) into clones; shared resource declaration (ports, databases) | A clean-clone gate is red only because of an ignored file; an environment red traced to two runs sharing one. |
| Security analysis and licence policy for managed projects | An escaped defect classed security, or a project declares such a tool or policy. |
| Test-tampering calibration; per-model variance reruns; mutation tools beyond Node and Python | An escaped defect traces to a passing but wrong test, or lands on a stack without a mutation tool. |
| Dependency role; i18n checks | A unit class fails audit 3 times for the same missing expertise. |
| Built-in scheduler; harness-shaped card export | The owner asks (an OS scheduler may call `work` until then); a harness needs its own card file. |
| Latency budget; API token budget per turn; self-hosted CI runner | A measured slow call; M4 doubles over 50 units; this month's minutes would exceed the plan's included minutes. |
| npm channel (GoReleaser OSS cannot publish to npm); SBOM file (the binary carries its module list) (§17) | The owner turns it on; a channel or user requires one. |
| Code signing (macOS notarization, Windows certificate); architecture-specific source files | Before `1.0.0` (Q7); a measured gain needs one. |
## 16. Decision log
Each decision keeps its id.

| Id | Decision | Status |
|---|---|---|
| O1 | Walking skeleton first: about 12 units to `aiwe work` on aiwe's own repository; hardening findings are frozen until aiwe works them (owner, 2026-10-12). | valid |
| O2 | aiwe keeps its own API agent loop for API-key sources, after the skeleton (owner, 2026-10-12). | valid |
| O3 | Routing is an ordered (source, model) fallback list per role; percentage shares are deferred (owner, 2026-10-12). | valid; the list is the session assignment of O9 |
| O4 | Operation units are deferred (§15) (owner, 2026-10-12). | valid |
| O5 | The issue format is the global issue standard, English variant (§8.4); no checkboxes, status labels or comments; large outcomes are native sub-issues; commits carry `Refs`/`Closes` and an effect line (owner, 2026-10-12). | valid |
| O6 | Source organization covers only work aiwe runs; reading other tools' logs stays a non-goal (owner, 2026-10-12). | valid |
| O7 | At most one consumer subscription per provider; API keys unlimited; a subscription at its limit waits or moves to another provider or API, never to a second account of the same provider (owner, 2026-10-12). | valid |
| O8 | After the skeleton aiwe absorbs the global issue tooling: `aiwe issue` (create, edit, check) and `aiwe report` across repositories; the template lives only in aiwe (owner, 2026-10-12). | valid |
| O9 | Model choice is automatic, by fixed rules, assigned per session at MCP server start or `aiwe work` start (§8.1); it is placed first in M-Usable, and the skeleton runs on pins (owner, 2026-10-12). | valid |
| O10 | The primary interface is an MCP server started by the user's harness session, with a detached work session and self-update (§6.3); placed at the start of M-Usable (owner, 2026-10-12). | valid |
| O11 | Many concurrent sessions work without conflict and without a daemon (§6.6); placed in M-Usable with O10 (owner, 2026-10-12). | valid |
| O12 | The Codex and Gemini CLI adapters come after self-hosting, in M-Usable (owner, 2026-10-12, #209). | valid |
| L1 | This file holds product, architecture and contracts, ≤ 900 lines; the delivery plan and the unknowns live in tracker issues; §17 keeps only cited facts (lead, 2026-10-12). | valid |
| L2 | Design rules consolidate to about 22 with one enforcement each; `repo/rules` stops parsing tables of this file (lead, 2026-10-12). | valid |
| L3 | The skeleton binds one tracker (GitHub over HTTPS with the standard library; gh is optional, §10.1) and one harness (Claude Code CLI), driven through the CLI; the MCP server opens M-Usable (O10) (lead, 2026-10-12). | valid |
| L4 | `tool` lines move to `tools/go.mod`, run with `go tool -modfile=tools/go.mod` (lead, 2026-10-12). | valid |
| L5 | Hand-worked units until self-hosting follow risk tiers: test-author only for risky work; one auditor run per batch; risky units reviewed separately (lead, 2026-10-12). | valid |
| L6 | The owner key and the keyed hash on owned-directory records and markers are removed; removal keeps the root, link, marker and mount-point checks. Reason: a forged marker can only remove aiwe's own temp directory, and a run below `os-sandbox` could read the key anyway (lead, 2026-10-12). | valid |
| L7 | Close on merge: the squash commit carries `Closes <owner>/<repo>#N`; a red `main` CI → revert pull request and the unit reopens (lead, 2026-10-12). | valid |
| L8 | Unit dependencies are native blocked-by links; goal → units are native sub-issues; owner-only work has label `owner-action` and is never dispatched (lead, 2026-10-12). | valid |
| L9 | `v0.1.0` when M-Usable closes, with the repository public; `1.0.0` when M-Trust closes (lead, 2026-10-12). | valid |
| L10 | Product metrics are M1–M5; duplicate-check counts and gate-slowdown reports are deferred (§15); footprint ceilings stay enforced by the sweep (§6.4) (lead, 2026-10-12). | valid |
| L11 | Working code is not removed only to shrink; meta-machinery is not extended, except code a decision removes (L2, L6, Q22 marking) (lead, 2026-10-12). | valid |
| L12 | Docs land with code: this file, README, AGENTS and CONTRIBUTING sit on one branch and pull request with the repo-check change (L2), the tools module (L4) and the code-comment rule renumbering, so the gate is green when it merges (lead, 2026-10-12). | valid |
| L13 | A judging run excludes the writer's provider when another provider is available; otherwise the independence order applies; one source still works (§8.1 point 5) (lead, 2026-10-12). | valid |
| L14 | Self-update only for a binary installed from a GitHub Release, one checker at a time, verified by an Ed25519 signature on `checksums.txt` (§6.3, §12.3); the owner creates the key pair (an `owner-action` issue) (lead, 2026-10-12). | valid |
| L15 | A check red, then green on rerun opens a blocking `owner-action` issue; closing it clears the flag (§7.4) (lead, 2026-10-12). | valid |
| L16 | Tiers are mechanical, with no provider-specific tier names (§8.1 point 7) (lead, 2026-10-12). | valid |
| L17 | A risky unit, decided mechanically (§9.1 **Risky unit**), runs test-author first and gets a separate review (lead, 2026-10-12). | valid |
| L18 | #234's refusal of a state root inside a repository work tree stays; state files never enter a work tree (lead, 2026-10-12). | valid |
| Q1 | License: MIT. | valid |
| Q2 | Product name aiwe, read from one constant (DR-17). | valid |
| Q3 | Nothing is published before the trust milestone; the first version is `1.0.0`. | superseded by L9 |
| Q4 | The repository is on a personal account; issue type follows the GraphQL rule of §8.4; contract tests prove both paths. | valid |
| Q6 | Independent audit as DR-5 states; the ledger records the level; one source is never refused. | valid |
| Q7 | Code signing: none until M-Trust closes; the owner decides then. | valid |
| Q8 | The operator only plans; the engine performs the access. | deferred with operation units (O4) |
| Q9 | Live approval is the terminal-only `aiwe ops approve`, which asks for the digest. | deferred with operation units (O4) |
| Q10 | A `not_measured` UI review does not block integration. | valid |
| Q11 | A conflict goes back as a resolution run on the same branch; two failures → skipped and requeued (owner, 2026-10-06). | valid |
| Q12 | Cognitive complexity ≤ 12 per function: `gocognit` here, D18 on managed projects (owner, 2026-10-06). | valid |
| Q13 | Source concurrency is measured: rate limits lower it, clean runs raise it up to the owner's value (owner, 2026-10-05 and 2026-10-10). | valid |
| Q14 | aiwe removes only a directory it created and marked, under the temp or cache root (owner, 2026-10-10). | valid; amended by L6 |
| Q15 | GoReleaser writes release notes in Keep a Changelog layout into the GitHub Release; no `CHANGELOG.md`; each pull request prints the computed version and notes (owner, 2026-10-10). | valid |
| Q16 | The repository starts private and becomes public at the usable milestone, with the `SECURITY.md` channel. | superseded by L9 |
| Q17 | The `Run-Id` CI check covers only commits that close or reference a unit issue; Dependabot and outside commits are `external`; a commit for an `owner-action` issue is `owner` (owner, 2026-10-10). | valid |
| Q18 | The end-to-end target is the private sandbox repository `aiwe-sandbox` under the owner's account (owner, 2026-10-10). | valid |
| Q19 | Agents run at `env-scrub`; a stronger level comes after M-Usable closes and before the first live operation (owner, 2026-10-10). | valid |
| Q20 | Windows and macOS tests run only when `os-variant` is on, at a milestone close (`all-os`) and before a live release (owner, 2026-10-10). | valid |
| Q21 | Trunk-based: each unit merges through its own pull request after CI, squash-merged by the engine when the head equals the audited tip (owner, 2026-10-10). | valid |
| Q22 | The write that closes a unit issue also marks each green Done item. | superseded by L7 (no checkboxes, O5) |
| Q23 | The 60-line limit applies to non-test functions only (owner, 2026-10-11). | valid |
| Q24 | A code unit has at most 5 source files (owner, 2026-10-11). | valid |
| Q25 | `go.mod` and `go.sum` are a manifest and its lock file, not source files (owner, 2026-10-11). | valid |

Owner actions and lead tasks are issues; an owner action has the label `owner-action` (L8).
## 17. Facts and sources
Checked 2026-10-10 unless dated.
- GitHub REST: `state_reason` is `completed`, `not_planned`, `duplicate` or `reopened`; sub-issues ≤ 100 per parent and
  ≤ 8 levels; dependencies at `.../dependencies/blocked_by` and `/blocking`; 5 000 requests per hour per user token,
  ≤ 100 concurrent, content creation about 80 per minute and 500 per hour; the merge call takes `merge_method`,
  `commit_title`, `commit_message` and `sha`, which the head must match. Source: docs.github.com.
- GitHub Actions: a path-filtered workflow leaves required checks pending; a job skipped by `if:` reports success, so a
  required job needs `needs` with `always()`; private repositories get 2 000 (Free) or 3 000 (Pro) minutes per month,
  public ones run free; each job rounds up to a whole minute. Private vulnerability reporting is for public
  repositories; attestations use `actions/attest` with `id-token: write` and `attestations: write` and verify with
  `gh attestation verify`. Source: docs.github.com.
- `actions/setup-go` v7 caches by default and, with no `toolchain` line, reads the `go` line; `go.mod` pins
  `go 1.27.2`; GoReleaser v2.18.3 needs at least 1.27.1. Source: actions/setup-go docs; `go.mod`, 2026-10-11.
- Anthropic: a rate limit returns 429 with `retry-after`; a spend limit returns 429 without it, code
  `enforced_spend_limit_reached`, reset on the 1st of the month 00:00 UTC; OpenRouter rejects with 402 and
  `limit_source` (platform.claude.com; openrouter.ai). Third parties may not "route requests through Free, Pro, or Max
  plan credentials on behalf of their users" or "collect, store, or intermediate Claude.ai credentials or session
  tokens"; a user's own login to the official CLI is allowed (code.claude.com, Legal and compliance). A router that
  altered subscription OAuth traffic saw bans about one minute after connecting, and its ban detector never
  auto-recovers (OmniRoute issues, `BAN_DETECTION.md`).
- Market: no surveyed tool combines engine-run gates, an independent auditor, credential-free multi-source routing and
  per-unit metrics; rotating consumer accounts of one provider is the high-risk pattern. Source: lead survey,
  2026-10-12.
- Runtime survey (one Windows 11 x64 machine): binary Go 7.3–7.9 MB, Node single executable 100.7–151.1 MB, Bun
  64.7–99.8 MB; MCP `initialize` median Go 22.2 ms, Node 147.4 ms; idle memory Go 9.1 MB, Node 49.7 MB, Bun 75.5 MB;
  Go cross-compiled to five targets with `CGO_ENABLED=0` and no download, Bun failed on all six; `go vet` caught 0 of 5
  classes (non-exhaustive switch, nil access, index, ignored error, data race).
- go-git merges only by fast-forward, without rebase or patch apply (`COMPATIBILITY.md`). MCP Go SDK v1.8.0 has
  `ServerOptions.Instructions`, prompts, resources, `AddTool` and `StdioTransport` (pkg.go.dev). An MCP Registry `mcpb`
  package is a release binary with a checked `fileSha256` (modelcontextprotocol/registry). winget ships with App
  Installer on Windows 11 (learn.microsoft.com). Homebrew 5.0.0 deprecates unsigned casks (brew.sh, 2025-11-12).
- GoReleaser v2.18.3 OSS: Homebrew casks (formulas deprecated), Scoop, winget, snapshots; npm is Pro only; "GoReleaser
  does not create any tags"; it attests `checksums.txt`; `changelog.groups` groups by first line; `.Version` drops the
  `v`. svu: `fix` patch, `feat` minor, `!` or `BREAKING CHANGE` major, `chore` none. release-please needs a release
  pull request, and `GITHUB_TOKEN` pull requests trigger no workflows; go-semantic-release has 485 stars, svu 876.
  Source: goreleaser.com and project pages, 2026-10-11.
- golangci-lint v2.14.0 has the §5.17 linters; nilaway's README warns of false positives; `gocognit` adds 1 per whole
  `switch`; govulncheck and `go mod tidy -diff` exit non-zero on a finding; deadcode lists functions unreachable from
  `main`; `go test -run` matching no test prints `[no tests to run]` and exits 0; `go version -m` prints a binary's
  modules. Source: golangci-lint.run; pkg.go.dev; Go 1.27.2 runs, 2026-10-11.
- Go: the race detector needs cgo and, except on macOS, a C compiler; `errgroup` cancels at the first error;
  `Cmd.Cancel` and `Cmd.WaitDelay` bound a child holding pipes; Windows quoting follows `CommandLineToArgvW` and batch
  files need `SysProcAttr.CmdLine`; lookup ignores the current directory (`ErrDot`). Source: go.dev; pkg.go.dev.
- git 2.52.0: an empty `credential.helper` resets helpers; `GIT_CONFIG_NOSYSTEM` skips system config;
  `GIT_TERMINAL_PROMPT=0` stops prompts; `core.hooksPath` moves hooks; `protocol.ext.allow` defaults to `never`;
  `git diff` has `--no-ext-diff` and `--no-textconv`. Source: git documentation.
- Isolation survey (one Windows machine, NTFS, 504-file repository): the §6.5 numbers; worktrees share refs and config;
  `--shared` clones break when the source prunes. Source: measurement; git-scm.com.
- The latest Contributor Covenant is 3.0 (contributor-covenant.org).
