package core

import "path"

// Module is one row of the module table: a package, its single responsibility, the packages it
// may import and the goals it serves. This table is the only copy (SPEC §6.1, SSOT); check
// repo/boundaries compares it with the real imports (DR-17).
type Module struct {
	Package        string
	Responsibility string
	Imports        []string
	Serves         string
}

// Modules returns the module table. Package and Imports are slash-separated paths relative to the
// module root; a package may import only the root packages it lists.
func Modules() []Module {
	const (
		core      = "internal/core"
		stacks    = "internal/stacks"
		gates     = "internal/gates"
		roles     = "internal/roles"
		adapters  = "internal/adapters"
		tracker   = "internal/tracker"
		ledger    = "internal/ledger"
		item      = "internal/item"
		engine    = "internal/engine"
		mcp       = "internal/mcp"
		repocheck = "internal/repocheck"
		hook      = "internal/hook"
		install   = "internal/install"
	)
	return []Module{
		{Package: core, Serves: "S1, M7", Responsibility: "Command contract, result and errors, params, " +
			"output, env, config, log, text, platform roots, capacity, process control, git calls, owned " +
			"removal, sweep, footprint, measured values, identities, sandbox port."},
		{Package: stacks, Serves: "S3", Imports: []string{core},
			Responsibility: "Stack profile table, detection, gate derivation."},
		{Package: gates, Serves: "S4, S6", Imports: []string{core},
			Responsibility: "Gate runner, check cache, diff checks D1–D18, payload scan."},
		{Package: roles, Serves: "S3, S7", Imports: []string{core, adapters},
			Responsibility: "Role cards (embedded), card schema, pinned model list."},
		{Package: adapters, Serves: "S1", Imports: []string{core},
			Responsibility: "Provider and harness adapters, adapter list, API agent loop."},
		{Package: tracker, Serves: "S5", Imports: []string{core},
			Responsibility: "Tracker port, the GitHub adapter (issues, CI status) and intake filtering."},
		{Package: ledger, Serves: "M1–M7", Imports: []string{core},
			Responsibility: "Append-only records, rotation, metrics."},
		{Package: item, Serves: "S5",
			Responsibility: "Work item record: Go type, value sets of type and status, schema version, stable id."},
		{Package: engine, Serves: "S2", Imports: []string{core, stacks, gates, roles, adapters, tracker, ledger},
			Responsibility: "Flows, run isolation, claims, sources, synthesis, prompt contract assembly."},
		{Package: mcp, Serves: "S7", Imports: []string{core, ledger},
			Responsibility: "MCP server generated from the command registry, with self-description."},
		{Package: repocheck, Serves: "S4", Imports: []string{core},
			Responsibility: "This repository's own checks (repo/*, commit message, CI changes and report). " +
				"Never shipped."},
		{Package: hook, Serves: "S2", Imports: []string{core},
			Responsibility: "The hook command of each harness event: reads the harness input and passes it on."},
		{Package: install, Serves: "S1", Imports: []string{core},
			Responsibility: "Harness installation: one adapter row per harness, hook entries merged into its " +
				"settings, drift repaired, foreign entries untouched."},
		{Package: path.Join("cmd", Product), Serves: "S7",
			Imports:        []string{core, stacks, gates, roles, adapters, tracker, ledger, engine, mcp, hook, install},
			Responsibility: "CLI and the mcp subcommand; wires the registry."},
		{Package: "internal/cmd/repocheck", Serves: "S4", Imports: []string{core, repocheck},
			Responsibility: "Entry point of repocheck."},
		{Package: "internal/e2e", Serves: "M2", Imports: []string{core},
			Responsibility: "End-to-end smoke tests (build tag smoke) that drive the built binary as a child " +
				"process. Never shipped."},
	}
}
