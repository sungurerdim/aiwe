# aiwe — Product Specification

aiwe — AI workflow engine: orchestrate all your AI subscriptions and agents, from issue to verified, merged code.

Status: final; owner decisions recorded (§16). Date: 2026-10-10. Revision 2, same date: the stack is decided
(owner): Go, one binary per OS and architecture. Revision 3, same date: owner decisions on the
definition, the role set and operation units. Revision 4, same date: final owner decisions; no open
question remains (§16). Revision 5, same date: the previous engine's lessons, issues and run ledger
merged where they prevent a named failure or move a metric (§13.1, Q11–Q13). Revision 6, same
date: independent audit fixes; owner decisions Q14–Q18; every unit's new code is reached (§14).
Revision 7, same date: re-audit and security design fixes; owner decisions Q19 and Q20.
Source of truth for the new repository. It replaces the old repository's PLAN, ENGINEERING,
DECISIONS and norm files. The old TypeScript repository is a read-only behaviour reference: nothing
is ported; every needed behaviour is rewritten in Go (§13).
Labels: **fact** = traced to a source read on 2026-10-10 (§17) · **default** = chosen here, owner
may reverse · **unknown** = not measured; a unit measures it before use (Un, §17).
Writing rule: each fact lives in one place; other places cite it by id (DR-n, §n, Sn, Mn, Qn, Un).
Decision order for every design choice: owner decisions, then the value order (§1.4), then YAGNI
(DR-33), then necessity (DR-42).

---

## 1. Purpose, users, problems, value, metrics

### 1.1 Purpose

You set the goal; aiwe delivers verified, merged code using the subscriptions and API keys you
already have from supported providers, in the shares you choose. Work runs as small, checked units,
each handled by a role-specific agent; the author never approves its own work, and every test and
gate runs once, mechanically. aiwe uses only each provider's official CLI or API — no proxies — on
Windows, macOS and Linux, with zero configuration.

Unit lifecycles: §7.3 (code) and §7.9 (operation). The engine adapts instead of guessing (DR-3).
It is independent of OS, device, harness, subscription, API, stack, tracker and human language
(DR-6, DR-27, DR-40). It is a public open-source product (§12).

### 1.2 Users

- Solo developers and small teams who run several projects with coding agents.
- The user acts only on product decisions and red lines.

### 1.3 Problems and how each is solved

| Id | Problem | Mechanism |
|---|---|---|
| S1 | Extra subscriptions and API credits are not usable in a standard way; settings stay per project. | One global source registry (§8.1), shared by every project. No per-project provider settings. |
| S2 | Cost, time, errors and waste are minimised by hand. | Routing (§8.1), measured parallelism (DR-7), quota reaction (§7.6), bounded footprint (DR-36), necessity (DR-42) and metrics (§1.5) are engine functions. |
| S3 | No standard role or task template; templates are local to a project or subscription. | Role cards and unit format ship with the engine and are served by it (DR-34, §8.4, §9). |
| S4 | The writer approves its own work, checks are skipped, scope is vague. | DR-9, DR-10, DR-11. |
| S5 | Context bloat on long, branching work causes wrong or missing work. | DR-11, a clean context per run, state only in tracker and ledger (DR-30). |
| S6 | Agents run broad checks outside their scope; a 5 s check becomes 5 min. | DR-12, §7.8. |
| S7 | Agents do not work the same way. | One prompt contract, one card format, one return format (§9). |

### 1.4 Value order

When goals conflict: **1 time** (user gives a goal and sees a result) · **2 trust** (every merged
change passed a mechanical gate and an independent audit; escaped defects 0) · **3 cost**
(lowest measured quota, money, disk, CPU, memory and network).

### 1.5 Success metrics

All metrics come from one command: `aiwe metrics [--repo R] [--since D] [--by goal|model]`. Input:
the ledger (§8.5) and git history of the working branch. Unmeasurable values follow DR-21.
`--by goal` groups M1, M4 and M5 by the unit's goal; `--by model` gives M1 per role, source and
model. M1, M3 and M5 count code units only. `metrics` also prints each source's fixed run overhead
(input tokens of its smallest completed run, per harness version) with its share of `TokensIn`,
and every `gate_slowdown` finding (§7.8).

| Id | Metric | Target | Exact definition |
|---|---|---|---|
| M1 | First-try green rate | ≥ 70 % | `A / B`. B = units closed `completed` in the window. A = units in B whose first engineer run reached `integrated` with `fixRounds = 0` and no second engineer run. |
| M2 | Interventions per unit | 0 | Ledger records with `kind = owner_action` per closed unit. The engine writes one when it stops for the owner, or when it sees a non-engine commit, label or state change on a claimed unit. |
| M3 | Escaped defects | 0 | Merged units U for which a later commit C on the working branch, within window W after U's merge commit M, is (a) a `git revert` of a commit in M's range, or (b) typed `fix` and changes or removes lines that `git blame C^` attributes to M's range. W = 30 days (default; `--window` overrides). Computed at every closeout and by `metrics`. |
| M4 | Cost per green unit | measured | Sum of `costUsd` over all runs (all roles, all attempts) of units integrated in the window ÷ number of those units. One unknown cost → `unknown` plus the measured lower bound. Tokens in/out the same way. |
| M5 | Unit lead time | measured | Median of `ciGreenAt − claimedAt` over units integrated in the window. |
| M6 | Duplicate check runs | 0 | Check records (§8.5) whose key already has an earlier record and that carry no `rerunReason`. Red-to-green flips of reasoned reruns are printed beside it. |
| M7 | Footprint | within the ceilings of §6.4; leftovers 0 | Bytes and file counts of the state and cache roots; temp directories without a live owner; peak memory of run trees where the OS reports it (U16). |

### 1.6 Glossary

One term per concept; the spec uses no synonyms.

- **Goal**: an issue that states an outcome and why; `plan` splits it into units.
- **Unit**: an issue in the unit format (§8.4). Identity = the issue number. Kind `code` changes
  repository files; kind `operation` changes a resource (§7.9).
- **Resource**: a server or service outside the repository that an operation unit changes; the
  engine reaches it only through the unit's access method (§8.4).
- **Role**: the kind of work a run does, defined by one role card (§9.1).
- **Run**: one execution of one role on one unit by one source. Id = ULID.
- **Session**: a harness's own conversation id, used to resume a run.
- **Batch**: units integrated together and gated once (DR-19).
- **Source**: one subscription or API account in the registry (§8.1).
- **Harness**: a CLI program that runs an agent (model plus tools) on the machine.
- **Adapter**: code that drives one harness or one API wire format through the provider port (§10.2).
- **Engine**: aiwe itself: queue, isolation, checks, integration, metrics.
- **Lead session**: an interactive agent session connected to the engine over MCP.
- **Owner**: the human who decides product questions.
- **Check**: one mechanical command with a recorded result: a gate step or a unit verify.
- **Gate**: an ordered list of checks. Same input, same result.
- **Audit**: a judgement by an auditor in its own run, with a clean context and the auditor card (DR-9).
- **Evidence**: engine-recorded raw results: command, exit code, last lines, key. Agent text is never evidence.
- **Done claim** vs **verified done**: an agent's "done" is a claim; verified done is the end of job (§2).
- **Canonical repository**: the local repository that run clones come from (§6.5).
- **Run clone**: a fresh shallow clone for one run, removed when the run ends (§6.5).
- **Sandbox level**: how far a run's processes are confined: `env-scrub`, `harness-sandbox` or
  `os-sandbox` (§6.7).
- **Artefact**: a ladder rung-2 product, stored with provenance (§7.7).
- **Module**: one package tree `internal/<m>` with its root package as the entry (§6.1).
- **Unattended**: no human waits. Unattended never means unapproved: every gate still runs.

---

## 2. Scope and non-goals

In scope:
- Onboarding any git repository with zero manual configuration (DR-8).
- A self-describing MCP server and a CLI from one registry (DR-23, DR-34).
- Planning a goal into units, written as tracker issues.
- The full unit lifecycle up to the end of job, each run isolated (DR-35).
- Global source registry, routing, quota reaction, metrics.
- Operation units: one non-code change to a resource, planned by a role and executed by the engine
  (§7.9).

**End of job** depends on the unit kind. After it, the issue is closed `completed` with evidence.
- **Code unit**: the unit's commit is merged into the working branch and pushed to the remote, and
  the remote CI run for that push is green.
- **Operation unit**: the change is applied, the after-measurement verifies it, and the rollback is
  ready (backup present, rollback command recorded).

Non-goals (permanent):
- Building or publishing releases of managed projects: a release stays in the project's own CI, as
  aiwe's own release stays in aiwe's CI (§12.3).
- A web UI, desktop or mobile app, terminal session manager, or chat bridges.
- A proxy or gateway for model traffic, or the use of one (DR-43); model choice by a learned router.
- Budget ceilings and quota forecasts: sources get shares only (§8.1).
- General pull-request review: the auditor judges only a unit's done criteria.
- A network transport for MCP: the server speaks stdio only, so it opens no listener.
- Storing credentials (§10.1).
- Reading logs, sessions or bills of tools the engine did not run.
- Benchmark harnesses.
- Deleting a directory the engine did not create (DR-25).

Deferred items: §15.

---

## 3. Design rules

One sentence each. "Gate step" = a step of this repository's `gate.json`, run by
`aiwe gates gate --repo .` (§12.2). `repo/<check>` = a check inside gate step `repo`. "Serves"
names the problem (§1.3) or metric (§1.5) a rule exists for (DR-33). Test paths are relative to
`internal/`. Check `repo/rules` is phase-aware. It fails when a Serves cell is empty, a named gate
step is missing from `gate.json`, a repository file of §12.1 is missing, or a named test is missing
while the source file beside it exists.
A test whose source is not yet in the tree passes only while a §14 Files cell lists that source.

