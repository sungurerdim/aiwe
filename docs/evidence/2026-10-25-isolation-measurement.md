# Working-tree isolation: measurement record

Date: 2026-10-25. Measurement and sources only; the decisions are O33 to O35 in `docs/SPEC.md`, the cited
summary is §17 "Isolation". Each number was measured or read in this task; `estimate` and `unknown` are marked.

Machine: Windows 11, NTFS, 16 cores, git 2.52.0, Go 1.25.7, Node 26.9.0, npm 11.14.1. Before every run
the load check ran for 3 s; the verdict was `start` in all 24 runs (CPU p90 about 10 %), none was
`unmeasured (load)`.

## M1. Isolation today (read from code, 2026-10-25)

Neither way reuses a clone.

| Way | Open | Reuse | Cleanup |
|---|---|---|---|
| Engine (DR-10, before O33) | Fresh shallow clone per run: `git clone --depth 1 --no-tags --no-checkout`, then `fetch --depth 1`, `checkout --detach` (`internal/engine/clone.go`) | None | Clone and scratch deleted at run end; 5 tries of 200 ms (`internal/core/remove.go`) |
| Bridge | One shallow clone per unit (`aiwe-wt/start.sh`) | The same clone for the whole unit life | Parked clones in `aiwe-wt-parked/` |

Left over by read (`du`, `ls`): 41 clone directories, about 480 MB (without the 1.6 GB harness home); how many are
live: unknown. No `.git/worktrees` entry and no `.claude/worktrees` directory in any inspected repository.

## M2. Conflict history of parallel work in one tree

Scope: all session logs and sub-agent logs (3 258 files, 2026-10-03 to 2026-10-25) and the agent reports
(3 116 files, 42 MB). Method: JSON parsing in Python; only tool results and system records; the hook's own source echoes and
this session's echoes removed; the same first 90 characters counted once per file. Every count is a lower bound
(estimate: ±20 %, echoes, copied transfers and missed phrasings).

| Event | Count | Days | Peak day |
|---|---|---|---|
| git `index.lock` error in logs | 59 files, 59 events | 2026-10-05 to 10-23 (14 days) | 10-23: 11 |
| `index.lock` in reports | 10 files | 10-09 to 10-23 | not counted |
| Foreign-file warning of the commit-scope hook ("also edited by another agent") | 169 events, 148 files | 10-10 to 10-23 (12 days) | 10-17: 36 |
| Hook denial of `commit --amend`, `stash`, `reset` for a sub-agent | 103 events, 99 files | 10-11 to 10-23 | 10-11: 34 |
| Edit or Write "file modified since read" | 12 | 10-07 to 10-23 (8 days) | 10-07: 3 |
| Test or build broken by another agent's half-done work (phrase match plus assistant text; blurred) | 257 matches, 142 files | 10-05 to 10-23 (16 days) | 10-23: 93 |
| The same, exact phrasing, in reports | 6 files | 10-13 to 10-14 | not counted |
| Worktree error or leftover (mostly text and command echoes) | 9 matches | 10-05 to 10-18 | 10-10: 3 |

Reading: a conflict in one tree happens daily (a hook warning on at least 12 days, `index.lock` on 14). The
protection was a warning plus a ban on amend, stash and reset for sub-agents; no tree isolation.
Not shown: a real double write (the 169 warnings are an upper bound; the count of real ones is unknown);
a person or a linter can also cause "file modified since read".

## M3. Time and disk

Method: 3 runs, median, seconds. Source repositories were copied with `--no-hardlinks` into a host; the
sources were not touched, one extra commit went into the host (a second base). All runs on local disk, no
network. Install and build ran behind the heavy-job queue; times are the command's own, without queue wait.
Four ways: S1 `git worktree`, S2 `git clone --local`, S3 `git clone --depth 1` from a `file://` URL, S4 reset of a
persistent clone (the slot, O33). Disk: `apparent` (sum of file sizes, hardlinks counted twice), `unique` (files
with link count 1 only: the real load of the clone), and free-space difference (includes the Go build cache and
disk noise; about 18 MB base for the Go repository).

### M3a. Go repository (aiwe: 232 tracked files)

Install = `go mod download` plus `go build ./...`; module and build caches are global and were warm; no download.

| | S1 worktree | S2 clone --local | S3 shallow clone | S4 slot reset |
|---|---|---|---|---|
| Clone or reset | 0.16 | 1.00 (with checkout) | 0.51 (clone, fetch, checkout) | 0.28 (fetch 0.12, checkout 0.06, reset 0.06, clean 0.05) |
| `go mod download` | 0.13 | 0.13 | 0.14 | 0.13 (no-op) |
| `go build ./...` | 2.39 | 2.38 | 2.38 | 1.02 (no-op, from cache) |
| Added disk, unique | 1.9 MB | 2.0 MB | 2.6 MB | 2.0 MB |
| Delete | 0.08 | 0.37 | 0.09 | 0.36 (at slot end) |
| Locked leftover | none | none | none | none |

