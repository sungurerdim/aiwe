# aiwe

Verified, merged code from the AI subscriptions and API keys you already have.

## Why aiwe

<!-- SPEC §1.1, §1.4, §6.5, §7.8, §8.5, §13 -->
You give aiwe a goal. It splits the goal into small work items, hands each one to an agent in its own clean workspace, checks the result with mechanical gates and an independent reviewer, and lands only what passed. Every step is recorded, so the speed and the cost of your agents are measured and not guessed. These are the design points; the status line below says what works today.

- **Trustworthy**: only the engine runs checks, an independent reviewer judges every item, and only reviewed work lands. A tamper-evident audit trail records who did what, when, with which agent, model and provider.
- **Measured**: every run is recorded, and lead time and flow come as p50 and p85, not averages.
- **Cheap**: the lowest measured use of quota, money, disk, CPU and memory; a low-cost author and a strong reviewer where that is enough.
- **Fast**: small items worked in parallel, each in its own leased workspace, with one queue for heavy checks.
- **Effective orchestration**: an idle agent asks for work, and one call gives it a free item with its claim, its files and its workspace.
- **Account-safe**: official CLI or API only, no proxy, and no reuse of subscription credentials, the pattern that provider terms forbid (SPEC §17). At most one subscription per provider (O7).
- **Mechanical, not prompted**: rules are enforced by the git hook, the landing queue and the command line, so they hold for any agent, harness or provider. Reports are built from data with no model call.

**Status: pre-release.** Working today: `aiwe version`, `aiwe config show`, `aiwe unit check` and `aiwe gates gate`. Everything else on this page is planned. The next milestone is M-Skeleton: the work store, the ledger, workspaces, the landing queue, the check executor, and `aiwe item take` and `return`.

Subscriptions and API keys are both supported, and aiwe holds no credential (SPEC DR-19).

The design is [docs/SPEC.md](docs/SPEC.md); the milestones are in its §13. Until the work store lands, the work items are GitHub issues.

## Risk

The first releases run agents at sandbox level `env-scrub`. A run gets no secret and no git credential helper, and
only aiwe pushes. The agent still runs with your rights, as in other coding agent tools, so it can read and change
what you can, the audit trail included; the trail shows a change and does not prevent it. Stronger sandbox levels come
later (SPEC §6.7).

## Install and first run

Not released yet. `v0.1.0` ships when the M-Release milestone closes (SPEC §12.3). Until then, build from source
with `go build ./cmd/aiwe`. Registering the MCP server in your harness with the entry that `aiwe mcp snippet` prints is
planned (SPEC §6.3).

## License

MIT — see [LICENSE](LICENSE). Repository: https://github.com/sungurerdim/aiwe
