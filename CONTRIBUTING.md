# Contributing

aiwe is pre-release. The design is [docs/SPEC.md](docs/SPEC.md); this page links to it.

- **Prerequisites**: Go and git only. Development tools run as `go tool -modfile=tools/go.mod <name>`;
  `go run ./internal/cmd/repocheck doctor --repo .` names anything missing (SPEC §12.2, L4).
- **Branch model**: trunk-based. Work on a short-lived branch and open a pull request into `main`. CI runs on the pull
  request; it merges as one squash commit (SPEC §7.3, Q21).
- **Commit messages**: the rules of SPEC §12.3. Units that aiwe runs also carry a `Run-Id` trailer; an outside pull
  request has none and is listed as `external` (Q17).
- **Tests first**: a behaviour change starts with a test that fails on `main` (SPEC §8.4, DR-7).
- **Checks**: the repository gate is `go run ./cmd/aiwe gates gate --repo .` until a release exists; a unit verify is
  requested through `aiwe verify`. Do not run broad ad hoc checks (SPEC §7.8, DR-8).
- **Versions**: never edit a version or a changelog; the release job derives both from commit messages (SPEC §12.3).
- **Issues**: every piece of work is a GitHub issue in the issue standard (SPEC §8.4). `aiwe unit check` validates a
  unit. Run `git config core.hooksPath .githooks` once.

## Adding a provider

One adapter file under `internal/adapters/`, one line in `adapters/all.go`, and the contract suite `providertest.Run`
green for it. No core change (SPEC §4). Harness behaviour comes from its documentation or the running program; no
leaked or unlicensed harness source is read, quoted or copied (SPEC §12.1).
