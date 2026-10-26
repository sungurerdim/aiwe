# Status

<!-- auto:start -->
<!-- generated 2026-10-26T20:34:03Z by go run ./internal/cmd/records from git, gh issue list and gh run list; do not edit by hand -->
- HEAD: debf13b05c59 docs: regenerate STATUS after the 7b0c154 ship
- Branch: main, ahead 1, behind 0 of the upstream
- Issues: 244 open, 235 closed
- CI (main, workflow ci): success, 2026-10-21T01:12:42Z, sha d130d8fce30c
<!-- auto:end -->

## Broken

- Measured 2026-10-26. The batch clone `aiwe-wt/batch` holds 11 staged units on top of origin/main 7b0c154: #432 #433 #437 #471 #524 #532 #539 #528 #435 #365 #323. Each passed a narrow lead verify and an audit; the batch has had no integration run and no CI run.
- Four units are fixed but not verified (the owner holds tests while other load runs; commands in `aiwe-wt/runs/lead-tools/pending-verify.md`):
  - #521 at `u521` 52a8048: full engine green at 17684b6; 3f03f87 red under full CPU load (a take gave up after 40 ms), fixed by a backoff.
  - #332 at `u332` 82eaa6e: full gates hung for 10 min at dfa0ee4 (a test fake never acted); fixed in 925e983, narrow run green; audit F1 (two clones replace each other's changed-tree program) fixed.
  - #535 at `u535` e5615ce: security RED at 6da2bff (M1, M2, S1, S2), fixed; needs a security re-review by a separate run.
  - #518 at `u518` 29e9e64: security RED at 1342b30 (M4, S1), fixed; needs a security re-review, then the records fix.
- #59 is blocked by #526: SPEC §7.4 retries an environment red in another leased slot, and the lifecycle leases no slot yet.
- main's commits ahead of origin (the Branch line above) are docs only and not pushed.
- The CI line above shows the last `ci.yml` run on main; the fast-forward of 45f760b after PR #511 started no new run.

## Live configuration

- Work is still tracked in GitHub issues; the work store under `work/` takes over at the G1 cutover (SPEC L61).
- The owner runs the lead in the main session only (2026-10-26): no subagents, no tests or gates while the owner's load runs; release, the full gate and CI wait for a batch of 25-30 issues.
- The D1 wave of M-Usable (#263) is planned in `aiwe-wt/runs/d1-plan.md`: 8 items, 19 units; the item records and blocked-by links are not yet rewritten except #59.
