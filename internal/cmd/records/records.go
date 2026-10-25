// Command records generates the four project record files at the repository root: TODO.md from
// the open issues, ARCHIVE.md from the closed issues, DECISIONS.md from the SPEC §16 decision log
// and the automatic section of STATUS.md from git, the issues and the last CI run (the records
// section of the owner's global rules).
//
// INTERIM: this tool stands in until `aiwe item list`, `aiwe item ready`, `aiwe item archive`
// and `aiwe status` exist (SPEC L41, K32). It is deleted when they land; nothing may depend on it.
//
// Usage: go run ./internal/cmd/records [--write] todo|archive|decisions|status|all. A dry run
// prints the records; --write replaces the files at the repository root. A source that cannot be
// read ends the run with one `records: ...` line and writes nothing.
//
// Process and file IO sit in main_io.go; everything else takes its inputs through deps, so the
// tests run with a fake tracker and no network.
package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"
)

const (
	// recordsCmd is the command named in every generated marker line.
	recordsCmd = "go run ./internal/cmd/records"
	// rootGoal is G0, the issue whose sub-issue tree TODO.md lists first.
	rootGoal = 438
	// specPath holds the decision log.
	specPath = "docs/SPEC.md"
	// exitUsage is the exit code of a bad command line.
	exitUsage = 2
)

// runner starts one command and returns its standard output; an error carries the first stderr line.
type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// deps are the inputs of a generation: processes, repository files and the clock.
type deps struct {
	run runner
	// read returns a file of the repository root; the bool is false when the file does not exist.
	read func(name string) ([]byte, bool, error)
	now  func() time.Time
}

// record is one generated file.
type record struct {
	name string
	body []byte
}

// result is what a generation returns: the records and the warnings that did not stop it.
type result struct {
	recs     []record
	warnings []string
}

// targets maps a command word to the record files it generates, in write order.
func targets() map[string][]string {
	return map[string][]string{
		"todo":      {"TODO.md"},
		"archive":   {"ARCHIVE.md"},
		"decisions": {"DECISIONS.md"},
		"status":    {"STATUS.md"},
		"all":       {"TODO.md", "ARCHIVE.md", "DECISIONS.md", "STATUS.md"},
	}
}

// header returns the first line of a generated file: when, by what and from which source.
func header(now time.Time, source string) string {
	return fmt.Sprintf("<!-- generated %s by %s from %s; do not edit by hand -->\n",
		now.UTC().Format(time.RFC3339), recordsCmd, source)
}

// generate builds the records of target from the sources; nothing is written here, so one failed
// source leaves every file as it was.
func generate(ctx context.Context, d deps, target string) (result, error) {
	names, ok := targets()[target]
	if !ok {
		return result{}, fmt.Errorf("unknown target %q", target)
	}
	s := &session{d: d}
	if target != "decisions" {
		if _, err := s.call(ctx, "gh", "auth", "status"); err != nil {
			return result{}, err
		}
	}
	var recs []record
	for _, name := range names {
		body, err := s.build(ctx, name)
		if err != nil {
			return result{}, err
		}
		recs = append(recs, record{name: name, body: []byte(body)})
	}
	return result{recs: recs, warnings: s.warnings}, nil
}

// build returns the content of one record file.
func (s *session) build(ctx context.Context, name string) (string, error) {
	switch name {
	case "TODO.md":
		return s.todo(ctx)
	case "ARCHIVE.md":
		return s.archive(ctx)
	case "DECISIONS.md":
		return s.decisions()
	case "STATUS.md":
		return s.status(ctx)
	}
	return "", fmt.Errorf("no generator for %s", name)
}

// execute runs one command line and returns the exit code: 0 done, 1 a source or write failed,
// 2 a usage error. Errors print as one `records: ...` line.
func execute(ctx context.Context, args []string, d deps, write func(record) error, out, errw io.Writer) int {
	doWrite, target, err := parseArgs(args)
	if err != nil {
		say(errw, "records: %s\n", oneLine(err.Error()))
		return exitUsage
	}
	res, err := generate(ctx, d, target)
	if err != nil {
		say(errw, "records: %s\n", oneLine(err.Error()))
		return 1
	}
	for _, w := range res.warnings {
		say(errw, "records: warning: %s\n", oneLine(w))
	}
	for _, r := range res.recs {
		if !doWrite {
			say(out, "==> %s <==\n%s\n", r.name, r.body)
		} else if err := write(r); err != nil {
			say(errw, "records: write %s: %s\n", r.name, oneLine(err.Error()))
			return 1
		}
	}
	return 0
}

// parseArgs reads `[--write] <target>`.
func parseArgs(args []string) (doWrite bool, target string, err error) {
	var words []string
	for _, a := range args {
		switch {
		case a == "--write":
			doWrite = true
		case strings.HasPrefix(a, "--"):
			return false, "", fmt.Errorf("usage: unknown flag %s; use [--write] todo|archive|decisions|status|all", a)
		default:
			words = append(words, a)
		}
	}
	if len(words) != 1 || targets()[words[0]] == nil {
		return false, "", fmt.Errorf("usage: pass exactly one of %s",
			strings.Join(slices.Sorted(maps.Keys(targets())), "|"))
	}
	return doWrite, words[0], nil
}

// say writes one formatted line; a failed write to the terminal has nowhere to be reported.
func say(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

// oneLine folds a message to one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
