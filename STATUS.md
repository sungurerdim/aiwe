# Status

<!-- auto:start -->
<!-- generated 2026-10-29T01:36:21Z by go run ./internal/cmd/records from git, gh issue list and gh run list; do not edit by hand -->
- HEAD: 35f47e91eea7 refactor(gates): use core.LedgerFile for ledger
- Branch: main, ahead 122, behind 0 of the upstream
- Issues: 382 open, 237 closed
- CI (main, workflow ci): success, 2026-10-21T01:12:42Z, sha d130d8fce30c
<!-- auto:end -->

## Broken

- Measured 2026-10-29. The batch clone `aiwe-wt/batch` (tip 7edc696) holds 28 staged units on top of origin/main: #73 #270 #290 #292 #323 #332 #365 #432 #433 #435 #437 #471 #518 #521 #524 #528 #532 #535 #539 #580 #581 #583 #584 #585 #586 #619 #622 #623. Each passed a narrow lead verify and an audit; the batch has had no integration run and no CI run.
- Measured 2026-10-27, not re-measured: core `TestOutcomes` is red on the batch (not on main): outcome strings used in staged code have no row in the outcome table. Needs its own issue before ship.
- Units in flight and units waiting for the lead: `aiwe-wt/runs/lead-tools/pending-dispatch.md` (newest section last) and `python ../aiwe-wt/runs/status.py --needs-lead`. #412 landed on the bridge as bb5daf1 (2026-10-29).
- Review of 95 issues closed since 2026-10-19 by external or unknown closers: 3 of 16 review runs done (17 issues: 6 OK, 11 with gaps, 1 high: #372 is fixed only for the Cursor writer). Ledger `aiwe-wt/runs/lead-tools/closed-review/PLAN.md`; test run `closed-review/tests.md` (main: go test, full gate, repocheck green; `aiwe item lint` named by #438 does not exist; the bridge pytest suite needs `PYTHONUTF8=1`).
- #59 is blocked by #526: SPEC §7.4 retries an environment red in another leased slot, and the lifecycle leases no slot yet.
- Test-system goal #587, measured 2026-10-29: local main carries Closes commits for #588 #589 #590 #591 #592 #593 #594 #596 #601 #602 #604 #605 #606 #607 #608 #609 #615 #628 #629 (code and docs, not pushed, so the issues stay open). Main at 35f47e9 after #596 and #594: gates and engine tests, vet, lint and repocheck green (`aiwe-wt/evidence/main-35f47e9-594-596.txt`).
- Measured 2026-10-27: the landed library code (#593 #601) had no production caller until #594 and #598 (not re-measured after #594 landed); the advisory deadcode step (required:false) lists it until then. `engine.Verify` has no production caller until #254, so the verify check record is not written in use yet.
- main's commits ahead of origin (the Branch line above) are not pushed; since the test-system units they include code.
- The CI line above shows the last `ci.yml` run on main; the fast-forward of 45f760b after PR #511 started no new run.

## Live configuration

- Work is still tracked in GitHub issues; the work store under `work/` takes over at the G1 cutover (SPEC L61).
- Owner, 2026-10-29: ask the owner as little as possible; at a decision point the lead takes the option that fully meets the industry standards and the project's purpose and goals (SPEC §1), and records it. The 28-unit batch is approved to ship.
- The owner runs up to 10 agents in parallel (2026-10-29; was 3 earlier the same day). Agents run no tests, gates or builds, the lead runs each check in sequence; release, the full gate and CI wait for a batch of 25-30 issues.
- First work since 2026-10-27 (owner): the test-system goal #587, units #588–#616 plus #522 and #257, in the order phase 0 → 1 → 5 → 4 → 3 → 2 → 6. Its decisions are SPEC §16 L63–L79. Baseline: `docs/evidence/2026-10-27-test-baseline.md`.
- Owner rules for this goal (2026-10-27): every piece of work has an issue; GitHub Actions is used as little as possible (the repository is private, so minutes are metered); everything sensible runs locally and efficiently.
- Every tool fix that is safe runs before its check, so a fixable finding never reaches anyone (owner 2026-10-27, SPEC L79, unit #617). Until #617 lands, the lead applies it by hand (`AGENTS.md` section 3, "Lead workflow").
- The D1 wave of M-Usable (#263) is planned in `aiwe-wt/runs/d1-plan.md`: 8 items, 19 units; the item records and blocked-by links are not yet rewritten except #59.
