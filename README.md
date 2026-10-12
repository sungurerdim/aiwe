# aiwe

aiwe turns a goal issue into verified, merged code, using the AI subscriptions and API keys you already have. It runs
on Windows, macOS and Linux with zero configuration, through each provider's own CLI or API, never through a proxy.
Its primary interface is an MCP server that the user's harness session starts. It is a public open-source product.

**Status: pre-release.** The design is [docs/SPEC.md](docs/SPEC.md). The milestones are in its §13; the tracker
holds the units.

## Features

- Each agent starts with a small, clean context.
- Nothing is left behind on disk.
- Every run is measured for time, cost and quality.
- Every unit is validated mechanically before work starts.

## Supported providers

`aiwe adapters list` prints the compiled adapter list. aiwe reaches a provider only through its official interface:
the provider's own CLI, logged in by you, or an API key in an environment variable. aiwe holds no credential. It never
stores, caches, prompts for or proxies a credential, and never alters subscription traffic (SPEC DR-19).

## Risk

The first releases run agents at sandbox level `env-scrub`. A run gets no secret and no git credential helper, and
only aiwe pushes. The agent still runs with your rights, as in other coding agent tools. Stronger sandbox levels come
later (SPEC §6.7).

## Install and first run

Not released yet. `v0.1.0` ships when the M-Usable milestone closes (SPEC §12.3, L9). Until then, build from source
with `go build ./cmd/aiwe`. Register the MCP server in your harness with the entry that `aiwe mcp snippet` prints
(SPEC §6.3).

## License

MIT — see [LICENSE](LICENSE). Repository: https://github.com/sungurerdim/aiwe
