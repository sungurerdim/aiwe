# DSH and OpenCode studies -> aiwe plan (2026-10-29)

Status: plan accepted by the owner on 2026-10-29 and filed as issues; nothing is built. The last section, "Final plan", lists every owner statement and decision and wins over the sections above it. Source: deepseek-ai/deepseek-harness at f3803c9; six read-only analyst passes with a per-file coverage ledger, then one architect synthesis. The last section adds the OpenCode study and merges both into one plan (M0-M15, units U0-U18); where it differs, it supersedes the DSH proposals and units above.

Inputs read whole: brief.md, A-, A2-, B-, C-, C2-, D-findings.md. DSH source (f3803c9) was read once, for the one conflict:
the retry default is 5 in code (packages/llm/llm/src/retry-policy.ts:14), while a design note says 2 (D-findings §4). The code wins.
aiwe side read: SPEC §7.3, §7.4, §7.7, §7.8; SPEC-contracts §8.1, §9, §10; standards.md; internal/adapters/{loop,loop_io,
tools,provider,openai_chat,opencode_go,deepseek,window,all}.go; internal/engine/{dispatch,contract}.go; aiwe-wt/ext.py and
HANDOFF.md.
New measurement this session: aggregate of the bridge's 1129 run logs in aiwe-wt/evidence/ext-*.jsonl. 457 runs are
API-driven (they carry turn records). Results: median 25 turns, p90 56, max 80; 6 runs ended at the turn cap; 2 runs had 3 or
more identical consecutive tool calls; 38 HTTP errors and 11 network errors across all logs; 0 `length` finishes; 71 runs called
`verify`, and in 6 the last verify was red; the aggregate cache hit is 0.941. The logs hold no tool-result text, so the
tool-error rate cannot be measured from them.

## 1. What explains DSH's results

The ranking is by likely effect. "Fact" means read in DSH code. "Measured" means a number exists. No DSH document states a
task-success or harness-versus-harness number (C §9, D §3). So every success effect below is inferred.

R1. A fail-soft loop: no model slip and no transient fault ends the task. Fact, effect inferred.
  - Unknown tool, bad arguments, a policy denial, an exception, a schema violation and a timeout all become an `Error: <msg>`
    tool result, and the loop continues. Argument violations list every failing path (A M2).
  - A half-written call is never executed (A M3). Missing tool results are filled with synthetic errors (A M4).
  - EMPTY_RESPONSE, RATE_LIMIT, SERVER, TIMEOUT and TRANSPORT are retried 5 times with backoff (A M5, C2 N9).
  - An overflow triggers one compaction and a retry (A M8).
  - Error texts name the fix: "file has not been read - read the file, then retry" (C M2, M3).
R2. Thinking is on by default at effort `high`, and reasoning is replayed into history. Fact, effect inferred.
  - `thinking:{enabled}` is sent with effort = request, else adapter default, else `high` (A M1, llm-deepseek/src/serialize.ts:147-156).
  - Reasoning is passed back on every turn that carried it (D §4).
  - Comparison harnesses may have run with thinking off or low, or on another route. That would be enough to produce a gap
    with the same model. Inferred.
R3. Recoverable truncation and context hygiene. Fact, effect inferred.
  - Output over 12,500 tokens becomes head + tail plus a pointer to the full saved copy, with "grep or read this path" (A M9).
  - Read has a 2000-line window with a continuation footer (C M5).
  - Grep and glob caps say "K of N" (C M7). Bash keeps the tail and writes `[exit code: N]` (C M8).
  - Under pressure: prune first (head 4096 + tail 1024), then an 8-section summary that keeps the warm prefix (A M6, M7).
  - The model always knows what it did not see and how to get it.
R4. Steering on the tool surface, not in the prompt. Fact; behaviour partly measured.
  - One line per tool is in the prompt only while that tool is visible (C M6).
  - The `required` order plus one sentence moved flash's description-first calls from 0/10 to 19/20 (measured, 310 requests,
    D §3 M2). That measures generation order, not success.
R5. One-sentence rules validated by probes. Measured for behaviour.
  - "end your turn instead of sleeping or polling": sleep calls went from 29/300 to 0/300; ended turns from 179/300 to 291/300;
    flash alone p=0.030 (D §3 M1).
  - The method (150 requests per arm, 2 models, Fisher test) is the transferable part.
R6. Cache-stable request construction. Measured for cost.
  - Append-only deltas, dynamic facts after history, the tool list constant across modes (A M10).
  - 99.9% versus 0.16% cache hit when node 0 was rewritten (D §3 M3). Cost and latency, not accuracy.
R7. Goal rounds with an evidence-before-complete prompt and a "blocked only after 3 rounds" gate; the wrap-up is grounded.
  Fact, effect inferred.
  - The wrap-up wording was A/B tested qualitatively, without counts (B rows 1-3).
  - These are only active for long-running goals.
R8. Repeat-call reminder at 3, 5 and 8 identical calls (B row 4). Inferred.
  - aiwe's bridge data shows the condition in 2 of 457 runs. Low value here.

What the code does not explain:
- No "1017" exists as a model, profile or alias. It appears only as a UI badge string in a test (A §0).
- No flash-specific prompt, sampling or tool variant exists. `temperature` is forwarded only if a caller sets it; `top_p` is
  absent. Per-model data is three catalog fields plus the window and max tokens (A §0, A2 Q1, C2 §3).
- The base prompt is almost empty: "You are an AI agent powered by DeepSeek Harness." plus "You are a coding agent powered by
  the {{model}} model." (D §0). The "Verify your work by running the code or tests" persona is a test fixture, not shipped (C2 §3).
- The benchmark profile is unknown.
  - BENCHMARK.md points at the `jsonrpc-agent` minimal variant: a one-line persona and one persistent shell (C2 §2).
  - If the owner's runs used minimal, R4, R5, R7 and R8 are absent, and the gain comes from the model I/O and loop (R1-R3) or
    from the server.
  - Only the owner knows which profile ran. Stating it narrows this ranking.
- `dsh_session_log`: by default the DeepSeek adapter sends the session log suffix in request-body fields to the configured
  base URL, together with `dsh_plugin_packages` and the headers `x-deepseek-harness-session-id` and `-compact`.
  - The suffix holds system prompts, user and assistant content, tool arguments and results, and the working directory.
  - Sources: docs/deepseek-llm-api-wire-extensions.md:7, the header table, and the exposure section.
  - The doc says the fields stay outside `messages`, so they add no model-input tokens.
  - The client code cannot show whether the server uses them, for example for cache affinity or routing. That leaves an open
    confound for any DSH-versus-other comparison.
  - aiwe must not copy it: it ships repository content off the machine outside the payload scan (SPEC §11), and it is not a
    quality mechanism.

