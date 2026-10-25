# aiwe — Specification: contracts

Sections 8 to 11 of the product specification. They keep their numbers, and `docs/SPEC.md` holds every other section; a cite such as §8.4 names a section of this file, and every other cite names `docs/SPEC.md`. The rules of that file's header apply here: facts live in one place, an unmeasured value is `unknown` (DR-6), and each file stays within the cap of L1.
## 8. Data contracts
Every engine file carries `formatVersion`; parsers are fail-closed (DR-6); JSON keys are `camelCase`.
### 8.1 Sources and model choice — `<state>/sources.json`
```json
{ "formatVersion": 1,
  "sources": [
    { "id": "sub-a", "adapter": "opencode-go", "kind": "subscription", "provider": "<provider-id>" },
    { "id": "api-b", "adapter": "openai-chat", "kind": "api", "provider": "<provider-id>",
      "credentialEnv": "B_API_KEY", "baseUrlEnv": "B_BASE_URL", "spendCap": { "usd": 50, "since": "<date>" } } ],
  "overrides": { "pin": { "engineer": [ { "source": "sub-a", "model": "<model-id>", "effort": "<level>" } ] },
                 "exclude": [ { "source": "api-b", "model": "<model-id>" } ], "tier": { "auditor": "top" } } }
```
- Row fields: `id`, `adapter`, `kind` (`subscription` or `api`), `provider` (its data file, §10.2), `models` (optional; limits the source's candidates), `credentialEnv`, `baseUrlEnv`, `maxConcurrent`, `spendCap`, `price`. A second `subscription` row with the same `provider` is refused (O7); API rows are unlimited.
- `credentialEnv` may not name `GH_TOKEN`, `GITHUB_TOKEN` or another tracker token. An `api` source needs a base URL (`baseUrlEnv`, else the provider data `baseUrl`).
- **Spend cap** `{usd, since}`: the ledger's `costUsd` since `since` against `usd`; at the cap → `capped`. A cap on a source whose cost is always `unknown` is refused; any `unknown` cost since `since` reads `not_measured` and caps the source. Runs in flight may pass it.
- **Concurrent-run ceiling** (DR-3, Q13): starts at 1, +1 after 10 consecutive clean runs (default) up to `maxConcurrent`, −1 (minimum 1) on a `rate_window` or concurrency error.
- `price {inputPerM, cachedInputPerM, outputPerM, currency, url, checkedOn}`: used for 30 days (default), else the catalogue price, else `unknown` (`no_price`).
- Runtime state `sources.state.json`: `active | passive | restricted | capped`, quota class, `resetAt` or `unknown`, `passiveSince`, `lastProbe`, `ceiling`, `cleanRuns`, `balance`, and each source's discovered models with the read date. `aiwe sources show` prints them (`unknown` when unread).
- `aiwe sources discover` writes `sources.json` rows from detected harness logins (`Detect`, §10.2) and API keys present in the environment (provider data `keyEnv`); the owner may exclude any row.

**Model choice** (O9) is automatic and follows fixed rules: the same inputs give the same choice. The owner keeps no
list; `overrides` are optional. A **candidate** is (source, model, effort), because effort changes quality and cost as
much as the model does.

**Assignment per session**: aiwe refreshes discovery and the catalogue at each session start: MCP server start (O10)
or `aiwe work` start. A long-lived engine refreshes at most once a day. It computes an ordered candidate list per role.
It prints the difference from the saved `assignments.json` and saves the new list as the next session's default. Every
run of a role takes the first available entry of its list. A quota, ceiling or cap moves to the next entry (O3).
Escalation moves up the list. No run recomputes the list.
1. **Discovery**: each source reports the models it offers now (API `GET /models`; a harness's model listing where one exists; else its provider data), refreshed by `aiwe sources discover` and at most daily, cached with the read date. An unoffered model is never a candidate. The effort levels a source accepts per model come from its provider data and harness capabilities (a CLI effort flag, an API reasoning parameter); a model without effort control has one.
2. **Identity**: one canonical model id per model; each source keeps its wire id as an alias. A catalogue row that measures one effort level (a `(high)` qualifier, for example) maps to that (model, effort) only; a level without a row has an `unknown` prior, never a neighbour's figure. One normalisation matches names; an ambiguous or unmatched name stays unmatched, with scores `unknown`.
3. **Public prior**: the Artificial Analysis catalogue, read at most once a day with the key in `ARTIFICIAL_ANALYSIS_API_KEY` and cached in `models.cache.json` with its read date: coding index, intelligence index, price per M input and output tokens, cost per task, output speed. Every output that uses it prints "Data: Artificial Analysis, https://artificialanalysis.ai/". A failed read or an age over one day is `unknown`, never served as current. `artificialanalysis.ai` is an allowed outbound host of the contract suite (DR-19).
4. **Own evidence**: the ledger per (role, canonical model, effort, adapter): runs, first-try green rate, cost per green unit, median duration; for judging roles also verdicts later contradicted by an escaped defect (M3).
5. **Candidates**: discovered models on `active` sources below their ceiling and spend cap, minus `exclude`, minus models that miss a required capability. A judging run excludes the writer's provider when another provider has a candidate; otherwise it takes the same provider's other model, then a new instance: the independence order is provider, model, instance (`auditIndependence`, §8.5). DR-5 states what is not required; one source still works; the run records the level it got.
6. **Role fit**: shipped data per role (the role registry, §9.5) names the measured fields its quality uses and their weights (writing roles: coding index first; planning: intelligence index first; judging: both), its required capabilities (tool use, context window) and a floor score. A writing role never takes a candidate below its floor.
7. **Quality**: the candidate's own first-try green rate once it has ≥ 10 runs in that role (default); before that its role-fit score from the public prior. **Tiers** are mechanical (L16): `base` = the cheapest candidate above the role floor; `raised` = the upper half by role-fit score; `top` = the best score. A role starts at its registry tier (§9.5); a risky unit raises it one step. Tiers filter the session list: entries below the tier floor are skipped.
8. **Objective**: writing roles take the lowest expected cost per green unit (run cost ÷ quality; an active subscription counts as prepaid and ranks before a metered API at equal quality); judging and planning roles take the highest quality, with cost breaking ties.
9. **Escalation**: a second red on one unit moves the next run up the list: first a higher effort of the same model where the list has one, then the next model; never the same (model, effort) again.
10. **New and demoted models**: a newly reported model competes at the next session start on its public prior; a demoted model returns when its catalogue entry changes or 30 days pass. No random exploration.
11. **Owner overrides**: `pin` (always first; a pin wins), `exclude`, `tier` per role. The skeleton runs on pins.
12. **Explanation**: `aiwe models` prints the current assignment per role, its inputs and every rejected candidate with its reason; `aiwe models --refresh` recomputes it outside a session; each run record stores the assignment version it used. Without catalogue or evidence the order is pins, then own evidence, then source order, and the output names the unknown inputs.
### 8.2 Stack profiles — `stacks/profiles/<stack>.json`
One embedded file per stack: `ID, Detect, Restore, Gate, Narrow, TestFiles, SourceExt, LockFiles, KeepDirs, TestCase, Assertion, NoTests, Mutation, Complexity, Registry, SecurityPaths, UIPaths, UIRunner`. The skeleton ships the Go row: detect `go.mod`, restore `go mod download`, gate `go vet ./...`, `go test ./...`, `gofmt -l .` (empty output), test files `*_test.go`, lock file `go.sum`, no `KeepDirs` (the module cache is global), complexity tool `gocognit`. `Narrow` is the command that runs the tests depending on the changed files (the Go row: `go test` over the changed packages and the packages that import them); a stack without one reads `not_measured`. Languages are detected through `Detect` and never stored (L25). Other stacks come with M-Trust (§13). A stack missing cells, or an unknown stack, reads them `not_measured` and takes the ladder (§7.7); every derived command is proved per repository before use (§7.1 step 5). Role path tables (default, DR-15): documents `*.md`, `*.mdx`, `*.rst`, `*.adoc`, `*.txt`, `docs/**`; security: a path segment containing `auth`, `login`, `session`, `token`, `secret`, `credential`, `password`, `crypto`, `permission`, `oauth` or `webhook`, a D16 hit, or a stack's input entry point (Go `*handler*.go`); UI: the stack's UI patterns and `UIRunner`, the project's own browser tool found by its config file (none → `not_measured`).
### 8.3 Stored gate — `projects/<repo-id>/gate.json` (a project `gate.json` uses the same format)
```json
{ "formatVersion": 1, "origin": "derived", "inputHash": "<sha256>",
  "steps": [ { "id": "test", "command": ["go", "test", "./..."], "required": true, "inputs": ["**"], "timeoutSeconds": 124, "expect": { "exitCode": 0 } } ],
  "proof": { "baseCommit": "<sha>", "steps": [ { "id": "test", "result": "green", "exitCode": 0, "durationMs": 41230 } ] } }
```
`command` is argv, never a shell string. A step may also carry `prevents` (the error it stops), `point` (`commit` or `ci`, default `ci`), `rule` (a registry id) and `budgetSeconds` (§7.10); a derived gate fills them from the stack profile. `origin`: `project | derived | synthesized`. `expect` also allows
`outputMatches` and `outputRejects`. A project `gate.json` omits `origin`, `inputHash` and `proof`.
### 8.4 The work item standard
Every goal, unit and owner action is a work item in this standard (O31, DR-20); a finding is not an item (R1). The `item-template` resource, `aiwe unit check`, `aiwe item` (O8), the intake parser and the architect card follow this section. `aiwe item` is the only writer: nobody edits an item file by hand, and pre-commit refuses an item file that does not validate or was not written by it. Field names and enum values are English ASCII; text is UTF-8 and the CLI takes long text from a file or stdin (DR-18). All records, task texts, role cards, evidence files and reports in this repository are English; the chat with the owner stays in the owner's language.

**Store**: flat JSON files in git under `work/` of the project's record repository: `schema.json` (the versioned schema, the one owner of the format), `areas.json` (the area tree, `[{id, parent, title}]`), `views.json` (saved queries), `severity.json` (the owner's severity table, below) and `items/<id>.json` (one file per item). `TODO.md`, `ARCHIVE.md`, the automatic part of `STATUS.md`, the owner summary and the board are generated by `aiwe item render`, carry a generated marker on the first line and are never edited by hand. An **id** is a prefix and 6 Crockford base32 characters drawn from time and random bits (`W-7K3F9Q`); it never changes, a title may; a duplicate id is red in lint. Every write of `aiwe item` is one commit. It goes through the landing queue of §6.5 like a code commit, and a commit that changes files under `work/` alone skips the code gate and runs `aiwe item lint` (L20). Two sessions cannot own one item, because ownership is a claim ref on the remote (§6.6, L19); the item file only shows it. **Cutover** (L35): `aiwe item migrate --from github` runs in stage G1 and fills `work/`. While the bridge runs, `work/` is not the authoritative source and the GitHub issue stays the work list. When the bridge retires, the migration runs again (it is idempotent), the GitHub issues close, and `work/` becomes the only source.

**Shape**: `type` is one of `goal`, `epic`, `story`, `task`, `bug`, `spike`, `chore`, `owner-action`, at any depth (a `bug` may be a direct child of an `epic`). `goal` and `epic` group items, are never dispatched, and close by themselves when every child is `done` or `cancelled` and their own `done` checks pass (§6.3, L22); a milestone goal of §13 and an audit goal are ordinary goals. A **unit** is a dispatchable leaf: type `story`, `task`, `bug`, `spike` or `chore`, with a `scope.in`, a resolving `done` entry, one outcome and one finding key, and at most 5 source files (DR-7). An `owner-action` waits for the owner and is never dispatched. There are no audit-generation or per-rule epics: a finding-key upsert makes a generation unnecessary, an item has one parent, and the per-rule view is a query on the rules (R4).

**Hierarchy** (O32): two dimensions, both unlimited in depth. The work breakdown is `parent` (one parent per item). The area is `area` (the primary area, required) and `areasRelated`; the tree is `areas.json` (`ui` → `ui/panel` → `ui/telegram`). Moving a group changes one `parent` in `areas.json` and touches no item file. Lint: an undefined area and a parent cycle are red; an empty area, an implausible type under a parent (an `epic` under a `task`) and a tree deeper than 5 are yellow.

**Record**: one JSON object per file (RFC 8259, `encoding/json`; STD-5), keys `camelCase`:

| Fields | Required | Meaning |
|---|---|---|
| `v`, `id`, `title`, `type`, `status`, `created`, `updated` | always | schema version; id; plain title of at most 72 characters; type; status (below); ISO 8601 UTC times |
| `priority` (`P1`–`P4` or `unknown`), `severity`, `key`, `rules`, `review.required` | always; derived | the engine derives them and the author never writes them. **Severity**: `affected` × `class` (#314 K8: `affected` is one or more of customer, owner, admin, developer, system; `class` is `must` or `nice`; the result is `high` for `must`, `low` for `nice` and `unknown` for no party, `roles.Severity`). **Priority**: the cell of the owner's table `work/severity.json` at the row of the severity and the column of the item's `urgency`; `unknown` while the table has no such cell (written table: #415). `lint` recomputes it, and a different stored value is `priority_mismatch` (L23). `key`: of generated units. `rules`: their revisions. `review.required`: the roles of §9.5 |
| `parent`, `area`, `areasRelated`, `blockedBy`, `related` | `area` always | hierarchy (above) and dependency kinds; the engine adds blockers from file-set overlap |
| `urgency`, `tags`, `writers`, `reviews[{role, verdict, runId, session, at, evidence}]` | optional | `urgency`: an input of priority; a goal or epic sets it, any other item takes the one of its nearest milestone-goal ancestor (R10). `tags`: labels that the required-role derivation reads (§9.5). `writers`: the sessions that wrote to the item; the engine adds one at each start and fix round. `reviews`: the verdict records (§9.3), written by `aiwe item review` |
| `problem{symptom, expected, cause{state: known\|unknown, text}, impact, affected, class}`, `need{problemEvidence, deleteOption, existing, smallestChange, alternatives[{what, files, whyNot}], ifNotDone, risksNotAdded[{risk, likelihood, complexity}]}`, `scope{in, new, out, constraints}`, `estimate{files, lines}`, `done[{id, check, expect} \| {id, judgement, text}]` | DoR | `alternatives` needs at least one entry; a `goal`, `epic` or `owner-action` carries `need` and `estimate` in short form (`problemEvidence`, `ifNotDone`) |
| `steps[{do, at}]`, `red{check, base, items}`, `distinct[{id, reason}]` (#314 K35), `legacy` (#314 K34), `context` (at most 3 lines), `timebox{hours \| sessions}`, `decisions` | optional | `legacy`: a migrated heading keeps its prose and is not ready until an agent rewrites it before dispatch; `timebox`: an overrun shrinks the scope, never the time; `decisions`: ids of the decisions the item rests on |
| `defer{until \| after, reason}`, `waiting{on, questionFile, options, recommendation}`, `cancel{reason, supersededBy}`, `handoff{state, next, at, updated}`, `close{commit, verifyOutputPath, effect}` | by status | required while `deferred`, `waiting`, `cancelled`, `active` (`handoff`) and `done` |

The validator refuses a missing field, a relative reference ("above", "same as"), a placeholder ("TBD", "etc."), a path that does not exist (`new` excepted), a `check` that does not resolve, and a `done` entry with neither a check nor a named judgement role. **Done-criteria lint** (L31): two `done` entries with an equal `check` and `expect` are red (`done_duplicate_check`), and so is a `check` whose argv names no path of `scope.in` or its directory, no test file of the file set and no identifier of `steps` (`done_unrelated_check`): one command does not close several different entries, and a command unrelated to the unit closes none. **Priority without the table** (L37): while `work/severity.json` has no cell for an item, migration writes priority `unknown`, and `lint` reports `priority_unknown` as yellow, neither red nor a default `P3`; migration and the rest of the store never wait for the owner's table. A schema change runs `aiwe item migrate`, which rewrites every item mechanically and is idempotent; formats never coexist. `migrate --from github` turns a GitHub issue into an item; a heading body becomes a **legacy** item (#314 K34): each `F1` line becomes a `done` entry, the Problem text goes verbatim into `problem.symptom` with `cause.state` `unknown`, and `legacy` stays true until an agent rewrites the unit before dispatch. The agent never reads raw JSON: `UNIT` (§9.2) is a deterministic text rendering of the item. The value sets of `type`, `status` and `v` are not in the code yet (planned #427).

**Status**: `open` → `active` (`start`) → `done` (`close`); `active` ⇄ `waiting` (`wait` with a question file, `answer`); `open` → `deferred` (`defer`) and back to `open` when its condition holds; `open` → `cancelled` (`cancel`). `waiting` holds only an item that waits for the owner: question, options and recommendation sit in `waiting.questionFile`, and the engine does not count the item as open work. `deferred` takes a date (`until`) or another item (`after: <id>`), never free text; `aiwe item revive` reopens a due item at session start and in CI. `cancelled` needs `cancel.reason`; an open item that depends on a cancelled one is red in lint, and a bulk cancel lists such items in its dry run; an item that rests on a superseded decision gets a yellow warning. **Ready and done**: `open` → `active` needs `problem`, `need`, `scope.in`, `estimate` and `done` filled, else the tool refuses. `done` needs a green verify output (`close.verifyOutputPath`), the sha of the commit on the default branch (`close.commit`) and `close.effect` (measured before → after, else `not measured`); a unit that a later gate run found green closes with the report path in `verifyOutputPath` and no commit (§6.3). A `judgement` entry closes only with the independent auditor's GREEN verdict record, and `close` is refused until every role in `review.required` has a GREEN verdict record from a session that is not one of the item's `writers` (DR-5, §9.5).

**Commands** (DR-16; a table for people, `--json` for agents; exit codes follow §5.3): `aiwe item new --type T --area A --file record.json` · `show ID` · `list QUERY [--view NAME]` · `ready` (`open`, blockers `done`, DoR complete; order priority, severity, age) · `start | handoff | wait | answer | close | defer | cancel | revive ID` · `brief ID --role R` · `review ID --role R --file record.json` · `set ID field=value` · `bulk QUERY (--set … | --cancel --reason-file F) [--apply]` · `lint` (schema, relations, status consistency) · `migrate [--from github] [--apply]` · `render` · `sync` (closes from commit traces and green reports, upserts units by finding key, flags inconsistencies: R4, §7.3) · `merge %O %A %B`; `aiwe area add | move | list`; `aiwe intake`. Every write is one of these; CI and pre-commit run `lint`.

**Query**: space-separated conditions are ANDed and `OR` is explicit: `status=open type=bug priority<=P2`, `area:ui/**` (subtree), `under:<id>` (breakdown subtree), `decision:<id>`, `defer.after:<id>`, `updated>2026-10-01`, `~"text"` (full text in title and text fields), any field path (`need.ifNotDone~"data loss"`), `-` negates (`-type=owner-action`); `views.json` saves a query under a name. Speed is unmeasured (`unknown`): the aim is under 1 s over 5 000 files by parallel reads, and an index cache is added only if the measure misses it. **Bulk**: a dry run is the default and prints a table, with items whose `areasRelated` touches the target but whose primary area differs listed apart; `--apply` makes one commit whose message carries the query and the count, and one `git revert` undoes it; nothing is deleted, only cancelled. **Merge**: one file per item avoids most conflicts; the JSON-aware driver `aiwe item merge` merges three ways per field, and a field changed differently on both sides stays a conflict for a person; generated files regenerate.

How aiwe reads it:
- **Intake gates** (R3): creating or checking a unit lists candidates and refuses to continue while one exists: the same `key` open; `scope.in` of two units intersecting; `scope.out` of one intersecting `scope.in` of the other; a blocked-by cycle; an equal normalised title; text similarity of `problem` and `steps` above a threshold (a candidate, never a verdict). The caller answers `--merge-into ID`, `--supersede ID` or `--distinct ID:<reason>`; the answer is stored and the pair is not flagged again. A unit without `scope.in` fails the validator, so every unit is covered.
- **File set** (DR-7): each `scope.in` path; `new` marks a file the unit creates. Plain paths must exist; `new` paths must not. A tracked test file outside the file set that holds an identifier of `steps` → `scope_outside_files`.
- **Verify**: each `done` check resolves to argv by shell-style quoting and runs without a shell; a shell operator outside quotes (`&&`, `\|\|`, `\|`, `;`, `>`, `<`, backtick, `$(`) or a `HEAD~<n>`/`HEAD^` base is refused; the first token must resolve. It is checked, never run, before review.
- **Test items**: a `done` entry that runs or names a test file of the file set. A **behaviour unit** (a file set path with a `SourceExt` of its stack) has at least one; its test commit is red on base and green on the tip (§7.3 step 5).
- **Admission** (R6): a defect finding from a judge or a person is admitted only with a failing reproduction the engine ran (`red` recorded); without one it is class `design` and goes to the owner-action path or is dropped; its finding key is the rule plus the reproduction's test id. A check-derived unit needs no new test: the recorded check report is its red proof, and its verify is the same check for that finding key. A behaviour unit's tests are written by the test-author as soon as intake accepts it (R6); the engine records the red proof, a moved base repeats it and renews the tests when it fails to hold, and the weak-test check (a removed assertion, an added skip, a changed expected value, a test without an assertion) runs first (#314 K38(c), K38(e)). A fix round receives only the report of its red run (R7).
- **Dependencies**: `blockedBy` (L8); a unit waits while a blocker is not finished.
- **Owner work**: type `owner-action`, never dispatched. A stop for the owner opens an `owner-action` item in this standard that blocks the unit: `problem` holds the decision, `waiting.recommendation` the recommendation and `waiting.options` each option with its consequence, `context` engine evidence (gate tail, diff stat, Run-Id). Text from a run's `decision:` lines passes intake, masking and the payload scan first.
- **Urgency and evidence**: a roadmap milestone (§13) is a goal item; a unit's urgency is that of its nearest milestone-goal ancestor, or of its rule's registry milestone (R10). An item has no comment thread: narrative, copied tool output and session plans are never in an item, the one running note is `handoff`, and evidence goes into the commit and the pull request. Closing is mechanical (R5, §6.3, §12.3): the close evidences, the manual-close rule and the goal rule are in §6.3.
- **GitHub** (O31): intake and view only, never the work list and never a second source of truth. A request arrives as a GitHub issue or an e-mail; `aiwe intake` reads it, creates the item when it is accepted and closes the GitHub issue `completed` with a comment naming the item id. There is no two-way sync: GitHub may show a one-way view of the store, and nothing is read back from it.
- **Intake filter** (DR-20): only the body by an author with write permission; HTML comments removed; bidirectional embedding, override or isolate controls → refused, no item created. The text enters prompts only in `UNIT`.
### 8.5 Ledger — `<state>/ledger.jsonl`
Append-only JSONL, one schema with `kind`: `run`, `check`, `session` or `owner_action`. `core.Measured[T]{Value; Known}`
serialises as a number or `"unknown"` (DR-6). Every record: `formatVersion, kind, at, repoId, goal, unit, runId, revision`
(`"none"` when absent; `revision` is the compare-and-set revision of a run record, §10.3).
- `run`: `role, source, adapter (harness or API wire format), model, modelReported, effort, tier, assignmentVersion, attempt, fixRounds, conversationId, resumeOf, startedAt, finishedAt, outcome, gate (green|red|incomplete|not_measured), audit (approved|returned|none), tokensIn, tokensOut, tokensCachedIn, maxPromptTokens, turns, longestGapMs, costUsd, unknownReason (no_price|no_usage_data|subscription), residue, auditIndependence (provider|model|instance|none), sandbox, tip, auditedTip, exit, stderrTail (40 lines, masked), claimedAt, mergedAt, ciGreenAt, mergeCommit, pullRequest`.
- `check` (`key` is the cache key): `key, stepId, commit (or "uncommitted"), exitCode, killed, durationMs, tail (masked), signature, rerunReason, flaky, report (the SARIF file, R7)`.
- `session` (§7.11): `sessionId, harness, reason (budget|until|queue-empty|all-waiting|lost|unknown), exitCode, lastHeartbeat, openItems`.
- **No compatibility path** (L32): a record that lacks a field reads `unknown` for it (DR-6), and no reader converts an older shape; a field added later is `unknown` in every earlier record.
- Each run start gets exactly one end record, or `lost` at the next start; a `run` without a unit is refused; each fix round is a new run. At 16 MB (default) the file rotates to `ledger.<n>.jsonl.gz`; `metrics` reads rotations; the oldest goes first under the state ceiling. An unwritable ledger stops dispatch. Owned-directory records (kind `owned_dir`) are written by `core` outside this schema (planned #424); the `owner_action` field list is not defined yet (planned #427).
### 8.6 Run result envelope
A run's `Result` data (§5.3) is `RunReport{RunID, Unit, Role, Source, Adapter, Model, ModelReported, Outcome, Branch, Verdict (GREEN|RED|none, parsed from the last message), Tip, Verify{Command, ExitCode (nil = not_run), Tail}, TokensIn, TokensOut, MaxPromptTokens, Turns, CostUSD, DurationMS}`. The child writes it to its run directory; no file and no live process = `lost`. A harness that reports only session totals converts them per run or reports `unknown`.
## 9. Roles and the standard agent contract
### 9.1 Role cards
A role exists only with its own kind of work and a gated boundary (DR-22). Skeleton: engineer, auditor, test-author, doc-writer (its card: #46).
Next: architect, security-reviewer. Later: analyst, debugger, ux-reviewer. Operator: deferred with operation
units (O4). A new role is added only after an existing role has made the same error class three times.

**Resolution** (DR-15), from the unit's type and labels, its file set, its diff paths after collect and §8.2: `aiwe
plan` or a plan artefact → architect, auditor · another artefact → engineer, auditor · type `documentation` or only
document paths → doc-writer, auditor (a non-document path → `role_unresolved`) · any other unit → engineer, auditor,
with the test-author first for every behaviour unit (R6) · a diff path in the security or UI table adds security-reviewer or
ux-reviewer · the same red signature twice adds debugger · an ambiguous already-done check runs analyst. These are the default rows of `requiredWhen` (§9.5); the item stores the result in `review.required` when it is written. A **risky
unit** (L17) has Severity `high`, or a file-set path that is in the security table (§8.2) or has a
segment containing `identity`, `authz`, `delete`, `purge`, `migrat`, `routing`, `router` or `matching` (default).

| Role | Purpose | May not (gate) | Tier |
|---|---|---|---|
| architect | Splits a goal into unit records with order and dependencies (§8.4, R2). | write files, commits or branches; name a worker model; name a path, name or library the survey did not place (parser) | raised |
| test-author | Writes the unit's tests before code, one commit. | write production code; add skipped, focused or empty tests (D1, D10, D11) | base |
| engineer | Implements the unit inside the file set. | touch the test-author's files or leave the file set (D1, DR-9); weaken tests, ignore errors, add TODOs, secrets or unregistered dependencies (D2, D4, D7, D9, D15, D16, D17) | base |
| doc-writer | Updates the given document, proved by the project's document gate; a new document only when the unit lists one `(new)`. | write a non-document path (D1) | base |
| auditor | Judges work it did not write. | share a run, context or session with the writer (DR-5, D14, §9.5); write `VERDICT:` into a branch commit (`verdict_in_branch`) | raised |
| security-reviewer | Attacks the change; proves each finding. | fix; run a reproduction itself (DR-8) | raised |
| ux-reviewer | Judges changed screens per width. | fix | base |
| analyst | Measures facts read-only before planning or work; answers "already done on base?". | write files; decide or plan (D1) | base |
| debugger | Proves the root cause of a repeated red. | change code; run an experiment itself (DR-8) | raised |

Method per role (the card body): **architect**: each unit record has one problem, its scope, a file boundary, `done` entries each with a check or a named judgement role and its expected signal, and dependencies; outcome items taken = items placed; omissions are named. **test-author**: tests check the `done` entries through the production entry point and fail on base for the missing behaviour, never for an import, syntax or fixture error; they are written at admission, as soon as intake accepts the unit (R6). **engineer**: "red before" only from a recorded result; a structural change removes no check (DR-12); a deliberate behaviour change goes under Scope deviation; three reds with no new cause → stop. **doc-writer**: the project's document gate proves the change; every fact is checked against code or a recorded result, and an unverified line goes under Not done. **auditor**: the writer's return is an assertion; each Done criterion gets PASS or FAIL with one evidence line; it looks for silent scope change, weakened assertion, removed check, compatibility shim, scratch file and fail-open reasoning; a test that only matches source text proves nothing; it never fixes. **security-reviewer**: a one-line threat model; a finding = location · reproduction the engine runs · result · severity; unreproduced → `not_measured`. **ux-reviewer**: the project's UI rules (none → `not_measured`); widths 375, 768, 1440 and 1920 px (default); empty, loading, error, filled and unauthorised states; targets at least 24 by 24 CSS pixels (STD-45; 2.5.8 AA, not 2.5.5); contrast, keyboard, focus. **analyst**: measures facts read-only before planning or work and answers "already done on base?"; each fact carries file and line, an unmeasured item is `not_measured`. **debugger**: one hypothesis, one experiment through `aiwe verify` that changes one variable; a root cause flips that variable red → green and back.

**Card format**: `id, purpose, trigger, needs, output, tier (the starting tier, §8.1 point 7), may, may_not[{text,
gate}]` and a body without shell commands; no harness, model, tool or path names; embedded and served as `roles/<id>`; the role registry generates the cards (§9.5).
### 9.2 Prompt contract
Same sections for every harness, fixed English headings, in this order: `ROLE` (the card) · `PACK` (the unit's input-hashed context pack, shared by its runs; #313) · `UNIT` (the deterministic rendering of
the record: `Key: value` lines, one fact each, sentences of at most 20 words; §8.4), or `FIX` in a fix round (§7.3 step 10) · `SCOPE`
(repository, run branch, file set, line cap) · `RULES` (the project's commit convention, default Conventional Commits;
DR-12; the registry rows named by the unit's rules; contract limits override project rules, which override card defaults; user-level harness rules are off) ·
`VERIFY` (the exact verify and how to request it) · `EVIDENCE` (judging roles and debugger: recorded results, full
diff, diff stat, D-check and mutation findings) · `RETURN` (§9.3). ≤ 32 KB (default), else `prompt_too_large`.
### 9.3 Return format
Last message, ≤ 12 lines: **Done** (verify command, exit code, last lines) · **Not done + why** (may carry `decision:`
lines) · **Scope deviation**. No file listing. Done opens with `PLAN:`, `TESTS:`, `IMPLEMENTED:`, `UPDATED:`,
`ANSWER:`, `ROOT CAUSE:` or `VERDICT: GREEN|RED`; the architect's unit texts follow as a separate block. The text is
never evidence; the engine re-reads branch, diff and results. A judging role's `VERDICT:` line becomes a **verdict record** on the item (§9.5). A Not done entry that needs an answer puts the item into `waiting`, with `waiting.on` naming who answers (`owner`, `analyst` or `debugger`); the work loop skips it and takes the next item (§7.11).
### 9.4 What an agent may run
May: read, search, edit inside the file set, call `aiwe verify`, commit on the run branch. May not: run test, lint or
gate commands (DR-8); anything DR-13 reserves; publish a package; reach the network beyond the adapter's policy;
delete recursively (DR-11).
### 9.5 Role registry
The role registry (an embedded, fail-closed data file beside the cards; planned: stage rule registry) is the one home of role data. A row: `id`, `tier`, `effort`, `model`, `needs`, `output` (the return schema of §9.3), `mayNot[{text, gate}]` and `requiredWhen`. `model` is used only by a card installed into a harness, which must name one; the engine's own runs resolve through §8.1 and ignore it (L24). Cards are generated from the registry: the shared core text exists once, and a card holds only its difference. `aiwe install` (§10.4) writes them into the harness directory; a card there that the registry did not generate is `card_drift`.
- **Required roles**: `review.required` is derived when an item is written, from its `scope.in` paths, its `tags` and the project profile (§11.1), through the `requiredWhen` rows. Default rows: a behaviour unit → test-author; a security-table path (§8.2) → security-reviewer; a UI path → ux-reviewer; every unit → auditor. Only a touched path or a profile flag triggers a role; an item does not ask every role. `close` is refused until each required role has a GREEN verdict record.
- **Writer separation**: an item keeps the sessions that wrote to it (`writers`). A verdict record from one of them does not count, so the writer cannot review its own work (DR-5).
- **Brief**: `aiwe item brief ID --role R` prints the §9.2 text for that role from the record: done criteria, file boundary, verify command, base sha and `Closes: ID`. The lead adds only what the item lacks.
- **Return hook**: `aiwe hook return`, the harness's subagent-stop hook, checks a role's final message against its `output` schema. A missing field rejects the return and names the field; a valid return is written to the item as a verdict record (`aiwe item review`). An engine run does the same through `report_result` (§10.3).
- **Fit**: `aiwe roles fit` counts, per role, model and effort, the first-try GREEN rate and the resubmissions from the verdict records and returns, and proposes changes to `tier`, `effort` and `model`. It never writes the registry (L24).
## 10. Interfaces
### 10.1 Tracker port (file store first)
```go
type Tracker interface {
    Identified
    ReadUnits(ctx context.Context, repo RepoID) ([]Unit, error)
    Write(ctx context.Context, op Op) error // create | edit | transition | intake-close | pr-create | pr-merge
    Permission(ctx context.Context, repo RepoID, login string) (Permission, error) // write | read | none
    CIStatus(ctx context.Context, repo RepoID, sha string) (CIStatus, error)       // pending | green | red | none
    Capabilities() TrackerCaps
}
```
- The **file store** is the primary adapter (O31): it reads and writes `work/items/<id>.json` through the schema (§8.4), validates before it writes, and needs no network, token or gh. `Capabilities()` tells the engine what an adapter supports; the file store has no CI status, pull request or intake.
- The **GitHub adapter** serves intake (`aiwe intake`, the `intake-close` write), the one-way view and the code host (CI status, pull requests). It calls REST over HTTPS with the standard library; GraphQL only where REST lacks the call (L3). The token comes from `GH_TOKEN`, then `GITHUB_TOKEN`, then `gh auth token` when gh is installed; gh is never required. No token → `tracker_unauthenticated` (§7.4). The token is held in memory for the call only (DR-19). Least permissions (default): Contents, Issues and Pull requests read and write; Actions and Metadata read. `git push` uses git's own credential helper.
- One GitHub read per dispatch cycle; a re-read follows a run end, a merge or an intake, at most once per 60 s.
- Every write passes masking and the payload scan (§11).
- `pr-create` = `POST /repos/{owner}/{repo}/pulls`; `pr-merge` = `PUT .../pulls/{pull_number}/merge` with `merge_method: rebase` (a fast-forward, so each unit commit keeps its own `Closes` trailer), the §7.3 step 11 title and message, and `sha` = the tested batch tip (§17).
- Hierarchy and dependencies are item fields (`parent`, `blockedBy`), not GitHub relations. GitHub's limits (§17) bind only its adapter; a `403` or `429` limit follows `rate_window`. Only `tracker/` knows GitHub or the file layout; `tracker/trackertest` holds the fake and runs against both adapters.
### 10.2 Provider port (model API and agent runtime)
```go
type Provider interface {
    ID() string
    Detect(ctx context.Context, env Env) Detection // env is adapters.Env; Present; Version, Reason
    Capabilities() Capabilities // Kind, Roles, Models, Effort; Resume, ReportsModel, RestrictsCommands, Images
    Run(ctx context.Context, req RunRequest) (core.Result[RunReport], error)
}
```
- Two kinds behind one interface (O14). **Model API**: aiwe's own lean agent loop calls the provider's HTTP API; the tools read, write, edit, list and shell run
  locally, confined to the clone (O2). **Agent runtime**: the vendor's own agent runs as a process in the run clone, only as a fallback for an account whose quota offers no API access (for example Cursor; Claude, ChatGPT/Codex, Gemini consumer subscriptions). API access is always the first choice for every account, subscriptions included (OpenCode Go). Each uses only the provider's official interface (DR-19).
- One vocabulary across providers and the whole system: `RunRequest` = role, task, tool permissions, model; `RunReport` = served model, usage (prompt, cached,
  completion, reasoning tokens), cost, quota, outcome; unmeasured is `unknown`, never `0` (DR-6). The stable prefix (role prompt, tool definitions) comes first and never changes within a run, so the prompt cache hits. Prefix order: tools → `ROLE` (the system prompt) → `PACK` → `UNIT` or `FIX` (the order of §9.2). Cache-hit floor: 80 %, where a cache hit = `tokensCachedIn ÷ tokensIn` of one run.
- `Detect` checks PATH, then the documented per-user install location: `harness_missing`, `harness_incompatible`, `harness_unreadable`. A runtime runs
  non-interactively under a written permission policy (`permission_blocked`, never a wait). A session resumes only in its own directory (`session_directory_mismatch`). A **runtime token proof**: every runtime aiwe starts (Cursor, Claude Code, others) reports per-request tokens, or uses the run-total proof — a run whose input, cache-read and cache-write tokens together are within the request limit of §7.4 is proven under the ceiling, else it is `ceiling_unproven` and is counted in the session report.
- Model API: sends `max_tokens` R (§7.4) and only a request that fits under the context ceiling; the language model's read tool takes a line range; one
  tool result is ≤ 32 KiB; clearing is batched (§7.4). It streams `data:` lines when allowed (each chunk resets the silence limit); sends and carries the
  provider's documented reasoning fields; a cut reply → `answer_truncated`. Edit is single exact-match, then the profile's syntax check. Retry rule (RFC 9110):
  `408`, `429`, `500`, `502`, `503`, `504`, a network fault and a timeout are retried 3 times, backoff 2 s base, 60 s cap, full jitter, `Retry-After` first;
  a wait past the run limit, or a `429` after the retries, is `rate_window`; another `4xx` is never retried; the request deadline is the silence limit; every
  exit returns the usage of the completed turns. `Capabilities().Models` is the source's discovered model list (§8.1).
- **Provider data** `adapters/providers/<provider>.json` (embedded, fail-closed): `id`, `adapter`, `baseUrl` (https), `models` (fallback list when discovery has none),
  `reasoning {request, carry}`, `errors [{status, code, class}]`, `balance {path, field, currency}`, `keyEnv` (the API key's usual variable name), and for a runtime
  `cli {binary, flags, loginEnv, limitText}`. A new compatible API or known runtime is one file and one row (DR-3); each value cites a §17 fact or the work item that measured it.
- **Lean run** (O13, O14): a runtime gets a run-owned `HOME`/`USERPROFILE` and config directory (Cursor: `CURSOR_CONFIG_DIR`), so no rules, `AGENTS.md`, `CLAUDE.md` or skills load; injected context is cut to what the role needs.
- **OpenCode Go** (skeleton, O14): `POST https://opencode.ai/zen/go/v1/chat/completions` (OpenAI format: deepseek, kimi, glm; `/messages`, `/responses` later);
  `Authorization: Bearer` from `OPENCODE_API_KEY`, read by name at run time; `x-opencode-session: <run id>` and `User-Agent` required. Only `/zen/go/v1` (Go quota), never `/zen/v1` (credit).
  Quota: `GET /zen/go/v1/usage` (rolling, weekly, monthly percent and reset; undocumented, unreadable → `unknown`). The reply names the served model and tokens, no cost: cost comes from a pinned price table (off-peak and peak rows, §17). Default model `deepseek-v4.1-flash`.
- Then model APIs DeepSeek, Fireworks, Gemini; agent runtime `cursor` via its CLI (M-Usable, §13), then `claude-code`, `codex`, `gemini` (O12). Rejected (O14): Cursor SDK Bridge, Cloud Agents API, the opencode CLI.
### 10.3 Run and session lifecycle
A run is `running`, `waiting_engine` or `stopping`, then `settled`; a start with no live tree is `lost` (§7.4). One owner settles
exactly once: a bounded stop of the tree, branch and residue collected, one run and check record written, clone and claim released;
a captured result survives a cleanup failure, and a retried stop is a no-op. Role, source and every requested option resolve before
a clone or a process exists; an option the adapter cannot honour is refused with an outcome, never accepted and ignored. A run
blocked in `aiwe verify` is `waiting_engine` with its request id: one answer per cache key, its time counted against the silence
rule, and an unanswered request is an outcome, never green. A conversation is a record in the run directory: unit, role, round,
route, prompt-pack and prefix fingerprints, message-log reference and length, budget snapshot and `continuable`. A fix round resumes
from that record alone (§7.3 step 10): the engine, not the adapter, decides eligibility; the round appends `FIX`, inherits the route
and limits literally, and never falls back to another route or a silent fresh run; a model API replays the log, a runtime resumes
only in its own directory. When the adapter owns the tools, the answer is a `report_result` call validated against the role's
object-rooted schema: a failure returns to the model in the same run, an absent result is an error, and the engine still re-reads
branch, diff and results (§9.3). Per-child lifetime caps, nested agents and an in-process registry are rejected: a run is an OS process or
an HTTP loop owned by the engine, so ownership, capacity and stop authority are durable records.
### 10.4 Harness installation and skills
`aiwe install [--harness H]` writes into a harness's own configuration directory what a lead session needs and cannot do without: the role cards generated from the registry (§9.5), the stop hook (§7.11), the return hook (§9.5), the heartbeat hook, the worktree and temporary-file hooks (§6.5) and the destructive-command guard. It is idempotent and prints a diff first; `aiwe doctor` reports a missing or stale entry. One adapter data row per harness names the directory and the hook syntax (DR-3); a harness without a hook is served by the work loop. aiwe installs and generates these files; it does not run inside the harness or CI, and it does not judge what a model reads (O25).

What aiwe owns of a skill set is the commands that replace a skill's mechanical steps: the commit and pull-request gates are `aiwe gates gate` and `aiwe verify`; a pipeline over items is `aiwe work`; an audit of a project against rules and standards is `aiwe sync` (§11.4); item creation is `aiwe item`. A skill that remains is judgement text for a rare, user-specific task and cites these commands instead of copying their steps. Which skills a user keeps, merges or deletes is outside aiwe (O25).
## 11. Quality system applied to managed projects
Mechanical: stack detection, gate derivation and base proof (§7.1); intake and item parse (§8.4); tests red on base
before code for every behaviour unit (§7.3 step 5); unit verify once per cache key (§7.8); scope check (DR-9); payload scan
before text leaves the machine; batch CI before the merge and revert on red (§7.3); red signature and goal pause
(§7.4). After the skeleton, on `<base>...<tip>` lines only, each diff check has one cell (L2). Mutation on changed
lines has a 300 s budget (default); a missing tool is `not_measured`. Judgement (§9.1): done criteria vs diff, D12 and
D13 flags, mutation survivors, security, UI and root cause.

| Id | Check | Enforced by |
|---|---|---|
| D1 | Forbidden paths per role | planned #24 |
| D2 | Test weakening | planned #24 |
| D3 | Suppression growth | planned #24 |
| D4 | Silent catch or ignored error | planned #24 |
| D5 | Untested code | planned #24 |
| D6 | Size | planned #24 |
| D7 | TODO markers | planned #24 |
| D8 | Git hygiene | planned #24 |
| D9 | Assertion count | planned #24 |
| D10 | Disabled or focused tests | planned #24 |
| D11 | Empty tests | planned #24 |
| D12 | Vacuous mocks (flag) | planned #24 |
| D13 | Test literals copied into production lines (flag; never a unit-text identifier) | planned #24 |
| D14 | Separation of powers | planned #24 |
| D15 | Test count | planned #24 |
| D16 | Secrets | planned #24 |
| D17 | Unregistered or deprecated dependency | planned #67 |
| D18 | Changed function over complexity 12 (Q12) | planned #68 |

### 11.1 Project profile and project stage
The project profile is the small record of what no tool can derive (L25). It is stored at `<state>/projects/<repo-id>/profile.json`; a project `aiwe.json` of the same shape wins, as `gate.json` does (DR-4). It holds:
- `stage`: the project stage, one of `idea`, `prototype`, `beta`, `live`, `maintenance`. It decides which catalogue rows bind (§11.3).
- Flags that a repository cannot show: `hasUsers`, `handlesPersonalData`, `hasUI`, `deployed`. A flag that is absent reads `unknown`, and a role or catalogue row that depends on it does not trigger (§9.5).

Everything else is detected on each run and never stored: languages (§8.2 `Detect`), package manager, test runner, CI provider, the presence of a UI path. A stored value that detection contradicts is a `profile_drift` finding, and detection wins.

### 11.2 Catalogues
Two shipped data files, embedded and fail-closed (DR-3), hold the system's quality knowledge. Neither restates a rule; each row cites a registry id (L27).
- `catalog/principles.json`, keyed by registry id: for each rule in `docs/standards.md` that the system applies to managed projects, `meaning` (one sentence in this system), `violation` (a short example), `check` (the enforcement cell grammar of `docs/standards.md`) and `brake` (what stops the rule from over-applying). A rule with no row has no catalogue text; a row whose id is not in the registry is red.
- `catalog/problems.json`: the recurring failures a managed project shows (a test that proves nothing, a gate that is slow, a duplicated rule), each with an `id`, how it is detected, the registry rule it breaks and the item template that fixes it.

### 11.3 Stage catalogue and project stage commands
`catalog/stages.json` lists the work a project needs by phase. A row: `id`, `phase`, `bindsFrom` (the project stage from which it binds), `principle` (a registry id), `check` (a cell of the grammar of `docs/standards.md`) and `item` (the template of the unit that closes it).

| Phase | Work |
|---|---|
| Discover | who the users are, what the problem is |
| Specify | requirements and done criteria in the item standard (§8.4) |
| Design | structure, data, interfaces, threat model |
| Build | code under the gates of §7.10 |
| Verify | tests, scans, the roles of §9.5 |
| Release | versioning, signed artefacts, rollback (§12.3) |
| Operate | monitoring, incident response, cost |

A row with `bindsFrom` later than the project's stage is not required: a prototype is not asked for an incident process. A row whose check needs data aiwe does not hold, such as an operating metric, reads `not_measured` (O4) and is never failed or passed by guess.
- `aiwe init [PATH]` (planned: stage init and sync): detects the stack (§8.2), derives the gate (§7.1), asks for the stage and the flags only, writes the profile (§11.1) and opens items for the rows that bind and fail. It writes no file the project did not agree to and runs twice with the same result.
- `aiwe stage next`: prints the one catalogue row that binds, fails and blocks the most others, with its item template, and the stage the project would reach when all rows of its stage pass. It changes nothing; moving the stage is the owner's edit of the profile.

### 11.4 `aiwe sync`
`aiwe sync [--incremental]` compares a managed project with the catalogues and opens the work that closes the difference (planned: stage init and sync). In order:
1. Read the profile (§11.1) and detect the derivable facts.
2. Select the catalogue rows that bind at the project's stage (§11.3).
3. Run each row's check; a `not_measured` row stays so.
4. For each failing row, compute the fingerprint `sync:<catalogue-id>:<path>`.
5. An open or `waiting` item with that fingerprint is left alone; a `cancelled` one is not reopened unless the row changed; a `done` one whose row now fails again is reopened with a note.
6. Otherwise open an item from the row's template, with its `done` criteria and the check as `verify`, through `aiwe item new` (§8.4).
7. Print the counts: rows checked, passing, failing, `not_measured`, items opened.

Invariants: a run changes nothing in the project's files, only its `work/` store; two runs on the same tree open the same items once; the fingerprint, not the title, decides identity. `--incremental` checks the rows whose inputs changed since the last run (by the cache key of §7.8). Acceptance: on a repository that passes every binding row, `aiwe sync` opens no item; after one row is made to fail, it opens exactly one; a second run opens none.