| # | Rule | Serves | Enforcement |
|---|---|---|---|
| DR-1 | The engine runs unattended: every wait has a time limit, every stop has a name, irreversible actions sit behind the engine's own key. | M2 | test `engine/lifecycle_test.go` (every exit path returns a named outcome) |
| DR-2 | Work whose answer is determined by its input is code, never a model call. | M4 | judgement: auditor (a new model call site needs a stated reason) |
| DR-3 | The engine adapts to project, agent, harness and provider by the adaptation ladder (§7.7) and never guesses. | S1, S3 | test `engine/synthesis_test.go` |
| DR-4 | Ladder rungs: (1) mechanical derivation; (2) an agent-made artefact, produced by one role, verified by another, proved on the base commit, stored with provenance; (3) the owner, for product decisions only. | S3 | test `engine/synthesis_test.go`; ledger `kind = artefact` |
| DR-5 | A stored artefact is reused while its input hash is unchanged and is never regenerated otherwise. | M4 | test `engine/synthesis_test.go` (idempotent rerun) |
| DR-6 | No OS, device, shell, path, core count, memory, harness, model, provider, stack or tracker is assumed; each difference lives in one adapter file or one data row. | S1 | checks `repo/boundaries`, `repo/source-rules`; CI on three OS (§12.2) |
| DR-7 | Capacity is measured at run time: workers = measured cores ÷ concurrent jobs, minimum 1; concurrent gates = measured cores ÷ the cores one gate uses; a source starts no more runs than its concurrent-run ceiling (§8.1, Q13); each chosen value is printed with the limit that bound it. | S2, M5 | tests `core/capacity_test.go`, `gates/admission_test.go`, `engine/routing_test.go` |
| DR-8 | A managed project needs no file, setting or setup step for the engine; a project `gate.json`, if present, wins over the derived gate. | S3 | test `engine/onboard_test.go` |
| DR-9 | Writing roles and judging roles (§9.1) are separate runs; a judging run starts with a clean context and its judging card; its source is the most independent one available, in the fixed order different provider > same provider, different model > same model, new instance; the run records the level used (`AuditIndependence`, §8.5) (Q6); a verdict counts only from the judging run's own result, bound to its RunID (§8.6), and a `VERDICT:` line in a branch commit is refused (`verdict_in_branch`). | S4, M3 | tests `engine/routing_test.go` (each level and the order), `engine/judge_test.go`; check D14 |
| DR-10 | A gate is a list of steps; a skipped or unrun step makes the whole gate `incomplete`, never green. | S4 | test `gates/runner_test.go` |
| DR-11 | A unit without goal, done criteria and its kind's scope is not dispatched: a code unit needs a verify and a file set of ≤ 5 source files (test, lock and generated files do not count; `go.mod` is a manifest and `go.sum` its lock file, so neither counts, Q25); an operation unit needs scope, access and measure (§8.4). | S5 | test `engine/unit_test.go` |
| DR-12 | Only the engine runs checks: an agent requests its unit verify through `aiwe verify`, the auditor reads recorded results, and a check runs at most once per key (§7.8). | S6, M6 | tests `gates/cache_test.go`, `engine/verify_test.go` |
| DR-13 | Absence is not evidence: an unknown stack or file type, an empty output, a missing tool, a non-zero exit or a test run that collects zero tests is `not_measured` or red, never clean; a verify that collects zero tests is `no_tests_collected` (§7.8). | S4 | tests `gates/checks_test.go`, `stacks/detect_test.go`, `engine/verify_test.go` |
| DR-14 | Scope is declared before a run and compared with the observed diff; a mismatch is `scope_mismatch`. | S4 | check D6; test `engine/coverage_test.go` |
| DR-15 | A unit has at most one open run; the engine knows every process it starts and stops its whole tree. | M2 | tests `engine/continuation_test.go`, `core/process_test.go` |
| DR-16 | Nothing leaves a run clone unaudited: committed work is collected into a run ref, residue is recorded, then the clone is removed; the sweeper collects a dead run the same way; a run ref stays while its unit is open (§6.5, §7.5). | S4 | tests `engine/residue_test.go`, `engine/continuation_test.go` |
| DR-17 | Behaviour loss is declared or red: base-commit tests run against the new code. | M3 | checks D2, D9, D15; test `gates/invariance_test.go` (Phase 3) |
| DR-18 | An agent commits only on its run branch; push, merge, issue close and tracker writes belong to the engine. | S4 | check D8; no tracker token in the run environment |
| DR-19 | Only an audited tip merges: each unit merges as one squash commit of its own pull request, only after the pull request's CI is green and only while its head equals the run record's `AuditedTip`, else `unaudited_commits`; batches of unit pull requests are gated; a red batch is bisected (§7.3 step 14); a removed pull request stays open with its reason and never re-enters a batch (Q21); a red `main` CI after a merge is undone by a revert pull request, never by a direct push (§7.3 step 16). | M3 | test `engine/integrate_test.go` |
| DR-20 | Every run names its model; `auto` only with explicit permission; the observed model is recorded and a mismatch is red; an adapter switches off harness features that call unnamed models. | M4 | contract suite `adapters/providertest` (§5.13) |
| DR-21 | Prices, limits, quotas and metrics are measured or `unknown` with a reason (`no_price`, `no_usage_data`, `subscription`), never `0` or invented; a price carries its source URL and check date and is used for 30 days (default); quotas are never forecast. | M4 | test `ledger/metrics_test.go`; type `Measured` (§8.5) |
| DR-22 | Every engine commit carries one opaque trailer `Run-Id: <ULID>`; other attributes live in the ledger. | M1 | test `engine/integrate_test.go` |
| DR-23 | Every capability is one command; CLI help and MCP tools are generated from one registry; a command that approves an irreversible action or changes trust is terminal-only: it is never an MCP tool and refuses to run without a terminal (§5.7). | S4, S7 | tests `mcp/surface_test.go`, `engine/commands_test.go` |
| DR-24 | Other modules import a module only through its root package and only if `core/modules.go` declares the edge (§6.1); a module's private packages sit under `internal/<m>/internal/`. | S5 | Go compiler (internal directories); check `repo/boundaries` |
| DR-25 | No machine-specific path; no product name outside the one constant; recursive removal (`os.RemoveAll`) only in `core/remove.go`, only on a directory the engine created, registered, placed under its temp root or its cache root and marked as its own (§5.15, Q14); a root that is or contains the user's home, the state root, a canonical repository or a file-system root is refused, and ownership records carry an HMAC (§5.15). | S1 | check `repo/source-rules`; gate step `lint` (`forbidigo`); test `core/remove_test.go` |
| DR-26 | No ignored error, no dead code, no duplicate implementation. | M3 | gate steps `lint` (`errcheck`, `unused`), `deadcode`; check D4 on own diffs |
| DR-27 | All text I/O is UTF-8 and byte-exact end to end (files, argv, stdin/stdout, env, git messages, issue bodies, provider requests); nothing depends on the locale; machine-parsed markers are fixed English keys, while content may be in any human language. | S1 | test `core/text_test.go` (fixtures: `İıŞşĞğ`, CJK, RTL, emoji, combining marks); check `repo/source-rules` (§5.14) |
| DR-28 | Tests are never weakened, skipped or mocked away to pass. | M3 | checks D2, D9, D10, D15 on own diffs |
| DR-29 | This repository's source, docs and commit messages are in English. | S7 | judgement: auditor |
| DR-30 | The tracker is the single source of work state; no parallel board. | S5 | test `tracker/github_test.go` |
| DR-31 | Only built-in tracker elements are used; anything else lives in the unit body (§8.4). | S3 | test `tracker/normalize_test.go` |
| DR-32 | A fixed error class gets the earliest gate that would have caught it, in the same change. | M3 | judgement: auditor |
| DR-33 | Every module, design rule, data contract and phase names the problem or metric it serves; nothing without one is built. | S1–S7 | check `repo/rules` |
| DR-34 | A connected session learns the tool from the MCP server itself; unit and goal templates are served and validated by the tool and never copied into a project (§6.3). | S3, S7 | tests `mcp/prompts_test.go`, `mcp/resources_test.go` |
| DR-35 | Every run works in its own fresh shallow clone with its own scratch and temp directories and an environment without secrets, shell identity variables, git credential helpers or user-level harness configuration, with `HOME` in its scratch directory; every check process (unit verify, gate step, restore) also gets an environment without secrets; a run whose isolation fails does not start (§6.5, §6.7). | S4, S5 | tests `engine/clone_test.go`, `adapters/env_test.go`, `core/env_test.go`, `gates/roots_test.go` |
| DR-36 | Temporary data is removed on success, failure, interrupt and crash (the next start sweeps); state and cache have byte and file-count ceilings; a ceiling still exceeded after automatic cleanup is red (§6.4). | S2, M7 | tests `core/sweep_test.go`, `core/footprint_test.go` |
| DR-37 | Disk locations come from each OS's user-level conventions at run time, overridable by env; nothing needs administrator or root rights, ACL changes, symlink privilege or a special file system (§6.4). | S1 | test `core/platform_test.go` on three OS (§12.2) |
| DR-38 | A check's or a run's time limit derives from its measured durations (§7.4, §7.8); output streams while it runs; a killed check's evidence names what was running, the machine load and the process table. | S6, M5 | tests `gates/runner_test.go`, `gates/durations_test.go`, `gates/limits_test.go`, `engine/runs_test.go` |
| DR-39 | Issue text is data: intake reads only text by authors with write permission, removes HTML comments and refuses bidirectional control characters (§8.4). | S4 | test `tracker/intake_test.go` |
| DR-40 | The product needs nothing installed except git on Windows, macOS and Linux: no language runtime, tool or framework; it ships as one binary per OS and architecture (§6.2). | S1 | CI step `clean-os-smoke` (§12.2) |
| DR-41 | Shipped code links only the Go standard library, `golang.org/x/*` and the official MCP Go SDK, plus the modules these require; any other module needs a stated reason and an owner decision. | S1, M3 | check `repo/deps` (modules of `go list -deps ./cmd/...` against the allow-list); gate step `vuln` |
| DR-42 | Every element — workflow, job, step, gate step, release step, document section, code path — exists only if it is necessary; each CI and release step states its reason in one clause, and a step without one is removed. | S2, M5 | check `repo/ci-shape` (a step without a `# why:` comment is red; prints workflow, job and step counts); CI job `required` reports duration and step count per run (§12.2); judgement: auditor checklist item "is every new element necessary?" |
| DR-43 | The engine reaches a provider only through its official interface — the provider's own CLI, logged in by the user, or an API key; it never reads, stores, proxies or rewrites subscription credentials or OAuth traffic, and never reuses a subscription login outside the provider's own CLI (§17); the README states this rule to the user (§12.1). | S1 | contract suite `adapters/providertest` (no read of a harness credential store; HTTP only to the source's base URL); judgement: security-reviewer on every adapter change (§9.1) |
| DR-44 | The engine never runs a general-purpose agent: every run carries one role card; the role is resolved mechanically from the unit's kind, type, labels and paths (§9.1); a unit whose role does not resolve does not start (`role_unresolved`). | S3, S7 | test `engine/role_test.go` (every resolution row, and an unresolvable unit) |
| DR-45 | A behaviour unit (§8.4) has at least one Done item ending in `— test: <path>` that names the test and the failure it catches; a code unit without a source path carries a one-line `test-exempt` reason instead; the unit parser refuses a unit that breaks either half (`invalid`). | S5, M3 | test `engine/unit_test.go` (`TestUnitTestItem`: a behaviour unit without a test item, an exempt unit with a source path) |
| DR-46 | aiwe is built with aiwe. Every standard aiwe applies to the projects it manages applies equally to its own development, with no exemptions. Until the self-hosting point, the lead session and its role-specific agents apply the standard by hand; no external agent runs outside aiwe. Once a capability merges, aiwe itself takes over that step. | S4, M3 | hand-over table and self-hosting point (§14); each hand-over unit has a Done item for it; after the self-hosting point a unit worked outside aiwe is a defect and is recorded as one: CI flags a unit commit on `main` without a `Run-Id` trailer (§12.2, Q17; test `repocheck/run_id_test.go`, unit 1.29); judgement: auditor |
| DR-47 | Every engine diff is `git diff <base>...<tip>` from the run's base commit, so a base that advances during the run changes nothing; a D-check reports only lines of that diff, and a flagged line byte-identical on the base is `pre_existing`, never red; a unit line that counts from `HEAD~<n>` or `HEAD^` is refused. | S4, M3 | tests `engine/coverage_test.go`, `gates/checks_test.go`, `engine/unit_test.go` |
| DR-48 | Every run starts through a sandbox adapter, and the run record names the level used; a live operation unit needs level `os-sandbox` (§6.7, Q19). | S4 | tests `core/sandbox_test.go`, `engine/clone_test.go`, `engine/operation_test.go` |
| DR-49 | Every git call of the engine runs with repository-configured programs off, so a run clone's `.git/config` cannot make the engine run code (§5.21). | S4 | tests `core/git_test.go`, `engine/residue_test.go` |
| DR-50 | Every done, planned or discovered piece of work is an issue in the §8.4 format before work starts, and issues are the single source of truth for work (DR-30); the engine closes a code unit issue at closeout, only after `main` CI of its squash commit is green, and in the same write marks `[x]` each `## Done` item whose evidence is green (§7.3 step 17, Q22; before unit 0.12, §14 row 0.12), an operation unit issue at its own closeout (§7.9 step 9), an owner unit issue once `main` CI of a commit that references it is green and a goal issue once every unit of its task list is closed and a milestone once every issue in it is closed and its phase gate is green (§7.3 step 17). | S5 | intake and `aiwe unit check` (§7.3 step 1, §8.4: only an issue in the §8.4 format is dispatched); closeout (§7.3 step 17, §7.9 step 9); judgement: auditor (a finding outside the unit is a new issue) |

---

## 4. Engineering principles

Only principles with a concrete benefit here.

| Principle | Purpose | Benefit here | Enforced by |
|---|---|---|---|
| KISS | Prefer the simplest design that meets the need. | A unit fits one agent context. | check `repo/size` (§5.9); review |
| YAGNI | Build only what S1–S7 and M1–M7 need now. | No module, rule, phase or CI step without a reason; the rewrite skips 34 old files (§13). | DR-33; DR-42; §15 |
| DRY | One implementation per behaviour. | One ledger schema, not two; one process-control package for gates and adapters (§5.20). | gate steps `lint` (`unused`), `deadcode`; review |
| SSOT | One fact in one place. | Docs cite commands; they never copy generated lists; templates live only in the tool; the module table lives only in `core/modules.go`. | check `repo/rules`; DR-34 |
| SoC | One module, one responsibility. | A tracker change never touches gating code. | check `repo/boundaries` |
| S — single responsibility | One reason to change per module. | §6.1 is the contract. | check `repo/boundaries`; review |
| O — open/closed | Add behaviour by adding a file or a row. | New provider = one adapter file plus one line in `adapters/all.go`, no core edit. | test `adapters/all_test.go` |
| L — Liskov substitution | Every adapter works through its port. | Engine code never branches on an adapter id. | contract suite per port (§5.13) |
| I — interface segregation | Ports stay minimal. | The provider port has 4 methods (§10.2). | review |
| D — dependency inversion | Engine depends on ports, not adapters. | Fakes replace GitHub and harnesses in tests. | check `repo/boundaries` |
| Composition | Combine small functions, structs and interfaces. | No base type to learn per adapter. | the language has no inheritance; review |
| Pure core, imperative shell | Decisions are pure; IO at the edges. | Routing, verdicts and metrics test without processes. | §5.6 |
| Explicit over implicit | Every input is a parameter or a named env variable. | A run is reproducible from its ledger record. | §5.4 |
| Immutability where practical | Records are values passed by copy; the ledger is append-only. | Concurrent readers never see partial state. | test `ledger/store_test.go` |
| Fail-closed | Unknown or unparsable input is refused or `not_measured`. | A bad gate never reads as green; a failed isolation never runs. | DR-13, DR-35 |
| Idempotency | Repeating a command gives the same state. | Crash resume is safe (§7.5); `onboard` and `plan` reruns change nothing; releases never repeat (§12.3). | a "run twice" test per command |
| Least privilege | A run gets only what its role needs. | Agents cannot push or touch the tracker; their environment holds no secrets and no git credential helper; only the release job may write. | DR-18, DR-35; §12.2 |
| Least astonishment | Same flag, same meaning, everywhere. | One parameter dictionary (`core/command.go`). | compiler; test `core/command_test.go` |
| 12-factor config | Config from env and state files, never from code. | One build runs on every machine. | §5.12; check `repo/source-rules` |
| Conventional Commits | Machine-readable commit types. | Version and release notes are derived (§12.3). | commit-msg hook; CI step `repocheck commit-msg` (§12.2) |
| SemVer | Version states compatibility. | Users pin safely. | §12.3 |
| Keep a Changelog | Readable change history. | Users see changes per release in the release notes. | §12.3 |
| OWASP: injection | Never execute or trust text from issue bodies. | A hostile issue cannot run code or steer an agent. | DR-39; §8.4 verify rule; gate step `lint` (`gosec`) |
| OWASP: secrets | No credential stored or logged. | A leaked log holds no key. | §10.1; DR-35; DR-43; contract suite; payload scan |
| OWASP: supply chain | Fewest dependencies, pinned, with provenance. | Users install one file and nothing else. | DR-40; DR-41; actions pinned by commit SHA (§12.2); §12.3 |
| Unicode and locale neutrality | Text is data in any language. | Users and repos in any language work. | DR-27 |
| Accessible CLI output | Output works without colour and in screen readers. | Status words carry meaning, not colour; `--format json` everywhere. | test `core/output_test.go` |

---

## 5. Coding rules and patterns

Each entry: where · shape · benefit · enforcement. Shapes are Go signatures. Package paths are under
`internal/`.

### 5.1 Ports and adapters
Where: provider/harness (§10.2), tracker (§10.1), stack profile (§8.2).
```go
type Identified interface{ ID() string }   // in core; every port embeds it
type Provider interface { Identified; /* port methods: §10.2 */ }
```
The engine holds `[]adapters.Provider`, never a vendor type. Vendor needs (auth, quota detection,
session resume, streaming) live inside the adapter. Benefit: every call site is the same for every
vendor. Enforced: contract suite per port (§5.13); check `repo/boundaries`.

### 5.2 Adapter list and runtime availability
Where: `adapters/all.go`, `stacks/profiles.go`, command registry.
```go
func All() []Provider                                              // the one list of compiled adapters
func Available(ctx context.Context, all []Provider, env core.Env) []Source
```
No provider list in core. API sources need no code: a source row names the `openai-chat` adapter
(§8.1). Benefit: a new harness is one file and one line. Enforced: test `adapters/all_test.go`
fails when an adapter file is missing from `All()`.

### 5.3 Result and error model
```go
type Status string                    // "ok" | "error" | "usage" | "red" | "no_change" → exit 0 1 2 3 4
type Result[T any] struct { Status Status; Outcome Outcome; Data T; Detail string }
type Error struct { Outcome Outcome; Expected, Found, Next string; Err error }  // Unwrap() returns Err
var ErrNotMeasured = errors.New("not_measured")                                   // sentinel example
func (c Command) Run(ctx context.Context, in Input) (Result[any], error)
```
- A red gate or `no_change` is a `Result`, not an error. An `error` means the command could not
  decide.
- Errors are values. A caller adds context with `fmt.Errorf("clone %s: %w", repo, err)` and tests
  with `errors.Is` or `errors.As`, never by text.
- Fixed conditions are sentinels; conditions that carry data are `*core.Error`. The CLI and MCP
  layers map both to the exit code and outcome from one table (`core/outcomes.go`).
- `panic` only for programmer errors, through `core/assert.go`. The CLI and MCP entry points recover
  once, write a crash record and exit 1.
Benefit: CLI exit code, MCP reply and ledger outcome come from one value. Enforced: gate step
`lint` (`errcheck`, `errorlint`, `forbidigo` on `panic`); test `core/errors_test.go`.

### 5.4 Dependency injection by struct field or parameter
```go
type Router struct { Now func() time.Time; Ledger LedgerView }
func (r Router) Route(in RouteInput) RouteOutcome
```
No package-level mutable variable except sentinel errors, compiled patterns and `var version
string` in `cmd/aiwe/main.go`, which the linker sets at release build (§6.3, §12.3). The process
environment is read only in `core/env.go`. Benefit: tests pass fakes; runs are reproducible.
Enforced: check `repo/source-rules` (package-level `var`); gate step `lint` (`forbidigo` on
`os.Getenv`, `os.LookupEnv`, `os.Environ` outside `core/env.go`).

### 5.5 Data tables over branching
Where: stack profiles, adapter capabilities, outcome → exit code, check catalogue, quota classes.
Benefit: a new stack is one row, not a new `if`. Enforced: review; a test that every row is used.

### 5.6 Pure core, imperative shell
Decision functions take and return data. Process, git, HTTP and file IO live in `*_io.go` files or
adapters. Outside `*_io.go`, only `cmd/aiwe/main.go`, `internal/cmd/repocheck/main.go`, `repocheck/source_rules.go` and the `core` files of
units 0.3, 0.20, 0.5, 0.6, 0.18 and 0.21 (§14), with their `_<os>` variants, may import `os` and `os/exec`. Enforced: check
`repo/source-rules` (other files may not import `os`, `os/exec`, `net` or `net/http`).

### 5.7 One command registry
```go
core.Command{Module: "engine", Object: "unit", Verb: "run", Params: []string{"repo", "unit"},
    Run: func(ctx context.Context, in core.Input) (core.Result[any], error) { … }}
```
`Terminal: true` marks a command that approves an irreversible action or changes trust:
`ops approve`, `sources enable`, `artefact submit` and `unit approve-breaking`. Such a command is
never an MCP tool, reads its confirmation from the terminal, and refuses to run when stdin is not a
terminal (`not_a_terminal`). Benefit and enforcement: DR-23.

### 5.8 Naming
Package names short and lower case; files `snake_case.go`; exported identifiers `PascalCase`, others
`camelCase`; outcomes `snake_case` strings from one table; commands `<module> <object>-<verb>`.
Enforced: gate step `format`; check `repo/source-rules` (file names).

### 5.9 Size limits
Source file ≤ 400 lines; test file ≤ 500 lines; non-test function ≤ 60 lines and cognitive
complexity ≤ 12 (Q12); a module's root package holds its public surface only. A test function has
no line limit (Q23). Benefit: a unit fits one context (S5), and no single test package
dominates the test step (DR-38). Enforced: check `repo/size` (`go/ast`, unit 0.9), with the same
limits; complexity by gate step `lint` (§5.17).

### 5.10 Error message format
`<outcome>: expected <x>; found <y>; next: <one action>`. `core.Error` renders it. Benefit: the
reader goes straight to the fix. Enforced: a test that every outcome has a template.

### 5.11 Logging format
One JSON object per line on stderr through `log/slog`'s JSON handler: `{ts, level, runId?, unit?,
event, detail}`. No colour codes. Benefit: logs are machine-read. Enforced: test `core/log_test.go`.

### 5.12 Config precedence
flags > environment variables > state files (`<state>/*.json`) > built-in defaults.
`aiwe config show` prints each value and its origin. Every path parameter resolves against `--repo`;
a relative path without `--repo` is a `usage` error; the process working directory is never used.
The one exception is `--repo` itself: a relative `--repo` resolves once, at the CLI boundary,
against the working directory, so `aiwe gates gate --repo .` works; an empty `--repo` is a `usage`
error.
Enforced: tests `core/config_test.go`, `core/args_test.go`.

### 5.13 Test structure
- A source file `x.go` has its behaviour test in `x_test.go` beside it when one exists, run by
  `go test`. A file covered by another file's test needs no own test file; the covering test names
  it.
- Tests are table-driven: one `[]struct{ name string; … }`, one `t.Run(tc.name, …)` per row. Each
  row name names the failure it catches.
- One contract suite per port, in a non-test package next to the port, run against every
  implementation and every fake:
  ```go
  // package providertest
  func Run(t *testing.T, newP func(t *testing.T) adapters.Provider)
  // package trackertest
  func Run(t *testing.T, newT func(t *testing.T) tracker.Tracker)
  ```
  The provider suite checks: every flag the adapter passes exists in the recorded harness help; no
  credential leaves env; DR-20; DR-43; quota and restriction errors are classified (§7.6).
- Parsers of untrusted text (unit body, intake, `gate.json`, sources) carry a `Fuzz` test; `go test`
  runs its seed corpus.
- Fixtures use realistic values and `t.TempDir()`; tests of owned removal use `core.CreateOwned`.
- Every check ships a red case on a known-bad input.
- The test step follows DR-38 per package: `go test -json` reports each package's duration; the
  step's limit is at least 2 × the longest recorded package and is never divided by the worker
  count; a package whose current duration exceeds half the limit turns the commit gate red, so it is
  split before it can time out. `-p` comes from DR-7.
- Live calls (real harness, real quota) are smoke tests behind the build tag `smoke`, outside the
  gate, run on demand.
Enforced: gate step `test`; test `gates/durations_test.go`.

### 5.14 Text and locale
```go
func ReadText(b []byte) (string, error)            // UTF-8 only; invalid bytes → invalid_utf8
func PassText(text string, to ChildSpec) ChildSpec // long or non-ASCII text → stdin or a UTF-8 file
```
- A Go `string` is bytes. Every input boundary (file, argv, stdin, env, HTTP, git output) checks
  `utf8.Valid` and refuses invalid input (DR-13).
- Case mapping only on fixed ASCII markers. Comparison is byte equality. Sorting uses byte order,
  which equals code-point order for valid UTF-8. Numbers and dates use ISO formats.
- No console code page is read or set. Windows argv and console handling live only in
  `core/process_windows.go`; Go passes UTF-8 argv to Windows children correctly (§17).
Benefit and enforcement: DR-27.

### 5.15 Owned removal
Where: run clones, scratch directories and engine temp directories under the temp root; bare clones
under the cache root (§6.5).
```go
type OwnedDir struct{ path string }   // unexported field: only core.CreateOwned makes one
type OwnedRoot int                    // TempRoot or CacheRoot; no other value exists
func CreateOwned(ctx context.Context, root OwnedRoot, owner string) (OwnedDir, error) // ledger record, then dir + marker
func RemoveOwned(ctx context.Context, d OwnedDir) error                              // checks root, marker, record; retries locks
```
Removal refuses unless all four hold: the real path is under the temp root or the cache root, the
ledger holds an `owned_dir` record for it, the marker file `.owner` matches that record, and no
symlink leaves that root. The state root and every user directory stay outside (Q14). At start, a
temp or cache root (env overrides included) that is, or contains, the user's home, the state root, a
canonical repository or a file-system root is refused (`root_unsafe`). The `owned_dir` record and
the `.owner` marker carry an HMAC-SHA256 under a key in `<state>/owner.key`; the key never enters a
run environment (DR-35), so a run cannot forge a marker from what it is given. Below `os-sandbox` a
run can still read the state root itself (§6.7). Benefit: a user directory cannot reach removal, by
type and at run time. Enforced: DR-25.

### 5.16 Check key
Where: `gates/key.go`.
```go
func CheckKey(step StepSpec, tree TreeDigest, env EnvFingerprint) string   // sha256 of the three
```
Benefit and enforcement: DR-12, §7.8.

### 5.17 Static analysis
The Go compiler with `go vet` caught none of five tested error classes (§17). Gate step `lint` runs
golangci-lint with these linters; each row names the class it closes.

| Class | Caught by |
|---|---|
| non-exhaustive `switch` over an enum type | `exhaustive` |
| nil pointer or nil map dereference | `nilaway` (module plugin; may report false positives, §17; U18) |
| ignored error return | `errcheck` |
| data race between goroutines | `go test -race`, CI step `race` on Linux (§12.2) |
| out-of-range index | no static tool; table tests and fuzz seeds (§5.13) |
| goroutine still blocked at test end | `testing/synctest` (§5.19) |
| error compared with `==`, or wrapped without `%w` | `errorlint` |
| context dropped, stored in a struct, or missing on an HTTP request | `contextcheck`, `containedctx`, `noctx` |
| HTTP response body not closed | `bodyclose` |
| banned identifier outside its one file (`os.RemoveAll`, `os.Getenv`, `panic`, `context.Background`) | `forbidigo` |
| a function too complex to read at once (cognitive complexity over 12, Q12) | `gocognit` with `min-complexity: 12`; chosen over `gocyclo` because a whole `switch` adds 1, so data tables (§5.5) and exhaustive switches stay cheap (§17) |
| security problems in source, such as commands built from data | `gosec` |
| bugs and dead code found by the staticcheck rule set | `staticcheck`, `unused` |
| suspicious constructs reported by `go vet` | `govet` (runs vet's passes, §17) |

### 5.18 Context, cancellation and time limits
```go
func (r *Runner) RunStep(ctx context.Context, step GateStep, dir string) (CheckRecord, error)
```
Every function that blocks, does IO or starts a process takes `ctx` first. A time limit (DR-1,
DR-38, §7.4) is a `context.WithTimeout` at the code that owns the limit. `context.Background()`
appears only in `cmd/aiwe` and tests. A struct never stores a context. Benefit: one cancel stops
every child, request and process of a run. Enforced: gate step `lint` (§5.17).

### 5.19 Goroutines
```go
g, ctx := errgroup.WithContext(ctx)   // golang.org/x/sync/errgroup
g.SetLimit(core.Workers(jobs))        // DR-7
for _, u := range units { g.Go(func() error { return run(ctx, u) }) }
return g.Wait()                       // the first error cancels ctx (§17)
```
Every goroutine has an owner that waits for it; no fire-and-forget `go`. A channel is closed only by
its sender. Tests of concurrent code run inside `synctest.Test`, which panics when every goroutine
of the test stays blocked (§17). Benefit: no leaked goroutine outlives its run. Enforced: gate step
`test` (synctest); CI step `race` (§12.2); review.

### 5.20 Subprocesses and process trees
Where: `core/process.go`, `core/process_unix.go`, `core/process_windows.go`. Gates and adapters
both use it (DRY).
```go
type ChildSpec struct { Argv []string; Dir string; Env []string; Stdin io.Reader }
func Start(ctx context.Context, spec ChildSpec) (*Child, error)  // child in its own group or job
func (c *Child) StopTree(grace time.Duration) error              // ask, wait grace, then kill the tree
```
- `exec.CommandContext` with `Cmd.Cancel` set to `StopTree` and `Cmd.WaitDelay` set, so a child
  that leaves pipes open cannot hang `Wait` (§17).
- Unix: `SysProcAttr.Setpgid`; `StopTree` signals the whole group.
- Windows: the child joins a Job Object (`CreateJobObject`, `AssignProcessToJobObject`,
  `TerminateJobObject` from `golang.org/x/sys/windows`, §17); `StopTree` terminates the job.
  Assignment before the child can start its own children: U22.
- Batch launchers (`.cmd`, `.bat`): Go quotes argv for `CommandLineToArgvW`, which `cmd.exe` does
  not follow (§17). The code builds `SysProcAttr.CmdLine` itself and refuses an argument it cannot
  quote safely.
- Program lookup follows `exec.ErrDot` (§17).
Dependencies: the standard library and `golang.org/x/sys` only. Process groups are in `syscall`;
Job Objects are in `golang.org/x/sys/windows`. Benefit: DR-15 on every OS. Enforced: test
`core/process_test.go` on three OS (§12.2).

### 5.21 Git calls
Where: `core/git.go`. Every git call of the engine and of `repocheck` goes through it (DR-49).
```go
func Git(ctx context.Context, dir string, args ...string) (GitResult, error) // starts git through §5.20
```
- Fixed options before every subcommand: `-c core.fsmonitor=false`, `-c core.hooksPath=<an empty
  engine-owned directory>`, `-c protocol.ext.allow=never`, `-c submodule.recurse=false`.
- `diff`, `log` and `show` also get `--no-ext-diff --no-textconv`.
- The `file` protocol serves only the engine's own clone of the canonical repository (§6.5).
These keys and flags are git's own (§17). Benefit: an agent that writes `diff.external`,
`core.fsmonitor` or a hook into its clone's `.git/config` cannot make the engine run it. Enforced:
tests `core/git_test.go`, `engine/residue_test.go`.

---

## 6. Architecture

### 6.1 Modules and packages

Each module is a package tree under `internal/`. Its root package is the entry; private packages
sit under `internal/<m>/internal/`, which the Go compiler refuses to import from outside the
module. Declared edges live in `core/modules.go`; check `repo/boundaries` compares them with
`go list -deps -json ./...` (DR-24). Rejected: depguard, because its allow-lists would repeat the
table in the lint config (SSOT).

| Package | Single responsibility | May import | Serves |
|---|---|---|---|
| `internal/core` | Command contract, result and errors, params, output, env, config, log, text, platform roots, capacity, process control, git calls, owned removal, sweep, footprint, measured values, identities, sandbox port. | — | S1, M7 |
| `internal/stacks` | Stack profile table, detection, gate derivation. | core | S3 |
| `internal/gates` | Gate runner, check cache, diff checks D1–D18, payload scan. | core | S4, S6 |
| `internal/roles` | Role cards (embedded), card schema, prompt contract assembly. | core | S3, S7 |
| `internal/adapters` | Provider and harness adapters, adapter list, API agent loop. | core | S1 |
| `internal/tracker` | Tracker port, the GitHub adapter (issues, CI status) and intake filtering. | core | S5 |
| `internal/ledger` | Append-only records, rotation, metrics. | core | M1–M7 |
| `internal/engine` | Flows (§7), run isolation, claims, sources, synthesis. | all above | S2 |
| `internal/mcp` | MCP server generated from the command registry, with self-description. | core, ledger | S7 |
| `internal/repocheck` | This repository's own checks (`repo/*`, commit message, CI changes and report). Never shipped. | core | S4 |
| `cmd/aiwe` (front) | CLI and the `mcp` subcommand; wires the registry. | root packages above, except `repocheck` | S7 |
| `internal/cmd/repocheck` | Entry point of `repocheck`. | core, repocheck | S4 |
| `internal/e2e` | End-to-end smoke tests (build tag `smoke`) that drive the built binary as a child process. Never shipped. | core | M2 |

### 6.2 Runtime and distribution — decided

Decision (owner, 2026-10-10): Go, one statically linked binary per target. Targets: windows, darwin
and linux, each on amd64 and arm64. The old TypeScript code is a behaviour reference only (§13).

Measured basis (zero-install survey, §17): of the measured candidates, the Go MCP binary is the
smallest, answers MCP `initialize` fastest, idles with the least memory, and cross-compiles from
one host to every target without an extra download.
Rejected, with the measurement in §17: Node single executable (larger binary, slower start, macOS
signature lost in cross-build); Bun compiled binary (automatic cross-compile failed on all six
targets); Deno (not measured); TypeScript through `npx` (needs Node, so it breaks DR-40).

Build: GoReleaser builds with `CGO_ENABLED=0` and `-trimpath`; the version comes from the tag only
(§12.3). No source file is architecture-specific (check `repo/source-rules`), so the three-OS test
matrix compiles every code path that differs. Targets that no CI runner runs: U21.

| Channel | User needs | Built by | Why it exists |
|---|---|---|---|
| GitHub Releases: six archives, `checksums.txt`, one attestation | a browser or `curl` | GoReleaser (§12.3) | the only channel that needs no user tool (§17) |
| MCP Registry, package type `mcpb` | an MCP client | release job, from `server.json`; bundle and publish tool: U20 | MCP users install from their client; clients check the file hash (§17) |
| winget | App Installer, shipped with Windows 11 (§17) | GoReleaser OSS manifest and pull request (§17) | Windows installs and updates without a new tool |
| Scoop | Scoop | GoReleaser OSS bucket manifest (§17) | owner decision; Windows users who manage tools with Scoop |
| Homebrew tap | Homebrew | GoReleaser OSS cask; formulas are deprecated there (§17) | macOS and Linux users; unsigned until Gate 3 (Q7) |
| npm (optional, off) | Node and npm | not GoReleaser OSS: npm publishing is Pro (§17) | convenience only; trigger in §15 |

Unit 3.6 configures GitHub Releases, winget, Scoop and Homebrew; unit 3.9 configures the MCP
Registry entry. No channel publishes before unit 3.10 is closed.

External tools:
- **git is the only prerequisite.** The binary runs `git --version` at start; missing →
  `git_missing` (exit 1, next action: install git); older than the minimum (U12) → `git_too_old`
  (exit 1, next action: update git). Reason (§17): the pure-Go git library has no rebase and no
  patch apply, and merges only by fast-forward.
- **No GitHub CLI.** GitHub is reached over HTTPS with the standard library (§10.1).
- Harnesses are sources the user may have, not prerequisites (§8.1). A managed project's own tools
  are used only when present (§7.1).
- Development and CI tools (§12.2) are never user prerequisites.

MCP layer: the official Go SDK `github.com/modelcontextprotocol/go-sdk`, pinned in `go.mod`. It
offers server instructions, prompts, resources, resource templates and a stdio transport (§17).
Reason: protocol conformance is the SDK's job. Dependency policy: DR-41.

### 6.3 CLI and MCP

- One command registry feeds both surfaces (DR-23). Long commands run detached and return a run
  id; status is a separate command. `aiwe runs list` shows every live run, foreground or detached;
  `aiwe runs stop --run <id>` stops its tree (DR-15), records `stopped_by_owner`, returns the unit to
  the queue and charges no attempt.
- The MCP server speaks stdio. `aiwe mcp snippet` prints the harness config entry that calls the
  installed binary by name with the argument `mcp`, never a file path. Registration is one-time and
  global; no project needs a step (DR-8).
- Terminal-only commands (§5.7) are absent from the MCP tool list.
- `aiwe version` prints the version GoReleaser injected at build time (§12.3); a build without one
  prints `unknown`.
- Any command that targets a repository without a stored gate runs onboarding first (§7.1).
- Self-description (DR-34):

| MCP element | Content | Built from |
|---|---|---|
| `instructions` | ≤ 40 lines: what the tool does, which tool serves which job, the unit rules in one line each, and the resolved state, cache and temp roots with their origin (§5.12), so a server started before an env change shows it. | command registry; §3; §6.4 |
| `prompts` | `plan-goal` (goal → units), `open-unit` (write one unit issue), `audit-unit` (audit from evidence), `owner-brief` (answer a brief). | role cards (§9.1); §8.4 |
| `resources` | `unit-template` (§8.4, its only definition), `goal-template`, `roles/<id>`, `quality-standard` (§3, §11), `projects/<repo-id>/gate`. | engine data |
| `tools` | Every command except the terminal-only ones (§5.7). `unit check` validates an issue body with the intake parser. | command registry |

Unit authoring paths: §7.2. SDK support for each element: §17.

### 6.4 Disk layout

Three roots. `core/platform.go` resolves them at run time; first hit wins per cell (DR-37).

| Root | Holds | Windows | macOS | Linux and other POSIX | Override |
|---|---|---|---|---|---|
| state | `sources.json`, `sources.state.json`, `ledger.jsonl` and its rotations (§8.5), `owner.key` (§5.15), `projects/<repo-id>/` (stored gate, artefacts, `operations.json` of §7.9) | `%LOCALAPPDATA%\aiwe\state` | `~/Library/Application Support/aiwe` | `$XDG_STATE_HOME/aiwe`, else `~/.local/state/aiwe` | `AIWE_HOME` |
| cache | bare clones of repositories onboarded by URL (§6.5) | `%LOCALAPPDATA%\aiwe\cache` | `~/Library/Caches/aiwe` | `$XDG_CACHE_HOME/aiwe`, else `~/.cache/aiwe` | `AIWE_CACHE` |
| temp | run clones, scratch, child result files | `%TEMP%\aiwe` | `$TMPDIR/aiwe` | `$TMPDIR/aiwe`, else `/tmp/aiwe` | `AIWE_TMP` |

Env variable names derive from the product constant. `repo-id` comes from the normalised remote URL
(`host/owner/repo`), never from a local path. Stored paths are relative and `/`-separated.

Ceilings (default) and cleanup (DR-36):
- state ≤ 64 MB and ≤ 1 000 files; cache ≤ 2 GB and ≤ 2 000 files; temp: 0 directories without a
  live owner.
- Before a clone, free disk ≥ 2 × the last measured run-clone size, else `no_disk`.
- Automatic cleanup order: ledger rotation (§8.5), then cache eviction: bare clones, least recently
  used first, each removed through `core.RemoveOwned` (§5.15). Still over a ceiling →
  `footprint_exceeded` (§7.4).
- Every exit path of a run removes its temp directory; the sweeper (§6.5) covers crashes.

OS differences, all inside `core/platform.go` (processes: §5.20):
- Windows path length: temp names stay short (`<temp>/r/<pid>-<time>`); the clone sets git's
  long-path option (effect: U14).
- Case: file sets compare exact case; two unit paths that differ only by case → `invalid`.
- Line endings: clones set `core.autocrlf=false`, so bytes match the canonical repository (DR-27).
- File locks: removal retries 5 times, 200 ms apart (default); still locked → `residue_locked`,
  and the next sweep retries.
- Optional accelerators (copy-on-write clones, developer volumes): used only when present and when
  a measurement shows a gain; otherwise the standard path, without error. The chosen path is
  printed.
Proof: contract tests run on three OS (§12.2).

### 6.5 Run isolation

Serves DR-35, DR-16.
- **Canonical repository**: the local path given at onboarding; for a URL, a bare clone in the
  cache root, made through `core.CreateOwned` (§5.15). Before each batch the engine fetches the remote into it. The fetch writes only
  remote-tracking refs and objects; the user's checked-out branch and files are never touched.
- **Clone**: `git clone --depth 1 file://<canonical>` into `core.CreateOwned` (§5.15), at the base
  commit (method: U10). No worktree, no persistent pool.
- **Scratch**: `<run dir>/tmp`; `TEMP`, `TMP` and `TMPDIR` point there.
- **Environment**: the parent environment minus every secret-shaped variable (every source's
  `credentialEnv`, tracker tokens, names that contain `TOKEN`, `SECRET`, `KEY` or `PASSWORD`, names
  that end in `_PAT` or `_PASS`, `NPM_CONFIG__AUTH`, `GOOGLE_APPLICATION_CREDENTIALS`, and a `_URL`
  variable whose value may carry a password; names compare without case). A `_URL` variable is
  dropped when its value has a `:` after a `://` and an `@` after that `:`; the check is
  fail-closed and drops such a value even without a password. A value without `://`, such as an
  scp-style git address, is not checked. The
  adapter adds back only its own source's credential. It also drops the parent shell's identity
  variables (listed in the adapter) and starts the harness with user-level rules, memory, hooks and
  plugins off, through the harness's documented flag or an isolated config home that keeps the
  login (U28). `HOME` points to the run's scratch directory. `GIT_CONFIG_NOSYSTEM=1` and an
  environment-set `credential.helper` with an empty value clear git's credential helpers, and
  `GIT_TERMINAL_PROMPT=0` stops git from asking (§17). `GIT_CONFIG_GLOBAL` points to the null
  device, so no user-level git setting such as `core.askPass` applies (`core/env.go`). Push stays the engine's (DR-18). These are
  cheap measures, not a sandbox (§6.7).
- **Check environment**: every check process (unit verify, gate step, the restore command) gets the
  same secret-free environment from `core/env.go` (DR-35); only an operation's own steps get a
  secret (§7.9).
- **Restore**: the stack profile's restore command (§8.2) inside the clone, using the package
  manager's own user-level cache. No junction, hardlink or shared dependency directory.
- **Collect**: after the run, the engine fetches the run branch into the canonical repository as
  `refs/aiwe/runs/<run-id>`, records residue (uncommitted paths and diff stat, DR-47) in the run
  record, and removes the clone. Path lists are read from git's output, never passed as arguments.
- **Isolation level**: files and git state are isolated per run; processes and network are confined
  only as far as the run's sandbox level reaches (§6.7).
- **Sweeper**: at engine start and after every run, it collects each dead run's clone as above
  (DR-16), then removes temp directories whose owner process is dead, under the rules of §5.15 (pid
  reuse: U13).
- Measured (fact, isolation survey, one Windows machine, 504-file repository): shallow `file://`
  clone 1.27–2.42 s and 7.9 MB; removal 0.35–0.74 s. Rejected: worktrees share refs, config and
  hooks with the main repository (git documentation; a config write leaked in the measurement);
  `--shared` as default, because pruning in the source can break the clone (git documentation).
  Other OS and large repositories: U17.

### 6.6 Device and OS

Capacity: DR-7 (cgroup limits on Linux). Commands: argv arrays (§8.3). Processes, process trees and
Windows batch launchers: §5.20. Coverage: §12.2.

### 6.7 Sandbox adapter

Serves S4. Decision: Q19. Rule: DR-48.
```go
type SandboxLevel string // "env-scrub" | "harness-sandbox" | "os-sandbox"
type Sandbox interface { Identified; Level() SandboxLevel; Wrap(ctx context.Context, spec ChildSpec) (ChildSpec, error) }
```

| Level | What it confines |
|---|---|
| `env-scrub` | Nothing beyond §6.5: no secret and no git credential helper in the environment. The agent runs with the user's rights. |
| `harness-sandbox` | What the harness's own documented sandbox enforces, switched on by the adapter. |
| `os-sandbox` | File writes outside the run clone, network beyond the adapter's policy and reach into other processes, enforced by the OS without elevated rights (DR-37). |

- The MVP ships `env-scrub` only. Every run record names the level used (`Sandbox`, §8.5).
- A stronger level comes after Gate 1 (§15). `os-sandbox` is a precondition of the first live
  operation (Phase 4); a fixture resource may run at `env-scrub` (§7.9).
- An external sandbox tool may implement the adapter if it meets the contract: the level it reports
  holds on all three OS without elevated rights, and the contract tests prove it.
- Open risk until then: an agent can read and change what the user can, as with other coding agent
  tools. README and SECURITY state it (§12.1).

---

## 7. Core flows

`[M]` = mechanical. `[J]` = judgement by an agent. `[O]` = owner.

### 7.1 Onboarding — automatic, or `aiwe onboard --repo <path-or-url>`

1. `[M]` Resolve `repo-id`, canonical repository (§6.5), working branch and CI presence. The working
   branch is the remote default branch, or `--branch <name>`, stored with the gate under the input
   hash (DR-5); integrate pushes only the stored working branch.
2. `[M]` Project `gate.json` present → use it (DR-8), go to step 5.
3. `[M]` Detect stacks from profile rules (§8.2). Outcomes: `stack_detected`; `multi_stack` (§15);
   `unknown_stack` (§8.2, §7.7); `detect_error` (named, red).
4. `[M]` Derive gate steps from the profile: only steps whose tool exists and whose config is present.
5. `[M]` Prove the gate on the base commit, in a clean clone. Each step gets
   `green | red_at_base | not_measured` and a measured duration (§7.8). A `red_at_base` step is
   stored as `known_red` and reported; it does not block onboarding.
6. `[M]` Store gate and proof under `projects/<repo-id>/` with the input hash (DR-5).
7. `[M]` Check tracker access. Normalise existing issues (§8.4).
A second run with unchanged inputs prints `no_change`.

### 7.2 Planning — `aiwe plan --repo R --goal <issue>`, or the user's own session

A goal becomes units on one of two paths. Both use the `unit-template` resource (§6.3), and the same
parser (§8.4) validates every unit before work starts.
- **architect path**: `aiwe plan` runs steps 1–5 below.
- **session path**: the user's own session writes each unit issue from the template and validates
  it with `unit check`; intake parses it again (§7.3 step 1). Steps 1–5 do not run.

1. `[J]` An `architect` run reads the goal issue and the repo and returns unit texts.
2. `[M]` Each text is parsed (§8.4). A failed parse returns to the architect once with the
   findings.
3. `[J]` An `auditor` run (DR-9) audits the plan once per goal: the split
   against the goal's Why and Outcome, file-set overlap, units of the goal that touch the same
   files and fit together within DR-11 (each run pays a fixed overhead, M4), and every
   `## Behaviour change`. The plan
   is a rung-2 artefact (DR-4). A return goes back to the architect once.
4. `[M]` A second parse failure or second return stops with `plan_invalid` and an owner brief on
   the goal (§8.4).
5. `[M]` Valid units become issues; the goal body gets a task list `- [ ] #N`. Idempotent: an
   existing entry is never duplicated; a rerun on an unchanged goal prints `no_change`.

### 7.3 Unit lifecycle — `aiwe work --repo R [--goal G]`

| Step | Kind | What happens |
|---|---|---|
| 1 intake | M | Read open issues through the intake filter (DR-39); parse (§8.4); skip `runnable: owner`. A unit waits while an earlier milestone or a `blocked-by` issue is open (§8.4). |
| 2 claim | M | Create `refs/work/claim/<issue>` atomically on the remote; a lost race = skip. Units whose file sets intersect never run together. Create the unit's status comment (§8.4). |
| 3 already done | M | Run the unit verify in a clean clone of the base commit. Green and done criteria met → close `completed` with evidence. Ambiguous → `scout` run [J]. |
| 4 route | M | Resolve the unit's roles (§9.1); none → `role_unresolved`. Pick a source per role (§8.1). |
| 5 isolate | M | Make the run clone (§6.5). Failure → `isolation_failed`; the run does not start. |
| 6 test-author | J | Writes the unit's tests on the run branch. The engine runs the unit verify on that commit; it must be red (red proof). |
| 7 engineer or doc-writer | J | Implements, or updates the document (§9.1); asks for the unit verify through `aiwe verify` (§7.8); commits on the run branch. |
| 8 collect | M | Collect the run branch, record residue, remove the clone (§6.5). |
| 9 gate | M | Stored gate on a clean clone of the tip; steps with a recorded result for their key are reused and listed (§7.8). |
| 10 diff checks | M | D1–D18 with the stack profile's patterns, over `<base>...<tip>` (§11, DR-47). |
| 11 mutation | M | Mutation run on changed lines where the profile names a tool; else `not_measured`. Evidence only (§11). |
| 12 audit | J | `auditor` (DR-9) compares done criteria with the diff, using only engine-made evidence (§9.2). `security-reviewer` and `ux-reviewer` run beside it when their path tables match (§9.1). Each verdict comes from the judging run's own result, bound to its RunID (DR-9); integration needs every verdict GREEN; a `not_measured` dimension is recorded in the status comment and the ledger. Before a RED verdict is acted on, the engine resolves every `path:line` and identifier it cites on the tip; an unresolved citation makes the verdict `unverified_finding`, sent to a new auditor run, never to the writer. |
| 13 fix loop | J | Returned or red → engineer fix round, up to the fix-round limit (§7.4), then `quarantined`. A repeated red signature calls the `debugger` (§7.4). |
| 14 integrate | M | DR-19 at pull-request level (Q21). The engine pushes the run branch and opens one pull request per unit through the tracker port: title = `<type>(<scope>)` of the unit's first non-test commit, then `: `, then the unit issue's title with its first letter in lower case; body carries `Refs #<n>` for the unit issue (§8.4) and ends with the `Run-Id` trailer. A batch = several unit pull requests, gated together in a temporary integration clone of the working branch tip. A unit commit marked breaking waits for the owner (§12.3). Batch states: `queued → running → splitting → merged \| removed`. A red batch splits in halves until each red pull request is isolated; a removed one stays open with its reason in a comment and returns as a fix round. A conflict follows §7.4. |
| 15 pull request CI | M | Wait for the CI run of each pull request head through the tracker port. Red → the pull request stays open with its reason; the unit returns as a fix round. No run observed within the wait limit → `ci_not_observed`: never green. |
| 16 merge and main CI | M | Green CI and head = `AuditedTip` → the engine squash-merges the pull request through the tracker port, giving the head SHA, so a moved head is refused (`unaudited_commits`). The canonical repository's checked-out branch is never a push target. A merge refused because `main` moved rebuilds the batch on the new tip once; a second → `push_rejected`, batch requeued. The engine then waits for the CI run of the squash commit on `main`, which also feeds the release job (§12.3): red → the engine opens a revert pull request for the squash commit through the tracker port, never a direct push, because the `main` ruleset refuses a change without a pull request (§16, Q21); it squash-merges the revert pull request once that pull request's CI is green, then requeues the unit as a fix round; a red revert pull request CI → `revert_red` (§7.4); none within the wait limit → `ci_not_observed`, the issue stays open. |
| 17 closeout | M | Only after `main` CI of the squash commit is green (step 16), close the unit issue `completed`; a red `main` CI or `ci_not_observed` leaves it open (DR-50). The same write that closes the issue marks `[x]` each `## Done` item whose evidence is green (Q22). Before unit 0.12 brings CI, the §14 row 0.12 applies. The status comment's final state is the evidence (gate summary, pull request and CI links, Run-Id). Delete the claim ref, run refs, the run branch and the integration clone. On each pass, close `completed` every open `runnable: owner` unit issue that a commit on `main` references once `main` CI of that commit is green; a red `main` CI leaves it open. Then close `completed` every goal issue whose task-list units are all closed (DR-50). Then close every milestone whose issues are all closed and whose phase gate (§14) is green; a gate that is not green leaves the milestone open (DR-50). |
| 18 ledger | M | Append the record (§8.5); run the escaped-defect scan (M3) and the footprint check (§6.4). |

### 7.4 Failure paths

A **red signature** = step id + sorted failing test ids (when the runner reports them) + exit code
+ first error line, with digits, hashes and temp paths normalised.

| Event | Outcome | Engine action |
|---|---|---|
| Onboarding detects two or more stacks | `multi_stack` | Onboarding stops with this named outcome (§7.1 step 3, §15). |
| Onboarding detects no known stack | `unknown_stack` | The stack profile comes from the adaptation ladder (§7.7, §8.2). |
| Stack detection fails | `detect_error` | Red, reported by name (§7.1 step 3). |
| A plan fails its parse or audit a second time | `plan_invalid` | Planning stops; owner brief on the goal (§7.2 step 4, §8.4). |
| No adapter can run rung 2 of the adaptation ladder | `artefact_requested` | MCP reply to the lead session; the owner enters the answer with the terminal-only `aiwe artefact submit`, then the same verify and proof (§7.7, §5.7). |
| Unit text invalid | `unit_invalid` | Label `invalid` + one comment with the findings. |
| Product decision needed | `owner_decision` | Label `question`; owner brief; ledger `owner_action`; other units continue. |
| Gate red, agent fault | `red` | Fix round. |
| Same red signature in two consecutive rounds | `repeated_red` | A `debugger` run (§9.1). Its root cause enters the next fix round if one remains, else the owner brief. The same signature after that round → stop the fix loop, label `help wanted`, owner brief. |
| Gate red, environment fault (missing tool, network, timeout of the environment) | `environment` | Retry once in a new clone with a recorded `rerunReason`; still red → `not_measured`; no fix round is charged to the agent. |
| Isolation failed or disk short | `isolation_failed`, `no_disk` | Run not started; the unit stays queued; a second failure → `environment`. |
| Run over its run limit, or silent past its silence limit (both below) | `timed_out`, `stalled` | Stop the tree (DR-15); evidence per DR-38; counts as an attempt. |
| Observed model differs from the requested one | `model_mismatch` | Red; the unit is requeued on another source; not charged. |
| Source account restricted | `account_restricted` | Source → `restricted` (§7.6); the run returns to the queue; not charged; owner brief. |
| No active source can serve a role | `no_capacity` | The unit waits; other roles continue (§7.6, §7.9). |
| git missing or too old at start | `git_missing`, `git_too_old` | Exit 1 with the next action (§6.2). |
| Rebase or merge conflict at integration | `conflict` | A resolution run on the same run branch, rebased onto the working-branch tip; the prompt names the conflicted files. Two failed resolutions → `skipped`, requeued (Q11). |
| Two failed conflict resolutions on one unit | `skipped` | Unit requeued (Q11). |
| Three failures on one unit | `quarantined` | Stop offering the unit; label `help wanted`. |
| First 5 units of a goal (default) have M1 < 70 % | `goal_paused` | Stop dispatching the goal's units; owner brief on the goal; label `question`. |
| A revert pull request's CI is red after a red `main` CI (§7.3 step 16) | `revert_red` | Stop the queue: no unit is dispatched or merged; owner brief (§8.4). |
| No CI run of a pull request head or of the squash commit observed within the wait limit | `ci_not_observed` | Never green; after the squash merge the unit issue stays open (§7.3 steps 15–17). |
| A merge refused a second time because `main` moved | `push_rejected` | Batch requeued (§7.3 step 16). |
| Run process killed or lost | `lost` (§8.6) | Unit returns to the queue. The record closes with the time since `StartedAt`, marked as an upper bound, and the collected tip; never 0 ms. |
| Ledger not writable | `ledger_unavailable` | No dispatch (fail-closed). |
| Prompt over its cap | `prompt_too_large` | Label `invalid` with the measured size (§9.2). |
| Footprint over a ceiling after cleanup | `footprint_exceeded` | Red; dispatch stops until under (§6.4). |
| No role resolves for the unit | `role_unresolved` | Label `invalid` with the resolution inputs (§9.1). |
| Operation plan misses a step, or plans a delete without a retention policy | `operation_incomplete`, `operation_refused` | Not started; label `invalid` with the finding (§7.9). |
| Operation after-measurement or audit red | `rolled_back` | Rollback command run and measured again (§7.9); owner brief. |
| A permission request the harness policy does not answer | `permission_blocked` | Run stopped, not charged; owner brief (§10.2). |
| API reply cut by the provider's output limit | `answer_truncated` | An attempt; never a finished run (§10.2). |
| API run over its token limit (below) | `token_limit` | Stop; an attempt. |
| Branch tip differs from `AuditedTip` | `unaudited_commits` | Refused; names the commits of `rev-list <audited>..<tip>` (DR-19). |
| Owner stops a run | `stopped_by_owner` | Unit back in the queue; not charged (§6.3). |
| A unit verify or test step collects zero tests | `no_tests_collected` | Red; a fix round (DR-13, §7.8). |
| A branch commit carries a `VERDICT:` line | `verdict_in_branch` | Refused; the verdict comes only from the judging run (DR-9). |
| A RED verdict cites a `path:line` or identifier that does not resolve on the tip | `unverified_finding` | Sent to a new auditor run, never to the writer (§7.3 step 12). |
| A unit commit is marked breaking (`!` or `BREAKING CHANGE`) | `breaking_change` | Not integrated; label `question`; owner brief; the owner answers with the terminal-only `aiwe unit approve-breaking` (§12.3). |
| An approval expired, or the before-state changed since approval | `approval_expired`, `before_state_changed` | Not applied; a new plan check and approval (§7.9). |
| A terminal-only command runs without a terminal | `not_a_terminal` | Refused (§5.7). |
| A temp or cache root contains home, state, a canonical repository or a file-system root | `root_unsafe` | Exit 1; nothing is removed (§5.15). |
| A directory's `owned_dir` record or `.owner` marker does not prove that aiwe made it | `not_owned` | Refused; the directory stays (§5.15). |
| A file in an owned directory stays locked after the removal retries | `residue_locked` | The directory stays; the next sweep retries (§6.4). |
| Text is not valid UTF-8 | `invalid_utf8` | Refused; the detail names the first invalid byte (§5.14). |

**Limits derived from the ledger** (DR-38). Each value is printed with its basis. Until its sample
exists, the default holds. Measured basis: §17, the previous engine's run ledger.
- Run limit per role, source and model: max(30 min, 1.5 × p90 of its green run durations) once 20
  green runs exist; before that, the role's p90 over all sources once it has 20; else 60 min. It
  covers the agent run only; checks have their own limits (§7.8).
- Silence limit per source: max(5 min, 2 × p99 of the longest output gap in its green runs) once 20
  exist; else 10 min. Time inside an `aiwe verify` call counts as output.
- Fix rounds per role: 2 until the role has 50 integrated units; then the smallest r for which
  round r + 1 added under 10 % of the role's greens over its last 50 units, minimum 1, recomputed at
  each closeout.
- Token limit per API run, by role and source: 2 × the largest input token count among its green
  runs once 20 exist; none before. A constant token ceiling is never used.

### 7.5 Resume

All state lives in remote refs, the tracker and the ledger. `work` after a crash reconciles:
open run records without a result → `lost`; claim refs with no live run → released; temp
directories with a dead owner → collected, then removed (§6.5). A run ref without an open run
record is deleted once its unit's issue is closed; while the unit is open, quarantined or skipped,
the ref stays (DR-16).

### 7.6 Quota exhaustion and account restriction

1. `[M]` The adapter classifies the error into one class below, from structured fields where the
   provider gives them, never from message text alone; a harness that gives only text gets a
   per-adapter rule under its contract suite.
2. `[M]` Quota classes: source → `passive` with its class and reset time (§8.1).
   `account_restricted`: source → `restricted`. A restricted source is never probed and never
   routed. The engine writes ledger `owner_action` and an owner brief on the unit; `sources show`
   lists it. Only the owner returns it with the terminal-only `aiwe sources enable <id>` (§5.7).
3. `[M]` The run returns to the queue and does not count as an attempt.
4. `[M]` Reactivation per class; a probe uses the adapter's cheapest health call.
5. No active source can serve a role → the unit waits with `no_capacity`; other roles continue.

P = probe interval, 30 min (default).

| Class | Detected by (fact examples, §17) | Passive until | Probe |
|---|---|---|---|
| `rate_window` | HTTP 429 with `retry-after` or a reset header | the stated time | none |
| `spend_cap` | a spend-limit error code | the stated reset; else `unknown` | every P when the reset is unknown |
| `balance` | HTTP 402 or a balance error with a limit source | a successful probe | every P |
| `subscription_window` | the harness's usage-limit result | `unknown`: remaining quota is not readable | every P |
| `account_restricted` | HTTP 401 or 403, or the provider's documented suspension or deactivation code (per provider: U25) | the owner enables it | never: a probe could deepen a ban (§17) |

A single text match never sets `restricted`. An unclassified error is `unknown_error`: not quota,
handled as an environment fault (§7.4).

### 7.7 Adaptation ladder — one generic mechanism (DR-3, DR-4, DR-5)

Used for: gate steps, stack profile for an unknown stack, adapter config, quota/limit detection
rules, unit decomposition (§7.2).
```
need(kind, inputs) -> hash(inputs)
  stored artefact with the same hash, verified?      -> reuse                        [M]
  rung 1: mechanical derivation succeeds?            -> prove -> store               [M]
  rung 2: synthesis unit (architect or engineer by kind)                             [J]
          -> verified by an auditor run (DR-9)                                       [J]
          -> proved: gate on base commit, or adapter dry run                         [M]
          -> store {artefact, inputHash, producerRunId, verifierRunId, evidence}
     no adapter can run rung 2 -> MCP reply `artefact_requested` to the lead session;
          the owner enters the answer via the terminal-only `aiwe artefact submit`
          (§5.7), then the same verify + proof
  rung 3: product decision -> owner (label `question`, owner brief)                  [O]
```

### 7.8 Check execution (DR-12, DR-38)

- **Key** (§5.16): argv + digest of the step's declared inputs, taken from git objects + env
  fingerprint (OS, architecture, version output of the step's first token, lock file digests).
- **Safe default**: a step without declared `inputs` uses the whole tracked tree. Shallow history or
  unknown inputs mean the step runs; nothing is ever assumed unchanged.
- **Rerun of the same key**: only after an environment-class failure (§7.4), once, with
  `rerunReason` recorded. Red-to-green flips are counted (M6).
- **Time limit** per step: max(60 s, 3 × p90 of the step's last 20 green durations), from the base
  proof alone until 20 exist (default). A project `gate.json` may set its own. A step whose p50
  doubles within 7 days is a `gate_slowdown` finding.
- **Kill evidence**: the running step and, where the runner reports them, the running test files
  or packages by name; machine load; the process table of the run's tree. Output streams to the
  evidence file while the step runs, so a kill never erases reported results.
- **Partial run**: `gates gate --step a,b` runs named steps only; its output says "partial" and
  never "gate green" (DR-10).
- **Zero tests**: a test step or unit verify whose runner reports zero tests run is red
  (`no_tests_collected`, DR-13), even at exit code 0. The stack profile names the runner's
  zero-test signal (`NoTests`, §8.2); Go prints `[no tests to run]` and exits 0 (§17). Until unit
  1.4 merges, the lead session applies the same rule by hand.
- **Early diff checks**: each `aiwe verify` call also runs D1, D3, D4, D7, D10, D11 and D16 on the
  run's `<base>...HEAD` and returns their findings with the verify result, so a violation goes back
  to the agent while it runs.
- **Roots and environment**: every check runs with the state, cache and temp roots (§6.4) pointed at
  one fresh owned directory and in the secret-free check environment of §6.5; an entry left there
  turns the check red, so no test writes into real state.
- **Concurrent gates**: DR-7 sets the count; the wait for a gate slot is recorded apart from the
  gate's duration.

Who runs what (owner decision):

| Who | Does | Runs checks? |
|---|---|---|
| engineer | writes code | No. Asks for the unit verify through `aiwe verify`; the engine runs and records it. When the last recorded verify's tree digest equals the final tree, that run is the evidence. |
| test-author | writes tests | No. The engine makes the red proof (§7.3 step 6). |
| engine | runs each commit's changed scope once | Yes. One evidence run per key, bound to the commit. Reused steps are printed by id and count; nothing is skipped silently. |
| auditor | compares the request with the diff; looks at boundaries and security | No. Reads the evidence, including mutation results (§11). |
| full gate | one full measurement before push (per batch) and before release (CI) | Only steps whose key changed. Per-test dependency maps: §15. |

Agents running checks directly: forbidden by the prompt contract (§9.4); adapters add a
harness-level command restriction where the harness supports one (U15).

### 7.9 Operation unit lifecycle

Serves S4, M2. An operation unit (`kind: operation`, §8.4) changes one resource. The operator plans;
the engine executes. The agent never reaches the resource and never sees a secret.

| Step | Kind | What happens |
|---|---|---|
| 1 intake, claim, route | M | As §7.3 steps 1–4. The operator and the auditor go only to `opsTrusted` sources (§8.1); none → `no_capacity`. |
| 2 before-state | M | The engine resolves the unit's `## Access` and `## Measure` ids in the project's `operations.json` (below); through that access method it runs the measure command and reads the `## Scope` items. A `(secret)` item shows key names and permissions only. |
| 3 plan | J | The operator returns the plan in fixed order: scope · backup command and path · rollback command · change and its syntax-check command · apply command · measurement command. |
| 4 plan check | M | Every field present and in order, else `operation_incomplete`. A command in the delete pattern table (default: removal of files, rows, tables, volumes or buckets) without a `## Retention` line → `operation_refused`. The engine records the plan digest: SHA-256 over the plan, the resolved access method, `## Scope`, the secret names and the before-state measurement. |
| 5 approval | O | Red line (DR-1). The owner brief carries the plan, the before-state and the digest. The owner runs the terminal-only `aiwe ops approve --repo R --unit N` (§5.7) and types the digest at its prompt; a digest passed as an argument, a stdin that is not a terminal or an MCP call is refused. An approval lasts 24 h (default); a changed plan or an expired approval needs a new one (`approval_expired`). Tracker text never approves (DR-39). Other units continue meanwhile. |
| 6 execute | M | The engine measures the before-state again; a result that differs from the approved one stops the unit (`before_state_changed`). Then, through the access method, in this order: backup → change → syntax check (red → restore the backup, stop) → apply → after-measurement. Each command is a check record (§8.5). |
| 7 verify | M | The after-measurement meets the Done items, else `rolled_back`: the engine runs the rollback command, measures again and records both. |
| 8 audit | J | The auditor (DR-9) judges the Done items against the before-state, the plan and the after-measurement. RED → `rolled_back`. |
| 9 closeout | M | The backup path exists (checked through the access method) and the rollback command is in the status comment; then close `completed` (DR-50). Ledger record (§8.5). |

Access file: `<state>/projects/<repo-id>/operations.json` (§6.4) maps each access id to an argv
prefix and the env variable names of its secrets, each measure id to an argv command, and marks a
fixture resource with `fixture: true`. Only the owner writes it, on the local machine; issue text
never supplies an argv, a command or an env variable name (§11.1). A resource without
`fixture: true` needs sandbox level `os-sandbox` (§6.7).

Secrets: the engine reads them from the env variable names of the access entry and places them
only in the environment of its own step 2 and step 6 processes. Their values are masked in every output
before it reaches a prompt, a log, a check record or the tracker; the payload scan (§11) runs as
well. Threat model: §11.1.

---

## 8. Data contracts

Every file carries `formatVersion`. Parsers are fail-closed (DR-13): unknown keys and bad values
are refused with a named finding. JSON keys are `camelCase`; Go field names map to them by struct
tags.

### 8.1 Source registry — `<state>/sources.json`

Serves S1, S2.
```json
{ "formatVersion": 1, "window": 100,
  "sources": [
    { "id": "sub-a", "adapter": "claude-code", "kind": "subscription", "share": 50, "models": ["<model-id>"] },
    { "id": "api-b", "adapter": "openai-chat", "kind": "api", "credentialEnv": "B_API_KEY",
      "baseUrlEnv": "B_BASE_URL", "models": ["<model-id>"] },
    { "id": "api-c", "adapter": "openai-chat", "kind": "api", "credentialEnv": "C_API_KEY", "models": ["<model-id>"] } ] }
```
- `kind` is `subscription` or `api`. No kind has priority; order comes only from shares.
- `credentialEnv` may not name `GH_TOKEN`, `GITHUB_TOKEN` or another tracker token variable
  (§10.1); such a row is refused, so a tracker token never reaches a run (DR-18).
- `opsTrusted: true` marks a source the user trusts with operation units (§7.9); absent = false.
- **Concurrent-run ceiling** (DR-7, Q13): it starts at 1, rises by one after 10 consecutive clean
  runs (default) up to `maxConcurrent` when the row sets it, and falls by one, minimum 1, on a
  `rate_window` or concurrency error. A source at its ceiling is excluded as a hard constraint.
- `price {inputPerM, cachedInputPerM, outputPerM, currency, url, checkedOn}`, optional, copied from
  the provider's page: `CostUSD` uses it within the age of DR-21, else `unknown` (`no_price`).
  `sources show` prints its age.
- Shares are integers 0–100. Effective shares always sum to exactly 100.
- Explicit sum > 100 → refused. All sources explicit and sum < 100 → refused.
- Unassigned share = 100 − explicit sum, split equally among sources without `share`; remainder
  points go to the first such sources by `id`.
- **Routing (deficit round robin)**: each source holds a credit `c` in `sources.state.json`. Per
  run and role, E = sources that are active and have the role's capability (§10.2). Each source in
  E gains `share ÷ (sum of shares in E)`. For a judging role, E first narrows to the sources of the most
  independent level available (DR-9). Among E minus sources excluded for this unit by a hard
  constraint, the largest credit wins (tie → lowest `id`) and pays 1.
- Effects: an idle source (passive, restricted, or without the capability) gains nothing, so its
  share passes to the others in proportion. A source skipped only by a hard constraint keeps its
  gain and is repaid on later runs. Over any stretch with a fixed E, each source stays within 2 runs
  of its target (default bound; a simulation in this revision measured at most 1.04 runs over 2 000
  random share sets × 500 runs). Test `engine/routing_test.go` asserts the bound.
- Runtime state (`sources.state.json`): `active | passive | restricted`, quota class (§7.6),
  `resetAt` or `unknown`, `passiveSince`, `lastProbe`, `credit`, `ceiling`, `cleanRuns`.
- `aiwe sources show` prints the window (default 100 runs, all projects), target and observed
  shares, credits and restricted sources. Unit 1.15 registers `sources show`, `sources discover`
  and the terminal-only `sources enable` (§5.7).
- `aiwe sources discover` lists detected harnesses and env-configured APIs and writes candidate
  entries without shares. Any subset works: one subscription, API only, many.

### 8.2 Stack profile table — `stacks/profiles/<stack>.json` (data)

Serves S3. One embedded JSON file per stack holds that stack's row; `stacks/profiles.go` holds the
type and loads every file through `embed`, so a new or changed stack touches only its own file.
```go
type StackProfile struct {
    ID string; Detect []FileRule; Restore [][]string; Gate []GateStep
    TestFiles, SourceExt, LockFiles []string; TestCase, Assertion, NoTests []*regexp.Regexp
    Mutation []string   // nil = none
    Complexity []string // D18 tool; nil = none
    Registry string     // "" = none
    SecurityPaths, UIPaths, UIRunner []string // role resolution (§9.1); nil = none
}
```

| Stack | Detect | Restore | Gate steps (only if tool + config exist) | Test files | Lock files | Test case / assertion |
|---|---|---|---|---|---|---|
| Node | `package.json` | `npm ci` / `pnpm install --frozen-lockfile` / `yarn install --frozen-lockfile`, by lock file | `npm run <s>` for each of `typecheck`, `lint`, `test` present in scripts | `*.test.*`, `*.spec.*`, `__tests__/` | `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock` | `test(`, `it(` / `expect(`, `assert` |
| Python | `pyproject.toml`, `setup.cfg`, `setup.py`, `requirements*.txt` | `uv sync --frozen` / `poetry install` / `pip install -r` | `ruff check .`, `mypy .`, `pytest` | `test_*.py`, `*_test.py`, `tests/` | `uv.lock`, `poetry.lock`, `requirements*.txt` | `def test_` / `assert` |
| Dart/Flutter | `pubspec.yaml` (`flutter:` sdk → Flutter) | `dart pub get` / `flutter pub get` | `dart analyze` / `flutter analyze`, `dart test` / `flutter test`, `dart format --set-exit-if-changed .` | `test/**/*_test.dart` | `pubspec.lock` | `test(`, `testWidgets(` / `expect(` |
| .NET | `*.sln`, `*.csproj`, `*.fsproj` | `dotnet restore` | `dotnet build --no-restore`, `dotnet test --no-build`, `dotnet format --verify-no-changes` | `*Tests.cs`, `*.Tests/` | `packages.lock.json` | `[Fact]`, `[Theory]`, `[Test]`, `[TestMethod]` / `Assert.` |
| Go | `go.mod` | `go mod download` | `go vet ./...`, `go test ./...`, `gofmt -l .` (empty output) | `*_test.go` | `go.sum` | `func Test` / `t.Error`, `t.Fatal`, `assert.`, `require.` |
| Rust | `Cargo.toml` | `cargo fetch` | `cargo clippy -- -D warnings`, `cargo test`, `cargo fmt --check` | `tests/`, files with `#[test]` | `Cargo.lock` | `#[test]` / `assert!`, `assert_eq!` |
| JVM | `pom.xml`, `build.gradle(.kts)` | `mvn -q dependency:go-offline` / `gradle dependencies` (wrapper preferred) | `mvn verify` / `gradle check` (wrapper preferred) | `src/test/**`, `*Test.java`, `*Tests.java`, `*Test.kt` | `gradle.lockfile` | `@Test` / `assert`, `assertEquals` |
| unknown | — | — | `not_measured` → ladder rung 2 (§7.7) | `not_measured` | `not_measured` | `not_measured` |

- These rows describe managed projects; this repository's own gate is §12.2.
- `NoTests` is the runner's zero-test signal (§7.8). Go: the suffix `[no tests to run]` (§17);
  other stacks: proved per repository at onboarding (U6).
- Mutation tool (Phase 3): Node → Stryker; Python → mutmut; other stacks → `none` (§15).
- Complexity tool for D18 (Phase 3): Go → `gocognit`; other stacks → `none`, so D18 is
  `not_measured` there.
- Package registry for D17 (Phase 3): Node → npm registry; Python → PyPI; others → `none`.
- Test commands for Node, Python, .NET, Go, Rust and Dart match GitHub's starter workflows (fact,
  §17). The Flutter and JVM commands are not verified from a primary source; every derived command
  is proved per repository before use (§7.1 step 5; U6). Multi-stack repositories: §15.
- Role path tables (default; DR-44). Document paths, every stack: `*.md`, `*.mdx`, `*.rst`,
  `*.adoc`, `*.txt`, `docs/**`. Security paths, every stack: a path segment or file name that
  contains `auth`, `login`, `session`, `token`, `secret`, `credential`, `password`, `crypto`,
  `permission`, `oauth` or `webhook`, plus any file D16 flags; external-input entry points per
  stack: Node `routes/`, `api/`, `middleware*`; Python `views.py`, `urls.py`, `routes/`; Go
  `*handler*.go`; .NET `Controllers/`; JVM `*Controller.*`; Rust `handlers/`, `routes/`. UI paths:
  Node `*.tsx`, `*.jsx`, `*.vue`, `*.svelte`, `*.css`, `*.scss`, `*.html`; Flutter `lib/**/*.dart`;
  .NET `*.razor`, `*.cshtml`, `*.xaml`; Python, Go, JVM and Rust `*.html`, `templates/**`; unknown
  stack → `not_measured`.
- `UIRunner`: the project's own browser automation tool, found by its config file (Node:
  `playwright.config.*`); other stacks → none, so their UI review is `not_measured`. Screen capture
  with that tool: U26.

### 8.3 Stored gate — `projects/<repo-id>/gate.json` (project `gate.json` uses the same format)

Serves S4, S6.
```json
{ "formatVersion": 1,
  "steps": [ { "id": "test", "command": ["go", "test", "./..."], "required": true,
               "inputs": ["**"], "timeoutSeconds": 124, "expect": { "exitCode": 0 } } ],
  "origin": "derived", "inputHash": "<sha256>",
  "proof": { "baseCommit": "<sha>", "steps": [ { "id": "test", "result": "green", "exitCode": 0, "durationMs": 41230 } ] } }
```
`command` is an argv array, never a shell string. `origin` is `project | derived | synthesized`.
`inputs` defaults to `["**"]` (§7.8). A derived `timeoutSeconds` comes from the proof (§7.8).
`expect` also allows `outputMatches` and `outputRejects`. A project `gate.json` omits `origin`,
`inputHash` and `proof`.

### 8.4 Issues: goal, unit, status comment, owner brief

Serves S3, S5. Built-in elements only (DR-31):
- **Type**: detected per repository through GraphQL `repository.issueTypes` (Q4). Null → the
  default label `bug`, `enhancement` or `documentation`. Non-null, as in an organisation
  repository → the built-in type `Bug`, `Feature` or `Task`. REST `issue-types` is not used: on a
  personal-account repository it misleads. The tracker contract suite proves both paths.
- **Order / priority**: milestone, one per phase. A unit is not claimed while an earlier milestone
  is open; closeout closes a milestone (§7.3 step 17).
- **Waiting on owner**: label `question`. **Bad unit text**: `invalid`. **Quarantined or repeated
  red**: `help wanted`.
- **Close reason**: `completed`, `not_planned`, `duplicate` (fact, §17).
- **Pull request**: one per unit, opened and squash-merged by the engine (§7.3 steps 14–16, Q21).
  Its body carries `Refs #<n>` for its unit issue, never a closing keyword: the engine closes the
  issue itself (§7.3 step 17, §10.1, DR-50).
- **Finding outside the unit**: a new issue in this format, never an untracked note (DR-50).
- **Assignee**: the human owner of a decision; the engine never assigns itself.
- **Goal → units**: task list `- [ ] #N` in the goal body. Closeout closes the goal issue
  `completed` when every unit of its task list is closed (§7.3 step 17, DR-50).
- No custom labels, fields or title prefixes. Normalisation strips prefixes such as `ST2`
  or `(P1)` and maps them to type and milestone.

**Intake filter** (DR-39): only the body and comments of authors with write permission are read;
HTML comments are removed; bidirectional embedding, override and isolate controls make the issue
`invalid`. The remaining text enters prompts only inside the `UNIT` section, as data.

**Goal body** (resource `goal-template`): `## Why` · `## Outcome` · optional `## Constraints`. The
engine appends `## Units` (the task list). An issue without the unit preamble is not a unit;
`plan` may take it as a goal.

**Unit template** (resource `unit-template`). This subsection is the only definition of a unit
issue. `unit check`, the intake parser, the architect card, the `open-unit` prompt and every plan
file follow it; none restates it (DR-34). Preamble keys and `##` headings are fixed English
markers; all other text may be in any language (DR-27).

- **Title**: the `## Goal` text without its final period; at most 72 characters; no unit id, phase,
  type or other prefix.
- **Labels**: the type rule above only: `documentation` when every `## Files` path is in the
  document table (§8.2); `bug` when the unit fixes a defect; `enhancement` otherwise.
- **Milestone**: the unit's phase.
- **Behaviour unit**: a code unit whose `## Files` lists a path with a source extension of its stack
  (`SourceExt`, §8.2), test files included. Every other code unit is test-exempt.
- **Order**, kind `code`: `kind` · `runnable` · `test-exempt` · Goal · Files · Steps · Verify ·
  Done · Behaviour change · Depends on.
- **Order**, kind `operation`: `kind` · `runnable` · Goal · Scope · Access · Steps · Measure ·
  Done · Retention · Depends on.
- Nothing else appears: no other key, heading or text outside the elements below. A unit never
  names its role; the engine resolves it (§9.1).

| Element | Code | Operation | Rule |
|---|---|---|---|
| `- kind:` | required | required | `code` or `operation`; missing or other → `role_unresolved` (DR-44). |
| `- runnable:` | required | required | `agent` allows dispatch. `owner` marks human work: never dispatched; the engine posts an owner brief. The owner's pull request carries `Refs #<n>`, never a closing keyword. Closeout closes the owner unit issue (§7.3 step 17). A harness or model name as the worker is refused; routing is by capability (DR-6). |
| `- test-exempt:` | required when not a behaviour unit, else absent | absent | One line: why the unit has no behaviour to test (DR-45). |
| `## Goal` | required | required | One sentence: an imperative, or what is true when the unit is done. |
| `## Files` | required | — | `- <path>` or `- <path> (new)`, optionally followed by one parenthesised note. DR-11; plain paths must exist, `(new)` paths must not. A tracked test file that contains a backticked identifier of `## Steps` and is not listed → `scope_outside_files`, naming each path. |
| `## Scope` | — | required | Resource, files or services, expected result; a secret-bearing item ends in `(secret)`. |
| `## Access` | — | required | One access id of the project's `operations.json` (§7.9). The argv prefix and the secret names come only from that file, never from issue text. |
| `## Steps` | required | required | `1.` to at most `12.`: one imperative action per line, no vague words. |
| `## Verify` | required | — | Command lines, then `expected: <signal>`. Each line splits into argv by shell-style quoting and runs without a shell; a shell operator outside quotes (`&&`, `\|\|`, `\|`, `;`, `>`, `<`, a backtick, `$(`) → refused. The first token must resolve; the command is checked, never run, before review. The engine first runs it at §7.3 step 3, before any audit, so intake (DR-39) is the only filter between its author and its run. A `HEAD~<n>` or `HEAD^` base in Verify or Done → refused (DR-47). |
| `## Measure` | — | required | One measure id of the project's `operations.json` (§7.9) and its expected signal. |
| `## Done` | required | required | `- [ ] <item> — test: <path>` (the path is in `## Files`; red on base, green on the tip; wording per DR-45), `- [ ] <item> — audit` (judged by the auditor) or, for an operation, `- [ ] <item> — measure`. A behaviour unit has at least one test item (DR-45). |
| `## Behaviour change` | optional | — | Existing behaviour that changes or is removed, and what must stay the same. Required when the unit changes structure, or when check D2 finds a changed or removed base test (DR-17). |
| `## Retention` | — | optional | The written policy that allows a planned delete. |
| `## Depends on` | required | required | `none`, or one `blocked-by: #<n>` line per issue. A plan not yet opened writes the plan's unit id (`blocked-by: 0.7`); opening maps each id to its issue number. |

Example:
```
- kind: code
- runnable: agent

## Goal
Registering with an empty email returns 400.

## Files
- internal/auth/register.go
- internal/auth/register_test.go (new)

## Steps
1. Write the test cases named in `## Done`; each fails on base for the missing behaviour.
2. Change `internal/auth/register.go`.

## Verify
go test ./internal/auth/ -run TestRegister
expected: exit code 0

## Done
- [ ] `TestRegister` catches an empty email that registers instead of returning 400 — test: internal/auth/register_test.go

## Depends on
blocked-by: #41
```

**Status comment**: one per unit, created at claim, edited in place at each lifecycle step. Its
final state is the closeout evidence (§7.3 step 17).

**Owner brief**: one comment with fixed English headings, checked like a unit:
`## Decision` (recommendation first, with its reason) · `## Options` (each: one sentence and its
consequence) · `## Evidence` (engine-made: gate tail, diff stat, Run-Id) · `## Owner action` ·
`## Unblocks when`. The engine builds the skeleton and the evidence. Options come from the outcome
table for mechanical stops, or from the stopping run's `decision:` lines (§9.3). Those lines pass
the intake filter (DR-39), masking and the payload scan before they enter the brief.