## 2. Gap table
(Go loop = internal/adapters/loop*.go. Note: no production engine.Runner exists yet; only test fakes implement
dispatch.go:57-60. The Go loop is not yet reached from dispatch, and today's external runs go through the bridge ext.py.)

| DSH mechanism | aiwe today | Gap | Every provider? |
|---|---|---|---|
| Tool error -> result, loop goes on | Go loop feeds the error text back: loop.go:188-203, tools.go:83, bad JSON tools.go:215-221; texts carry no remedy | partial | yes: plain text in any wire format |
| Transient + empty-reply retry | Go loop: non-2xx ends the run (loop_io.go:96-98); an empty reply is an error (loop_io.go:124-126; TestLoopRefusesAReplyThatAnsweredNothing). SPEC §10.2 specifies 3 retries, uncoded. Bridge retries 5 times (ext.py:437-467), not empty replies | full (code) | yes |
| Cut reply handled as a named outcome | Go loop: a `length` cut is a run error (loop_io.go:121-123); SPEC wants `answer_truncated` (§10.2); bridge asks to continue (ext.py:1079-1086) | partial | yes |
| Thinking default + reasoning replay | Effort is sent only if the candidate names one (loop_io.go:35). wireMessage has no reasoning field, so reasoning is dropped (loop_io.go:40-45). SPEC §10.2 "sends and carries the provider's documented reasoning fields". Bridge carries it: "Thinking mode rejects the next turn unless this field comes back" (ext.py:1059-1062) | full (Go loop) | yes, as a per-provider data row `reasoning {request, carry}` (SPEC-contracts §10.2, DR-3) |
| max_tokens reserve | loopRequest has none (loop_io.go:31-36); SPEC §7.4 R = 16,000 default | full | yes |
| Spill head+tail + pointer; read window | Read and shell over 256 KiB are refused whole (tools.go:22-25, loop_io.go:193-196, 299-301). No line range, though SPEC §10.2 asks for one and caps a result at 32 KiB. Bridge: read window (ext.py:82-94), cut at 30,000 chars with no pointer (ext.py:1108-1109) | full | yes |
| Pressure prune / summary | SPEC §7.4 ceiling with stubs at 150k; not coded in adapters (grep: none); stubs lose content without a pointer | full (code), partial (design) | yes |
| Per-tool guidance, parameter descriptions and order | Go: names plus untyped string params, no descriptions (tools.go:236-276); bridge has descriptions (ext.py:180-253) | full (Go) | yes (API path) |
| Shell exit marker | Go error text "shell: argv0: exit status N" (loop_io.go:303-305); nothing on exit 0 | partial | yes |
| Probe-validated prompt sentences | Contract text unmeasured (contract.go:81-145) | full | yes, also CLI |
| Cache-stable prefix | Fixed tool order (tools.go:233-235); 80% floor (SPEC-contracts §10.2); bridge 0.941 measured | none | yes |
| Turn-budget wind-down | Hard stop (loop.go:103-105); SPEC wind-down at 90% (§7.4); bridge notice at cap-6 (ext.py:1111-1120) | partial | yes |
| Repeat-call reminder | none; bridge 2/457 | full, low value | yes |
| Goal rounds / evidence before done | Engine verify + separate auditor, return "never evidence" (§7.3 steps 6-9, §9.3) | none (aiwe stronger) | yes |
| Grounded wrap-up | §9.3 format; bridge "Never claim a check passed unless its output is in the task" (ext.py:265-273); Go contract lacks it | partial | yes, also CLI |
| In-run check feedback | Go loop has no verify tool; contract says "ask the engine" (contract.go:128-131); bridge verify tool (ext.py:247-253, 327-392) | full (Go) | yes; CLI through `aiwe verify` |
| Read-before-edit version guard | Edit reads and writes in one call (loop_io.go:228-240) | partial, low value (one writer per slot) | yes |
| Workspace instruction files, persistent shell, session-log telemetry | Excluded by design: lean run (SPEC-contracts §10.2), no shell (§5.20), payload scan (§11) | intentional | n/a |

## 3. Proposals (ordered by value / cost)
Path key: [API] = direct-API Go loop only; [API+CLI] = also helps provider-CLI agents.

P0. Repeatable baseline bench (Measure-Before-Optimize, prerequisite of every P below). [API+CLI]
- Mechanism: a fixed corpus of already-closed units. Each has a red proof on its base and a green verify on its landed tip
  (from the bridge evidence and the ledger).
  - Each arm runs each unit k times on the same candidate: deepseek-v4.1-flash on OpenCode Go, plus at least one other
    provider or model to prove the effect is generic.
  - Metrics: the §8.1 point 4 set (first-try green rate, cost per green, median duration) plus outcome distribution, turns,
    tokens, cache hit, retries, and tool errors by class.
  - Comparison as DSH did: Fisher exact on green counts.
  - DSH reference arm: the owner runs DSH by hand on the same units. aiwe never drives DSH (SPEC-contracts §10.2 rejects a
    general harness CLI as provider).
- Lives: a test-only driver over adapters.Loop until a production Runner exists (dispatch.go:57-60); its home is the
  architect's call (DR-16 if it becomes a command).
- DR: DR-6 (unknown is never 0), DR-2 (selection is mechanical), DR-8 (verify through the executor), DR-19 (key from env only).
- Cost / risk: API spend of k x units x arms. The bridge median of 25 turns per run is the size guide. Risk: too few units
  hide a small effect.
- Units: S0, S0b, S0c.

P1. Carry reasoning, send max_tokens, take effort from the candidate. [API]
- Mechanism: wireMessage keeps the provider's documented reasoning field and sends it back on every turn, as the bridge does.
  - Each request sends `max_tokens` R from the §7.4 limits row.
  - Effort comes from the (source, model, effort) entry (DR-15). A default effort for an entry that names none is owner Q1.
  - The carry rule is data per provider (SPEC-contracts §10.2 `reasoning {request, carry}`), kept in the adapter source until
    the provider data file exists.
- Lives: adapters/loop_io.go, loop.go; SPEC-contracts §10.2 (already states it).
- DR: DR-3 (data row, no model branch), DR-15, DR-6.
- Measure: on P0, the share of runs ending `final_answer`, first-try green rate, and turns per green; before and after.
  Today's Go loop drops reasoning, so a thinking-mode run may break at its second tool turn. That is inferred from the bridge
  comment and should be the first P0 row.
- Cost / risk: low, about 3 files. Risk: reasoning tokens raise input size, so watch the cache hit.
- Units: S1.

P2. Fail-soft transport and reply handling. [API]
- Mechanism:
  - Code the SPEC §10.2 retry rule: 408, 429 and 5xx, network and timeout faults; 3 times; 2 s base, 60 s cap, full jitter;
    `Retry-After` first; `rate_window` past the limit.
  - Add an empty reply as a retryable class (new SPEC line).
  - Map a `length` cut to `answer_truncated` with the completed usage (SPEC §7.4), never a bare error.
  - Add `rate_window` to dispatch.go providerStop (it is missing at dispatch.go:256-266).
- Lives: adapters/loop_io.go, loop.go, engine/dispatch.go; SPEC-contracts §10.2.
- DR: DR-1 (every wait bounded, named outcome), DR-6, STD-32.
- Measure: P0 outcome distribution, where `environment` runs should fall to near zero. Bridge baseline: 49 HTTP and network
  faults in 1129 logs. The bridge retried them; the Go loop would have ended those runs.
- Cost / risk: low. Risk: a retry must never resend after a reply that ran tools. The retry wraps only the HTTP request.
- Units: S0c (SPEC), S2.

P3. A tool surface that teaches. [API]
- Mechanism:
  - One plain description per tool and per parameter, in the stable prefix, with the path before the large body (DSH's
    measured argument-order effect).
  - Each refusal ends with its remedy, for example "holds that text 2 times: give a longer unique `old`".
  - Shell results end with a fixed English marker `[exit code: N]` on every exit (DR-18 fixed keys).
  - Error results start with a fixed marker, so a model can tell an error from output.
- Lives: adapters/tools.go (definitions, texts), loop_io.go (shell marker).
- DR: DR-18, DR-3 (one wording for every provider), DR-22 (each sentence measured by P6, else dropped).
- Measure: P0 tool errors by class per run, turns per green, first-try green rate.
- Cost / risk: low. Risk: description bytes change the prefix once; the cache hit stays stable within a run.
- Units: S3, S4.

P4. Recoverable truncation. [API]
- Mechanism:
  - Read takes `offset` and `limit` (SPEC §10.2 already) with a footer like "lines A-B of N; offset=B+1 continues".
  - Output over the 32 KiB result cap becomes head + tail with "N bytes omitted; full copy at <run-relative path>; read it with
    offset/limit". The full copy is written in the run-owned temp area and removed at settle (DR-11).
  - This replaces "refused whole" (tools.go:22-25), which wastes a turn and leaves the model blind.
- Lives: adapters/tools.go, loop_io.go; SPEC §7.4 and SPEC-contracts §10.2 (policy change, owner Q2).
- DR: DR-10 and O2 (the copy is reachable only inside the run's confinement), DR-11, DR-6.
- Measure: P0 count of `read_too_large` refusals per run, turns per green, and runs that re-read the same file.
- Cost / risk: medium. Risk: confinement of the spill path, so a security-reviewer step is needed. Removing the copy is a
  deletion, so a test-author step is needed.
- Units: S0c (SPEC), S5.

P5. Wind-down notice and a report at the cap. [API]
- Mechanism: at 90% of the turn or time limit (SPEC §7.4), one user-role notice: "N turns left: commit finished work, then
  give the §9.3 return with each unfinished item". The bridge does this at cap-6.
- Lives: adapters/loop.go.
- DR: DR-1.
- Measure: P0 share of `turn_limit` and `time_limit` runs with a parsable §9.3 return. Bridge baseline: 6 of 457 runs hit
  the cap.
- Cost / risk: very low.
- Units: S6.

P6. Probe-validated contract sentences (DSH's own method). [API+CLI]
- Mechanism: each behaviour sentence of the §9.2 contract and the cards gets a paired probe.
  - Without and with the sentence; at least 2 models; a fixed request set; Fisher test.
  - A sentence stays only with a recorded effect. Its result goes in the artefact store as rung-2 evidence (§7.7).
  - First candidates: "never claim a check passed unless its output is in this run" (DSH wrap-up grounding), "end with the
    return and no tool call", "on a red verify, read the failing list before editing".
- Lives: the contract (engine/contract.go) plus the P0 driver; SPEC §9.2.
- DR: DR-2 (sentences chosen by stored evidence), DR-22 (a sentence without effect is deleted), DR-3.
- Measure: per-sentence behaviour rate, then first-try green rate on P0.
- Cost / risk: medium (probe spend). Risk: a small sample overfits one model, so 2 models are required.
- Units: S7.

P7. Context ceiling with recoverable stubs. [API]
- Mechanism: code SPEC §7.4 (E <= 184,000; at E >= 150,000 the oldest tool results become stubs). Each stub keeps the P4
  pointer, so cleared content is still readable. No model-written summary: aiwe units are small (DR-7) and bridge runs
  have a median of 25 turns.
- Lives: adapters/loop.go, loop_io.go.
- DR: DR-6, DR-21 (no tokenizer).
- Measure: P0 count of `token_limit` runs and the peak prompt tokens per run. The peak is not in today's bridge aggregate;
  S0 adds it.
- Cost / risk: medium; low value until P0 shows long runs.
- Units: S8.

P8. Repeat-call reminder. [API]
- Mechanism: after 3 identical (tool, canonical args) calls, append one advisory line to the tool result. Never block.
- Lives: adapters/loop.go.
- DR: DR-3.
- Measure: P0 repeat incidence. Bridge baseline: 2 of 457 runs.
- Cost / risk: low cost, low value. First to drop.
- Units: S9.

## 4. Further ideas (not in DSH, from the same logic)

F1. Check results fed back in-run the way DSH feeds tool errors. [API+CLI]
- Mechanism: DSH has no post-edit check. aiwe's engine already runs the unit verify (§7.3 step 6, DR-8). Return its red as a
  teaching result:
  - the failing items first;
  - which `done` entry each one fails;
  - the red class and subclass with its one-line fix hint (§7.4 L64, SPEC-contracts §8.5);
  - the early diff-check findings (§7.8);
  - the path of the full log, never the full log (§7.8 Result).
- Go loop: a no-argument `verify` tool with a per-run call limit, as in the bridge (#346). It runs only the unit's verify
  through the executor, so the cache key dedupes repeats.
- CLI agents: the same compact rendering from `aiwe verify`.
- Lives: SPEC §7.8 Result and §7.9; adapters (a verify callback in RunRequest, so adapters do not import engine; DR-17
  module edges); engine check executor.
- DR: DR-8, DR-6, DR-17, DR-9.
- Measure: P0 fix rounds per green unit and first-try green rate. Bridge baseline: 71 of 457 runs called verify, and in 6
  the last verify was red.
- Cost / risk: medium. It runs a command, so a security-reviewer step is needed.
- Units: S10.

F2. File-set guard at write time. [API+CLI]
- Mechanism: a write or edit outside the unit's file set is refused in-run with its remedy, instead of surfacing later as
  `scope_mismatch` after collect (DR-9). CLI agents get the same through the install hook as an early warning (§10.4).
- DR: DR-9, DR-3.
- Measure: `scope_mismatch` rate per unit.
- Cost: low.

F3. In-run return validation. [API+CLI]
- Mechanism: the §9.3 return is checked against the role's output schema in the same run: `report_result` in the API path
  (§10.3, already planned), the return hook for CLI (§9.5). A failure goes back to the model as a tool error, like DSH's
  `structured_output`.
- DR: DR-6.
- Measure: rejected returns per run.
- Cost: low to medium.

F4. Loop signals into own evidence. [API+CLI where the runtime reports]
- Mechanism: per run, record tool errors by class, retries, truncations, wind-down hits and reasoning tokens, next to §8.1
  point 4. Model choice then learns which (model, effort) handles aiwe's harness, not only the public prior.
- DR: DR-6, DR-2.
- Measure: P0 again, read from the ledger.
- Cost: low.

F5. Escalate effort from evidence, not taste. [API+CLI]
- Mechanism: P0 measures low versus high per role. §8.1 point 9 already moves to a higher effort on a second red; the bench
  sets the starting effort per role (owner Q1).
- DR: DR-15, DR-2.
- Cost: bench spend only.

## 5. Open questions (Decision Framing)

Q1. Default effort for a candidate that names none.
- Recommendation: the provider's documented `high` level (DSH's default), measured against `low` on P0 before it is final.
- (a) Start at `high`; a second red moves to `max` where the provider has it.
- (b) Start at the lowest level and rely on §8.1 point 9 escalation.
- (c) Set the start per role from P0 results only.

Q2. Truncation policy for oversized reads and outputs.
- Recommendation: (b).
- (a) Keep refusing the whole result (today).
- (b) Head + tail inline, full copy in the run-owned area, and a pointer the read tool can open.
- (c) Head + tail inline with no saved copy.

Q3. In-run verify for the Go loop.
- Recommendation: (a), with the bridge's limit of 3.
- (a) A no-argument verify tool with a per-run call limit and the compact red rendering.
- (b) No tool: red only reaches the agent as a fix round.
- (c) A tool with no call limit, relying on the cache key.

## 6. Plan

Epic: Harness-quality learnings from the DSH study, for the model-API loop and the prompt contract.

S0 - Baseline bench corpus (analyst, read-only)
  Done: a list of N closed units, each with a red proof on its base and a green verify on its tip, every fact with file:line.
  Files: none written.
  Todo:
  - select the units from the ledger and the bridge evidence — verify: list count and source per row -> N rows, 0 rows without a red proof
  - add peak prompt tokens and tool-error class to the baseline aggregate — verify: the aggregate re-run on aiwe-wt/evidence -> the numbers printed with their basis, unknowns marked unknown

S0b - Bench driver (engineer; test first)
  Done: one command runs arm x unit x k over adapters.Loop and prints the §8.1 point 4 metrics per arm.
  Files: decided by the architect, at most 5 files, inside internal/adapters tests or one registry command (DR-16).
  Test-author first; security-reviewer (the key is read from env, DR-19).
  Todo:
  - a test with a fake endpoint pins the metric roll-up and unknown-not-0 — verify: go test ./internal/adapters/ -count=1 -> ok, and the new row red on base
  - a dry run on 2 units — verify: the driver output -> one row per arm with green, turns, tokens, cost or unknown

S0c - SPEC lines for the changes (doc-writer)
  Done: SPEC-contracts §10.2 names the empty-reply retry, reasoning carry by data row and truncation per Q2; SPEC §7.4 names
  the wind-down notice.
  Files: docs/SPEC.md, docs/SPEC-contracts.md.
  Todo:
  - write the lines after Q1-Q3 are answered — verify: the project's document gate -> green

S1 - Reasoning carry and max_tokens (engineer) [after S0b; can run with S3]
  Done: turn 2's body carries turn 1's reasoning field; every body carries max_tokens R.
  Files: internal/adapters/loop_io.go, loop.go, loop_test.go, loop_io_test.go.
  Todo:
  - test rows: reasoning echoed back; max_tokens present; effort unchanged when the candidate names none — verify: go test ./internal/adapters/ -run TestLoop -count=1 -> ok, new rows red on base
  - code — verify: same -> ok; TestLoopKeepsTheStablePrefixAcrossTurns still ok

S2 - Retry, empty reply, cut reply (engineer) [after S0c, S1]
  Done: 503 then 200 ends `final_answer`; empty then an answer ends `final_answer`; 400 is not retried; `length` ends
  `answer_truncated` with usage; `rate_window` is accepted by providerStop.
  Files: internal/adapters/loop_io.go, loop.go, loop_test.go, internal/engine/dispatch.go, dispatch_test.go.
  Todo:
  - test rows for each case above — verify: go test ./internal/adapters/ ./internal/engine/ -count=1 -> ok, new rows red on base
  - code — verify: same -> ok; TestLoopRefusesAReplyThatAnsweredNothing replaced, with a Behavior change line (DR-12)

S3 - Tool definitions with descriptions and order (engineer) [after S0b; can run with S1]
  Done: each offered tool and parameter has a description; the path comes first.
  Files: internal/adapters/tools.go, tools_test.go.
  Todo:
  - test row: the definitions hold a description per tool and per parameter, in a fixed order — verify: go test ./internal/adapters/ -run TestTools -count=1 -> ok, new row red on base
  - code — verify: same -> ok

S4 - Remedy texts and markers (engineer) [after S2, S3]
  Done: every refusal ends with its remedy; shell results end `[exit code: N]` on every exit.
  Files: internal/adapters/tools.go, loop_io.go, tools_test.go, loop_io_test.go.
  Todo:
  - test rows per refusal class and per exit code 0 and non-0 — verify: go test ./internal/adapters/ -count=1 -> ok, new rows red on base
  - code — verify: same -> ok

S5 - Recoverable truncation (test-author, engineer, security-reviewer) [after S0c, S4]
  Done: read takes offset and limit with a footer; an oversized result is head + tail plus a pointer; the copy is readable
  only inside the run and removed at settle.
  Files: internal/adapters/tools.go, loop_io.go, tools_test.go, loop_io_test.go.
  Todo:
  - test-author: rows for the window footer, head+tail, pointer read, a pointer outside the run refused, copy removed — verify: go test ./internal/adapters/ -count=1 -> new rows red on base
  - engineer: code — verify: same -> ok; TestLoopRefusesShellOutputOverTheCap replaced, with a Behavior change line
  - security-reviewer: threat line on spill path confinement — verify: the reviewer's finding list -> reproduced or not_measured

S6 - Wind-down notice (engineer) [after S2]
  Done: at 90% of the turn limit, exactly one notice is appended.
  Files: internal/adapters/loop.go, loop_test.go.
  Todo:
  - test row: the notice is at turn ceil(0.9 x cap) and only once — verify: go test ./internal/adapters/ -run TestLoop -count=1 -> ok, new row red on base

S7 - Contract sentence probes (engineer, then auditor) [after S0b]
  Done: each candidate sentence has a stored probe result on 2 models; sentences without an effect are not added.
  Files: internal/engine/contract.go, contract_test.go, plus the probe data at the S0b home.
  Todo:
  - probe the 3 sentences of P6 — verify: probe output -> counts per arm, Fisher p, per model
  - add only sentences with an effect — verify: go test ./internal/engine/ -run TestContract -count=1 -> ok

S8 - Context ceiling with stubs (engineer) [after S5]
  Done: E is computed per §7.4; at 150k the oldest results become stubs with a pointer; above 184k the run ends `token_limit`.
  Files: internal/adapters/loop.go, loop_io.go, loop_test.go.
  Todo:
  - test rows for the stub batch and the token_limit end — verify: go test ./internal/adapters/ -run TestLoop -count=1 -> ok, new rows red on base

S9 - Repeat-call reminder (engineer) [after S4]
  Done: the 3rd identical call gets one advisory line.
  Files: internal/adapters/loop.go, loop_test.go.
  Todo:
  - test row — verify: go test ./internal/adapters/ -run TestLoop -count=1 -> ok, new row red on base

S10 - In-run verify feedback (test-author, engineer, security-reviewer) [after S0c, S4]
  Done: the Go loop offers a no-argument verify through a RunRequest callback; the result is the compact red rendering with
  done-entry mapping; a call past the limit is refused with its remedy.
  Files: internal/adapters/provider.go, tools.go, loop.go, tools_test.go, loop_test.go.
  Todo:
  - test-author: rows for no-argument enforcement, the limit, the rendering — verify: go test ./internal/adapters/ -count=1 -> new rows red on base
  - engineer: code — verify: same -> ok
  - security-reviewer: the model cannot choose argv — verify: the finding list -> reproduced or not_measured

Parallel and dependencies:
- Can run together: S0, S0b, S0c; S1 and S3; S6 and S10 after their dependencies.
- In order: S1 -> S2 -> S4 -> S5 -> S8; S3 -> S4; S4 -> S9 and S10.

Out of scope: workspace instruction loading (conflicts with the lean run); goal rounds and the summary compactor (aiwe's engine
done check is stronger; a summary is a model call); PTC `run_code` (runtime size, DR-21, DR-22); parallel tool calls
(latency only); a persistent shell (§5.20); session-log telemetry (payload scan, §11); a read-before-edit version guard (one
writer per slot).

Drop order if time runs short: S9, S8, S6, S7, then F4 and F5 (F1-F5 are not yet units).
Criteria: 17 received, 16 placed; missing: "write proposal.md" (this role writes no files).

## OpenCode study and the merged plan

Status: this is a recorded plan only (owner, 2026-10-29). Nothing is built or filed.
Source: anomalyco/opencode at 77a0fd6f3b247235b09941d55ff0faf72be1dae4. Six read-only analyst passes kept per-file coverage ledgers. One merge pass followed.
I re-read the aiwe side on main at 4ec9550:
- internal/adapters/{loop,loop_io,tools,openai_chat,opencode_go,deepseek,provider}.go;
- internal/engine/dispatch.go;
- docs/SPEC-contracts.md §10, docs/SPEC.md §7.4 and docs/standards.md.
Where the DSH plan says "SPEC §10.2", it means docs/SPEC-contracts.md §10.2. docs/SPEC.md has no §10.

### Coverage

- A, `packages/core` (v2 loop, prompts, context, compaction): 121 files listed, 130 ledger rows, 0 missing. 41 read, 22 skimmed, 67 skipped.
- B, tools, Code Mode, plugin hooks, permissions: 620 files listed, 620 rows. 28 read, 30 skimmed, 562 skipped by pattern, each with a reason.
- B2, B's read gaps and the provider plugins: 67 rows. 41 read, 26 skimmed, 0 skipped. All 33 provider plugin files have a row.
- C, `packages/ai`, catalogue and agent schema: 1615 files, in 1314 rows plus 1 pattern row covering 301 recordings. 19 read, 27 skimmed, 1269 skipped. C says long-tail provider quirks are under-reported.
- C2, the protocol, provider and route files C skipped: 181 listed, 181 rows. 34 read, 109 skimmed, 38 skipped (media and wire plumbing).
- C3, openai-chat.ts, open-responses.ts, provider-error.ts, lifecycle.ts and the partial-json pair: 8 rows, 8 read whole.
- Accepted gap 1: OAuth, PKCE and token-refresh code is not read. aiwe holds no such credential (DR-19).
- Accepted gap 2: the body of `packages/core/src/shell/scan.ts`, lines 250-2409, is not read. Its behaviour comes from header comments and oracle tests.
- No part covers `packages/opencode`, the v1 loop. So whether v1 has doom-loop or edit-repair code is unknown.
- The merge pass read `packages/schema/src/model-compatibility.ts` whole. It also checked cited lines in shell.ts, read.ts, read-filesystem.ts, publish-llm-event.ts, llm.ts, openai-chat.ts, provider-error.ts and tool-stream.ts.

I re-checked each aiwe claim the DSH plan relies on:
- No reasoning carry: confirmed. wireMessage holds only role, content, tool_call_id and tool_calls (internal/adapters/loop_io.go:40-45).
- No max_tokens: confirmed (loop_io.go:31-36).
- Retry rule not coded: confirmed. Any non-2xx reply ends the run (loop_io.go:96-98). The rule itself is at docs/SPEC-contracts.md:253-256.
- An oversized result is refused whole: confirmed (internal/adapters/tools.go:22-25; loop_io.go:193-196 and 299-301).
- Tools have no descriptions: confirmed. chatFunction has only a name and parameters (internal/adapters/openai_chat.go:164-167; tools.go:256-276).
- `rate_window` is missing from providerStop: confirmed (internal/engine/dispatch.go:256-266).
- New fact: of the two providers, only OpenCode Go runs the loop (internal/adapters/opencode_go.go:95-124). DeepSeek sends a single chat turn (internal/adapters/deepseek.go:117-122).

### What OpenCode does that DSH does not

Evidence strength: the OpenCode tree holds no eval or benchmark numbers (A §6, B §3, C §0). Every success effect below is therefore inferred. The evidence column only says what pins the behaviour itself.

| # | Mechanism | OpenCode file:line | aiwe gap it closes (checked) | Generic or data row | Cost | Evidence |
|---|---|---|---|---|---|---|
| 1 | Ordered failure classifier. It reads status, body codes and message text. Unknown failures are retryable. The `x-should-retry` header overrides. Throttle text never counts as overflow. | packages/ai/src/provider-error.ts:62-68, 87-117, 246-330 | Any non-2xx ends the run with only the status text (internal/adapters/loop_io.go:96-98). | Generic rules; the code sets become `errors [{status, code, class}]` rows (docs/SPEC-contracts.md:258) | Low; no tokens | Inferred; code read whole (C3) |
| 2 | Compatibility row. It names the reasoning field and requires reasoning on every prior assistant message. It names the max-tokens field. Models inherit provider rows. | packages/schema/src/model-compatibility.ts:25-55; packages/ai/src/protocols/openai-chat.ts:558-574, 660-666 | No reasoning field (loop_io.go:40-45). No max_tokens (loop_io.go:31-36). No provider data file exists yet. | Data row | Low; replayed reasoning adds input tokens | Inferred; code only |
| 3 | Finish-reason table: `stop` with calls means tool-calls; `content_filter` is named; `error` is hard; `network_error` is retryable; anything else is `unknown` | packages/ai/src/protocols/openai-chat.ts:935-964, 1132-1138 | Only `length` is checked (loop_io.go:121). A `content_filter` reply that carries text ends as `final_answer` (internal/adapters/loop.go:177-178). | Generic | Low | Inferred; code only |
| 4 | An error object inside a 2xx body is classified like an HTTP failure | packages/ai/src/protocols/openai-chat.ts:321-327, 1114-1123 | Such a body fails as "the reply names no model" and is never retried (loop_io.go:114-116). | Generic | Low | Inferred; code only |
| 5 | Per-call shell time limit, 120 s by default. The notice names the limit. | packages/core/src/tool/plugin/shell.ts:24, 201, 230 | A shell call has no limit of its own. Only the run's time limit stops it (loop_io.go:283-307). | Generic; the limit comes from the §7.4 limits derivation (DR-1) | Low | Inferred; code only |
| 6 | Text-only final turn at the step cap. It sends `toolChoice: "none"` and keeps the tool list for the cache. It asks for done, remaining and next. | packages/core/src/session/runner/llm.ts:225-246; packages/core/src/session/runner/max-steps.ts | The turn cap is a hard stop with no return (loop.go:103-105). | Generic | One turn | Inferred; test packages/core/test/session-runner.test.ts:5336 |
| 7 | Shell keeps the tail of the output and saves the full copy. It notes exit, signal or timeout. Empty output becomes `(no output)`. | packages/core/src/shell.ts:244-259; packages/core/src/shell/result.ts:30-34 | Output over 256 KiB is refused whole (loop_io.go:299-301). | Generic | Disk only | Inferred; code only. DSH keeps the tail too, and differs on the exit marker. |
| 8 | Read hygiene: a NUL byte means binary, and binary is refused. Lines are cut at 2000 chars. An out-of-range offset is named. "Did you mean" lists up to 3 similar names. | packages/core/src/tool/read-filesystem.ts:14-52, 134-198; packages/core/src/tool/plugin/read.ts:132-165 | Read returns the bytes of any regular file as text, binary included (loop_io.go:183-202). | Generic | Low | Inferred; code only |
| 9 | A reply cut by `length` never runs its pending tool calls. The model is told to split the payload. | packages/ai/src/protocols/openai-chat.ts:1255-1275; packages/core/src/session/runner/publish-llm-event.ts:36-72 | aiwe never runs them either. But the cut is a run error, not `answer_truncated` (loop_io.go:121-123). | Generic | None | Inferred; tests session-runner.test.ts:5693 and 5737. Partly in DSH (R1). |
| 10 | Empty tool output becomes a fixed marker | packages/ai/src/tool-history.ts:6-98; packages/core/src/shell.ts:259 | An empty shell, list or write result goes back as empty content (loop.go:190-203). | Generic | None | Inferred; code only |
| 11 | The non-interactive refusal tells the model to continue without the action | packages/cli/src/run/noninteractive.ts:70-74 | A disallowed tool is refused with no next step (tools.go:69). | Generic | None | Inferred; code only |
| 12 | Cached-token field chain across hosts, including DeepSeek's `prompt_cache_hit_tokens` | packages/ai/src/protocols/openai-chat.ts:250-281 | Cached tokens are read only from `prompt_tokens_details.cached_tokens` (openai_chat.go:241-244). | Data row (field path) | None | Inferred; affects measurement only |
| 13 | Per-model prompt sentences on verifying and finishing | packages/core/src/plugin/system-prompt/meta.txt:146-149; gpt-astra.txt:102-104; kimi.txt:34; chosen in packages/core/src/plugin/optimize.ts:16-62 | Contract text is unmeasured (DSH gap table). aiwe has no per-model text. | A data row, and only after a probe | Prompt bytes | Inferred; no eval for any variant |
| 14 | Tiered edit matcher: exact match, then a typography fold, then a line match that ignores trailing whitespace. Exactly one match is still required. | packages/core/src/tool/plugin/edit.ts:49-125, 168-188 | Edit is exact-only (loop_io.go:236-238). SPEC fixes "single exact-match" (docs/SPEC-contracts.md:253). | Generic | CPU only | Inferred; tests packages/core/test/tool-edit.test.ts:485-672 |

### Where OpenCode and DSH agree or differ

| DSH item | Verdict | How |
|---|---|---|
| P1 reasoning carry, max_tokens, effort | Confirms, refines | OpenCode sends reasoning on every prior assistant message for DeepSeek-like rows, even an empty one (openai-chat.ts:558-574, 660-666). The field name comes from the row, else from the field the reply used. The max-tokens field name is a row value (model-compatibility.ts:31-32). OpenCode fits the output limit to the remaining window, with a floor of 1024 (packages/core/src/session/model-request.ts:89-97). aiwe's fixed ceiling already leaves 16,000 (200,000 − 184,000). So only a cap by the model's output limit is needed. |
| P2 fail-soft transport | Refines; differs on the empty reply | Classification uses ordered rules over status, body code and text, not status alone (provider-error.ts:246-330). OpenCode retries up to 10 times, 2-10 s per gap. Timeouts are capped at 3, and Retry-After at 15 min (A §2.2). The SPEC's 3 retries stay (DR-1). OpenCode has no empty-reply retry; an empty reply ends the turn (A §2.1; C3 M15). DSH's bounded retry stays, and P0 measures it. |
| P3 a tool surface that teaches | Confirms, refines | Error texts name the fix (edit.ts:233-242, 284-299; packages/core/src/tool/runtime.ts:88-100). The shell description names the OS and the shell (packages/core/src/tool/plugin/shell.ts:283-291). aiwe must instead say there is no shell. OpenCode marks only a non-zero exit (shell/result.ts:30-34). DSH's marker on every exit stays (DR-18 fixed key). |
| P4 recoverable truncation | Confirms, refines | Read pages at 2000 lines or 50 KiB, with a footer that names the next offset (read.ts:190, 203). Shell keeps the tail. Other results keep the head and get a saved copy with a pointer (packages/core/src/tool-output.ts:13-16). OpenCode keeps copies 7 days. aiwe removes them at settle (DR-11). |
| P5 wind-down | Refines | OpenCode has no notice at 90 %. At the cap it sends one text-only turn and keeps the tools for the cache (llm.ts:225-246). The plan uses both. |
| P6 probe-validated sentences | Confirms the need | Seven prompt variants exist, and none was evaluated (A §1.2, §6). The gpt and meta prompts give opposite delegation rules. So a stance can depend on the model, and only a probe can show it. |
| P7 context ceiling with stubs | Confirms the estimator; refines; differs | OpenCode's prompt estimate is the measured usage plus chars ÷ 4 of what follows (packages/core/src/session/compaction.ts:1102-1126). The SPEC §7.4 E has the same shape. An overflow before any output gets one recovery, then a resend (packages/core/src/session/runner/step.ts:155-167). The newest exchange is never cut (compaction.ts:1004-1026). OpenCode then writes a model summary. aiwe keeps stubs only, since a summary is a model call (DR-2). |
| P8 repeat-call reminder | No support | The v2 core has no doom-loop code (A §2.1; B §0). Only one prompt line exists (trinity.txt:86). |
| F1 in-run verify | Neutral | There is no verify tool and no post-edit diagnostics. Only TODOs exist (edit.ts:205; write.ts:44; packages/core/src/file-mutation.ts:131). Verification lives in prompts only. |
| F2 file-set guard | Confirms the shape | Plan mode denies edits outside one directory by rule. It rewrites the denial with a remedy (packages/core/src/plugin/plan.ts:14-25, 33-53). |
| F3 in-run return validation | Confirms the pattern | Compaction checks the summary template, nudges once, then fails (compaction.ts:239-240, 478-503). aiwe already plans `report_result` (docs/SPEC-contracts.md:274-276). |
| F4 loop signals into evidence | Confirms | The projector totals tokens, cost and retries per session (packages/core/src/session/projector.ts:43-64, 748-776). No success eval uses them. |
| F5 effort from evidence | Refines | Effort sets differ by model in the catalogue. Changing effort mid-run can break the cache, so OpenCode sends a per-message update where the model supports it (packages/ai/src/effort-updates.ts:1-25). The chat wire has no such update. A fix round at a new effort may therefore lose the cached prefix; P0 should measure that. |

### Per-model data rows

Source: packages/schema/src/model-compatibility.ts:25-55, read whole. Provider rows hold the same fields except the two model-version fields (lines 51-55).

| Field | Line | Effect | aiwe needs it now |
|---|---|---|---|
| `reasoningField` | 27-28 | Names the assistant field that carries reasoning. Unset means the field the reply used, else `reasoning_content`. | Yes, for the M1 carry |
| `requireReasoning` | 29-30 | Sends the reasoning field on every prior assistant message, even an empty one | Yes; OpenCode turns it on for DeepSeek ids (openai-chat.ts:660-666) |
| `maxTokensField` | 31-32 | Chooses `max_tokens` or `max_completion_tokens` | Yes, once M1 sends R |
| `requireFinishReason` | 33-34 | Fails a stream that ends without `finish_reason`. The default is true. | Only once streaming is coded |
| `requireAssistantAfterTool` | 35-36 | Inserts an assistant message between a tool result and the next user message | Only for a host that needs it, once M9 adds a user notice |
| `supportsPromptCacheKey` | 37-38 | Sends `prompt_cache_key` only where the endpoint accepts it | No; the bridge hit 0.941 without it |
| `streamUsage` | 39-40 | Asks for usage inside the stream | Only once streaming is coded |
| `cacheFormat` | 41-42 | Chooses the explicit cache-marker syntax (anthropic or alibaba) | No current host |
| `supportsThinkingBlockBinding` | 43-44 | Anthropic and Bedrock thinking-prefix control; model rows only | No; aiwe has no Anthropic wire |
| `supportsEffortUpdates` | 45-46 | Changes effort per message without rewriting the prefix; model rows only | No; the chat wire has none |
| `mediaInput` | 47-48 | Where a decision host reads media | No |

Catalogue fields (packages/core/src/models-dev/v1.ts:28-79). The snapshot holds 8787 models from 230 providers (C row 2):
- `reasoning.effort[]` and `reasoning.default` let DR-15 refuse an effort the model lacks. They also feed owner Q1.
  - [low, medium, high]: 727 models.
  - [low, medium, high, xhigh, max]: 595.
  - [low, high, max]: 388.
  - [none, high]: 229.
- `limit.output` caps R. `limit.context` lets the 200,000 ceiling become a measured row value (DR-3).
- `temperature` is false on 2044 models and `top_p` on 1855. aiwe sends neither, so no row is needed yet (DR-22).
- 6763 models list reasoning, 1441 a toggle and 604 a budget. `system_updates` is true on 819 and `effort_updates` on 53. No current aiwe host needs these.
- C3 also found three code-level chat fields: `supportsStore`, `supportsStrictMode` and `zaiToolStream` (openai-chat.ts:889-894, 910). No current aiwe host needs them.
- The cached-usage field path is not in the schema; it lives in code (openai-chat.ts:250-281). aiwe should hold it as a row.

### Merged proposals

Path key: [API] means the direct-API Go loop only. [API+CLI] means it also helps provider-CLI agents.

M0. Repeatable baseline bench. [API+CLI]
- Gap: there is no repeatable baseline, and no production Runner exists (internal/engine/dispatch.go:57-60).
- Change: P0 as written. Add per-run counts of finish reasons, failure classes, refusals by class and cap hits. The arms include low versus high effort (F5).
- Source: DSH P0 and F5. OpenCode adds nothing, because it has no evals.
- Verify: a dry run on 2 units prints one row per arm, with green, turns, tokens and cost, or unknown.
- Depends: none.

M1. Provider compatibility row. [API]
- Gap: internal/adapters/loop_io.go:31-45; internal/adapters/openai_chat.go:241-244.
- Change: one row per provider, overridable per model, holds the reasoning field, the require flag, the max-tokens field, the output limit and the cached-usage path. It stays in adapter source until the data file exists (docs/SPEC-contracts.md:261). The loop then carries reasoning on every prior assistant turn and sends R = min(§7.4 R, output limit). Effort comes from the candidate.
- Source: DSH P1. OpenCode C row 1, B2 N1, C3 M6 and M11, A M9.
- Verify: `go test ./internal/adapters/` shows three things.
  - Turn 2's body carries turn 1's reasoning field.
  - Every body carries the row's max-tokens field.
  - Another cached-usage field yields a known Cached count.
  - The new rows are red on base. On P0, compare the `final_answer` share and the cache hit.
- Depends: M0.

M2. Failure classes and bounded retry. [API]
- Gap: loop_io.go:96-98, 114-116.
- Change: classify every failure in this order: overflow, too large, content policy, quota, auth, rate limit, provider internal, invalid request, unknown.
  - Status, body code and the `errors` rows decide the class.
  - Only rate limit, provider internal, network, timeout and unknown follow the SPEC retry rule.
  - A 2xx body holding an error object is classified the same way. An empty reply is retryable.
  - The retry wraps only the HTTP request.
- Source: DSH P2. OpenCode C3 P1-P5 and M5, B2 N4.
- Verify: tests check these cases. Each new row is red on base.
  - 503 then 200 ends `final_answer`.
  - A 400 carrying a server code is not retried.
  - A 429 with quota text is not retried.
  - A 200 holding an error object is retried.
  - An empty reply, then an answer, ends `final_answer`.
  - On P0, `environment` outcomes fall.
- Depends: M1.

M3. Finish reasons and named outcomes. [API]
- Gap: loop_io.go:121-126; loop.go:177-178; internal/engine/dispatch.go:256-266.
- Change: one table maps finish reasons.
  - `length` gives `answer_truncated`, with the usage of the completed turns.
  - `content_filter` gives a named outcome that U2 adds to the SPEC.
  - `stop` with calls runs the calls. `network_error` follows the M2 class. An unknown reason is kept raw.
  - providerStop accepts `rate_window` and the new outcome.
- Source: DSH P2. OpenCode C3 M3 and M4, C2 N4, A M1.
- Verify: one test row per reason gives its named outcome. TestLoopRefusesAReplyThatAnsweredNothing is replaced, with a Behavior change line (DR-12).
- Depends: M2.

M4. Tool definitions that teach. [API]
- Gap: internal/adapters/tools.go:236-276; openai_chat.go:164-167.
- Change: each tool and each parameter gets one description, with the path first. The shell description says it runs one program with arguments, with no shell. It names the OS from the runtime (DR-3).
- Source: DSH P3. OpenCode B M16 and the A §1 tool guidance.
- Verify: a TestTools row shows a description for every tool and parameter, in a fixed order. TestLoopKeepsTheStablePrefixAcrossTurns stays ok.
- Depends: M1, because both change openai_chat.go.

M5. Remedy texts and markers. [API]
- Gap: tools.go:69, 83, 215-221, 228; loop_io.go:229-237, 303-305; loop.go:190-203.
- Change: every refusal starts with a fixed error marker and ends with its remedy.
  - Bad JSON says the call was not run. It gives a bounded excerpt.
  - A disallowed or unknown tool names the offered tools and says to continue without it.
  - A missing file lists up to 3 similar names.
  - An empty result gets a fixed marker.
  - Shell ends with `[exit code: N]` on every exit.
- Source: DSH P3. OpenCode B M2, M11 and M23, C rows 12 and 13, publish-llm-event.ts:43-62.
- Verify: one row per refusal class, plus exit 0 and non-0, each red on base.
- Depends: M3, M4.

M6. Read window and binary guard. [API]
- Gap: loop_io.go:183-202; tools.go:22-25.
- Change: read takes offset and limit, and a footer names the next offset. A page stays within the 32 KiB SPEC cap.
  - Long lines are cut with a marker.
  - A file with a NUL byte is refused with a remedy.
  - An out-of-range offset is named.
- Source: DSH P4. OpenCode B M10, B2 N23.
- Verify: test rows for the footer, the line cut, the NUL refusal and the out-of-range offset. On P0, count `read_too_large` refusals and re-reads per run.
- Depends: M5, U2.

M7. Spill with a saved copy. [API]
- Gap: loop_io.go:299-301.
- Change: output over 32 KiB follows owner Q2. The recommended shape is head plus tail, then "N bytes omitted; full copy at <run-relative path>". The copy sits in the run-owned area and is removed at settle.
- Source: DSH P4. OpenCode B M13 and M14.
- Verify: test rows show head plus tail, a pointer readable through M6, a pointer outside the run refused, and the copy removed. TestLoopRefusesShellOutputOverTheCap is replaced.
- Depends: M6 and Q2.

M8. Shell per-call time limit. [API]
- Gap: loop_io.go:283-307.
- Change: each shell call gets a limit from the §7.4 limits derivation (DR-1). At the limit the process tree stops (§5.20). The result names the limit and a remedy: narrow the command.
- Source: new, from OpenCode B M14 (packages/core/src/tool/plugin/shell.ts:24, 230).
- Verify: with a short limit, a test command past it returns the notice. The run continues, and no child remains.
- Depends: M5, U2.

M9. Wind-down notice and a text-only last turn. [API]
- Gap: loop.go:103-105.
- Change: at 90 % of the turn or time limit, one user-role notice is sent (DSH).
  - The last turn under the cap sends tool_choice none with the same tool list. It asks for the §9.3 return.
  - A tool call in that turn is not run.
  - The outcome stays `turn_limit`, with the return attached.
- Source: DSH P5. OpenCode A M3.
- Verify: test rows show the notice once, at ceil(0.9 × cap). The last body has tool_choice none and unchanged tools. A call in that turn is not run. On P0, measure the share of capped runs with a parsable return.
- Depends: M3.

M10. Probe-validated contract sentences. [API+CLI]
- Gap: the contract text is unmeasured (DSH gap table).
- Change: use the DSH P6 method. Add OpenCode candidates: meta.txt:146-149, gpt-astra.txt:102-104 and kimi.txt:34. A sentence whose effect differs by model becomes a model data row, never a model branch (DR-3).
- Source: DSH P6. OpenCode A §1.2.
- Verify: counts per arm, a Fisher p and per-model results, on 2 models.
- Depends: M0.

M11. Context ceiling with stubs and one overflow recovery. [API]
- Gap: SPEC §7.4 is not coded in the adapters.
- Change: P7, plus one addition. An M2 overflow before any output triggers one stub batch and one resend. A second overflow ends `token_limit`. The newest exchange is never stubbed.
- Source: DSH P7. OpenCode A §2.3 (step.ts:155-167), compaction.ts:1004-1026.
- Verify: test rows for a stub batch at 150k, one recovery on overflow, and `token_limit` on the second overflow.
- Depends: M2, M7.

M12. In-run verify feedback. [API+CLI]
- Gap: the Go loop has no verify tool (DSH gap table).
- Change: DSH F1, as decided in owner Q3.
- Source: DSH F1. OpenCode offers no counterpart.
- Verify: the S10 rows: no-argument enforcement, the call limit and the rendering.
- Depends: U2, M5.

M13. File-set guard at write time. [API]
- Gap: write and edit accept any path inside the clone (tools.go:160-189).
- Change: a write or edit outside the unit's file set is refused in-run. The remedy names the set. DR-9 still checks the diff after the run.
- Source: DSH F2. OpenCode B M20 (plan.ts:33-53).
- Verify: test rows show a write inside the set is ok, and one outside returns the refusal. On P0, track the `scope_mismatch` rate.
- Depends: M5.

M14. Loop signals in the run report. [API]
- Gap: RunReport holds served model, usage, cost, outcome and turns only (loop.go:210-218).
- Change: add counts of refusals by class, retries, finish reasons, truncations and wind-down hits. A count the loop could not observe stays unknown (DR-6).
- Source: DSH F4. OpenCode C2 C9 (projector.ts:43-64).
- Verify: a run with 1 retry and 2 refusals reports exactly those counts.
- Depends: M3, M5.

M15. Repeat-call reminder. [API]
- Gap: none is coded. The bridge shows 2 of 457 runs.
- Change: DSH P8 as written.
- Source: DSH P8. OpenCode does not support it.
- Verify: the 3rd identical call gets one advisory line.
- Depends: M5.

### Rejected or deferred

- Repairing final tool-argument JSON: OpenCode itself rejects malformed final arguments without repair (packages/ai/src/protocols/utils/tool-stream.ts:203-204). Its repair chain feeds only streaming previews. M5's remedy text covers the case.
- Schema-driven argument coercion (packages/core/src/plugin/tool-input-repair.ts): every aiwe parameter is a string (tools.go:258-264), so it is unnecessary (DR-22).
- Tiered edit matcher: deferred. The SPEC fixes single exact-match. It returns only if P0 shows "text 0 times" as a leading refusal class, and then needs an owner SPEC decision.
- Patch tool chosen by model id (packages/core/src/tool/plugin/patch.ts:244-259): it adds a second edit format and a tool. It keys code on a model-id substring, which DR-3 forbids.
- In-run continuation after a `length` cut (llm.ts:37-38, 330-337): the SPEC names `answer_truncated`, and the bridge shows 0 `length` finishes in 457 runs. It returns if P0 shows any.
- Stream-only mechanisms: mid-stream resume, the tool-delta binder, the terminal-event contract and the keepalive filter. aiwe reads whole, non-streamed replies (loop_io.go:99). They return once the SPEC streaming line is coded.
- Model-written summary, background pre-compute and the template nudge: each is a model call (DR-2). Units are small, with a median of 25 turns.
- Tool-call id normalisation and cross-model replay rules: a fix round keeps its route literally (docs/SPEC-contracts.md:272-273). So ids never cross providers.
- AGENTS.md discovery, lazy instruction injection and instruction deltas: these conflict with the lean run (docs/SPEC-contracts.md:260).
- Subagents, question tool, permission prompts, plan mode, todo, Code Mode, MCP catalogue, web tools, image resize, cache warming, model identity block, verbosity and title model: each is interactive, adds a tool or a runtime (DR-21), or has no success effect.
- Shell command scanner: aiwe runs no shell (§5.20). The scanner is 2409 lines plus oracle tests.
- Post-write formatter: the result would differ from what the model wrote. The engine's gates own style.
- Provider quirks for hosts aiwe does not serve (Copilot, ChatGPT sign-in, Azure, Bedrock, Vertex, local discovery): each becomes a row when that provider is added (DR-3).
- OpenCode's retry counts (10 retries, Retry-After up to 15 min): the SPEC's 3 retries and `rate_window` stay (DR-1).
- Thinking-budget fitting and per-message effort updates: aiwe has no budget-style host, and its chat wire has no update.

### Owner questions

Q1. Default effort for a candidate that names none.
- Recommendation: (a), with the default the row documents. The DeepSeek v4-pro catalogue default is `high` (C §2). Measure it against the lowest level on P0 before it is final.
- (a) Start at the row's documented default. A second red moves up one level where the row lists one.
- (b) Start at the lowest level the row lists, and rely on §8.1 point 9 escalation.
- (c) Set the start per role from P0 results only.

Q2. Truncation policy for oversized outputs.
- Recommendation: (b). It is one rule for every tool, and it keeps both ends.
- (a) Keep refusing the whole result, as today.
- (b) Head plus tail inline, a full copy in the run-owned area, and a pointer the read tool can open.
- (c) Head plus tail inline, with no saved copy.
- (d) OpenCode's shape: tail for shell, head for other results, plus a saved copy. It splits the rule without evidence.

Q3. In-run verify for the Go loop.
- Recommendation: (a), with the bridge's limit of 3. OpenCode gives no evidence for or against it.
- (a) A no-argument verify tool with a per-run call limit and the compact red rendering.
- (b) No tool: a red result reaches the agent only as a fix round.
- (c) A tool with no call limit, relying on the cache key.

### Units

Epic: harness-quality learnings from the DSH and OpenCode studies, for the model-API loop and the prompt contract.
Every adapter path below sits under internal/adapters/ unless it is shown in full. Each file list counts tests and stays at 5 files or fewer.

U0 - Baseline corpus (analyst, read-only) [replaces S0; M0]
  Done: a list of N closed units, each with a red proof on its base and a green verify on its tip.
  Files: none written.
  Todo:
  - select the units from the ledger and the bridge evidence — verify: list count and source per row → N rows, 0 rows without a red proof
  - add peak prompt tokens, finish reasons and HTTP classes to the bridge aggregate — verify: the aggregate re-run → numbers printed with their basis, unknown where the logs lack them

U1 - Bench driver (test-author, engineer, security-reviewer) [replaces S0b; M0]
  Done: one command runs arm × unit × k over adapters.Loop and prints the §8.1 point 4 metrics plus the M0 counts.
  Files: the architect picks them, at most 5, inside the internal/adapters tests or one registry command (DR-16).
  Todo:
  - test-author: a fake-endpoint test pins the metric roll-up and unknown-not-0 — verify: go test ./internal/adapters/ -count=1 → new row red on base
  - engineer: the driver — verify: dry run on 2 units → one row per arm with green, turns, tokens, cost or unknown
  - security-reviewer: the key is read from env only (DR-19) — verify: the finding list → reproduced or not_measured

U2 - SPEC lines (doc-writer) [replaces S0c; M1-M9, M12]
  Done: docs/SPEC-contracts.md §10.2 names each item below. docs/SPEC.md §7.4 names the notice, the last turn and the shell limit row.
  - The row fields from the per-model table.
  - The failure classes and the empty-reply retry.
  - The finish table, with the content-filter outcome.
  - Truncation, per Q2.
  Files: docs/SPEC.md, docs/SPEC-contracts.md.
  Todo:
  - write the lines after Q1-Q3 are answered — verify: the project's document gate → green

U3 - Compatibility row in the loop wire (engineer) [replaces S1 in part; M1] [after U1, U2]
  Done: turn 2 carries turn 1's reasoning field. Every body carries the row's max-tokens field with R. Cached usage is read by the row's path.
  Files: loop_io.go, loop.go, openai_chat.go, loop_io_test.go, openai_chat_test.go.
  Todo:
  - test rows: reasoning echoed, also when empty; the max-tokens field named; R capped by the output limit; a cached path that is not the default — verify: go test ./internal/adapters/ -count=1 → new rows red on base
  - code — verify: same → ok; TestLoopKeepsTheStablePrefixAcrossTurns still ok

U4 - The OpenCode Go row (engineer) [replaces S1 in part; M1] [after U3]
  Done: the OpenCode Go loop config carries its row, and every value cites a §17 fact.
  Files: opencode_go.go, opencode_go_test.go.
  Todo:
  - test row: loopConfig carries the row values — verify: go test ./internal/adapters/ -run TestOpenCodeGo -count=1 → ok, new row red on base

U5 - Failure classes and retry (engineer) [replaces S2 in part; M2] [after U2, U3]
  Done: each M2 case gives its named result. A retry never resends after a reply that ran tools.
  Files: loop_io.go, loop.go, loop_io_test.go, loop_test.go.
  Todo:
  - test rows per M2 case — verify: go test ./internal/adapters/ -count=1 → new rows red on base
  - code — verify: same → ok

U6 - Finish reasons and outcomes (engineer) [replaces S2 in part; M3] [after U5]
  Done: each finish reason gives its outcome. providerStop accepts `rate_window` and the new outcome.
  Files: loop_io.go, loop.go, loop_test.go, internal/engine/dispatch.go, internal/engine/dispatch_test.go.
  Todo:
  - test rows per reason, and for providerStop — verify: go test ./internal/adapters/ ./internal/engine/ -count=1 → new rows red on base
  - code — verify: same → ok; TestLoopRefusesAReplyThatAnsweredNothing replaced, with a Behavior change line (DR-12)

U7 - Tool definitions (engineer) [replaces S3; M4] [after U3; can run with U5 and U6]
  Done: each tool and each parameter has a description, with the path first. The shell description says there is no shell and names the OS.
  Files: tools.go, openai_chat.go, tools_test.go.
  Todo:
  - test row: descriptions in a fixed order — verify: go test ./internal/adapters/ -run TestTools -count=1 → ok, new row red on base

U8 - Remedy texts and markers (engineer) [replaces S4; M5] [after U6, U7]
  Done: every refusal starts with the error marker and ends with its remedy. Empty results carry the marker. Shell ends with `[exit code: N]` on every exit.
  Files: tools.go, loop_io.go, loop.go, tools_test.go, loop_io_test.go.
  Todo:
  - test rows per refusal class, per empty result, and for exit 0 and non-0 — verify: go test ./internal/adapters/ -count=1 → new rows red on base
  - code — verify: same → ok

U9 - Read window and binary guard (engineer) [replaces S5 in part; M6] [after U2, U8]
  Done: read takes offset and limit with a footer. Long lines are cut. NUL files are refused. An out-of-range offset is named.
  Files: tools.go, loop_io.go, tools_test.go, loop_io_test.go.
  Todo:
  - test rows for each case — verify: go test ./internal/adapters/ -count=1 → new rows red on base
  - code — verify: same → ok; TestToolsCapARead updated, with a Behavior change line

U10 - Spill with saved copy (test-author, engineer, security-reviewer) [replaces S5 in part; M7] [after U9, Q2]
  Done: an oversized result follows Q2. The copy is readable only inside the run and is removed at settle.
  Files: tools.go, loop_io.go, tools_test.go, loop_io_test.go.
  Todo:
  - test-author: rows for head plus tail, the pointer read, a pointer outside the run refused, and the copy removed — verify: go test ./internal/adapters/ -count=1 → new rows red on base
  - engineer: code — verify: same → ok; TestLoopRefusesShellOutputOverTheCap replaced, with a Behavior change line
  - security-reviewer: a threat line on spill-path confinement — verify: the finding list → reproduced or not_measured

U11 - Shell per-call limit (engineer) [new; M8] [after U2, U10]
  Done: a shell call past its limit stops its tree and returns the notice. The run continues.
  Files: loop_io.go, loop.go, loop_io_test.go.
  Todo:
  - test row with a short limit — verify: go test ./internal/adapters/ -run TestLoop -count=1 → ok, new row red on base, no child left

U12 - Wind-down and text-only last turn (engineer) [replaces S6; M9] [after U11]
  Done: one notice comes at 90 %. The last turn under the cap sends tool_choice none and the same tools. A call in it is not run.
  Files: loop.go, loop_io.go, loop_test.go.
  Todo:
  - test rows for each part — verify: go test ./internal/adapters/ -run TestLoop -count=1 → ok, new rows red on base

U13 - Contract sentence probes (engineer, then auditor) [replaces S7; M10] [after U1; can run with U3-U12]
  Done: each candidate sentence has a stored probe result on 2 models. A sentence without an effect is not added.
  Files: internal/engine/contract.go, internal/engine/contract_test.go, plus the probe data at the U1 home.
  Todo:
  - probe the DSH P6 sentences and the OpenCode candidates — verify: probe output → counts per arm, Fisher p, per model
  - add only sentences with an effect — verify: go test ./internal/engine/ -run TestContract -count=1 → ok

U14 - Ceiling, stubs and overflow recovery (engineer) [replaces S8; M11] [after U5, U10, U12]
  Done: E follows §7.4. At 150k the oldest results become stubs with a pointer. One overflow recovery happens, then `token_limit`.
  Files: loop.go, loop_io.go, loop_test.go.
  Todo:
  - test rows for the stub batch, the recovery and the `token_limit` end — verify: go test ./internal/adapters/ -run TestLoop -count=1 → ok, new rows red on base

U15 - In-run verify (test-author, engineer, security-reviewer) [replaces S10; M12] [after U2, U8, Q3]
  Done: the Go loop offers a no-argument verify through a RunRequest callback, with the compact red rendering and a call limit.
  Files: provider.go, tools.go, loop.go, tools_test.go, loop_test.go.
  Todo:
  - test-author: rows for no-argument enforcement, the limit and the rendering — verify: go test ./internal/adapters/ -count=1 → new rows red on base
  - engineer: code — verify: same → ok
  - security-reviewer: the model cannot choose argv — verify: the finding list → reproduced or not_measured

U16 - File-set guard (test-author, engineer, security-reviewer) [new; M13] [after U8]
  Done: a write or edit outside the unit's file set is refused with its remedy.
  Files: provider.go, tools.go, loop.go, tools_test.go.
  Todo:
  - test-author: rows inside the set, outside it, and through a link — verify: go test ./internal/adapters/ -count=1 → new rows red on base
  - engineer: code — verify: same → ok
  - security-reviewer: no path form escapes the guard — verify: the finding list → reproduced or not_measured

U17 - Loop signals in the run report (engineer) [new; M14] [after U8]
  Done: RunReport carries the M14 counts, and a count the loop could not observe stays unknown.
  Files: provider.go, loop.go, loop_test.go.
  Todo:
  - test row: 1 retry and 2 refusals are reported exactly — verify: go test ./internal/adapters/ -run TestLoop -count=1 → ok, new row red on base

U18 - Repeat-call reminder (engineer) [replaces S9; M15] [after U8]
  Done: the 3rd identical call gets one advisory line.
  Files: loop.go, loop_test.go.
  Todo:
  - test row — verify: go test ./internal/adapters/ -run TestLoop -count=1 → ok, new row red on base

Parallel and dependencies:
- Can run together: U0, U1 and U2. U5 and U7. U13 beside any adapter unit.
- Must run in order: U3 → U4. U3 → U5 → U6 → U8 → U9 → U10 → U11 → U12 → U14. U3 → U7 → U8.
- After U8: U15, U16, U17 and U18 touch loop.go, so they run one at a time.

Old S to new unit:
- S0 → U0. S0b → U1. S0c → U2.
- S1 → U3 and U4. S2 → U5 and U6. S3 → U7. S4 → U8.
- S5 → U9 and U10. S6 → U12. S7 → U13. S8 → U14. S9 → U18. S10 → U15.
- New units: U11, U16 and U17.

Out of scope: everything under "Rejected or deferred", plus F3. `report_result` is already planned (docs/SPEC-contracts.md:274-276).

Drop order if time runs short: U18, U17, U14, U16, U13, U12.

Plan checks: 19 units. 3 owner questions. Criteria: 15 received, 15 placed.

## Final plan (owner review, 2026-10-29)

Source: a re-read of the session transcript, from the owner's DSH task to the last owner message, after a context compaction. Every owner statement and every settled decision is listed with the place that now owns it. This section supersedes the status line of this file where they differ.

### Owner statements and where each lands

| # | Owner statement (paraphrased, 2026-10-28/29) | Lands in |
|---|---|---|
| 1 | Learn from DSH's code why DSH with DeepSeek v4.1 flash scores higher; adopt it, or an adapted form, as a systemic gain for every provider, agent and model, not for one model; find further ideas from the same logic and consult. | Sections 1-6 and the merged plan (Track A) |
| 2 | Scan the DSH repository from its code, in full and in order, not by sampling. | Coverage ledgers of the six passes (header of this file) |
| 3 | Record the DSH plan, do not apply it yet; study OpenCode the same way and merge what is worth taking. | "OpenCode study and the merged plan" |
| 4 | The OpenCode proposals are suitable; record the work in a file, open the issues at once, set the right priority. | Track A filing below |
| 5 | Livvmonitor audit of DSH work: about half of ~110 closes were wrong (11) or incomplete (~30). aiwe must prevent this as mechanically as possible, measuring only the real change, with nothing out of scope; it must find untested items, endpoints and functions by itself, then tell the lead or start a role agent; the agent waits for an outside check before it returns and fixes in the same context; lead-agent round trips stay minimal. | Track B |
| 6 | "Your proposal is fine": file the test-system items under #587. | Track B filing below |
| 7 | Can the separate audit become mechanical? Should tests be written with the issue so the agent has a ready test to turn green? Owner on the answer: "your proposal on tests is fine too". | Track B items B6, B7; decisions D3, D4 |
| 8 | Can slot management be mechanical, dynamic and fully automatic, residue cleanup included, with an agent only where needed? | Track C |
| 9 | Up to 10 agents in parallel; agents never run tests or gates, only the lead does. | `STATUS.md` "Live configuration" |

### Decisions

- D1. Q1 = (a), Q2 = (b), Q3 = (a): the owner accepted the OpenCode proposals and their recommendations (statement 4). Recorded as accepted recommendations; say so if any was meant otherwise.
- D2. DSH session log (`dsh_session_log`) is not adopted: it sends repository content off the machine.
- D3. The audit is not removed. A mechanical pre-audit runs first and the auditor judges only the residue; wrong behaviour (the livvmonitor "wrong" class) needs judgement.
- D4. Acceptance tests are written at dispatch time, not at issue creation, because the base moves in between (#594's base moved four units). The test author adds a stub of the interface so the red is an assertion, not a build failure.
- D5. A finding class seen a third time becomes a gate at the earliest stage (rule of Prevention Order). Seen this session: escape sequence turned into a raw character (3x) -> `gofmt -l` in the clone pre-commit hook, batched with the session-end hook edits.
- D6. Every wait of an agent on an outside check has a time limit (DR-1), since a wait past the prompt-cache lifetime re-reads the whole context.
- D7. #596: the caller passes the lease index k through `LaneRequest` (audit B1); lane lock files are never deleted, so the waiter needs no polling (audit B2, lead decision: fewest moving parts, reversible).

### Open, owner only

- O1. Which DSH profile produced the scores the owner saw (minimal or full)? It decides which arm U0 compares against. U0 also needs the owner to run DSH by hand as the comparison arm.

### Track A: harness loop (DSH + OpenCode), parent epic + U0-U18

Units, dependencies and done criteria: "Units" above. Priority is derived (impact x urgency, `aiwe-wt/issue.py`); severity is set so the foundation ranks first: U0, U1, U2, U3, U5, U6 high; U4, U7-U12, U14-U16 medium; U13, U17, U18 low. U15 (in-run verify) and B1 are one outcome and become one issue.

### Track B: test system, under #587

- B1. In-run verify: the agent calls `aiwe verify`, the engine runs the check and returns the result in the same call; the final report is refused until verify is green or the try limit (3) is spent. The auditor runs in its own run with a clean context (DR-5), and the writer can receive the verdict in the same bounded wait. A red result comes back compact: the failed checks, the `done` item each one serves, a one-line fix hint and the path of the full record; the agent fixes it in the same context, and a green result ends the run at once. Each unit records lead-agent round trips and first-pass green, so the gain is measured. Same outcome as U15 (one issue).
- B2. Changed-line coverage: the coverage profile is intersected with the diff hunks; a finding names `file:line-line` and the function no test ran.
- B3. New public surface inventory: every exported symbol, endpoint or route the diff adds, with no test that calls it, is a finding; the surface kinds are stack rows (DR-3).
- B4. Each `done` item maps to a recorded green check; an item with no evidence blocks the close. A diff that touches a stack row's `UIPaths` requires the UX check result.
- B5. Freshness scan: after a green full run, symbols and endpoints with no test become work items, and the dispatcher starts `test-author` for them; the lead sees only a decision.
- B6. Mechanical pre-audit report as the auditor's input. Its checks, each from a finding class seen this session: mutation on the changed lines (#66), a fuzz or empty-input row for every parser (an empty report read as clean, #609), a duplicate-constant scan (#592), the DR-6 empty-field rule (#592), raw escape characters through `gofmt -l` (D5), cognitive complexity, the deleted-test proof (L72), and the evidence summary matched against the raw step exits. Every audit finding carries a class tag and a counter; the third finding of a class opens the gate work (D5).
- B7. Acceptance tests at dispatch by `test-author`, with an interface stub (D4); the writer's diff to a locked test file is refused, and a test fix needs its own approval.
- B8. The ledger check record carries `failures[]` (gap left by #594).
- Related, owned elsewhere: a write outside the declared file set is refused during the run (U16, merged into #295); test weakening is caught by D2 (#24) and the L72 proof lines.

Existing issues to extend instead of duplicating: #254 (in-run verify), #24 (untested-code check), #66 (mutation on changed lines), #522, #597.

### Track C: slot operations, mechanical

Already filed: #526 (take: claim, scope lease and slot in one call), #527 (return: collect and release), #528, #531, #536 (refs that outlive their item), #521, #572, #573 (lease expiry and reclaim), #293 (measured capacity), #444, #480 (residue). One gap: a session-start sweep that finds dead leases and ownerless clones and cleans them; added to #444 as a to-do line. An agent enters only when the same run failure repeats (`debugger` for the root cause).

### Session-end batch

- `gofmt -l` in the clone pre-commit hook (D5).
- `vfy.sh` prints raw step exits next to its summary (audit fail-open note).
