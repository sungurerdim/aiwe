# Security policy

This repository stays private until Gate 1 of the delivery plan ([docs/SPEC.md](docs/SPEC.md) §16,
Q16). GitHub private vulnerability reporting is for public repositories, so the reporting channel
opens when this repository becomes public.

From then on, report a vulnerability privately through this repository's **Security** tab →
**Report a vulnerability**. Do not open a public issue.

No version is released yet; the latest release will be the supported one.

## Known risk

Until a stronger sandbox level ships, an agent that aiwe starts runs with the rights of the user
who starts aiwe, as in other coding agent tools. Its environment holds no secret and no git
credential helper, and only aiwe pushes; these limit the damage but are not a sandbox
([docs/SPEC.md](docs/SPEC.md) §6.7). A report that an agent can reach the user's files is a known
risk, not a vulnerability; a way around the listed limits is a vulnerability.
