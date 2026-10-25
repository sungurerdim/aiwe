# Status

<!-- auto:start -->
<!-- generated 2026-10-25T22:58:20Z by go run ./internal/cmd/records from git, gh issue list and gh run list; do not edit by hand -->
- HEAD: 2a6d1e956c08 test(core): stop the live child without a race
- Branch: main, ahead 0, behind 0 of the upstream
- Issues: 227 open, 211 closed
- CI (main, workflow ci): success, 2026-10-21T01:12:42Z, sha d130d8fce30c
<!-- auto:end -->

## Broken

- Unlanded G1 roots: #454 (bridge clone `u454`, branch `unit/454-item-id-schema`, 466e206 + 9f1c9d4) and #503 (`u503`, branch `unit/503-ledger-lifecycle-events`, 701539b + d4b2d23). Package tests green, not audited, not landed. Open notes: no enum on `flow`/`quality`/`priority`, `v≠1` not refused, `checkKind` enum not enforced.
- main is 6 commits ahead of origin (007fc49..7cf3d7b), not audited, not pushed.
- The CI line above shows the last `ci.yml` run on main; the fast-forward of 45f760b after PR #511 started no new run.

## Live configuration

- Work is still tracked in GitHub issues; the work store under `work/` takes over at the G1 cutover (SPEC L61).
- Units are written by the lead's own subagents; external agents are not used (owner decision 2026-10-25).
