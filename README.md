# aiwe

aiwe — AI workflow engine: orchestrate all your AI subscriptions and agents, from issue to verified, merged code.

You set the goal; aiwe delivers verified, merged code using the subscriptions and API keys you
already have from supported providers, in the shares you choose. Work runs as small, checked units,
each handled by a role-specific agent; the author never approves its own work, and every test and
gate runs once, mechanically. aiwe uses only each provider's official CLI or API — no proxies — on
Windows, macOS and Linux, with zero configuration.

**Status: pre-release, design phase.** There is no code yet. The design is
[docs/SPEC.md](docs/SPEC.md); the delivery plan is its §14.

## Features

- Each agent starts with a small, clean context.
- Nothing is left behind on disk.
- Every run is measured for time, cost and quality.
- A goal is split by aiwe's architect role or by your own session from aiwe's template, and every
  unit is validated mechanically before work starts.

## Supported providers

`aiwe adapters list` prints the compiled adapter list. aiwe reaches a provider only through its
official interface: the provider's own CLI, logged in by you, or an API key. It never reads, stores,
proxies or rewrites subscription credentials or OAuth traffic.

## Risk

The first releases run agents at sandbox level `env-scrub`: a run gets no secret and no git
credential helper, and only aiwe pushes, but the agent still runs with your rights, as in other
coding agent tools. Stronger sandbox levels come later ([docs/SPEC.md](docs/SPEC.md) §6.7).

## Install

Not released yet. The first release is `1.0.0`, after Gate 3 of the delivery plan
([docs/SPEC.md](docs/SPEC.md) §14).

## License

MIT — see [LICENSE](LICENSE). Repository: https://github.com/sungurerdim/aiwe
