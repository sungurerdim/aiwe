# Contributing

aiwe is pre-release. The design is [docs/SPEC.md](docs/SPEC.md); this page links to it.

- **Prerequisites**: Go and git only. Every other tool runs as `go tool <name>` from `go.mod`;
  `go run ./internal/cmd/repocheck doctor --repo .` names anything missing (SPEC §12.2).
- **Branch model**: trunk-based. Work on a short-lived branch and open a pull request into `main`.
  CI runs on the pull request; it merges as one squash commit (SPEC §7.3, Q21).
- **Pull request title**: a Conventional Commit subject in English (SPEC §12.3). Units that aiwe
  runs also carry a `Run-Id` trailer; an outside pull request has none and is listed as
  `external` (SPEC §16, Q17).
- **Tests first**: a behaviour change starts with a test that fails on `main` (SPEC §8.4, DR-45).
- **Checks**: run the repository gate, `go run ./cmd/aiwe gates gate --repo .` until a release
  exists (SPEC §14), or ask for a unit verify through `aiwe verify`; do not run broad ad hoc checks
  (SPEC §7.8). Data races are checked in CI on Linux (`-race`).
- **Versions**: never edit a version or a changelog; the release job derives both from commit
  messages (SPEC §12.3).
- **Units**: work is tracked as GitHub issues in the unit format; `aiwe unit template` and the MCP
  resources give the templates (SPEC §8.4). Run `git config core.hooksPath .githooks` once.

## Adding a provider

One adapter file under `internal/adapters/`, one line in `adapters/all.go`, and the contract suite
`providertest.Run` green for it. No core change (SPEC §4). Harness behaviour comes from its
documentation or the running program; no leaked or unlicensed harness source is read, quoted or
copied (SPEC §12.1).
