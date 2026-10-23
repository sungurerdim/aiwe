# Working in this repository

Read this first. It points at the rules; it does not repeat them.

## What this is

aiwe turns a goal issue into verified, merged code through small units, each run by a role-specific agent. Go, one
binary per OS and architecture. The design lives in [docs/SPEC.md](docs/SPEC.md); the rule registry is
[docs/standards.md](docs/standards.md) (#314 K41), and the SPEC §3 tables come from it. The tracker holds the work:
one goal issue per milestone, units as its sub-issues (SPEC §13).

## First priority

The owner set the issue and process redesign K33–K44 (tracker goal and plan: #314, confirmed 2026-10-18) as the first
priority. Order, from #314 K43: K39 report format (bridge unit #339, closed) → K34, K35, K37 in the bridge issue tool
and queue script (new bridge units) → clear obsolete open issues, then migrate the rest → SPEC unit with the K41
registry and K42 rule changes → K40 derived status → K36 sync. Defects in the bridge that every unit runs through come
before it; the current queue is in `aiwe-wt/HANDOFF.md`. Other open work continues only where it does not touch these
files and does not wait on them.

## Checks

An agent never runs the gate, a unit verify, or a test, lint or build command itself (DR-8, SPEC §9.4). It asks for
its unit verify through `aiwe verify`; the engine runs it and records the result. Until `aiwe verify` exists, the lead
session runs each verify once and records it. Until a release exists, the gate is
`go run ./cmd/aiwe gates gate --repo .` (SPEC §12.2); the engine or the lead session runs it, and it must exit 0
before a unit is done.

## Red lines

Each line names the rule; the rule text is in the registry `docs/standards.md` (SPEC §3 is generated from it).

- aiwe is built with aiwe (DR-23). Until M-Skeleton closes, only the lead session and its role-specific agents work, by
  hand. The lead applies the standard under L5. After M-Skeleton closes, a unit worked outside aiwe is a defect and is
  recorded as one. From then on, CI flags a `main` commit that closes or references a unit issue and has no `Run-Id`
  trailer (Q17).
- Issues are the single source of truth for work: every piece of work is an issue before it starts (DR-20, O5), and
  the issue is a typed record (SPEC §8.4, R2). A unit commit carries exactly one `Closes <owner>/<repo>#N` and no
  `Refs`, written by the engine; a non-unit commit carries no issue trailer (R5, SPEC §12.3). A bridge unit has no
  remote: the engine closes it with its evidence file until #269 removes the bridge.
- Every diff counts from the run's base commit (DR-9).
- Commit only on your run branch; no push, merge, issue close or tracker write (DR-13).
- A unit stays inside its declared file set (DR-7, DR-9).
- Tests are never weakened, skipped or mocked away (DR-12).
- Absence is not evidence; unmeasured is `unknown`, never `0` (DR-6).
- No machine-specific path and no product name outside its one constant (DR-17); no recursive removal outside its one
  owner (DR-11).
- No new third-party dependency outside the allow-list without an owner decision (DR-21).
- Source, docs and commit messages are in English (DR-18); commit messages follow SPEC §12.3. A breaking marker needs
  the owner's approval.
- Every element must be necessary (DR-22).

## Where things are

| Need | Place |
|---|---|
| Purpose, users, metrics | SPEC §1 |
| Design rules DR-1…DR-23 and the §3.1 standards | `docs/standards.md` (the one registry, R9); SPEC §3 is generated from it |
| The issue record, intake gates and closing | SPEC §8.4, §7.3 |
| One report format (SARIF) | SPEC §7.9 |
| Coding rules and patterns | SPEC §4, §5 |
| Modules, MCP and CLI, disk layout, concurrent sessions | SPEC §6 |
| Sandbox levels and the open risk | SPEC §6.7 |
| Roles and what each may not do | SPEC §9 |
| Gate, CI, commit messages and release | SPEC §12 |
| Milestones | SPEC §13 |
| Deferred work and its triggers | SPEC §15 |
| Decisions | SPEC §16 |
| Facts and sources | SPEC §17 |

## Host tools (dsh)

The dsh host may carry three plugins: `dsh-github`, `dsh-taskboard` and `dsh-mcp-panel`. They add tools and change no
rule; where one is absent, its CLI equivalent (`gh`, the tracker API) is the fallback. All three load at profile level,
so every session carries their schemas whether or not it uses them.

| Need | Tool | Boundary |
|---|---|---|
| Tracker state from the API | `gh_repo`, `gh_issue`, `gh_search`, `gh_review`, `gh_checks`, `gh_file` | Read-only. Counts and status come from the API, never from notes |
| Issue and PR writes | `issue_*`, `pr_*` | Lead only. DR-13 keeps a unit agent from any tracker write, push or merge; the lead needs green CI and a GREEN audit first |
| MCP health while debugging the server | `/mcp`, `mcp_probe`, one trial call | Pointer: `aiwe-wt/evidence/mcp-path.md`; a finding is an issue (`bulgu-her-zaman-issue.md`) |
| Session-local work mirror | `taskboard_*` | Not a work list. DR-20 stands: one issue before any work, no second status and no second tracker |

`todo_write` and `taskboard_*` are scratch state for one session: they hold no status that outlives the run, and
neither is a tracker (DR-20). A unit's state is its issue, its evidence file and its run branch.

Tool output is not evidence by itself (DR-6): name the issue, the SHA and the run. A green `gh_checks` is green for the
ref it was read from; it is not the audit verdict and it does not stand in for the gate.

## Taking over from another harness

Start here if you are Cursor, OpenCode or any other host picking up work a Claude Code session left. This section is a
map. It holds no state; the facts live in the places below. Notes marked (tr) are written in Turkish. Reply to the
owner in Turkish; write everything committed to this repository in English (DR-18).

Read in this order: this file → `aiwe-wt/HANDOFF.md` → `docs/SPEC.md` §1, §3, §13 → `docs/standards.md` → memory
`MEMORY.md` → the Known traps below → open tracker items.

**Split of work.** An external agent drives units to the point where they wait for an audit. Claude, the lead session,
audits them, lands and ships them, pushes, merges and closes issues, all in one batch. `aiwe-wt/HANDOFF.md` holds the
contract (what each side may run), the loop, the derived ready-for-audit list and the current queue.

Warning: `durum.md` is stale. It describes the pause of 2026-10-15, not the present, and it is deleted when #314 K40
lands. Do not take its work order, its issue lists or its clone list as current: the first priority above, #314 and the
live tracker win. Its tool and Windows traps are not all written down here yet; the "Known traps" section below holds
the ones that are (R8).

### Outside the repository

`~` is the owner's home. None of this is in git.

| Need | Place |
|---|---|
| Owner decisions and the latest pause state (tr) | `~/.claude/projects/D--GitHub-aiwe/memory/`: `MEMORY.md` indexes every note; the newest `oturum-*.md` is the latest session; `durum.md` is stale (see above); `tasarim-k33-k44.md` points to the confirmed design revision |
| Machine and user rules, Windows and Git Bash traps (tr) | `~/.claude/CLAUDE.md` (the `@`-imports there are Claude-only: `RTK.md` is a token proxy, ignore it) |
| General engineering rules (en) | `~/.claude/rules/dev-rules.md` (binding order at the top); lead-only part `~/.claude/dev-rules-lead.md` |
| Extra guardrails for hosts weaker than Claude Code | `~/.claude/dev-rules-references/portable-supplement.md`; domain rules `safety.md`, `operations.md` in the same folder |
| Role cards (analyst, architect, engineer, auditor, test-author, debugger, security-reviewer, ux-reviewer, doc-writer, operator) | `~/.claude/agents/*.md`; the bridge reads its own copy in `aiwe-wt/roles/` |
| Lead tools: load measurement, narrow test, issue writer | `~/.claude/araclar/` (`yuk.py`, `dar-test.py`, `issue.py`) |
| Unattended mode (work with the owner away) | skill `otopilot` in `~/.claude/skills/`; state in `~/.claude/otopilot.d/` |
| Past subagent reports | `~/.claude/raporlar/` |
| Rule rationale and model measurements (tr) | `~/.claude/olcumler.md` |

Claude hooks in `~/.claude/hooks/` do not run in other hosts; follow the Known traps section below by hand. The bridge
also has its own traps paragraph in its docstring.

### The bridge: `aiwe-wt/`

aiwe cannot yet work on itself, so a sibling folder `aiwe-wt/` next to this repository is the stand-in (DR-23). It has
its own local git history and no remote. It is host-independent Python and shell; any host can drive it.

| Need | Place |
|---|---|
| Handoff contract, the loop and the current queue | `aiwe-wt/HANDOFF.md` |
| Lead audit verdict that resumes a waiting unit (lead only) | `aiwe-wt/runs/lead_audit.py <N> GREEN\|RED <report>` |
| Unit pipeline (tests → red proof → audit → implement → verify → audit → queue), usage and stop states | docstring of `aiwe-wt/pipe.py`; per-unit state `aiwe-wt/runs/u<N>.state`, overview `aiwe-wt/runs/summary.txt` |
| Lead commands (verify, confirm, land) | `aiwe-wt/lead.py`, `land.sh`, `queue.sh`, `ship.sh` (one PR from the batch clone; the lead ships) |
| Run one role on an external provider (OpenCode Go API, Cursor CLI) | `aiwe-wt/ext.py`, `ext.sh`; model and provider notes in memory `dis-ajan-yurutme.md` |
| Open a unit clone | `aiwe-wt/start.sh <issue> <slug>` (a bridge unit is cloned by hand until #422, see `HANDOFF.md`); never a git worktree (memory `worktree-yok.md`, SPEC §6.5) |
| Bridge issue writer (this repository only) | `aiwe-wt/issue.py`; the global `issue.py` conflicts with SPEC §8.4 |
| Evidence, audits, calibration, mapping of issues to plans | `aiwe-wt/evidence/` (start with `calibration-pairs.md`, `apply-2026-10-17.md`) |
| Batch of finished units waiting to ship | clone `aiwe-wt/batch` |
| Parked and stopped clones | `aiwe-wt-parked/`; clones in `aiwe-wt/u<N>` with state `needs_lead` |

### Tracker

GitHub issues of this repository are the only work list (DR-20). Read counts and status from the API, never from notes.
Anchors: M-Skeleton goal #262, MCP goal #263, work-session goal #293, design revision and its to-do list #314, bridge
issue #269 and its children. Open `gh issue list` and read #314 before choosing work. Issue format: SPEC §8.4; write
issues through the bridge `issue.py`.

The design revision K33–K44 is in SPEC (revision 10, R1–R12): one registry, typed JSON issue records, intake gates
against duplicates and overlaps, engine-written `Closes`, one SARIF-based report format, derived status. The bridge
keeps thin versions of the same contract until #269 retires it (#314 K43). With it, this file's commit-trailer rule,
the "Known traps" source and the `durum.md` read step changed (#314 K37, K40, K42), and SPEC §8.4 is the record, not
the headings. All records, task texts and reports are English.

### Owner decisions that change what you do

Full text is in the memory notes named here; they are not repeated.

- Quality over time over cost; "right the first time" (`durum.md`, #314 K32).
- No mention of any other project in any aiwe text (`eski-proje-atif-yasagi.md`).
- aiwe stores no secret; provider CLI login or an environment variable only (`kimlik-bilgisi-sifir-sorumluluk.md`).
- Unit work is delegated to external agents; the lead stays small. Mechanism units are audited by Claude until #341
  closes; the external auditors were measured too lenient (`dis-ajan-yurutme.md`, `aiwe-wt/evidence/calibration-pairs.md`).
  There is no fallback: the owner rejected an external audit on 2026-10-22. With no Claude session, the unit waits
  for one; it is never audited by the model that wrote it.
- The lead merges when CI is green and the audit is GREEN, and files every finding as an issue (`lead-yetkileri.md`,
  `bulgu-her-zaman-issue.md`).
- The lead ships without asking. A ship is a boundary, never a per-unit step: related units are batched, the batch is
  merged onto `main`, the gate runs, and green CI fast-forwards `main`. Ship when the batched group is done, and about
  every two hours while work keeps arriving.
- Parallel runs are limited per provider and by measured load, not by a fixed cap (`paralel-sinir.md`).
- Never start a file without a runnable extension (`masaustu-pencere.md`); aiwe does not manage the owner's own
  session settings (`aiwe-sorumluluk-siniri.md`).
- Owner-only actions pending: opening the repository to the public (`depo-acilinca.md`, `ci-batch.md`).

## Known traps

Tool facts that cost a run, kept here until the bridge retires (R8).
- Claude hooks in `~/.claude/hooks/` do not run in other hosts. Follow by hand what they enforce: no `--no-verify`,
  no `git filter-branch`, no `git reset --hard`, no `git clean -f`; commit messages come from a file
  (`git commit -F <explicit path>`).
- Turkish text never enters git or `gh` argv, a commit message or a tracked file (DR-18).
- The `@`-imports in `~/.claude/CLAUDE.md` are Claude-only; `RTK.md` there is a token proxy, ignore it.
- A lint step that builds `custom-gcl` from the upstream clone loaded no packages on the measured host; the gate runs
  the tools-module golangci-lint instead (SPEC §5.17, §12.2, #365).
- `durum.md` "Tuzaklar" still holds the Windows and Git Bash traps; they move here one by one (R8).
- Read a process's command line before killing it. `pipe.py`, `ext.py` and `pytest` all belong to some unit's run,
  and a kill by process name takes a neighbour's work with it; name the pid and the unit it serves.
- One heavy integration at a time. `queue.sh` and `ship.sh` hold `.batch.lock` because two of them collide in the
  process tree, not out of caution: a second queue is refused with a reason, and that refusal is correct.
- A session that drives units keeps its working discipline in `aiwe-wt/evidence/mcp-path.md` (parallel width,
  gate batching, evidence before a close). It is a pointer: the rules stay in the SPEC and the registry.