### 8.5 Ledger — `<state>/ledger.jsonl`

Serves M1–M7. One JSON object per line, append-only, one schema with a `kind`.
```go
type Kind string // "run" | "check" | "owner_action" | "artefact" | "owned_dir" | "state"
type Measured[T int64 | float64] struct { Value T; Known bool }  // in core; JSON: a number or "unknown" (DR-21)
type RecordBase struct { FormatVersion int; Kind Kind; At time.Time
    RepoID, Goal, Unit, RunID string }                             // "none" when absent
type RunRecord struct { RecordBase
    Role, Source, Model, ModelReported, Effort string               // ModelReported may be "unknown"
    Attempt, FixRounds int; StartedAt time.Time; FinishedAt *time.Time
    Outcome string; Gate string /* green|red|incomplete|not_measured */; Audit string /* approved|returned|none */
    TokensIn, TokensOut Measured[int64]; CostUSD Measured[float64]
    UnknownReason string /* no_price|no_usage_data|subscription */; Residue []string
    AuditIndependence string /* provider|model|instance (DR-9); "none" for a writing run */
    Sandbox string /* env-scrub|harness-sandbox|os-sandbox (§6.7) */
    Tip, AuditedTip string; TokensCachedIn, LongestGapMS Measured[int64]
    Exit, StderrTail string /* no result file: exit code or signal; last 40 lines, masked */
    ClaimedAt, MergedAt, CIGreenAt *time.Time; MergeCommit, PullRequest string }
type CheckRecord struct { RecordBase
    Key, StepID, Commit string /* or "uncommitted" */; ExitCode int; Killed bool
    DurationMS int64; Tail /* masked before write (§11) */, Signature, RerunReason string }
type TempDirRecord struct { RecordBase; Root, Path, Owner string; PID int
    StartID string /* "none" without an exact start identity */; RemovedAt *time.Time
    HMAC string /* HMAC-SHA256 under <state>/owner.key (§5.15) */ }
```
- `Measured` makes "unknown" a separate state from 0 at the type level (DR-21).
- Integrity: each run start gets exactly one end record, or a `lost` record at the next start
  (§7.5). A `run` record carries `Unit`, `Attempt` and `RunID`; one without a unit is refused at
  write. Each fix round is a new run with its own `RunID`.
