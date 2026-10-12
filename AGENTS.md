# Working in this repository

Read this first. It points at the rules; it does not repeat them.

## What this is

aiwe turns a goal issue into verified, merged code through small units, each run by a role-specific agent. Go, one
binary per OS and architecture. The design and every rule live in [docs/SPEC.md](docs/SPEC.md); it is the only source
of truth. The tracker holds the work: one goal issue per milestone, units as its sub-issues (SPEC §13).

## Checks

An agent never runs the gate, a unit verify, or a test, lint or build command itself (DR-8, SPEC §9.4). It asks for
its unit verify through `aiwe verify`; the engine runs it and records the result. Until `aiwe verify` exists, the lead
session runs each verify once and records it. Until a release exists, the gate is
`go run ./cmd/aiwe gates gate --repo .` (SPEC §12.2); the engine or the lead session runs it, and it must exit 0
before a unit is done.

## Red lines

Each line names the rule; the rule text is in SPEC §3.

- aiwe is built with aiwe (DR-23). Until M-Skeleton closes, only the lead session and its role-specific agents work, by
  hand. The lead applies the standard under L5. After M-Skeleton closes, a unit worked outside aiwe is a defect and is
  recorded as one. From then on, CI flags a `main` commit that closes or references a unit issue and has no `Run-Id`
  trailer (Q17).
- Issues are the single source of truth for work: every piece of work is an issue before it starts (DR-20, O5).
  An intermediate commit carries `Refs <owner>/<repo>#N`; the commit that finishes the work carries
  `Closes <owner>/<repo>#N`, so the issue closes on merge (SPEC §12.3, L7).
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
| Design rules DR-1…DR-23 | SPEC §3 |
| Coding rules and patterns | SPEC §4, §5 |
| Modules, MCP and CLI, disk layout, concurrent sessions | SPEC §6 |
| Sandbox levels and the open risk | SPEC §6.7 |
| Issue standard | SPEC §8.4 |
| Roles and what each may not do | SPEC §9 |
| Gate, CI, commit messages and release | SPEC §12 |
| Milestones | SPEC §13 |
| Deferred work and its triggers | SPEC §15 |
| Decisions | SPEC §16 |
| Facts and sources | SPEC §17 |
