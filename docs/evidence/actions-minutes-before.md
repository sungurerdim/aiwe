# Actions minutes before the minimal CI (measured 2026-10-27)

Issue #590. The after-measurement (#616) repeats these commands unchanged.
Repository `sungurerdim/aiwe`, private, free plan.

## Result

Window: 2026-09-27 .. 2026-10-27 (30 days). `--limit 200` returned 52 runs,
all 2026-10-10 .. 2026-10-26 (limit not reached). Why none exist earlier is
`not measured` (the list shows no cause).

Every attempt is billed, so jobs are read with `filter=all`; the default
`filter=latest` missed 3 jobs (run 39774634492, attempt 1), 16 weighted minutes.

| Measure | Value |
|---|---|
| Runs, all / billed | 52 / 41 |
| Jobs, all / billed | 102 / 91 (78 success, 13 failure) |
| Billed minutes, rounded per job, not weighted | 283 |
| Billed minutes, weighted (Windows 2x, Linux 1x) | 432 |
| Free line: `Dependabot Updates` (11 runs, 11 jobs) | 12 raw minutes, not billed |
| Account billing endpoint | `not measured` (HTTP 404, both endpoints) |
| `billable` in `/timing` | `not measured`: `total_ms` 0 for every run |

Free line: GitHub billing docs say standard hosted runners are free for
Dependabot (docs.github.com/en/billing/reference/actions-runner-pricing, read
2026-10-27, per the audit; one source). Excluded from every total below.
`Dependency Graph` (10 runs, 11 minutes) is counted as billed; the docs are
silent, so whether it is billed is `not measured`.

The `/timing` `billable` object returned 0 ms in all 52 runs although jobs ran
(`run_duration_ms` is non-zero). Billed minutes are therefore computed from
per-job start/end in `/jobs?filter=all`: `ceil(seconds / 60)`, at least 1,
times the OS multiplier. This estimates the bill; the gap to GitHub's own
figure is `not measured`.
macOS jobs: 0 (only `ubuntu-latest` and `windows-latest` seen).

## Multiplier source and price-weighted figure

The 2x (Windows) and 10x (macOS) multipliers come from the issue #590 rule;
the current docs page (docs.github.com/en/billing/reference/actions-runner-pricing,
read 2026-10-27) lists no multiplier, only a price per minute: Linux 2-core
$0.006, Windows 2-core $0.010 (1.67x Linux). Both are reported:

| Basis | Linux | Windows | Total |
|---|---|---|---|
| Raw minutes | 134 | 149 | 283 |
| Multiplier 2x (issue rule) | 134 | 298 | 432 |
| Price (USD, per-minute price x raw) | 0.80 | 1.49 | 2.29 |

The account's included quota and remaining minutes are `not measured` (404).

## Per OS (billed minutes per job, whole minutes, Dependabot excluded)
| OS | Jobs | p50 | p90 | max | Sum raw | Sum weighted |
|---|---|---|---|---|---|---|
| Linux (1x) | 59 | 2 | 4 | 4 | 134 | 134 |
| Windows (2x) | 32 | 4 | 5 | 13 | 149 | 298 |

Windows is 69% of the weighted total with 35% of the billed jobs.
## Per job (workflow / job)
| Workflow / job | OS | Jobs | p50 | p90 | max | Sum raw | Sum weighted |
|---|---|---|---|---|---|---|---|
| ci / build (windows-latest) | Win | 32 | 4 | 5 | 13 | 149 | 298 |
| ci / build (ubuntu-latest) | Lin | 32 | 2 | 2 | 2 | 64 | 64 |
| ci / gate | Lin | 15 | 4 | 4 | 4 | 57 | 57 |
| ci / changes | Lin | 2 | 1 | 1 | 1 | 2 | 2 |
| Dependency Graph / update-go_modules-graph | Lin | 10 | 1 | 1 | 2 | 11 | 11 |
| Dependabot Updates / Dependabot (free, not in total) | Lin | 11 | 1 | 1 | 2 | 12 | 0 |

Workflow `ci` sums to 421 weighted of 432.

## Per trigger event (weighted minutes, Dependabot excluded)
| Event | Runs | Jobs | Weighted | Job p50 | p90 | max |
|---|---|---|---|---|---|---|
| pull_request | 25 | 69 | 365 | 3 | 5 | 13 |
| push (main) | 5 | 10 | 46 | 2 | 4 | 4 |
| workflow_dispatch | 1 | 2 | 10 | 2 | 4 | 4 |
| dynamic (Dependency Graph) | 10 | 10 | 11 | 1 | 1 | 2 |

## Minutes per batch PR

`batch/*`: 13 branches, 15 pull_request runs, 255 weighted minutes (59%).
Per branch: min 8, p50 16, max 64, mean 19.6. Other non-main branches: 110;
`main` (push, dispatch, Dependency Graph): 67. "Per PR" is per branch (a branch
re-runs on each push); PR numbers were not queried (`not measured`).

## Monthly figure #616 repeats

Before value: 432 weighted billed minutes per 30 days (283 raw; Linux 134,
Windows 298 weighted), 41 billed runs, plus 12 free Dependabot minutes. Only 17
days hold activity: #616 reports the same rows and its run count, and compares
per-run figures too (432 / 41 = 10.5 weighted minutes per billed run).

## Commands (rerun unchanged)

```
gh run list --repo sungurerdim/aiwe --limit 200 --json databaseId,createdAt,event,conclusion,headBranch,workflowName
gh api repos/sungurerdim/aiwe/actions/runs/<id>/timing
gh api "repos/sungurerdim/aiwe/actions/runs/<id>/jobs?filter=all" --paginate
gh api -i users/sungurerdim/settings/billing/usage
gh api -i users/sungurerdim/settings/billing/actions
```

Do not start an API path with `/` in Git Bash (it rewrites the path).

Rules: keep runs with `createdAt` in the 30-day window. If `gh run list`
returns exactly the limit, the window is cut: raise `--limit` or split the
window with `--created <from>..<to>` and merge. Per run read `/jobs` with
`filter=all` (every attempt is billed); skip jobs without `started_at` or
`completed_at`; OS from `labels` (`windows` 2x, `macos` 10x, else 1x); billed
= max(1, ceil(seconds / 60)) x multiplier; leave `Dependabot Updates` out of
the billed total and show it as its own line; percentiles use the nearest rank
on the sorted per-job billed values (index `ceil(f * n) - 1`); group by OS,
`workflowName / job name`, `event`, and `headBranch` (`batch/*` for batch
PRs). Also record `/timing` `billable` totals and both billing endpoint
statuses, as above.

Verify: `git grep -c -i -e billable -- docs/evidence/actions-minutes-before.md`
prints the file name with a count of 1 or more.