- Rotation: at 16 MB (default) the file becomes `ledger.<n>.jsonl.gz` (`compress/gzip`).
  `metrics` reads rotations. Under the state ceiling (§6.4), the oldest rotation goes first.
- A ledger that cannot be written stops dispatch (§7.4).

### 8.6 Run result envelope

Serves M2. Every command returns `Result[T]` (§5.3). A run's `Data`:
```go
type RunReport struct { RunID, Unit, Role, Source, Model, ModelReported, Outcome, Branch string
    Verdict string // judging roles: GREEN, RED or none, parsed by the engine from the run's last message
    Tip string // "none" before a commit
    Verify struct { Command string; ExitCode *int /* nil = not_run */; Tail string }
    TokensIn, TokensOut Measured[int64]; CostUSD Measured[float64]; DurationMS int64 }
```
The child process writes its own result file in its run directory. A missing result file with no
live process = `lost`. Cost is per run: an adapter whose harness reports only session or step
totals converts them to the run, or reports `unknown` (`no_usage_data`); M4 sums per-run values.

---

## 9. Roles and the standard agent contract

### 9.1 Role cards

A role exists only if it has its own kind of work and a gated boundary (DR-42). Ten roles do.
Writing roles: architect, test-author, engineer, doc-writer, operator. Judging roles: auditor,
security-reviewer, ux-reviewer. Read-only helpers: scout, debugger.

**Resolution** (DR-44). Inputs: the unit's kind, type and labels (§8.4), its `## Files` paths before
a run, its diff paths after collect, and the path tables of §8.2.

| Input | Roles |
|---|---|
| `aiwe plan`, or a rung-2 artefact of kind plan (§7.7) | architect, auditor |
| a rung-2 artefact of any other kind | engineer, auditor |
| `kind: operation` | operator, auditor (§7.9) |
| `kind: code`; type or label `documentation`, or every `## Files` path in the document table | doc-writer, auditor; a `documentation` unit that lists a non-document path → `role_unresolved` |
| `kind: code`, any other unit | test-author (every behaviour unit has a `— test:` item, DR-45), engineer, auditor |
| after collect: a diff path in the security table | adds security-reviewer |
| after collect: a diff path in the UI table | adds ux-reviewer |
| same red signature twice (§7.4) | adds debugger |
| ambiguous already-done check (§7.3 step 3) | scout |
| `kind` missing or unknown; no row matches | `role_unresolved` |

**test-author is required** for every behaviour unit (§8.4): the unit parser refuses a behaviour
unit without a `— test:` item (DR-45), so the code row always adds test-author. Only a
`test-exempt` unit runs without it.

| Role | Purpose | Called at | Input | Output | Tier |
|---|---|---|---|---|---|
| architect | Splits a goal into units. | §7.2 step 1; a returned plan, once (§7.2 step 3). | Goal issue; read-only clone. | Unit texts (§8.4); no file, commit or issue. | raised |
| test-author | Writes the unit's tests before code. | §7.3 step 6. | Unit; run clone at base. | Test files and fixtures in the file set, one commit. | base |
| engineer | Implements one unit. | §7.3 step 7 after the red proof; each fix round (§7.3 step 13). | Unit; run clone with the test-author's commit; on a fix round, the returned findings. | Commits inside the file set. | base; raised on a fix round after a returned audit |
| doc-writer | Updates existing documents, and creates a document only when `## Files` lists it `(new)`; checks every stated fact. | §7.3 step 7. | Unit; run clone at base. | Commits to document paths inside the file set. | base |
| operator | Plans one non-code change to a resource. | §7.9 step 3. | Unit; the masked before-state (§7.9 step 2). | The operation plan; no file, commit or command. | raised |
| auditor | Judges a unit, plan, artefact or operation it did not write: GREEN or RED. | §7.2 step 3; §7.3 step 12; rung-2 verification (§7.7); §7.9 step 8. | Unit or goal; `EVIDENCE` (§9.2). | A verdict in its last message only (§9.3). | one tier above the writer run it judges |
| security-reviewer | Attacks the change; proves each finding; never fixes. | §7.3 step 12. | Unit; `EVIDENCE`; read-only clone of the tip. | A verdict in its last message; findings with reproductions. | raised |
| ux-reviewer | Judges the changed screens; never fixes. | §7.3 step 12, when the profile has a `UIRunner` (§8.2); without one the engine records `not_measured: ux` and starts no run. | Unit; screenshots and runner measurements made by the engine; `EVIDENCE`. Needs a source with `Images` (§10.2). | A verdict in its last message: per screen and width PASS, FAIL or `not_measured`. | base |
| scout | Answers "already done on base?" when the mechanical check is ambiguous. | §7.3 step 3. | Unit; read-only base clone; recorded base verify. | Answer only. | base |
| debugger | Proves the root cause of a repeated red; changes no code. | §7.4 `repeated_red`. | Unit; the red's evidence; read-only clone of the tip. | Root cause, experiment keys, fix recipe. | raised |

May not, each with the gate that enforces it (limits shared by every role: §9.4):

| Role | May not | Gate |
|---|---|---|
| architect, operator, auditor, security-reviewer, ux-reviewer, scout, debugger | Write any file | read-only clone; D1 |
| architect | Name a harness or model as worker; list a plain path that does not exist | unit parser (§8.4) |
| test-author | Write production code; add skipped, focused or empty tests | D1; D10, D11 |
| engineer | Change the test-author's files or leave the file set | D1; DR-14 |
| engineer | Weaken, skip or delete tests | D2, D9, D10, D15; DR-28 |
| engineer | Ignore an error, add a TODO, add a secret, add an unregistered dependency | D4, D7, D16, D17 |
| doc-writer | Write a non-document path; create a document that `## Files` does not list `(new)` | D1 with the document table (§8.2); scope check (DR-14) |
| operator | Reach the resource; see a secret value | no access method or secret in the run environment (DR-35); masking (§7.9) |
| operator | Skip a plan step; plan a delete without a retention policy | plan check (§7.9 step 4) |
| operator, and the auditor of an operation | Run on a source without `opsTrusted` | routing (§8.1) |
| auditor, security-reviewer, ux-reviewer | Share a run or a context with the writer they judge; use a less independent source than the registry allows | DR-9, D14 |
| security-reviewer, debugger | Run a check or a reproduction itself | DR-12: the engine runs it |

