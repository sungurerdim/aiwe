# Contributing

aiwe is pre-release. The design is [docs/SPEC.md](docs/SPEC.md); this page links to it.

- **Prerequisites**: Go and git only. Development tools run as `go tool -modfile=tools/go.mod <name>`;
  `go run ./internal/cmd/repocheck doctor --repo .` names anything missing (SPEC §12.2, L4).
- **Branch model**: trunk-based. Work on a short-lived branch and open a pull request into `main`. CI runs on the pull
  request. The design keeps one commit per unit and moves `main` only by fast-forward to a tested tip (SPEC §7.3, Q21).
- **Commit messages**: the rules of SPEC §12.3. Units that aiwe runs also carry a `Run-Id` trailer; an outside pull
  request has none and is listed as `external` (Q17).
- **Tests first**: a behaviour change starts with a test that fails on `main` (SPEC §8.4, DR-7).
- **Checks**: the full gate runs in CI (SPEC §7.8). Locally run the steps your change touches with
  `go run ./cmd/aiwe gates gate --repo . --step <id>`. Do not run broad ad hoc checks (DR-8). The central `aiwe check`
  and `aiwe verify` are planned (SPEC §13).
- **Versions**: never edit a version or a changelog; the release job derives both from commit messages (SPEC §12.3).
- **Work items**: every piece of work is a work item in the standard of SPEC §8.4; until the work store lands (SPEC
  §12.3) it is a GitHub issue. `aiwe unit check` validates a unit.
- **Hook**: `git config core.hooksPath .githooks` runs the commit-message check locally. Today that hook is its only
  enforcement; CI takes it over when the `static` job lands (SPEC §12.2).

## Adding a provider

One adapter file under `internal/adapters/`, one line in `adapters/all.go`, and the contract suite `providertest.Run`
green for it. No core change (SPEC §4). Harness behaviour comes from its documentation or the running program; no
leaked or unlicensed harness source is read, quoted or copied (SPEC §12.1).