Clean check of S4: in 3 of 3 runs a dirty state (an untracked file, an untracked directory, a changed tracked file;
3 lines of `status --porcelain`) gave 0 lines of `git status --porcelain --ignored` after the reset.

### M3b. Node repository (the largest dependency set measured; 1 138 tracked files, lock file 344 KB)

Install = `npm ci --offline --no-audit --no-fund` with scripts enabled; the npm cache (2.7 GB) was warm and the
offline install succeeded 9 of 9. The Python repositories were not measured.

| | S1 worktree | S2 clone --local | S3 shallow clone | S4 slot reset |
|---|---|---|---|---|
| Clone or reset | 0.45 | 2.73 | 1.34 | 0.32 (fetch 0.13, checkout 0.07, reset 0.07, clean 0.06) |
| Install | 39.27 | 42.94 | 41.65 | 1.07 (`npm install --offline`, lock file unchanged, no-op); lock hash compare 0.0007; first install 44.2 |
| Added disk, apparent | 598.1 MB | 631.9 MB | 601.9 MB | slot: 631.9 MB |
| Added disk, unique | 598.1 MB | 598.4 MB | 601.9 MB | 598.4 MB |
| Delete | 6.73 | 9.48 | 8.82 | 9.2 (at slot end) |
| Locked leftover | none | none | none | none |

Clean check of S4: after `git clean -ffdx -e node_modules`, `git status --porcelain --ignored` showed 1 line,
`!! node_modules/` (3 of 3), the kept dependency directory only. A clean check must therefore compare against the
expected ignored list (the stack profile's `KeepDirs`).

Result: every new tree of the Node repository costs about 600 MB, about 40 s of install and 7 to 9 s of delete;
the install difference among S1, S2 and S3 is noise (39 to 43 s) and the disk differs not at all (npm copies, no
hardlinks). The one thing that changes the cost is keeping the dependencies between runs (S4: 0.32 plus 1.07 s). In
the Go repository all four ways stay under 2.5 s; the difference is the first `go build`.

Not measured (`unknown`): the incremental install after a lock-file change; a Python dependency repository; `npm ci
--ignore-scripts`; a content-addressed package store shared between clones; a cold cache or a network install;
several worktrees at once in one repository (only sequential runs; the shared refs, config and hooks risk of
worktrees is documented, not measured).

## M4. File-lock options

Tests ran in a scratch directory with git-bash `sed`, git, Python and Node on Windows 11 and NTFS. Question: does the
mechanism stop another process from writing.

| Mechanism | Result | Leftover after a crash | OS independent |
|---|---|---|---|
| Windows share lock (holder opens the file with `FileShare.Read`) | Stopped `echo >>`, `sed -i`, Python write and `os.replace`, `git checkout --`, `stash`, `reset --hard`, `rm` (Node write not tried, same mechanism) | None: writing worked after `taskkill /F` | No, Windows only |
| Read-only attribute (`attrib +R`) | Stopped `echo >>`, the Python write, the Node write (EPERM); passed `sed -i`, `git checkout --` and `rm` (git cleared the attribute) | Stays; an agent clears it with `attrib -R` | Partly (`chmod -w`; root ignores it) |
| Byte-range lock (`LockFileEx`, mandatory) | Passed an append outside the range; stopped `sed -i`, `os.replace`, git, `rm` | None | No |
| `flock` (advisory) | `flock` is not installed in git-bash, not tried; the manual page says the lock is advisory and a process may ignore it, so `sed -i`, a formatter and `git checkout` are not stopped | Released when the descriptors close | POSIX only |
| git-lfs lock | Does not block a local write: the lock is a server lock checked at push; a `--lockable` file is only made read-only (acts like the attribute row); needs a server and LFS files; the server lock stays after a crash | Stays (on the server) | Yes, needs network and server |
| Claude Code PreToolUse hook | Stops only `Edit` and `Write` tool calls; Bash `sed -i`, a formatter and `git checkout` need a `Bash` matcher and command parsing, which the documentation calls best-effort; works in sub-agents (`agent_id` in the input); the commit-scope hook denied sub-agent amend, stash and reset 103 times (M2) | None (state file; the hook fails open) | Yes, for the tool; covers no process outside the tool (CI, a person) |

Notes: (1) Only the Windows share lock stopped every write path, git included, and only while its holder lived; a
long-lived process that wraps the agent would have to hold it, and the locked file stays readable. (2) The
read-only attribute alone has no effect against git and `sed -i`. (3) The byte-range lock does not stop a write outside
the range; full cover needs a lock over the whole length (not tried).

Not tried (`unknown`): `git-lfs lock` (not installed; documentation only), `flock` (no tool), the matcher name of the `MultiEdit` and
PowerShell tools, 181 770 characters of the hook documentation (the first 100 000 were read).

Sources (read 2026-10-25): man7.org `flock(2)`; git-lfs `git-lfs-lock` and `git-lfs-track` manuals; the Claude Code hooks
documentation.