Method per role (the card body):
- **architect**: each unit has one goal, a kind, its kind's scope (§8.4), Done items each ending in
  `— test:`, `— measure` or `— audit`, and dependencies. Every behaviour unit has a `— test:` item,
  and any other code unit has `test-exempt` (DR-45).
  An operation unit takes `## Access` from the project's rules. A structural change carries `## Behaviour change`. Count: Outcome items
  taken = items placed in units; a gap names the missing item. Work left out is named under Not
  done.
- **test-author**: tests check the Done items through the production entry point, not
  implementation detail. They fail on base for the missing behaviour, never for an import, syntax
  or fixture error. Already green on base, or not testable → stop and say why.
- **engineer**: claim "red before" only from a recorded result. A structural change keeps
  behaviour: no check is removed (fail-closed included) and no assertion is weakened; base tests
  run on the new code (DR-17). A deliberate behaviour change goes under Scope deviation. Three red
  verify results with no new cause → stop with what was tried and the options.
- **doc-writer**: updates a document that exists, or creates a document that `## Files` lists
  `(new)`; any other needed document → stop and name the proposed place under Not done. Every stated fact is checked against code or a recorded result;
  an unverified line is not written and is listed under Not done. The project's document rules,
  when present, apply.
- **operator**: fills the plan fields of §7.9 step 3 from the before-state. Reload is preferred; a
  restart is stated. A secret-bearing item appears by name and permissions only. Writes no code and
  no secret value.
- **auditor**: the writer's return is a claim, never evidence. Each Done item gets PASS or FAIL
  with one evidence line. The diff is searched for silent scope change, weakened assertion,
  removed check, compatibility shim, renamed unused symbol, committed scratch file and untracked
  interface change. Fail-open search: an empty list, zero matches, an uncollected test or a
  skipped step counted as pass. A test that only matches source text or a fixed string proves no
  behaviour; mutation survivors (§11) decide. A red already present on base is a separate
  finding. Boundaries and security are judged here unless security-reviewer runs. The auditor
  never fixes; a simple fix goes in the RED reason.
- **security-reviewer**: starts from a one-line threat model: who reaches what, and what is lost.
  Checks authorisation on every action, server-side and fail-closed; secret flow through code,
  logs, errors, URLs and history; validation of external input at the boundary, and injection;
  check-then-use races. A finding = location · reproduction (an argv command, or a test file the
  engine applies to a scratch clone of the tip that never merges) · observed result · severity. The
  engine runs each reproduction (DR-12); one it does not reproduce is `not_measured: <reason>`,
  never a finding.
- **ux-reviewer**: criteria and the screen list come from the project's UI rules; none →
  `not_measured`. Widths 375, 768, 1440 and 1920 px (default). Per screen: one task; the empty,
  loading, error, filled and unauthorised states; no horizontal overflow; touch targets ≥ 44 px;
  accessibility, contrast, keyboard and visible focus; error text that names the real cause.
  Overflow and accessibility counts are runner measurements; the reviewer judges the rest. A
  finding names screen, width, criterion and the screenshot or measurement.
- **scout**: facts carry their file and line; anything not read in this run is unknown; a guess
  is labelled `guess`.
- **debugger**: writes a hypothesis, then requests through `aiwe verify` the one experiment that
  separates it from its rivals: one changed variable (env value, argument, input or tool version).
  The variant is part of the check key, so no key reruns (§7.8). Root cause = one variable that
  turns red to green and back, both directions recorded. Suspect order: encoding and locale,
  process tree and environment, parallelism, timing, path form, versions. No hypothesis left →
  stop with the eliminated and remaining candidates.

**Effort tier**: each adapter maps `base`, `raised` and `top` onto its ordered effort list
(`Capabilities().Effort`); `raised` is one step above `base`, `top` one above `raised`, capped at
the last entry. A source without an effort list records effort `unknown` (DR-21). Test
`engine/routing_test.go` asserts the tier per role.

Card format: `id, purpose, trigger, needs, output, tier, may, may_not[{text, gate}]` + a body
without shell commands. Every `may_not` names a gate (a D-id, a DR or a run-environment
restriction); no `gate_owed`. Cards name no harness, model, tool name or path. Cards are
embedded in the binary (`embed`) and served as resource `roles/<id>`. More roles: §15.

### 9.2 Prompt contract (assembled by the engine, identical for every harness)

Sections in order, headings are fixed English markers:
1. `ROLE` — the card.
2. `UNIT` — the filtered issue body (DR-39), in whatever language it is written.
3. `SCOPE` — repo, run branch, file set, line cap; for an operation unit, the resource scope and
   the masked before-state (§7.9).
4. `RULES` — the project's commit convention (discovered from history and hooks; default
   Conventional Commits), DR-28, and one precedence line: contract limits (§9.4, DR-18, DR-28)
   override project rules, project rules override the card's defaults, and user-level harness
   rules are off (§6.5).
5. `VERIFY` — the exact unit verify string and how to request it (DR-12).
6. `EVIDENCE` (judging roles and debugger, §9.1) — engine-made: recorded check results, the full diff, diff stat,
   D-check findings, mutation results.
7. `RETURN` — §9.3.

The assembled prompt is ≤ 32 KB (default); its size is recorded; larger → `prompt_too_large`.

### 9.3 Return format

Last message, ≤ 12 lines, three headings: **Done** (verify command, exit code, last lines) ·
**Not done + why** (may carry `decision:` lines for a product question) · **Scope deviation**.
No file listing. The Done heading opens with a fixed English marker per role:

| Role | Done heading |
|---|---|
| architect | `PLAN:` unit count · Outcome items taken/placed. The unit texts follow as a separate block and do not count toward the 12 lines. |
| test-author | `TESTS:` test paths · red reason · tests still red. |
| engineer | `IMPLEMENTED:` commit hashes · verify command · exit code · last lines. |
| auditor | `VERDICT: GREEN` or `VERDICT: RED` · Done items PASS/total · each FAIL with its evidence line · `not_measured` dimensions. |
| scout | `ANSWER:` done, not done or unknown · evidence lines. |
| doc-writer | `UPDATED:` commit hashes · verify command · exit code · facts checked · unverified lines left out. |
| operator | `OPERATION:` the plan fields of §7.9 step 3, one per line. |
| security-reviewer | `VERDICT: GREEN` or `VERDICT: RED` · threat model in one line · findings by severity, each with location and reproduction · `not_measured` items with reason. |
| ux-reviewer | `VERDICT: GREEN` or `VERDICT: RED` · screens × widths judged · findings by severity with the screenshot · `not_measured` screens with reason. |
| debugger | `ROOT CAUSE:` one sentence · the variable and the check keys, red → green → red · fix recipe in one sentence · the earliest gate for this class (DR-32). |

This text is never evidence; the engine re-reads branch, diff and recorded results itself. A judging
run's `VERDICT:` line is a judgement, not evidence: the engine parses it from that run's own last
message into its run result (§8.6, DR-9).

### 9.4 What an agent may run

- May: read, search, edit inside the file set, call `aiwe verify`, commit on the run branch.
- The operator, security-reviewer, ux-reviewer and debugger change nothing. Their operation
  commands, reproductions and experiments run only through the engine (§7.9, §9.1).
- May not: run test, lint or gate commands directly (DR-12); anything DR-18 reserves for the
  engine; package publish; network beyond the adapter's policy; recursive delete (DR-25).

---

## 10. Interfaces

### 10.1 Tracker port (GitHub first)

```go
type Tracker interface {
    Identified
    ReadUnits(ctx context.Context, repo RepoID) ([]Unit, error)                     // open issues
    Write(ctx context.Context, op Op) error   // create | comment | edit-comment | label | close | milestone | pr-create | pr-merge
    Permission(ctx context.Context, repo RepoID, login string) (Permission, error)  // write | read | none
    CIStatus(ctx context.Context, repo RepoID, sha string) (CIStatus, error)        // pending | green | red | none
    Capabilities() TrackerCaps                                                       // IssueTypes bool
}
```
- The GitHub adapter calls the REST API, and GraphQL only where REST lacks the call, over HTTPS
  with `net/http`. No tracker CLI is used.
- Token resolution, first hit wins: `GH_TOKEN`, `GITHUB_TOKEN`, then `git credential fill` for the
  remote host. The token is held in memory for the call, never stored, never logged, never placed in
  a run environment (DR-18).
- Least permissions (default; unit 1.25 proves them): a fine-grained token on the managed repository
  with Contents read and write, Issues read and write, Pull requests read and write, Actions read
  and Metadata read. A missing
  permission ends the call with its `403` outcome. A token from `git credential fill` is used as it
  is.
- Every write passes masking and the payload scan first (§11); a text that fails the scan is not
  sent.
- Pull requests, a built-in GitHub element (DR-31): `pr-create` is REST
  `POST /repos/{owner}/{repo}/pulls`; `pr-merge` is REST `PUT /repos/{owner}/{repo}/pulls/{pull_number}/merge`
  with `merge_method: squash`, `commit_title` = `<type>(<scope>)` of the unit's first non-test
  commit, then `: `, then the unit issue's title with its first letter in lower case (§7.3 step 14),
  `commit_message` = `Refs #N` for the unit issue N followed by the `Run-Id` trailer (DR-22, Q17),
  and `sha` = the audited head, which GitHub requires the head to match (§17). The message uses
  `Refs`, not `Closes`: the engine closes the issue itself at closeout, after `main` CI (§7.3
  step 17).
- Request volume stays under GitHub's primary and secondary limits (§17): the status comment is
  edited in place; a `403` or `429` limit response follows `rate_window` (§7.6).
- Only `tracker/` knows GitHub. `tracker/trackertest` holds the fake and the contract suite.

### 10.2 Provider port (harness and API)

```go
type Provider interface {
    Identified
    Detect(ctx context.Context, env core.Env) Detection   // Present bool; Version, Reason string
    Capabilities() Capabilities   // Roles, Models, Effort []string; Resume, ReportsModel, RestrictsCommands, Images bool
    Run(ctx context.Context, req RunRequest) (core.Result[RunReport], error)
}
```
- Two internal kinds share the port: CLI harness (spawned process in a run clone) and API channel
  (agent loop inside the engine over HTTPS, file tools confined to the run clone by the engine).
- Each kind uses only the provider's official interface (DR-43).
- Routing reads `Capabilities()`, never the adapter id.
- Contract suite per adapter: §5.13.
- `Detect` looks on PATH, then in the harness's documented per-user install location, derived from
  an env variable. `harness_missing`, `harness_incompatible` and `harness_unreadable` (non-zero exit
  or empty output) stay distinct outcomes; the contract suite has a case for each.
- A CLI harness runs non-interactively under a written permission policy. A request the policy does
  not answer ends the run `permission_blocked` (§7.4), never a wait.
- A session resumes only in the directory that created it. Collect removes the clone, so a fix
  round starts a new session with the findings; an adapter asked to resume elsewhere refuses
  (`session_directory_mismatch`).
- API channel: it streams when the source row allows it, reading server-sent `data:` lines into the
  same reply as a single JSON body; each chunk resets the silence limit. It sends the reasoning
  parameters its provider documents and returns the fields the provider requires between turns,
  from adapter data. A reply cut by the output limit ends `answer_truncated`.
- API file tools: read a line window with line numbers, search contents, edit by one exact match,
  and run the profile's syntax or lint check after each edit where one exists. Misuse guidance
  lives in the refusal text, not in the tool description. Context is condensed only when a
  recorded comparison shows a lower cost per run at the same verify result.
- Phase 1 adapters: `claude-code`, `opencode`, `cursor` (CLI) and `openai-chat` (API; fact: all
  six old API specs use the `openai-chat` wire format).
- Deferred: API adapters with other wire formats, remote cloud agents (§15).

---

## 11. Quality system applied to managed projects

