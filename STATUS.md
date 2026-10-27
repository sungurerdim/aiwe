# Status

<!-- auto:start -->
<!-- generated 2026-10-27T10:54:44Z by go run ./internal/cmd/records from git, gh issue list and gh run list; do not edit by hand -->
- HEAD: e4f82cde73e4 docs(spec): L79 fix before check at gate points
- Branch: main, ahead 29, behind 0 of the upstream
- Issues: 289 open, 235 closed
- CI (main, workflow ci): success, 2026-10-21T01:12:42Z, sha d130d8fce30c
<!-- auto:end -->

## Broken

- Measured 2026-10-27. The batch clone `aiwe-wt/batch` holds 17 staged units on top of origin/main 7b0c154: #432 #433 #437 #471 #524 #532 #539 #528 #435 #365 #323 #332 #73 #580 #586 #518 #535. Each passed a narrow lead verify and an audit; the batch has had no integration run and no CI run.
- core `TestOutcomes` is red on the batch (not on main): outcome strings used in staged code have no row in the outcome table (conflict, landed, claim_missing, claim_schema_unsupported and three run-spec messages). Needs its own issue before ship.
- Units in flight, each with its next step in `aiwe-wt/runs/lead-tools/pending-dispatch.md` (2026-10-27 sections): #521 audit RED (N1 cancel test, N3 stuck reclaimer); #270 security RED (multi-line paste reaches the shell); #583 audit RED (lows); #290 audit RED (fake gh differs from gh); #585 fixed, needs re-audit; #292 #581 #584 green, need audit; #430 root cause proven (slots.run passes no stdin), fix pending.
- Review of 95 issues closed since 2026-10-19 by external or unknown closers: 3 of 16 review runs done (17 issues: 6 OK, 11 with gaps, 1 high: #372 is fixed only for the Cursor writer). Ledger `aiwe-wt/runs/lead-tools/closed-review/PLAN.md`; test run `closed-review/tests.md` (main: go test, full gate, repocheck green; `aiwe item lint` named by #438 does not exist; the bridge pytest suite needs `PYTHONUTF8=1`).
- #59 is blocked by #526: SPEC §7.4 retries an environment red in another leased slot, and the lifecycle leases no slot yet.
- Test-system goal #587, measured 2026-10-27: #588 #589 #590 #593 #601 are landed on local main (code and docs, not pushed). #591 #602 #604 #608 #609 are in fix or re-audit rounds in clones `aiwe-wt/u<N>`; each next step, sha and lead log is in `aiwe-wt/runs/lead-tools/pending-dispatch.md` ("test-system goal #587"); lead logs in `aiwe-wt/evidence/test-system-2026-10-27/`.
- The landed library code (#593 #601) has no production caller until #594 and #598; the advisory deadcode step (required:false) lists it until then.
- main's commits ahead of origin (the Branch line above) are not pushed; since the test-system units they include code.
- The CI line above shows the last `ci.yml` run on main; the fast-forward of 45f760b after PR #511 started no new run.

## Live configuration

- Work is still tracked in GitHub issues; the work store under `work/` takes over at the G1 cutover (SPEC L61).
- The owner runs up to 10 agents in parallel (2026-10-27); agents run no tests, gates or builds, the lead runs each check in sequence; release, the full gate and CI wait for a batch of 25-30 issues.
- First work since 2026-10-27 (owner): the test-system goal #587, units #588–#616 plus #522 and #257, in the order phase 0 → 1 → 5 → 4 → 3 → 2 → 6. Its decisions are SPEC §16 L63–L79. Baseline: `docs/evidence/2026-10-27-test-baseline.md`.
- Owner rules for this goal (2026-10-27): every piece of work has an issue; GitHub Actions is used as little as possible (the repository is private, so minutes are metered); everything sensible runs locally and efficiently.
- Every tool fix that is safe runs before its check, so a fixable finding never reaches anyone (owner 2026-10-27, SPEC L79, unit #617). Until #617 lands, the lead applies it by hand (`AGENTS.md` section 3, "Lead workflow").
- The D1 wave of M-Usable (#263) is planned in `aiwe-wt/runs/d1-plan.md`: 8 items, 19 units; the item records and blocked-by links are not yet rewritten except #59.
