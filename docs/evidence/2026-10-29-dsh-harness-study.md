# DSH study -> aiwe proposal (2026-10-29, for discussion; nothing is built)

Status: plan only, recorded for later (owner, 2026-10-29: "note it, do not apply it now"). Nothing below is built or filed. Source: deepseek-ai/deepseek-harness at f3803c9; six read-only analyst passes with a per-file coverage ledger, then one architect synthesis.

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