| Item | Kind | Where |
|---|---|---|
| Stack detection, gate derivation, base proof | M | §7.1 |
| Intake filter, unit template, test item, file existence | M | DR-39, DR-45; §7.3 step 1 |
| Plan audit | J | §7.2 step 3 |
| Tests red on base before code | M | §7.3 step 6 |
| Unit verify, run only by the engine; check once per key | M | §7.8 |
| Full gate on a clean clone | M | §7.3 step 9 |
| D1 forbidden paths (per role: the engineer may not change the test-author's files) · D2 test weakening · D3 suppression growth · D4 silent catch or ignored error · D5 untested code · D6 size · D7 TODO markers · D8 git hygiene · D9 assertion count · D10 disabled or focused tests (`skip`, `xfail`, `only`) · D11 empty tests · D12 vacuous mocks (flag) · D13 copied expectations and test literals that appear in changed production lines (flag; never for an identifier from the unit text or from a test commit made before the code commit; a string joined from literal parts that spells an outcome name is flagged) · D14 separation of powers · D15 test count · D16 secrets in the diff (payload scan patterns) · D17 new dependency missing from its registry or deprecated (Phase 3) · D18 a changed function over cognitive complexity 12 (Phase 3, Q12; tool per profile, §8.2) | M | §7.3 step 10; lines of `<base>...<tip>` only (DR-47); unreadable file types per DR-13 |
| Done item ↔ test mapping | M | §8.4 |
| Mutation on changed lines: Stryker (Node), mutmut (Python); 300 s budget (default); tool missing → `not_measured` | M | §7.3 step 11 (Phase 3) |
| Payload scan before any text leaves the machine | M | before each run and each tracker write (§10.1) |
| Project commit convention | M | integrate (the project's hook decides) |
| Batch gate and bisect | M | DR-19 |
| CI green after push | M | §7.3 step 16 |
| Red signature, goal pause | M | §7.4 |
| Escaped-defect scan | M | M3 |
| Role resolution | M | DR-44; §9.1 |
| Security review, UI review, root cause of a repeated red | J | §9.1 |
| Operation plan check, approval, ordered execution, rollback | M, O | §7.9 |
| Done criteria vs diff | J | auditor |
| D12, D13 flags; mutation survivors | J | auditor reads the flagged findings |

### 11.1 Threat model

| Threat | Mechanism |
|---|---|
| Secret leak: a secret value ends up in model input, logs, evidence or the tracker | The agent's run environment holds no secret (DR-35); only the engine's own step 2 and step 6 processes get one, and every output is masked (§7.9); the payload scan runs before text leaves the machine. |
| Irreversible change: data loss or an outage the rollback cannot undo | Backup before change; a red syntax check restores the backup; one owner approval bound to the plan digest (DR-1); a delete needs a written retention policy; a red after-measurement or audit runs the rollback (§7.9). |
| Untrusted provider: a source the user does not trust sees the resource or steers the change | Operation units route only to `opsTrusted` sources (§8.1); the agent only plans; the engine executes only the approved digest; tracker text is data (DR-39). |
| Agent with the user's rights: below `os-sandbox` an agent can read and change what the user can | Push is the engine's alone (DR-18); no secret and no git credential helper in the run environment (§6.5); the engine's git calls ignore the clone's config (§5.21); verdicts come only from judging runs (DR-9); a live operation needs `os-sandbox` (§6.7). |
| Hostile issue text: an argv, a command or a secret name planted in an issue | Intake filter (DR-39); verify runs without a shell (§8.4); operation access and measure come only from the owner's local `operations.json` (§7.9). |
| Forged approval: an approval given by text, over MCP or with a replayed digest | `ops approve` is terminal-only and asks for the digest; the digest covers plan, access, scope, secret names and before-state; an approval expires; the before-state is measured again before apply (§7.9). |

---

## 12. Open source, versioning and release

### 12.1 Repository files (industry standard only)

`LICENSE` (MIT, Q1) · `README.md` · `CONTRIBUTING.md` · `SECURITY.md` (private vulnerability
reporting, open from Gate 1, Q16; the open risk of §6.7) · `CODE_OF_CONDUCT.md` (a link to
Contributor Covenant 3.0; conduct reports go to the maintainer directly on GitHub, never through the
vulnerability form; U5) · `go.mod`, `go.sum` ·
`gate.json` (§12.2) · `.golangci.yml`, `.custom-gcl.yml` (§12.2) · `.goreleaser.yaml` (§12.3) ·
`.githooks/commit-msg` · `.gitattributes` (LF in every checkout, DR-27) · `.gitignore` (`/dist/`,
the release output, §12.3) · `.github/dependabot.yml` (ecosystems `gomod` and `github-actions`; why:
security updates for pinned modules and actions; its pull requests follow Q17) ·
`.github/workflows/ci.yml` (the only workflow).
No issue form and no pull-request template: templates come from the tool (DR-34), and
`CONTRIBUTING.md` names `aiwe unit template`, the MCP resources and `git config core.hooksPath
.githooks`. No `CHANGELOG.md`: release notes live on each GitHub Release (§12.3).

`README.md` outline, in order:
1. The tagline under this spec's title and the §1.1 definition paragraph, verbatim; check
   `repo/rules` fails when either differs from this spec.
2. **Features**, one line each: each agent starts with a small, clean context (DR-11, DR-35);
   nothing is left behind on disk (DR-36); every run is measured for time, cost and quality (§1.5);
   a goal is split by aiwe's architect role or by your own session from aiwe's template, and every
   unit is validated mechanically before work starts (§7.2).
3. **Supported providers**: the command `aiwe adapters list`, which prints the compiled adapter list
   (§5.2); the README never copies that list. One sentence states DR-43.
4. **Risk**: below `os-sandbox` an agent runs with your rights, as in other coding agent tools
   (§6.7).
5. Install (§6.2), first run and MCP registration (§6.3).

`CONTRIBUTING.md` section **Adding a provider**: one adapter file under `internal/adapters/`, one
line in `adapters/all.go`, and the contract suite `providertest.Run` (§5.13) green for it; no core
change (§4, open/closed). It also states that no leaked or unlicensed source of any harness is
read, quoted or copied: harness behaviour comes from its documentation or the running program.

### 12.2 Gate, tool chain and CI

**Gate** — `gate.json`, run by `aiwe gates gate --repo .` locally and in CI (one declaration).

| Step | Command | Catches | Why |
|---|---|---|---|
| `format` | `gofmt -l .` (empty output) | unformatted files | one style; `gofmt` ships with Go. gofumpt rejected: a second formatter for style only |
| `lint` | `custom-gcl run ./...` (golangci-lint built with the nilaway plugin) | the classes of §5.17 | the compiler misses them (§17); `go vet` runs inside it, so it has no step of its own |
| `vuln` | `go tool govulncheck ./...` | known vulnerabilities in the module's code paths | supply chain (DR-41); exits non-zero on a finding (§17) |
| `tidy` | `go mod tidy -diff` | `go.mod` or `go.sum` drift | exits non-zero on drift (§17) |
| `deadcode` | `go tool deadcode ./cmd/...` (empty output) | functions unreachable from `main` | `unused` does not see exported functions (DR-26) |
| `repo` | `go run ./internal/cmd/repocheck all --repo .` | checks `rules`, `size`, `boundaries`, `source-rules`, `deps`, `ci-shape` | this repository's own rules (§3) |
| `test` | `go test -json -p <workers> ./...` | behaviour | DR-38 limits apply; data races: CI step `race` on Linux, since `-race` needs cgo and a C compiler (§17) that contributors need not install |

Tools: the Go toolchain version is pinned in `go.mod`, which `actions/setup-go` reads (§17).
govulncheck, deadcode, svu, golangci-lint and GoReleaser are `tool` lines in `go.mod`, run by
`go tool <name>` (§17), so a contributor needs only Go and git and no setup script; check
`repo/deps` counts only modules linked into `cmd/aiwe`, so tools never reach the binary. The nilaway
version is pinned in `.custom-gcl.yml`; `go tool golangci-lint custom` builds `custom-gcl` from it
(§17; build cost: U18). A missing tool makes its step `not_measured` (DR-13). `repocheck doctor`
names each missing prerequisite with its install command for the running OS (unit 0.9).

**CI** — `.github/workflows/ci.yml`, the only workflow. Every step carries a `# why:` comment
(DR-42). Path filtering uses job conditions, never workflow `paths`: a workflow skipped by a path
filter leaves required checks pending, while a job skipped by `if:` reports success (§17).

Triggers: `push` with `branches: [main]` only (why: the integration evidence the engine waits for,
§7.3 step 16; a push trigger on run branches would run CI twice per commit, once for the push and
once for its pull request), `pull_request` (why: every unit merges through its pull request after
CI is green, Q21; review for outside contributors and Dependabot, Q17) and `workflow_dispatch` with
input `all-os` (why: a phase gate runs every OS once, Q20). `concurrency`
uses `group: ci-${{ github.ref }}` and `cancel-in-progress: ${{ github.event_name ==
'pull_request' }}` (why: agents push often, and a superseded pull request run's result is no longer
used; a `main` run is never cancelled, because each squash commit needs its own CI evidence). The
job count stays minimal (why: GitHub rounds each job's minutes up to a whole minute, §17): a new
Linux check is a step in an existing Linux job, never a new job; the release dry run stays a step of
`static`. Each job has `timeout-minutes` (why: DR-1). Permissions are read-only except in `release`
(why: least privilege). Every `uses:` names a full commit SHA with the version in a comment (why:
a moved tag cannot change a step; Dependabot updates both); check `repo/ci-shape` refuses any other form.

| Job | Runs on | Runs when | Steps (why) |
|---|---|---|---|
| `changes` | Linux | always | checkout; setup-go (module and build cache on by default, §17); `repocheck changes` → outputs `code`, `release`, `docs`, `green-tree` and `os-variant` (why: later jobs skip unchanged inputs; `green-tree` skips a job whose tree and OS already have a green run, DR-12, U23; `os-variant` is on when an OS-specific file changed (a Go file with an OS suffix or build constraint, `core/platform.go`, `core/process*.go`), when `go.mod` or `ci.yml` changed, on `workflow_dispatch` with `all-os`, and on a push the release job publishes live, Q20; once this month's minutes pass 70 % of the plan's included minutes, only the last two turn it on, U29; both minute counts come from the minutes source below the table) |
| `static` | Linux only | `code`, `docs` or `release` | checkout; setup-go; restore `custom-gcl` from cache keyed by `.custom-gcl.yml` (why: building it is slow, U18); `gates gate --step format,lint,vuln,tidy,deadcode,repo` (docs only → `--step repo`) (why: these results do not depend on the OS); `repocheck commit-msg` over the PR's commits, PR only (why: contributors without the hook); `repocheck run-id` over the pushed range, push to `main` only: from the first `main` commit with a `Run-Id` trailer on, a first-parent unit commit (Q17) without a `Run-Id` trailer is red, a commit that references a `runnable: owner` unit is listed as `owner`, and every other commit is listed as `external` in the job summary (why: DR-46, Q17, unit 1.29); `go tool svu next` and the release notes, PR only (why: the author sees the computed version and notes before merge, Q15; method: U3); `go tool goreleaser release --snapshot --clean`, only when `release` (why: a broken release config fails before merge, §17) |
| `test` | Linux; Windows and macOS only when `os-variant` | `code` | checkout; setup-go; `go env GOARCH CGO_ENABLED CC` (why: U9); `gates gate --step test` (why: processes, paths, file locks and launchers differ by OS, DR-6, DR-37; other pushes test on Linux only, Q20); step `race`, Linux only: `go test -race ./...` (why: data races, §5.17; the Linux image has cgo and a C compiler, U9); `go build ./cmd/aiwe` and step `clean-os-smoke` with `PATH` reduced to git and the binary: `--help` and `version` from unit 0.12, `mcp snippet` and onboarding of a fixture repository from unit 1.25 (why: DR-40; clean images: U12) |
| `required` | Linux | always, after all jobs (`needs`, `if: always()`) | checkout; setup-go; `repocheck ci-report`: fails when a needed job failed or was cancelled, and writes run duration, per-job duration, step count, cache hits, and this run's and this month's minutes per OS against the plan's included minutes (§17) to the job summary, and says so when this month's minutes pass 70 % of them, both from the minutes source below the table (why: the one required check, since a job skipped through a failed dependency reports no failure, §17; the DR-42 metric; U29) |
| `release` | Linux | push to `main`, `required` green, `code` or `release` | §12.3 |

**Minutes source** for `changes` and `ci-report`. This month's used minutes per OS: REST
`GET /users/{username}/settings/billing/usage/summary` with `product=actions`, field
`grossQuantity` per SKU. The plan's included minutes: the §17 fact "GitHub Actions on private repositories", for the plan name that
`GET /user` returns in `plan.name`. A source that does not answer gives `unknown`, never `0`
(DR-21), and the 70 % rule does not apply (U30).

Merged or removed elements: separate `go vet` step (inside `lint`); separate
`release.yml` (job in `ci.yml`); separate `clean-os-smoke` job (step in `test`); separate report
job (inside `required`); release snapshot build on every PR (only when release inputs change);
`version-field` and `changelog-guard` steps (no version field, no changelog file); three-OS runs of
lint, format and vulnerability checks (Linux only); three-OS test runs on a push that changes no
OS-specific input (Q20).

### 12.3 Release — automatic and correct by construction

Tool choice (facts: §17):
- GoReleaser alone: not enough; it "does not create any tags".
- release-please + GoReleaser: a release pull request must be merged per release; `CHANGELOG.md`
  repeats the release notes; checks on that pull request need a personal token.
- go-semantic-release + GoReleaser hook: one automatic flow; less adopted.
- **svu + GoReleaser OSS (chosen)**: one command computes the next version from Conventional
  Commits; GoReleaser's own cookbook pairs them; no pull request, no extra file, no human step
  between green and release.

Release job steps (each states its reason):
1. Checkout with full history and tags (why: svu reads tags and commits).
2. setup-go (why: `go tool svu`).
3. `go tool svu next` prints the next tag: `v` and the SemVer version, svu's default prefix (§17);
   equal to the current tag → stop with `no_change` (why: idempotent; a rerun on a tagged commit
   releases nothing; U4).
4. Dry mode, until unit 3.10 is closed (Q3): print the computed version, then stop (why:
   Gate 0 proves the computation; the build is proved by the snapshot in `static`). A live run
   needs the three-OS `test` job of the same push (`os-variant`, Q20).
5. Live mode: `git tag` with the computed version; `go tool goreleaser release --clean` (why: one run builds
   six targets, archives, `checksums.txt`, the GitHub Release with its notes (Q15), and the Scoop,
   winget and Homebrew manifests; push token: U19).
6. `actions/attest` on `checksums.txt` (why: one provenance statement covers every archive; §17).
   Permissions: `contents: write`, `id-token: write`, `attestations: write`. Then
   `gh attestation verify dist/checksums.txt --repo "$GITHUB_REPOSITORY"` (why: it proves the
   published attestation verifies; a red fails the job, and the releasing unit's closeout waits for
   it, §7.3 step 16; `gh` on the runner: U9).
7. Publish the `mcpb` entry to the MCP Registry (why: MCP clients install by name; tool: U20).

Rules:
- Bump from Conventional Commits (svu, §17): breaking (`!` or `BREAKING CHANGE`) → major, `feat`
  → minor, `fix` → patch, other types → no release. Publishing waits for Gate 3 and unit 3.10; the
  first tag is `v1.0.0` (Q3, unit 3.6). svu's behaviour: U4.
- A breaking marker is a red line (DR-1): integration refuses a unit commit that carries one
  (`breaking_change`, §7.4) until the owner runs the terminal-only `aiwe unit approve-breaking`, so
  no agent commit makes a major version alone.
- No version in any file. The version exists only as a tag; GoReleaser injects it at build time.
  Check `repo/source-rules` rejects a version literal in source. Nobody sets a version by hand.
- Release notes (Q15): GoReleaser writes them from the commits since the last tag into the GitHub
  Release body, in Keep a Changelog layout through `changelog.groups` (`feat` → Added, `fix` →
  Fixed, every other type → Changed). The repository has no `CHANGELOG.md`, and CI never commits to
  `main`.
- Provenance: one GitHub artifact attestation; users verify with `gh attestation verify` (optional;
  the product never needs gh) or with `checksums.txt`. Rejected: cosign — a second tool and a
  second signature for the same claim. SBOM file: §15; the binary already carries its module list
  (`go version -m`, §17).
- Code signing: Q7.
- The release job runs only on `main`, only after `required` is green.

---

## 13. Rewrite map

Reference code is read for behaviour and edge cases only; Go code is written fresh from this spec;
a listed defect or anti-pattern is never reproduced.
Anti-patterns: an outcome name joined from string pieces, which hides the literal from D13, and an
`mcp` test that imports `providers`, which the boundary gate rejected.

Nothing is ported. Each old group is either rewritten in Go with the old files as the behaviour
reference, or not rewritten. The old tests named in the last column become behaviour contracts:
their scenarios are rewritten as table-driven Go tests in the named unit. Old test files not named
there are not converted.
Measured with `wc -l` on the old repository's main branch: 147 non-test `.ts` files, 42 105
lines; 117 test files.

| Old group (files) | Lines | Decision | New package · unit | Old tests → behaviour contracts |
|---|---|---|---|---|
| core: args, cli, command, output, result, git-config, depth, index (8) | ~760 | rewrite (reference: these files) | `core` · 0.1, 0.2 | `core/cli.test.ts` (exit code per status, usage errors), `core/output.test.ts` (`--format json`, no colour) |
| core: paths (1) | 95 | rewrite (reference: `core/paths.ts`) | `core/platform.go` · 0.3 | `core/paths.test.ts` → root table on three OS (DR-37) |
| core: modules (1) | 67 | rewrite (reference: `core/modules.ts`) | `core/modules.go` · 0.3 | via `scripts/boundaries.test.ts` (0.9) |
| core: commit-rules (1) | 82 | rewrite (reference: `core/commit-rules.ts`) | `repocheck` · 0.10 | `scripts/commit-message.test.ts`, `scripts/check-commit-message.test.ts`, `scripts/commit-scope.test.ts` |
| gates: audit, diff, failures, payload, index (5) | ~1 525 | rewrite (reference: these files) | `gates` · 1.3, 1.5 | `gates/audit`, `diff`, `failures` (red signature), `payload` (secret patterns), `index` tests |
| gates: checks, test-shape (2) | 1 070 | rewrite (reference: these files) | `gates` · 1.5 | `gates/checks.test.ts`: each D-check's red case on known-bad input |
| gates: definition, runner (2) | 781 | rewrite (reference: these files) | `gates` · 0.7, 1.2, 1.3 | `gates/definition`, `runner`, `runner-commit` tests; `scripts/gate-single-source.test.ts` (the gate is declared once) |
| gates: exec (1) | 335 | rewrite (reference: `gates/exec.ts`) | `core/process*.go` · 0.6 | `gates/exec.test.ts` (launchers, exit codes, kill) |
| engine: acceptance, capacity, continuation, declared-coverage, gate-copy, git, lock, quarantine, queue, runs, temp-dir (11) | ~2 470 | rewrite (reference: these files) | `engine`, `core` · 0.5, 0.8, 1.3, 1.16, 1.19, 1.20, 2.8–2.10 | the eleven `engine/*.test.ts` of these files |
| engine: admission (1) | 394 | rewrite (reference: `engine/admission.ts`) | `engine`, `gates` · 2.11 | `engine/admission.test.ts`, `scripts/gate-admission.test.ts` |
| engine: claim, residue (2) | 945 | rewrite (reference: these files) | `engine` · 1.16 | `claim`, `residue` tests; `slot-rescue.test.ts` cases where committed work must survive a dead run |
| engine: closeout (1) | 359 | rewrite (reference: `engine/closeout.ts`) | `engine` · 1.20 | `closeout.test.ts` |
| engine: contract (1) | 83 | rewrite (reference: `engine/contract.ts`) | `engine` · 1.17 | `contract.test.ts`; `providers/prompt-floor.test.ts` (fixed per-turn prompt cost) |
| engine: detach (1) | 367 | rewrite (reference: `engine/detach.ts`) | `engine` · 2.9 | `detach.test.ts`, `status-foreground.test.ts` |
| engine: integrate (1) | 647 | rewrite (reference: `engine/integrate.ts`) | `engine` · 1.20 | `integrate.test.ts`, `integrate-land.test.ts` |
| engine: judge (1) | 170 | rewrite (reference: `engine/judge.ts`) | `engine` · 1.19 | `judge.test.ts` |
| engine: ledger (1) | 714 | rewrite (reference: `engine/ledger.ts`) | `ledger` · 1.10 | `engine/ledger.test.ts` merged with `efficiency/ledger.test.ts` into one schema |
| engine: slots (1) | 1 817 | not rewritten | — (run clones, §6.5) | none; rescue cases go to 1.16 |
| engine: board, commands, dispatch, gh, routing, work, index, testing/fake-gh (8) | ~6 700 | rewrite (reference: these files) | `engine`, `tracker` · 1.7, 1.9, 1.15, 1.19, 1.21 | `board.test.ts` parser cases and `board-full-gate.test.ts` (a unit whose verify runs the full gate) → 1.9; `dispatch.test.ts` lifecycle scenarios → 1.19; `gh.test.ts` call kinds → REST in 1.7; `routing.test.ts` capability and separation cases → 1.15; `commands.test.ts`, `work.test.ts` → 1.21 |
| engine: archive, form, race (3) | 1 151 | not rewritten | — (§7.3 step 17; §15) | none |
| efficiency: contract, index, lines, testing/append-worker (4) | ~325 | rewrite (reference: these files) | `ledger` · 1.10 | `efficiency/contract.test.ts`, `lines.test.ts` (concurrent appends keep whole lines) |
| efficiency: ledger, report (2) | 459 | rewrite (reference: these files) | `ledger` · 1.10, 1.24 | `efficiency/ledger`, `report` tests |
| efficiency: usage (1) | 395 | not rewritten | — (§15) | none |
| mcp: budget, build, call, index, resources, schema, server, supervise, surface (9) | 1 492 | rewrite (reference: these files; `build` has no Go counterpart) | `mcp` · 1.22, 1.23 | `budget`, `call`, `resources`, `stdio`, `supervise`, `surface` tests |
| bin (2), smoke (1) | 201 | rewrite (reference: these files) | `cmd/aiwe`, `e2e` · 0.1, 1.25 | the smoke scenario |
| norms: all (8) + norm `.md` files | 1 391 | not rewritten | — (no run consumes them; content is in §3 and §8.4) | none |
| pipelines (1) | 3 | not rewritten | — (empty module) | none |
| roles: cards, index (2) | ~300 | rewrite (reference: these files) | `roles` · 1.17, 1.18 | `roles/cards`, `index` tests |
| roles: schema (1) | 396 | rewrite (reference: `roles/schema.ts`) | `roles` · 1.17 | `roles/schema.test.ts` |
| roles: export (1) | 180 | not rewritten | — (§15) | none |
| providers: activity, api-errors, api-provider, condense, contract, effort, env, home, identity, preload, process, sessions, tools, testing/fake-harness (14) | ~4 900 | rewrite (reference: these files) | `adapters`, `core` · 0.6, 1.11–1.14 | `activity`, `api-errors`, `api-provider`, `condense`, `condense-repeat`, `contract`, `effort`, `env`, `home`, `preload`, `process`, `tools` tests |
| providers: aa-catalogue, catalogue, model-catalogue, host, demotion, context-cap (6) | 2 067 | not rewritten | — (§15; sources name their models, §8.1) | none |
| providers: cli-provider (1) | 494 | rewrite (reference: `providers/cli-provider.ts`) | `adapters` · 1.12 | `cli-provider.test.ts` |
| providers: provider, types (2) | 621 | rewrite (reference: these files) | `adapters` · 1.11 | via `providers/contract.test.ts` |
| providers: specs/* (9) | 1 116 | rewrite (reference: these files) | `adapters` · 1.13, 1.14 | `specs/specs.test.ts` |
| providers: commands, defaults, registry, index (4) | 1 222 | rewrite (reference: these files) | `adapters`, `engine` · 1.11, 1.15 | `registry.test.ts`; `commands.test.ts` discovery cases |
| providers: recommend, shipped-defaults (2) | 731 | not rewritten | — (routing replaces recommend; shipped defaults name one harness, DR-6) | none |
| scripts: boundaries, check-boundaries, check-commit-message, commit-message, commit-scope, gate-admission, tracked-files (7) | ~700 | rewrite (reference: these files) | `repocheck` · 0.9, 0.10; `gates` · 2.11 | `boundaries.test.ts` (0.9); commit tests (row core: commit-rules) |
| scripts: affected-tests (1) | 303 | not rewritten | — (Go's test cache reruns only changed packages, §17) | none |
| scripts: check-tests, gate-workers (2) | 926 | rewrite (reference: these files) | `gates`, `core` · 0.8 | `check-tests`, `gate-workers`, `gate-workers-deadline` tests (the limit is never divided by workers) |
| scripts: source-rules, check-source-rules (2) | 415 | rewrite (reference: these files) | `repocheck` · 0.4, 0.9 | `source-rules.test.ts` |
| scripts: check-unit-format (1) | 109 | rewrite as command `unit check` (reference: this file) | `engine` · 1.9 | `check-unit-format.test.ts` |
| scripts: bootstrap (1) | 431 | rewrite (reference: this file) | `engine` · 1.21 | `bootstrap.test.ts` (onboarding, `mcp snippet`) |
| scripts: board-index, board-reconcile, check-clean-env, clean-env, check-norms, gate-debt, norm-outcomes, release, release-verify, roles-export (10) | 1 358 | not rewritten | — (board, norms and debt gone; §12.2 replaces clean-env; §12.3 replaces release scripts) | none |

Summary, summed from the rows: **rewrite 113 files (~31.7 k lines) · not rewritten 34 files (~9.4 k
lines)**; rounded row counts sum to ~41.1 k of the 42 105 measured lines. Of 117 old
test files, 85 become behaviour contracts; 32 belong to groups that are not rewritten or have no Go
counterpart (`mcp/build.test.ts`).

### 13.1 Behaviour contracts from the previous engine

Each row is a failure the previous engine fixed after its tracker reported it. It becomes one
table-driven case in the named Go test, and the unit that owns the test file cites the row id in its
`— test:` item. Rows that serve the same failure are merged.

| Id | Scenario | Failure it catches | Go test · unit |
|---|---|---|---|
| BC-1 | An outcome string used in code is missing from the outcome table | a stop without a class or a next action | `core/outcomes_test.go` · 0.2 |
| BC-2 | A test makes temporary directories | a test that leaves one behind | `core/footprint_test.go` · 0.5 |
| BC-3 | A batch launcher gets an argument with metacharacters; a launched tree times out | a quoting fault that turns every run of a harness red; a child that outlives the timeout | `core/process_test.go` · 0.6 |
| BC-4 | A gate or an integration refuses a branch | a refusal without its step, command, exit code and last lines | `gates/evidence_test.go` · 1.3 |
| BC-5 | The gate measures a commit | a result taken on another tree than a clean clone of that commit, or recorded without it | `engine/gate_clone_test.go` · 1.3 |
| BC-6 | D13 meets the unit's own identifier, and a string joined from literal parts | a false flag on contract names; a missed literal workaround | `gates/checks_test.go` · 1.5 |
| BC-7 | A unit's Verify runs the full gate | a unit verify that costs a full gate per call (DR-12) | `engine/unit_test.go` · 1.9 |
| BC-8 | Steps name an identifier found in a test file outside `## Files` | a run that breaks a test outside its file set (§8.4) | `engine/unit_test.go` · 1.9 |
| BC-9 | A Done line counts from `HEAD~1` | a check that sees only the last commit of a multi-commit run (DR-47) | `engine/unit_test.go` · 1.9 |
| BC-10 | A harness exits without a result file | a failed run without exit code, signal, duration or stderr tail (§8.5) | `adapters/cli_test.go` · 1.12 |
| BC-11 | A run changes a path outside its declared file set | silent scope growth (DR-14) | `engine/coverage_test.go` · 1.16 |
| BC-12 | A test-author commit changes a production path | a writer testing its own code (D1, D14) | `engine/coverage_test.go` · 1.16 |
| BC-13 | A run dies after it committed | committed work removed with the clone (DR-16) | `engine/residue_test.go` · 1.16 |
| BC-14 | A run leaves uncommitted files | unaudited residue committed to the run branch | `engine/residue_test.go` · 1.16 |
| BC-15 | A run leaves 2 000 undeclared paths | a command line over the Windows length limit (§6.5) | `engine/residue_test.go` · 1.16 |
| BC-16 | A unit reaches closeout | a close without the unit's own verify run on the tip | `engine/acceptance_test.go` · 1.19 |
| BC-17 | A fix round leaves no change | a round called green from an earlier result instead of a new gate and audit | `engine/lifecycle_test.go` · 1.19 |
| BC-18 | The test-author's expected red sits in an existing test file | a deliberate red that opens a fix round | `engine/lifecycle_test.go` · 1.19 |
| BC-19 | The working branch moved after the run started | a branch gated on its stale base | `engine/integrate_test.go` · 1.20 |
| BC-20 | A batch merges | an ungated merge, or a push into the user's checkout | `engine/integrate_test.go` · 1.20 |
| BC-21 | A rebase conflicts | finished work discarded instead of a resolution run (Q11) | `engine/integrate_test.go` · 1.20 |
| BC-22 | Commits land after the audit | an unaudited tip merged (DR-19) | `engine/integrate_test.go` · 1.20 |
| BC-23 | Bisect removes a branch | the removed branch merged inside a later batch member | `engine/integrate_test.go` · 1.20 |
| BC-24 | No CI run appears for the push | a unit closed green without CI | `engine/ci_test.go` · 1.20 |
| BC-25 | A ref name is built for a run, claim or batch | two spellings of one ref, or an invalid segment accepted | `engine/git_test.go` · 1.20 |
| BC-26 | A reply over the structured budget holds a long nested list | `data` dropped whole instead of the longest list cut and named in `truncation.field` | `mcp/budget_test.go` · 1.23 |
| BC-27 | A tool call ends red or `no_change` | a completed call reported to the client with `isError` | `mcp/call_test.go` · 1.23 |
| BC-28 | Metrics read an empty or missing ledger | a cost or rate printed as `0` (DR-21) | `ledger/metrics_test.go` · 1.24 |
| BC-29 | A streaming source sends a long reply | a live reply killed as stalled (§10.2) | `adapters/sse_test.go` · 1.28 |
| BC-30 | The engine restarts with a run record open | a run shown as running forever (§7.5) | `engine/runs_test.go` · 2.9 |
| BC-31 | A run starts in the foreground | a live run missing from `runs list` and status | `engine/runs_test.go` · 2.9 |
| BC-32 | A run is killed from outside | a record closed with 0 ms or without its tip (§7.4) | `engine/runs_test.go` · 2.9 |
| BC-33 | A detached child starts | a child reading other input than its parent; a missing `--input` that is not a usage error | `engine/detach_test.go` · 2.9 |
| BC-34 | A claim ref outlives its run | a unit blocked forever by a stale claim | `engine/continuation_test.go` · 2.9 |
| BC-35 | A unit fails three times | a unit claimed again without end | `engine/quarantine_test.go` · 2.10 |
| BC-36 | A red comes from the machine, not the agent | a fix round charged to the agent | `engine/quarantine_test.go` · 2.10 |
| BC-37 | More runs are offered than the measured cores carry | an overloaded machine that slows gates and turns them red (DR-7) | `engine/admission_test.go` · 2.11 |
| BC-38 | A source is at its concurrent-run ceiling | another run started on it (§8.1) | `engine/routing_test.go` · 2.14 |
| BC-39 | `main` CI turns red after a squash merge | a direct push to `main` instead of a revert pull request, or a red revert pull request CI that does not end in `revert_red` (§7.3 step 16, §7.4) | `engine/ci_test.go` · 1.20 |

### 13.2 Reference code per group

The maintainer keeps the per-group reference map (exact commits of the previous engine, defects to avoid, tests to take as behaviour only) outside this repository, because it points to private history. The rule at the top of §13 applies to every entry.

---

## 14. Delivery plan

Every unit obeys DR-11 and Q24: the Files column names source files; tests sit beside them (§5.13). Paths
are under `internal/` unless they start with `internal/`, `cmd/`, `.github/`, `.githooks/` or `docs/`, or name a
root file. Verify is the
unit's own command. A phase gate is `aiwe gates gate --repo .` green in CI (§12.2) plus the phase
criterion. Each phase is one milestone (§8.4). Each issue body is generated from these tables in the
unit template (§8.4).

**Test items** (DR-45): the 71 behaviour units carry 179 `— test:` items; an item cites the §13.1
contract rows its test covers. Ten units are `test-exempt` (documents, configuration or an owner
approval only): 0.16, 0.13, 0.14, 0.11, 1.18, 1.27, 3.9, 3.10, 3.6, 4.5.

**Dependencies**, derived mechanically into each `## Depends on`, inside one phase (across phases
the milestone order holds): (1) a unit that changes a file another unit creates depends on it;
(2) units that change the same file run in table order; (3) a unit with code in a module depends on
the unit that first creates, earlier in the table, each module it may import (§6.1); for `cmd/aiwe`
only the modules of the unit's own files count, since the front imports a module only to register
its commands; (4) a verify that runs a module or a `repocheck` check depends on the unit that
creates it and on the unit that
creates each file the check reads (`rules`: `gate.json`; `ci-shape`: `.github/workflows/ci.yml`);
(5) the unit that creates the CI workflow, and a verify that runs the whole gate, `aiwe work`,
`aiwe metrics`, a smoke test or the release build, depend on every earlier unit of their phase;
(6) a unit whose code calls another unit's new code depends on it: 0.7 on 0.6 (process control),
0.18 on 0.6 (git through process control), 0.19 on 0.20 (config), 0.5 on 0.21 (process identity), 1.3 on 1.5 (masking), 1.4 on 1.5
(early diff checks), 1.8 on 1.5 (payload scan), 1.13 on 1.12 (harness runs), 1.19 on 1.3, 1.8,
1.9, 1.15, 1.16, 1.26 and 1.30 (gate clone, status, unit parser, routing, clones, roles, sandbox),
1.20 on 1.19, 1.21 on 1.2, 1.7, 1.19 and 1.20 (gate derivation, GitHub adapter, lifecycle,
integration), 4.4 on 4.3 (masked secrets). An edge implied by another path is left out. The graph
has no cycle. Measured critical path, in units: Phase 0 7, Phase 1 12, Phase 2 2, Phase 3 7,
Phase 4 3.

**Reach**: every new source file is called. It is an entry point under `cmd/`, or its own unit or a
unit that depends on it changes a file of the same module, or of a module that may import it
(§6.1), on a path back to an entry point. Each command is registered, and tested, by the unit that
brings it. `cmd/aiwe/main.go`: 0.1 (`version`), 0.7 (`gates`), 0.19 (`config show`), 1.4 (the
engine registry `engine/commands.go`), 1.11 (`adapters`) and 1.22 (`mcp`, `mcp snippet`). `--help`:
0.2 (`core/cli.go`). The engine registry: 1.9 (`unit check`, `unit template`), 1.15
(`sources show`, `sources discover`, `sources enable`), 1.21 (`work`, `plan`, `onboard`,
`unit approve-breaking`), 1.24 (`metrics`), 2.7 (`artefact submit`), 2.9 (`runs list`,
`runs stop`) and 4.2 (`ops approve`). `internal/cmd/repocheck/main.go`: 0.9, 0.10, 0.12 and 1.29.

**Self-hosting** (DR-46). At each hand-over unit aiwe takes over one step; the unit's Done list says
so. From the self-hosting point on, every unit is dispatched through aiwe.

| After unit | aiwe takes over | Until then, by hand |
|---|---|---|
| 0.7 | The gate result: `aiwe gates gate` | the unit's own verify |
| 0.10 | The commit message check: `.githooks/commit-msg` | the lead session checks each message |
| 0.12 | The push gate: the CI workflow | the full gate, run locally; the closeout records "no workflow on main yet" and closes the issue on the lead session's integration evidence (DR-50) |
| 1.4 | One check run per key: `aiwe verify` | the lead session runs each verify once and records it |
| 1.9 | The unit template check: `aiwe unit check` | a generator and validator over the plan file |
| 1.16 | Run isolation: the run clone, its scratch `HOME` and `TEMP`, and a secret-free environment (DR-35, §6.5) | the lead session makes one shallow clone per unit with `core.autocrlf=false`; scratch `HOME` and `TEMP` and the secret-free environment wait for the engine |
| 1.21 | The goal split: `aiwe plan` | the architect role in the lead session |
| 1.25 | **Self-hosting point**: `aiwe work --repo .` dispatches every later unit | test-author, engineer and auditor as separate runs, started by the lead session; the lead session opens and squash-merges each unit pull request with `Refs #<n>`, and closes the unit issue with an evidence comment once `main` CI is green, as §7.3 steps 14–17 do (Q21, DR-50) |

Unit 1.29 is the first unit dispatched through aiwe. It makes hand work after the self-hosting point
visible in CI (DR-46).

The last step of each body follows the 1.4 row. A unit of Phase 2 or later gets its verify result
through the engine. All of Phase 0, and 1.1, 1.3, 1.4 and 1.5, which 1.4 needs, leave the verify to
the lead session, which runs it once and records it. Every other Phase 1 unit does not wait for 1.4:
its last step uses `aiwe verify` when 1.4 is closed, and otherwise the lead session runs the verify
once.

### Phase 0 — Go skeleton, gate, CI and release dry run

Serves S1, S7.

| Unit | Goal | Files | Verify |
|---|---|---|---|
| 0.1 | The CLI prints every command result in one result envelope. | `go.mod`, `cmd/aiwe/main.go`, `core/result.go`, `core/command.go`, `core/output.go` | `go test ./internal/core/ ./cmd/...` |
| 0.16 | The release configuration builds every target in a snapshot. | `.goreleaser.yaml`, `go.mod` (`tool` line for GoReleaser) | `go tool goreleaser release --snapshot --clean` |
| 0.2 | Every error maps to one named outcome and one exit code. | `core/args.go`, `core/cli.go`, `core/errors.go`, `core/outcomes.go`, `core/assert.go` | `go test ./internal/core/` |
| 0.3 | Disk roots, module edges and env resolve in one place each. | `core/platform.go`, `core/modules.go`, `core/env.go`, `core/outcomes.go` | `go test ./internal/core/` |
| 0.20 | Config and logs resolve in one place each. | `core/config.go`, `core/log.go`, `core/outcomes.go` | `go test ./internal/core/` |
| 0.4 | Text round-trips byte-exact in UTF-8, whatever the locale. | `core/text.go`, `repocheck/source_rules.go` | `go test -run 'Text|SourceRules' ./internal/core/ ./internal/repocheck/` |
| 0.21 | Process identity and real paths resolve in one place on every OS. | `core/procstart.go`, `core/procstart_windows.go`, `core/procstart_unix.go`, `core/procstart_darwin.go`, `core/realpath_unix.go`, `go.mod`, `go.sum` | `go test -run 'CurrentProcess|StartID|ProcessStart|RealPath' ./internal/core/` |
| 0.5 | Only owned directories are removed, and leftovers are swept and counted. | `core/remove.go`, `core/owner_key.go`, `core/sweep.go`, `core/footprint.go`, `core/outcomes.go` | `go test -run 'Owned|Sweep|Footprint' ./internal/core/` |
| 0.6 | A stopped process tree leaves no child running on any OS. | `core/process.go`, `core/process_unix.go`, `core/process_windows.go`, `core/launcher_windows.go` | `go test -run Process ./internal/core/` |
| 0.17 | Measured values and identities are core types that every module shares. | `core/measured.go`, `core/identified.go` | `go test -run Measured ./internal/core/` |
| 0.18 | Every engine git call runs with repository-configured programs off. | `core/git.go` | `go test -run Git ./internal/core/` |
| 0.7 | `aiwe gates gate` runs the declared gate and reports each step. | `gates/definition.go`, `gates/runner.go`, `gates/commands.go`, `cmd/aiwe/main.go`, `gate.json` | `go test ./internal/gates/` |
| 0.8 | The test step runs with measured workers and time limits. | `core/capacity.go`, `gates/durations.go`, `gates/limits.go`, `gates/runner.go` | `go test -run 'Capacity|Durations|Limits' ./internal/core/ ./internal/gates/` |
| 0.15 | Each check streams its output and runs in fresh, clean roots. | `gates/stream.go`, `gates/roots.go`, `gates/runner.go` | `go test -run 'Stream|Roots' ./internal/gates/` |
| 0.19 | `aiwe config show` prints each setting with its origin. | `core/config_cmd.go`, `cmd/aiwe/main.go` | `go test -run ConfigShow ./internal/core/` |
| 0.9 | Repository checks enforce boundaries, dependencies, size and rules. | `repocheck/boundaries.go`, `repocheck/deps.go`, `repocheck/size.go`, `repocheck/rules.go`, `internal/cmd/repocheck/main.go` | `go test ./internal/repocheck/` |
| 0.10 | Commit messages and CI steps are checked against their rules. | `repocheck/commit_message.go`, `repocheck/ci_shape.go`, `.githooks/commit-msg`, `internal/cmd/repocheck/main.go` | `go test -run 'CommitMessage|CIShape' ./internal/repocheck/` |
| 0.13 | The open-source files exist and pass the repository checks. | `LICENSE`, `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `.github/dependabot.yml` | `go run ./internal/cmd/repocheck rules --repo .` |
| 0.14 | README, AGENTS and the spec pass the rules check. | `README.md`, `AGENTS.md`, `docs/SPEC.md` | `go run ./internal/cmd/repocheck rules --repo .` |
| 0.11 | Lint and tool settings make the full gate pass on the tree. | `.golangci.yml`, `.custom-gcl.yml`, `go.mod` (`tool` lines) | `go run ./cmd/aiwe gates gate --repo .` |
| 0.12 | CI gates every push, picks its OS set and reports each run. | `.github/workflows/ci.yml`, `repocheck/changes.go`, `repocheck/ci_report.go`, `internal/cmd/repocheck/main.go` | `go test -run 'CIReport|Changes' ./internal/repocheck/` |

The first commit holds `LICENSE`, `README.md`, `AGENTS.md`, `CONTRIBUTING.md`, `SECURITY.md`,
`CODE_OF_CONDUCT.md` and `docs/SPEC.md`, and no code. Units 0.13 and 0.14 bring them under
`repocheck`.

Gate 0: CI green on three OS; `clean-os-smoke` green with `aiwe --help` and `aiwe version`; the
snapshot builds six targets; the release job prints the computed version and publishes nothing; a
pull request that the lead session opens (§16) shows its release dry run (Q15); the `required` job
reports run duration, step count and minutes per OS; one `workflow_dispatch` run with `all-os` is
green on three OS (Q20).

### Phase 1 — One sandbox repository end to end

Serves S1–S7, M1–M7. Target: the sandbox repository of Q18. Unit 1.25 fills it with a fixture
project of one §8.2 stack with GitHub Actions CI.

| Unit | Goal | Files | Verify |
|---|---|---|---|
| 1.1 | Stack profiles are data rows, and detection names every outcome. | `stacks/profiles.go`, `stacks/profiles/` (one JSON row per stack of §8.2), `stacks/detect.go`, `stacks/stacks.go` | `go test ./internal/stacks/` |
| 1.2 | The gate is derived from the profile with inputs and measured limits. | `stacks/derive.go`, `gates/definition.go` | `go test ./internal/stacks/ ./internal/gates/` |
| 1.3 | The runner proves the gate on a clean clone and explains every kill. | `gates/runner.go`, `gates/failures.go`, `gates/evidence.go`, `engine/gate_clone.go` | `go test -run 'Runner|Failures|Evidence|GateClone' ./internal/gates/ ./internal/engine/` |
| 1.4 | Each check runs once per key, and agents request it via `aiwe verify`. | `gates/cache.go`, `gates/key.go`, `engine/verify.go`, `engine/commands.go`, `cmd/aiwe/main.go` | `go test -run 'Cache|Key|Verify' ./internal/gates/ ./internal/engine/` |
| 1.5 | Diff checks D1–D16 run with the stack profile's patterns. | `gates/checks.go`, `gates/test_shape.go`, `gates/diff.go`, `gates/audit.go`, `gates/payload.go` | `go test ./internal/gates/` |
| 1.6 | The tracker port has a fake that passes its contract suite. | `tracker/tracker.go`, `tracker/trackertest/fake.go`, `tracker/trackertest/contract.go` | `go test ./internal/tracker/...` |
| 1.7 | The GitHub adapter works over HTTPS and normalises old issues. | `tracker/github.go`, `tracker/http.go`, `tracker/credential.go`, `tracker/normalize.go` | `go test ./internal/tracker/...` |
| 1.8 | Intake filters issue text, and status comments and owner briefs work. | `tracker/intake.go`, `engine/status.go`, `engine/brief.go` | `go test -run 'Intake|Status|Brief' ./internal/tracker/ ./internal/engine/` |
| 1.9 | `aiwe unit check` accepts only bodies that follow the unit template. | `engine/unit.go`, `engine/goal.go`, `engine/unit_check.go`, `engine/commands.go` | `go test -run 'Unit|Goal' ./internal/engine/` |
| 1.10 | The ledger keeps one append-only schema of measured or unknown values. | `ledger/record.go`, `ledger/store.go`, `ledger/rotate.go` | `go test ./internal/ledger/` |
| 1.11 | Adapters register in one list and pass the provider contract suite. | `adapters/provider.go`, `adapters/all.go`, `adapters/commands.go`, `adapters/providertest/contract.go`, `cmd/aiwe/main.go` | `go test ./internal/adapters/...` |
| 1.12 | Harnesses run in a clean environment with liveness and exit evidence. | `adapters/cli.go`, `adapters/env.go`, `adapters/home.go`, `adapters/sessions.go`, `adapters/activity.go` | `go test ./internal/adapters/...` |
| 1.13 | The CLI adapters run their harness and record the observed model. | `adapters/claude_code.go`, `adapters/opencode.go`, `adapters/cursor.go`, `adapters/effort.go`, `adapters/all.go` | `go test ./internal/adapters/...` |
| 1.14 | The API adapter runs a confined agent loop over the chat wire format. | `adapters/api_loop.go`, `adapters/tools.go`, `adapters/api_errors.go`, `adapters/condense.go`, `adapters/openai_chat.go` | `go test ./internal/adapters/...` |
| 1.15 | Routing spreads runs over the sources by their shares. | `engine/sources.go`, `engine/routing.go`, `engine/commands.go` | `go test -run 'Sources|Routing' ./internal/engine/` |
| 1.16 | Each run works in its own clone, collected before the clone goes. | `engine/clone.go`, `engine/residue.go`, `engine/claim.go`, `engine/lock.go`, `engine/coverage.go` | `go test -run 'Clone|Residue|Claim|Lock|Coverage' ./internal/engine/` |
| 1.30 | Every run starts through a sandbox adapter that records its level. | `core/sandbox.go`, `ledger/record.go`, `engine/clone.go` | `go test -run Sandbox ./internal/core/ ./internal/engine/` |
| 1.17 | The engine builds the prompt contract from role cards within its cap. | `roles/schema.go`, `roles/cards.go`, `roles/roles.go`, `engine/contract.go` | `go test -run 'Schema|Contract' ./internal/roles/ ./internal/engine/` |
| 1.18 | The five base role cards state each role's method and limits. | `roles/cards/architect.md`, `roles/cards/test-author.md`, `roles/cards/engineer.md`, `roles/cards/auditor.md`, `roles/cards/scout.md` | `go test ./internal/roles/` |
| 1.26 | Each unit resolves to its roles, or stops with `role_unresolved`. | `engine/role.go`, `engine/unit.go`, `stacks/profiles.go` | `go test -run 'Role|Unit|Detect|Profile|Derive' ./internal/engine/ ./internal/stacks/` |
| 1.19 | A unit moves from intake through audit and fix rounds (steps 1–13). | `engine/lifecycle.go`, `engine/dispatch.go`, `engine/judge.go`, `engine/acceptance.go`, `engine/continuation.go` | `go test -run 'Lifecycle|Dispatch|Judge|Acceptance|Continuation' ./internal/engine/` |
| 1.20 | An audited unit's pull request passes CI, is squash-merged and closed. | `engine/integrate.go`, `engine/ci.go`, `engine/closeout.go`, `engine/git.go` | `go test -run 'Integrate|CI|Closeout|Git' ./internal/engine/` |
| 1.21 | Onboarding is automatic, and `aiwe plan` writes audited units once. | `engine/onboard.go`, `engine/plan.go`, `engine/commands.go` | `go test -run 'Onboard|Plan|Commands' ./internal/engine/` |
| 1.22 | The MCP server describes the tool to any connected session. | `mcp/server.go`, `mcp/surface.go`, `mcp/resources.go`, `mcp/prompts.go`, `cmd/aiwe/main.go` | `go test ./internal/mcp/ ./cmd/aiwe/` |
| 1.23 | MCP calls follow the command schema, payload budget and supervision. | `mcp/call.go`, `mcp/schema.go`, `mcp/budget.go`, `mcp/supervise.go`, `mcp/server.go` | `go test ./internal/mcp/` |
| 1.24 | `aiwe metrics` prints M1–M7, by goal or by model when asked. | `ledger/metrics.go`, `ledger/report.go`, `engine/commands.go` | `go test -run 'Metrics|Report' ./internal/ledger/` |
| 1.27 | The doc-writer card states how documents are written and checked. | `roles/cards/doc-writer.md` | `go test ./internal/roles/` |
| 1.28 | API runs stream, keep reasoning state and name cut answers. | `adapters/sse.go`, `adapters/openai_chat.go`, `adapters/api_loop.go`, `adapters/all.go` | `go test -run 'SSE|Reasoning|Truncated' ./internal/adapters/` |
| 1.25 | One goal on the sandbox repository ends the job with zero owner actions. | `e2e/external_test.go` (build tag `smoke`), `e2e/testdata/sandbox/`, `README.md`, `.github/workflows/ci.yml` | `go test -tags smoke -run External ./internal/e2e/` |
| 1.29 | CI turns red on a unit commit to `main` without a `Run-Id` trailer. | `repocheck/run_id.go`, `.github/workflows/ci.yml`, `internal/cmd/repocheck/main.go` | `go test -run RunID ./internal/repocheck/` |

Gate 1 (usable): on the sandbox repository one goal issue becomes ≥ 3 units; each unit ends the job
(§2) with zero owner actions; `metrics` prints every metric of §1.5; the run also completes with one
subscription only and with API only; no temp directory is left (M7); `clean-os-smoke` also runs
`aiwe mcp snippet` and onboarding. At Gate 1 the repository becomes public (Q16).

### Phase 2 — Every stack, every source subset, every failure path

Serves S1, S2, S3.

| Unit | Goal | Files | Verify |
|---|---|---|---|
| 2.1 | The Python profile proves green on its fixture repository. | `stacks/profiles/python.json`, `stacks/testdata/python/` | `go test -run TestProfilePython ./internal/stacks/` |
| 2.2 | The Dart/Flutter profile proves green on its fixture repository. | `stacks/profiles/dart.json`, `stacks/testdata/dart/` | `go test -run TestProfileDart ./internal/stacks/` |
| 2.3 | The .NET profile proves green on its fixture repository. | `stacks/profiles/dotnet.json`, `stacks/testdata/dotnet/` | `go test -run TestProfileDotnet ./internal/stacks/` |
| 2.4 | The Go profile proves green on its fixture repository. | `stacks/profiles/go.json`, `stacks/testdata/go/` | `go test -run TestProfileGo ./internal/stacks/` |
| 2.5 | The Rust profile proves green on its fixture repository. | `stacks/profiles/rust.json`, `stacks/testdata/rust/` | `go test -run TestProfileRust ./internal/stacks/` |
| 2.6 | The JVM profile proves green on its fixture repository. | `stacks/profiles/jvm.json`, `stacks/testdata/jvm/` | `go test -run TestProfileJVM ./internal/stacks/` |
| 2.7 | An unknown need is met through the adaptation ladder and stored. | `engine/synthesis.go`, `engine/commands.go`, `ledger/record.go` | `go test -run Synthesis ./internal/engine/` |
| 2.8 | Quota and restriction errors move a source to passive or restricted. | `engine/sources.go`, `adapters/api_errors.go`, `engine/queue.go` | `go test -run 'Sources|Queue|APIErrors' ./internal/engine/ ./internal/adapters/` |
| 2.9 | Lost, killed, timed-out and stalled runs end with a named outcome. | `engine/detach.go`, `engine/runs.go`, `engine/continuation.go`, `engine/commands.go` | `go test -run 'Detach|Runs|Continuation' ./internal/engine/` |
| 2.10 | Environment faults are told from agent faults, and failing units stop. | `engine/lifecycle.go`, `gates/failures.go`, `engine/brief.go`, `engine/quarantine.go` | `go test -run 'Lifecycle|Failures|Brief|Quarantine' ./internal/engine/ ./internal/gates/` |
| 2.11 | All layers together never start more workers than the measured cores. | `core/capacity.go`, `engine/admission.go`, `gates/admission.go`, `engine/routing.go`, `gates/runner.go` | `go test -run 'Capacity|Admission' ./internal/core/ ./internal/engine/ ./internal/gates/` |
| 2.12 | The disk contract holds on Windows, macOS and Linux. | `core/platform.go`, `core/remove.go`, `core/testdata/disk/` | `go test -run DiskContract ./internal/core/` |
| 2.13 | A repeated red calls the debugger, whose experiments are new keys. | `roles/cards/debugger.md`, `engine/debug.go`, `gates/key.go`, `engine/lifecycle.go` | `go test -run 'Debug|Key' ./internal/engine/ ./internal/gates/` |
| 2.14 | No source runs more concurrent runs than its measured ceiling. | `engine/sources.go`, `engine/routing.go` | `go test -run 'Routing|Ceiling' ./internal/engine/` |

Gate 2: each of the seven profiles proves green on a fixture repo in CI; an unknown-stack fixture
completes through ladder rung 2; a fixture per quota class moves a source to passive and back; a
restricted fixture receives zero further requests; the disk contract tests are green on three OS; a
repeated-red fixture calls the debugger once and reruns no check key; a source at its ceiling
receives no further run.

### Phase 3 — Trust hardening and first release

Serves M3, M6, M7.

| Unit | Goal | Files | Verify |
|---|---|---|---|
| 3.1 | Base-commit tests run against the new code (DR-17). | `gates/invariance.go`, `engine/lifecycle.go` | `go test -run Invariance ./internal/gates/` |
| 3.2 | Escaped defects are found and counted in M3. | `ledger/escaped.go`, `engine/closeout.go` | `go test -run Escaped ./internal/ledger/` |
| 3.3 | Mutation results on changed lines become evidence. | `gates/mutation.go`, `stacks/profiles.go` | `go test -run Mutation ./internal/gates/` |
| 3.4 | D17 flags a new dependency that its registry lacks or deprecates. | `gates/dependency.go`, `stacks/profiles.go` | `go test -run Dependency ./internal/gates/` |
| 3.5 | D18 flags a changed function whose cognitive complexity exceeds 12. | `gates/complexity.go`, `stacks/profiles.go` | `go test -run Complexity ./internal/gates/` |
| 3.7 | The security-reviewer's findings are reproduced by the engine. | `roles/cards/security-reviewer.md`, `engine/review.go`, `engine/repro.go`, `stacks/profiles.go`, `engine/lifecycle.go` | `go test -run 'Review|Repro|Detect|Profile|Derive' ./internal/engine/ ./internal/stacks/` |
| 3.8 | The ux-reviewer judges screens captured by the project's UI runner. | `roles/cards/ux-reviewer.md`, `engine/screens.go`, `stacks/profiles.go`, `adapters/provider.go`, `engine/lifecycle.go` | `go test ./internal/engine/ ./internal/stacks/ ./internal/adapters/` |
| 3.9 | The release job publishes the MCP Registry entry for every target. | `server.json`, `.github/workflows/ci.yml` | `go run ./internal/cmd/repocheck ci-shape --repo .` |
| 3.10 | The owner approves the 1.0.0 release once Gate 3, Q7 and U19 hold. | `docs/SPEC.md` | `aiwe metrics --repo .` |
| 3.6 | The release job publishes `v1.0.0` and verifies its attestation. | `.goreleaser.yaml`, `.github/workflows/ci.yml` | `go tool goreleaser release --snapshot --clean` |

Unit 3.10 is `runnable: owner`: the engine never dispatches it, closes it by the owner unit rule (§8.4), and 3.6 waits for it. The live
release is checked after the merge of 3.6: the release job of that push publishes `v1.0.0`
and verifies its attestation (§12.3 step 6); the closeout of 3.6 waits for that CI run (§7.3 step
16). The owner makes the `docs/SPEC.md` change of 3.10 through a pull request too, because the
`main` ruleset refuses a push without one (Q21). Its squash commit has no `Run-Id` trailer, because
no run made it: the `Run-Id` check (Q17, unit 1.29) lists a commit that references a
`runnable: owner` unit as `owner`, never as a defect.

Gate 3: M1 ≥ 70 % over the last 20 units; M3 = 0 in the window; M6 = 0; M7 within ceilings; a
security fixture finding is reproduced by the engine; a UI fixture without a runner records
`not_measured: ux`; the owner has decided code signing (Q7), and unit 3.10 is closed.

### Phase 4 — Operation units

Serves S4, M2. Why last: an operation is irreversible on a live resource, so it comes only after the
code path has proved trust (Gate 3: M1 ≥ 70 %, M3 = 0); one repository end to end (Gate 1) does not
need it.

| Unit | Goal | Files | Verify |
|---|---|---|---|
| 4.1 | Operation units parse and route only to `opsTrusted` sources. | `engine/unit.go`, `engine/sources.go`, `engine/routing.go`, `engine/ops_config.go` | `go test -run 'Unit|Sources|Routing|OpsConfig' ./internal/engine/` |
| 4.2 | An operation plan is checked, digested and approved once. | `engine/operation_plan.go`, `engine/approve.go`, `engine/commands.go` | `go test -run 'OperationPlan|Approve' ./internal/engine/` |
| 4.3 | Secrets reach only engine steps and are masked in every output. | `engine/secrets.go`, `gates/payload.go`, `core/process.go` | `go test -run 'Secrets|Payload' ./internal/engine/ ./internal/gates/` |
| 4.4 | Operations run in order through the access method and roll back on red. | `engine/operation.go`, `engine/access.go`, `engine/lifecycle.go` | `go test -run 'Operation|Access' ./internal/engine/` |
| 4.5 | The operator card states how an operation plan is written. | `roles/cards/operator.md` | `go test ./internal/roles/` |
| 4.6 | One fixture operation ends the job with one owner approval. | `e2e/operation_test.go` (build tag `smoke`) | `go test -tags smoke -run Operation ./internal/e2e/` |

Gate 4: on a fixture resource reached through a local access method, one operation unit ends the job
(§2) with exactly one owner approval; a plan that misses a step does not start; a planted secret
value appears 0 times in prompt, ledger, evidence and status comment; a red after-measurement runs
the rollback and the second measurement equals the before-state. The first live operation follows
Gate 4, on a resource the owner names, and needs sandbox level `os-sandbox` (§6.7).

Totals: 5 phases, 79 units.

---

## 15. Deferred (YAGNI) — trigger that brings each back

| Item | Trigger |
|---|---|
| Monorepo / several stacks in one repo | A target repo has two detected stacks; until then `multi_stack` stops with a named outcome. |
| Race or second opinion: one unit on two providers | Lead time p90 > 3× median over 50 units, or `repeated_red` on 3 of 50 units. |
| Branch archive beyond deleting merged refs | Run refs exceed 500 in one remote. |
| Session usage report | `lost` runs > 5 % in the ledger. |
| Native sub-issues and native `blocked_by` | Official docs confirm personal-repo support (U2) and task lists prove insufficient. |
| Dependency cache across runs | Restore time > 50 % of run time on that stack (U8). |
| Sandbox levels `harness-sandbox` and `os-sandbox` (§6.7): network egress policy, command allow-list, container profile | After Gate 1, when an adapter, or an external tool that meets the §6.7 contract, enforces it on all three OS without elevated rights (DR-37). `os-sandbox` is required before the first live operation (Phase 4, Q19). |
| Process-tree end on engine crash (Unix parent-death signal; Windows kill-on-close job limit) | The sweeper finds a live process of a dead engine. |
| Loop counters (repeated tool calls inside a run) | `stalled` plus `timed_out` > 5 % of runs over 50 runs. |
| Copying ignored files (for example `.env`) into run clones, opt-in per repository by the owner and never automatic | A clean-clone gate is red where the user's checkout is green because of an ignored file. |
| Shared resource declaration (ports, databases) | An environment red is traced to two runs sharing a port or a database. |
| Static security analysis and dependency vulnerability audit for managed projects | An escaped defect is classed security, or a managed repo carries such a tool's config. |
| Dependency licence allow-list and maintenance check | A managed project declares a licence policy. |
| Test-tampering calibration set; per-model variance reruns | An escaped defect traces to a passing but wrong test. |
| Dependency role; i18n checks | A unit class fails audit 3 times for the same missing expertise. |
| Mutation tools beyond Node and Python | An escaped defect on a stack without one. |
| Per-test dependency map for the full gate | Full-gate time > 50 % of M5 over 50 units. |
| Unit `## Access` section for code units (tools and permissions a unit needs; operation units have one, §8.4) | Two units get `environment` for the same missing tool. |
| Built-in scheduler for `work` | The owner asks; until then an OS scheduler may call `work`. |
| Decision-model API as a pre-filter (scout need, duplicate issues) | 50 labelled audit decisions show agreement above an owner-set threshold, and an official source confirms its terms and OS support. |
| Harness-shaped card export | A harness needs its own card file to run. |
| Non-GitHub tracker adapters | A user asks, with a repository to test on. |
| Remote cloud agent adapters | The provider documents dispatch, repo hosting and concurrency. |
| Latency budget | A measured slow command or MCP call. |
| API token budget per turn | M4 doubles over 50 units. |
| Rule contradiction hunt (old norms layer 3) | Rules exceed 60 entries. |
| Self-hosted CI runner on the owner's machine: zero GitHub minutes, but its jobs take the machine's cores from local runs (DR-7) | U29 shows that this month's minutes would exceed the plan's included minutes. |
| npm channel: platform packages through `optionalDependencies`, no install script | The owner turns it on; GoReleaser OSS cannot publish to npm (§17). |
| SBOM file per release | A channel or a user requires an SBOM file. |
| Model catalogues, host detection, model demotion, long-context price guard (old `providers/`, §13) | Routing needs a model no source names, or one model doubles M4 over 50 units, or `metrics --by model` shows a role, source and model under 10 % first-try green over 20 runs. |
| Code signing: macOS signing and notarization, Windows certificate (U24) | Gate 3: the owner decides before `1.0.0` (Q7). |
| Architecture-specific source files | A measured gain needs one; CI then builds every target on each change (§12.2). |

---

## 16. Owner decisions, owner actions and lead tasks

No open question remains. Each decision keeps its id; other sections cite it.

| Id | Decision |
|---|---|
| Q1 | License: **MIT**. |
| Q2 | Product name: **aiwe**; the code reads it from one constant (DR-25). |
| Q3 | First release: nothing is published before Gate 3; the release job runs dry until then. The first published version is `1.0.0`, tagged `v1.0.0` (unit 3.6). |
| Q4 | The repository lives on a personal account. Issue type follows the GraphQL rule of §8.4: default labels there, built-in types in an organisation repository. Contract tests prove both paths. |
| Q5 | Old repository: archived after Gate 1; it stays readable as the behaviour reference (default). |
| Q6 | Independent audit is required as DR-9 states; the ledger records the level used (§8.5); no setup is refused for having one source. |
| Q7 | Code signing: none until Gate 3; the owner decides it at Gate 3 from U24 (§15). |
| Q8 | The operator only plans; the engine performs the access (§7.9). |
| Q9 | Live approval is the local, terminal-only command `aiwe ops approve`, which asks for the digest (§7.9 step 5). |
| Q10 | A `not_measured` UI review does not block integration; the status comment and the ledger record it (§7.3 step 12). |
| Q11 | Integration conflict (owner, 2026-10-06): a rebase or merge conflict goes back to the agent as a resolution run on the same branch; after two failed resolutions the unit is skipped and requeued (§7.4). |
| Q12 | Complexity (owner, 2026-10-06): cognitive complexity ≤ 12 per function, enforced by `gocognit` in this repository's gate (§5.17) and by D18 on changed functions of managed projects (§11). |
| Q13 | Source concurrency (owner, 2026-10-05 and 2026-10-10): capacity is measured, never assumed; a rate-limit response lowers a source's concurrent runs and clean runs raise them, up to the owner's value (§8.1). |
| Q14 | Owned removal (owner, 2026-10-10): aiwe removes only a directory it created, registered and marked as its own, under one of two roots: the temp root or the cache root (DR-25, §5.15). |
| Q15 | Release notes (owner, 2026-10-10): GoReleaser writes each release's notes from the commits, in Keep a Changelog layout, into the GitHub Release (§12.3). The repository keeps no `CHANGELOG.md`; CI never commits to `main`. Each pull request prints the computed version and the notes as a dry run (§12.2). |
| Q16 | Visibility (owner, 2026-10-10): the repository starts private and becomes public at Gate 1. GitHub private vulnerability reporting is for public repositories (§17), so the reporting channel of `SECURITY.md` opens at Gate 1. |
| Q17 | `Run-Id` scope (owner, 2026-10-10): the CI check applies only to a commit that closes or references a unit issue (an issue whose body has the unit preamble, §8.4). Dependabot and outside pull requests pass normal review and CI; the check lists their commits as `external` (DR-46, §12.2). A commit that references a `runnable: owner` unit (3.10) is listed as `owner`, never as a defect: an owner unit is never dispatched, so it has no run. |
| Q18 | Gate 1 target (owner, 2026-10-10): the private sandbox repository `aiwe-sandbox` under the owner's account, created on 2026-10-10. aiwe reaches it with the owner's own git and GitHub credentials (§14). |
| Q19 | Sandbox (owner, 2026-10-10): the MVP runs agents at sandbox level `env-scrub`; a stronger level comes after Gate 1 and is required before the first live operation (§6.7, §15). README and SECURITY state the open risk. |
| Q20 | CI minutes (owner, 2026-10-10): Windows and macOS tests run only when `os-variant` is on (an OS-specific file, `go.mod` or `ci.yml` changed), at a phase gate (`workflow_dispatch` with `all-os`) and before a live release; every other push tests on Linux only (§12.2, U29). |
| Q21 | Branch model (owner, 2026-10-10): trunk-based, no development branch. Each unit works on a short-lived run branch and merges through its own pull request: CI runs on the pull request, and the engine squash-merges it when CI is green and the head equals the audited tip; batch gate and bisect work on pull requests (DR-19, §7.3 steps 14–16). Before the self-hosting point the lead session does the same by hand (§14). |
| Q22 | Closeout marks Done (owner, 2026-10-11): the write that closes a unit issue also marks `[x]` each `## Done` item whose evidence is green, mechanically (DR-50, §7.3 step 17). |
| Q23 | Test functions (owner, 2026-10-11): the 60-line function limit applies to non-test functions; a test function has no line limit (§5.9). |
| Q24 | Unit size (owner, 2026-10-11): a code unit has at most 5 source files (DR-11). Row 0.3 splits into 0.3 and 0.20, and row 0.5 into 0.21 and 0.5 (§14). |
| Q25 | Manifest files (owner, 2026-10-11): `go.mod` and `go.sum` are a manifest and its lock file; they do not count as source files (DR-11). Reason: `go.mod` declares dependencies and carries no behaviour code, and splitting every unit that adds a dependency adds work without adding quality. |

**Owner actions** (only work that needs a human; the engine never does it):

| Action | When | Unblocks | Needs a human because |
|---|---|---|---|
| Create the release push token (U19). | Before closing unit 3.10 | Live release | GitHub documents token creation only on its web settings page (§17). |
| Decide code signing from the U24 measurements (Q7). | Gate 3 | `1.0.0` | It may commit the owner to a paid developer programme and its legal terms (U24). |
| Name the resource of the first live operation. | After Gate 4 | First live operation | It approves an irreversible action on a live resource (§7.9). |

**Lead tasks** (no owner step: the lead session or an agent does them with the owner's own
credentials):

| Task | When | Method |
|---|---|---|
| Install Go and git at user level, without administrator rights; every other development tool runs through `go tool`, and `-race` runs only in CI (§12.2). | Before Phase 0 | a user-level package manager; `repocheck doctor` once unit 0.9 merges |
| Create the private repository under the owner's account and push the first commit. | Before Phase 0 | git; REST API |
| Open one pull request whose CI run shows the release dry run (Gate 0, Q15); at each phase gate start one `workflow_dispatch` run with `all-os` (Q20). | At each phase gate | git; REST API |
| Make the repository public, then enable private vulnerability reporting (Q16). | At Gate 1 | REST `PATCH /repos/{owner}/{repo}` (`visibility: public`); REST `PUT /repos/{owner}/{repo}/private-vulnerability-reporting` (admin access, §17) |
| Enable the `main` ruleset (block deletion and non-fast-forward pushes; require a pull request for every change, Q21), secret scanning and push protection; a free-plan private repository refuses each (§17). | At Gate 1, after the repository is public | REST API |
| Push or tag the previous engine's reference commits named in the maintainer's reference map (§13.2); on 2026-10-10 they existed only locally. | Before the old repository is archived | git |
| Archive the old repository (Q5). | After Gate 1 | REST `PATCH /repos/{owner}/{repo}` (`archived: true`, §17) |

---

## 17. Facts, unknowns and sources

Facts checked on 2026-10-10:
- GitHub default labels (10): `accessibility`, `bug`, `documentation`, `duplicate`,
  `enhancement`, `good first issue`, `help wanted`, `invalid`, `question`, `wontfix`.
  Source: docs.github.com, Managing labels.
- REST `state_reason`: `completed`, `not_planned`, `duplicate`, `reopened`. Source:
  docs.github.com, REST issues.
- Sub-issues: ≤ 100 per parent, ≤ 8 levels. Source: docs.github.com, Adding sub-issues.
- Issue dependencies REST: `.../dependencies/blocked_by` and `/blocking`. Source:
  docs.github.com, REST issue dependencies.
- Issue fields are generally available for GitHub organisations; not used (DR-31). Source:
  github.blog changelog, 2026-07-02.
- GitHub REST limits: 5 000 requests per hour per user token; secondary limits: at most 100
  concurrent requests, 900 points per minute (GET 1, write 5), content-creating requests about 80
  per minute and 500 per hour. Source: docs.github.com, Rate limits for the REST API.
- Starter-workflow test commands: Node `npm test`, Python `pytest`, .NET `dotnet test`, Go
  `go test ./...`, Rust `cargo test`, Dart `dart test`. Source: github.com/actions/starter-workflows,
  `ci/` (competitor survey C).
- Quota signals: Anthropic rate limits return 429 with `retry-after`; a spend limit returns 429
  without `retry-after`, error code `enforced_spend_limit_reached`, reset on the 1st of the month,
  00:00 UTC. Source: platform.claude.com, rate limits. OpenRouter rejects with 402 and a
  `limit_source` field. Source: openrouter.ai, API limits. (Competitor survey C.)
- Provider terms (DR-43): Anthropic does not permit third-party developers "to route requests
  through Free, Pro, or Max plan credentials on behalf of their users", and developers "may not
  collect, store, or intermediate Claude.ai credentials or session tokens"; a user logging in to
  the official CLI with their own subscription is allowed. Source: code.claude.com, Legal and
  compliance (OmniRoute survey). A third-party router that rewrites subscription OAuth traffic
  carries a critical remote-code-execution advisory (CVE-2026-88062, versions before 3.8.49) and 83
  runtime dependencies; account bans reported about one minute after connecting. Source: OmniRoute
  advisories, issues and package (OmniRoute survey). Its ban detector keeps terminal states for the
  operator and never auto-recovers them; one broad keyword falsely banned a healthy connection.
  Source: OmniRoute `BAN_DETECTION.md`, `RESILIENCE_GUIDE.md`.
- Zero-install survey (one Windows 11 x64 machine; same stdio MCP server in each candidate):
  binary size Go 7.3–7.9 MB (gzip 2.8–3.1 MB), Node single executable 100.7–151.1 MB, Bun
  64.7–99.8 MB; MCP `initialize` min / median Go 15.6 / 22.2 ms, Node single executable 116.6 /
  147.4 ms; idle memory Go 9.1 MB, Node single executable 49.7 MB, Bun 75.5 MB. Go cross-compiled
  from Windows to five other targets with `CGO_ENABLED=0`, no extra download, 22.6–33.6 s each
  (cold cache), all exit 0; not run on the targets. Bun's automatic cross-compile exited 1 on all
  six targets. A Node single executable built on Windows for darwin-arm64 had no code-signature
  block. Go darwin-arm64 carries a signature block, darwin-amd64 none; validity not checked. Go
  started a `.cmd` file without a shell (exit 0), and UTF-8 argv and tool arguments round-tripped
  byte-exact. The Go SDK v1.8.0 served `initialize`, `tools/list` and `tools/call`.
- Stack survey (same machine, 21 rounds): Go hello start min / median 11.5 / 17.1 ms. The Go
  compiler with `go vet` caught 0 of 5 tested classes (non-exhaustive switch, nil access, index,
  ignored error, data race); a nil-map write panicked at run time.
- git libraries: go-git merges only by fast-forward, has no rebase and no patch apply. Source:
  github.com/go-git/go-git, `COMPATIBILITY.md` (zero-install survey).
- MCP Go SDK v1.8.0: `ServerOptions.Instructions`; `Server.AddPrompt`, `AddResource`,
  `AddResourceTemplate`; generic `AddTool`; `StdioTransport`. Source: pkg.go.dev and
  github.com/modelcontextprotocol/go-sdk `mcp/server.go`. The SDK brings 8–11 further modules
  (the two surveys count differently).
- MCP Registry package type `mcpb`: a prebuilt binary hosted on GitHub or GitLab releases; the
  entry must carry `fileSha256`; the URL must contain "mcp"; the registry does not check the hash,
  clients do. Source: github.com/modelcontextprotocol/registry, `package-types.mdx`.
- winget ships with App Installer on Windows 11. Source: learn.microsoft.com, winget.
- SmartScreen warns on files that are not well known; a signed file also counts the
  certificate's reputation. Source: learn.microsoft.com, Microsoft Defender SmartScreen.
- Homebrew 5.0.0: casks without code signing are deprecated; casks in the official cask repository
  that fail Gatekeeper are disabled from September 2026. Source: brew.sh, 2025-11-12.
- GoReleaser v2.18 (stack survey: v2.18.3). OSS: Homebrew casks (since v2.10; formulas
  deprecated; unsigned casks need a quarantine-removal hook that may stop working), Scoop and
  winget manifests (pull request optional), SBOM through syft, cosign signing, snapshot builds into
  `dist` without upload. Pro only: npm publishing. MCP Registry publisher (since v2.13,
  experimental) supports `oci`, `npm`, `pypi`, `nuget`, not `mcpb`. "GoReleaser does not create
  any tags." Its attestation recipe attests `checksums.txt` with `actions/attest` (`id-token:
  write`, `attestations: write`). Source: goreleaser.com (Pro, homebrew_casks, winget, scoop,
  sbom, sign, snapshots, mcp, attestations, cookbooks/semantic-release).
- svu: next version from Conventional Commits (`fix` patch, `feat` minor, `!` or `BREAKING
  CHANGE` major, `chore` none); 876 stars; GoReleaser's cookbook pairs it with GoReleaser. Source:
  github.com/caarlos0/svu; goreleaser.com cookbook.
- release-please: release pull request model; `go` release type; tags and creates the GitHub
  Release on merge; does not publish; pull requests created with `GITHUB_TOKEN` trigger no
  workflows, so their checks need a personal token. Source: googleapis/release-please and
  release-please-action READMEs.
- go-semantic-release: single binary, GoReleaser hook; 485 stars. Source: its GitHub page.
- GitHub artifact attestations: `actions/attest`; permissions `id-token: write`, `contents: read`,
  `attestations: write`; verification `gh attestation verify`. Source: docs.github.com, Using
  artifact attestations.
- GitHub Actions: a workflow skipped by path, branch or commit-message filtering leaves required
  checks pending; a job skipped by `if:` reports success; jobs skipped because a dependency failed
  report no failure, so a required job needs `needs` with `always()`. Source: docs.github.com,
  Troubleshooting required status checks; Using conditions to control job execution.
- `actions/setup-go` v7: caching on by default (key from `go.mod`, or `cache-dependency-path`);
  `go-version-file` reads the `toolchain` line. Source: github.com/actions/setup-go.
- golangci-lint v2.14.0 (stack survey). Linters present: `staticcheck`, `errcheck`, `unused`,
  `govet` ("roughly the same as 'go vet' and uses its passes"), `exhaustive`, `errorlint`,
  `contextcheck`, `containedctx`, `noctx`, `bodyclose`, `forbidigo`, `gosec`, `gomodguard_v2`;
  formatters `gofmt`, `gofumpt`. nilaway is not built in; it runs as a module plugin
  (golangci-lint ≥ v1.57.0, `golangci-lint custom`) and its README warns of false positives.
  Source: golangci-lint.run linters and formatters pages; github.com/uber-go/nilaway.
- govulncheck exits non-zero when it finds a vulnerability (text output); a binary mode reads the
  binary's symbols. Source: pkg.go.dev, govulncheck.
- deadcode reports functions unreachable from `main` packages (Rapid Type Analysis). Source:
  pkg.go.dev, golang.org/x/tools/cmd/deadcode.
- `go mod tidy -diff` changes nothing and exits non-zero when the diff is not empty; `go test`
  caches successful package results and reruns only on changed inputs. Source: `go help mod tidy`,
  `go help test` (Go 1.25.7, local).
- The `tool` directive in `go.mod` exists since Go 1.24 and runs through `go tool`. Source:
  go.dev/ref/mod.
- `go version -m` prints a binary's embedded module versions. Source: pkg.go.dev/cmd/go.
- The race detector needs cgo and, except on macOS, a C compiler; on Windows a compiler with
  mingw-w64 runtime v8 or later; Windows support is amd64 only. Source: go.dev, Data race detector.
- `testing/synctest.Test` (since Go 1.25) panics when every goroutine of its bubble is durably
  blocked; `synctest.Sleep` arrived in Go 1.27.0. Source: pkg.go.dev, testing/synctest.
- `golang.org/x/sync/errgroup`: `WithContext`, `Go`, `SetLimit`, `TryGo`, `Wait`; the derived
  context is cancelled at the first non-nil error. Source: pkg.go.dev (v0.24.0).
- `os/exec`: `Cmd.Cancel` and `Cmd.WaitDelay` bound a child that ignores cancel or holds pipes
  open; on Windows `Command` quotes for `CommandLineToArgvW`, and `cmd.exe` batch files need
  `SysProcAttr.CmdLine`; since Go 1.19 lookup never resolves through the current directory
  (`ErrDot`). Source: pkg.go.dev, os/exec. `syscall.SysProcAttr` has `CmdLine` and
  `CreationFlags` on Windows and `Setpgid` on Linux. Source: `go doc` (Go 1.25.7, local).
- `golang.org/x/sys/windows` exports `CreateJobObject`, `AssignProcessToJobObject`,
  `TerminateJobObject`, `SetInformationJobObject`, `JOBOBJECT_EXTENDED_LIMIT_INFORMATION`. Source:
  pkg.go.dev (v0.49.0 page).
- Batch-file argument injection (BatBadBut): Go answered with a documentation update, not a code
  change. Source: flatt.tech, BatBadBut.
- npm trusted publishing (for the deferred npm channel, §15): npm CLI ≥ 11.5.1, workflow permission
  `id-token: write`, GitHub-hosted runners only. Source: docs.npmjs.com, Trusted publishers.
- Run isolation (isolation survey, measured on one Windows machine, NTFS, 504-file repository):
  numbers in §6.5. Worktrees share refs and config by default. Source: git-scm.com, git-worktree.
  `--shared` clones break when the source prunes objects. Source: git-scm.com, git-clone.
- Old code: `wc -l`, file headers, test-file lists and greps on the old repository's main branch.
- GitHub private vulnerability reporting: "Owners and administrators of public repositories can
  allow security researchers to report vulnerabilities securely." REST
  `PUT /repos/{owner}/{repo}/private-vulnerability-reporting` enables it; "The authenticated user
  must have admin access to the repository." REST `PATCH /repos/{owner}/{repo}` sets `visibility`
  (`public` or `private`) and `archived`. Source: docs.github.com, Configuring private
  vulnerability reporting for a repository; REST repositories.
- Fine-grained personal access tokens: the documented way to create one is the web page Settings →
  Developer settings → Personal access tokens → Fine-grained tokens; no creation API is documented.
  Source: docs.github.com, Managing your personal access tokens.
- GoReleaser changelog: `changelog.groups` (`title`, `regexp`, `order`) groups commits by the first
  line of the message; `use: git` is the default; `paths`, subgroups, `divider` and AI enhancement
  are Pro only; the page names the `goreleaser changelog` command inside its Pro-only AI section.
  Source: goreleaser.com, Changelog.
- svu: flag `tag.prefix` defaults to `v`, so the first release tag is `v1.0.0`. Source:
  github.com/caarlos0/svu, `main.go`.
- golangci-lint `gocognit` and `gocyclo`: setting `min-complexity`, default 30. Source:
  golangci-lint.run, linter settings. gocognit adds 1 for a whole `switch` plus nesting increments,
  where cyclomatic complexity adds 1 per case. Source: github.com/uudashr/gocognit, README.
- Previous engine's run ledger (375 finished runs, 2026-09-20 to 2026-10-08): one source's green
  runs had p90 89.2 min, and 17 of its 68 greens exceeded 60 min; 17 of 18 stalls came 12–64 min
  into a run; the third attempt added 12 of 81 greens; with 4 or more concurrent gates the test
  step's p50 was 2.8 × longer and it was red 2.6 × as often; gate p50 grew from 57 s to 1 220 s in
  18 days; budget-ended runs stopped near 4 M input tokens while green runs had p90 0.68 M; 135 of
  375 records named no unit; 11 of 375 carried a cost.
- Previous engine's lessons: one harness's fixed input per run measured 16 398 tokens; user-level
  rules and memory loaded into an unattended run cost about 28 k tokens on its first call.
- GitHub Actions on private repositories: included minutes per month GitHub Free 2 000, GitHub Pro
  3 000; price per minute Linux 2-core x64 $0.006, Windows 2-core x64 $0.010, macOS 3- or 4-core
  $0.062; standard hosted runners are free for public repositories. Source: docs.github.com, GitHub
  Actions billing, checked 2026-10-10. Actions usage is free for self-hosted runners. Source: the
  same page, checked 2026-10-10. GitHub rounds the minutes each job uses up to the nearest whole
  minute. Source: docs.github.com, Actions runner pricing, checked 2026-10-10.
- Repository rulesets, secret scanning and push protection on a free-plan private repository: the
  API answers HTTP 403 "Upgrade to GitHub Pro or make this repository public". Source: measured by
  the lead session, 2026-10-10.
- MCP Registry entry: the server entry is `server.json`; an MCPB package uses `"registryType":
  "mcpb"`. Source: github.com/modelcontextprotocol/registry, `package-types.mdx`, checked 2026-10-10.
- `go test -run <pattern>` that matches no test prints `ok <package> <time> [no tests to run]` and
  exits 0. Source: Go 1.25.7, local run on the standard library package `strings`.
- git: an empty `credential.helper` resets the helper list; `GIT_CONFIG_NOSYSTEM` skips the system
  config; `GIT_CONFIG_COUNT` with key and value pairs sets config from the environment;
  `GIT_TERMINAL_PROMPT=0` stops terminal prompts; `core.hooksPath` moves the hook directory;
  `core.fsmonitor` starts a file system monitor; `protocol.ext.allow` defaults to `never` and the
  `file` protocol to `user`; `submodule.recurse` defaults to false; `git diff` has `--no-ext-diff`
  and `--no-textconv`. Source: git 2.52.0 documentation (`git-config`, `gitcredentials`, `git`,
  `git-diff`), local.
- GitHub pull requests REST: create `POST /repos/{owner}/{repo}/pulls`; merge
  `PUT /repos/{owner}/{repo}/pulls/{pull_number}/merge` with `merge_method` `merge`, `squash` or
  `rebase`, `commit_title`, `commit_message`, and `sha`, the SHA the head must match to allow the
  merge. Source: docs.github.com, REST pulls, checked 2026-10-10.
- Lead machine (Windows 11 x64, measured 2026-10-10): Go 1.25.7 with `CGO_ENABLED=0`; gcc,
  GoReleaser, golangci-lint and svu are not on `PATH`.

Unknown. Each open item names the unit whose Done item measures it before the engine relies on it,
or why no measurement is needed. The measured value is that unit's evidence (§8.4) and then moves to
the facts above.
- U1 Resolved by owner rule (§8.4, Q4): GraphQL `repository.issueTypes` decides; the tracker
  contract suite proves the label path and the built-in type path.
- U2 Native issue dependencies in personal-account repositories. No measurement needed: only the
  deferred native dependencies use it (§15), and their trigger checks it.
- U3 How a pull-request dry run prints the release notes of `changelog.groups` in GoReleaser OSS,
  given that the page lists `goreleaser changelog` in a Pro-only section (§17). Measured by unit 0.12
  on the Gate 0 pull request.
- U4 svu: the first version without a prior tag, the bump of a breaking change before 1.0, and the
  result on an already-tagged commit. Measured by unit 3.6 in the dry-run job logs.
- U5 Resolved: the latest Contributor Covenant is 3.0 (contributor-covenant.org, "Latest Version" →
  `/version/3/0`, checked 2026-10-10); the repository links 3.0.
  Source: contributor-covenant.org, checked 2026-10-10.
- U6 Each derived gate command on each target repo, including the unverified Flutter and JVM
  commands (proved per repo at onboarding, §7.1). Measured by unit 1.21 at each onboarding, and for
  Flutter and JVM by units 2.2 and 2.6.
- U7 Where the old code sets the default of 2 fix rounds. No measurement needed: §7.4 sets the
  default, and the ledger replaces it after 50 units.
- U8 Dependency restore time in a fresh clone with a warm user-level cache. No measurement needed
  until its §15 trigger; check records hold restore times.
- U9 Hosted-runner images: CPU architecture per OS, cgo and a C compiler on the Linux image for
  `-race`, and the GitHub CLI on the Linux image for the release job (§12.3).
  Measured by unit 0.12 (architecture, cgo and compiler per runner) and unit 3.6 (`gh`).
- U10 Starting a shallow clone at a commit that is not a local branch head. Measured by unit 1.16.
- U11 Resolved: §10.1 states the least permissions as a default; unit 1.25 proves them.
- U12 Clean OS images for `clean-os-smoke`; the minimum git version. Measured by unit 0.12 (images)
  and unit 1.20 (the oldest git that passes the git tests).
- U13 Detecting pid reuse per OS (process start time). Measured by unit 0.21.
- U14 Effect of git's long-path option on Windows clones. Measured by unit 2.12.
- U15 Which harnesses report the model they ran and accept a command restriction. Measured by unit
  1.13.
- U16 Memory use of a run's process tree per OS. No measurement needed: M7 prints `unknown` where
  the OS reports none (DR-21); unit 1.24 tests it.
- U17 Clone and removal times on macOS, Linux and repositories with ≥ 10 000 files. Measured by unit
  2.12 on three OS; the free-disk rule uses each machine's own measured clone size (§6.4).
- U18 nilaway as a golangci-lint module plugin: build time of `custom-gcl`, its cache, and its
  false-positive rate on this code. Measured by unit 0.11.
- U19 The token that pushes the Homebrew tap, Scoop bucket and winget manifests from CI (GitHub
  App or fine-grained token). Checked by unit 3.10, an owner unit.
- U20 MCPB: one registry entry for six targets, the bundle layout, and the publishing tool.
  Measured by unit 3.9.
- U21 Cross-built binaries for the architectures no hosted runner runs (U9) start on real hardware.
  No measurement needed before release: unit 3.6 names in the release notes each target that no
  runner started.
- U22 Windows: assigning a child to a Job Object before it can start its own children, through
  `os/exec`. Measured by unit 0.6 on the Windows runner.
- U23 CI: finding an earlier green run of the same job for the same tree and OS (DR-12 in CI).
  Measured by unit 0.12.
- U24 Gatekeeper and SmartScreen behaviour for unsigned binaries per channel (browser, winget,
  Scoop, Homebrew tap); the Apple Developer Program fee; a macOS signing and notarization tool that
  runs in the release job. Measured for unit 3.10, where the owner decides Q7.
- U25 Which providers return a structured code for a suspended or deactivated account. Measured by
  unit 2.8.
- U26 How the engine captures screens at fixed widths with a project's own browser automation tool,
  without adding a dependency. Measured by unit 3.8.
- U27 Whether argv survives as separate arguments through an access method such as a remote shell
  client. Deferred with Phase 4: measured by unit 4.4 before the first live operation.
- U28 Per harness and OS, which config isolation keeps the user's login (one harness on macOS needs
  the login keychain; Linux and Windows not measured). Measured by unit 1.12.
- U29 GitHub Actions minutes for private repositories on the owner's plan: the aiwe repository
  runs CI while it is private (Q16), and `aiwe-sandbox` runs its fixture CI (Q18). Measured by unit
  0.12: the `required` job prints the month's minutes per OS against the plan (§17, Q20).
- U30 The minutes source of §12.2: which token the CI job uses to read user-level billing, and
  whether the usage summary answers for the owner's account. The REST docs list
  `GET /users/{username}/settings/billing/usage/summary` as public preview, and the usage report
  only for the enhanced billing platform. Source: docs.github.com, REST billing usage, checked
  2026-10-10. Measured by unit 0.12.
- U31 The Windows `OpenProcess` access-denied branch of the process start read
  (`core/procstart_windows.go`, unit 0.21) has no test: no test can reach it without a seam.
