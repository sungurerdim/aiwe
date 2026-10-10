# Working in this repository

Read this first. It points at the rules; it does not repeat them.

## What this is

aiwe is an engine that turns a goal issue into verified, merged code through small units, each run
by a role-specific agent. Go, one binary per OS and architecture. The design and every rule live in
[docs/SPEC.md](docs/SPEC.md); it is the only source of truth.

## Checks

An agent never runs the gate, a unit verify, or a test, lint or build command itself (DR-12,
SPEC §9.4). It asks for its unit verify through `aiwe verify`, and the engine runs it and records
the result. Until unit 1.4 brings `aiwe verify`, the lead session runs each verify once and records
it (SPEC §14). Until a release exists, the gate is `go run ./cmd/aiwe gates gate --repo .` (SPEC
§12.2, §14); the engine or the lead session runs it, and it must exit 0 before a unit is done.
Until unit 0.7 brings `aiwe gates gate`, the gate result is the unit's own verify (SPEC §14, row
0.7).

## Red lines

Each line names the rule; the rule text is in SPEC §3.

- aiwe is built with aiwe (DR-46); the hand-over points are in SPEC §14. Before the self-hosting
  point (unit 1.25) only the lead session and its role-specific agents work, by hand, under the
  same standard. After it, no unit is worked outside aiwe; a unit worked by hand is a defect and is
  recorded as one. From unit 1.29 on, CI flags a `main` commit that closes or references a unit
  issue and has no `Run-Id` trailer; Dependabot and outside commits are `external` (Q17).
- Every piece of work is an issue before it starts, and a finished one closes mechanically (DR-50).
  Until unit 0.12 brings CI, the closeout records "no workflow on main yet" and closes on the lead
  session's integration evidence (SPEC §14, row 0.12).
- Every diff counts from the run's base commit (DR-47).
- Commit only on your run branch; no push, merge, issue close or tracker write (DR-18).
- A unit stays inside its declared file set (DR-11, DR-14).
- Tests are never weakened, skipped or mocked away (DR-28).
- Absence is not evidence; unmeasured is `unknown`, never `0` (DR-13, DR-21).
- No machine-specific path, no product name outside its one constant, no recursive removal outside
  its one owner (DR-25).
- No new third-party dependency outside the allow-list without an owner decision (DR-41).
- Source, docs and commit messages are in English; commits follow Conventional Commits (DR-29,
  SPEC §12.3). A breaking marker needs the owner's approval (SPEC §12.3).
- Every element must be necessary (DR-42).

## Where things are

| Need | Place |
|---|---|
| Purpose, users, metrics | SPEC §1 |
| Design rules DR-1…DR-50 | SPEC §3 |
| Coding rules and patterns | SPEC §4, §5 |
| Modules and package layout | SPEC §6.1 |
| Sandbox levels and the open risk | SPEC §6.7 |
| Unit template | SPEC §8.4 |
| Roles and what each may not do | SPEC §9 |
| Gate, CI and release | SPEC §12 |
| Behaviour contracts from the previous engine, as Go test cases | SPEC §13.1 |
| Delivery plan, self-hosting hand-over; one issue per unit, milestone per phase | SPEC §14 |
| Deferred work and its triggers | SPEC §15 |
| Owner decisions, owner actions and lead tasks | SPEC §16 |
| Facts and unknowns | SPEC §17 |
