package gates

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// redScanMax bounds the bytes of one output line that a rule reads, so a hostile line cannot
	// make the table slow (STD-19).
	redScanMax = 4096
	// redShowMax is the most runes of the matched line that a verdict keeps.
	redShowMax = 200
	// redNamePlaceholder stands for a captured name in a subclass and in its hint.
	redNamePlaceholder = "<NAME>"
	// redUnknownSub is the subclass of a run that no rule matches.
	redUnknownSub = "unknown"
	// redUnknownHint is the hint of a run that no rule matches.
	redUnknownHint = "Read the log for the cause; a signature that repeats gets a row in the rule table."
	// redToolExit is the shell exit code of a command that was not found.
	redToolExit = 127
	// redToolLine is the line named for a run that ended with redToolExit and no matching line.
	redToolLine = "exit status 127"
)

// RedClass is the class of a red run (SPEC decision L64).
type RedClass string

// The classes a rule table returns; RedUnknown is the verdict of a run that no rule matches.
// flaky is not here: it is read from history, and FlakyTests returns test ids, not a class.
const (
	RedRealBug     RedClass = "real_bug"
	RedEnvironment RedClass = "environment"
	RedStale       RedClass = "stale"
	RedUnknown     RedClass = "unknown"
)

// RedRun is what the classifier reads of one red step: its id, its exit code and its output lines.
type RedRun struct {
	Step   string
	Exit   int
	Output []string
}

// RedVerdict is the class of a red run. Line is the output line that the winning rule matched,
// trimmed and shortened, and empty when no rule matched.
type RedVerdict struct {
	Step     string
	Class    RedClass
	Subclass string
	Line     string
	Hint     string
}

// redRule is one row of the table: a subclass, its default fix hint and the pattern of the line
// that signals it. A subclass that holds redNamePlaceholder takes the first capture group.
type redRule struct {
	subclass string
	hint     string
	re       *regexp.Regexp
}

// redEnvName is the shape of an environment variable name that a subclass may carry. It keeps a
// line break or a control character out of a hint.
var redEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func newRule(subclass, hint, pattern string) redRule {
	return redRule{subclass: subclass, hint: hint, re: regexp.MustCompile(pattern)}
}

// redRules returns the rule table; the repository allows no package-level table (repocheck
// package_var), so each caller builds it once per use. A rule decides before the rules after it:
// the cause that sits outside the code (environment, stale) comes before the failure it can
// produce, and a specific real_bug signature comes before the plain assertion line.
func redRules() []redRule {
	return []redRule{
		newRule("environment.workdir", "Run the command from the directory that holds the file the log names, or restore the file.",
			`go\.mod file not found|configuration file not found`),
		newRule("environment.container-missing", "Start the container that the row declares.",
			`(?i)cannot connect to the docker daemon|no such container|is the docker daemon running`),
		newRule("environment.permission", "Give the process the permission that the log names.",
			`(?i)permission denied|access is denied|read the ACL of|\bEACCES\b`),
		newRule("environment.db-missing", "Start the database, or run the project's setup script.",
			`(?i)\bdatabase\b.{0,80}\bdoes not exist|unknown database`),
		newRule("environment.port-busy", "Free the port, or end the process that holds it.",
			`(?i)address already in use|\bEADDRINUSE\b`),
		newRule("environment.network", "Restore the network and run again.",
			`(?i)no such host|connection refused|network is unreachable|i/o timeout|TLS handshake timeout|could not resolve host|\bECONNREFUSED\b|\bENOTFOUND\b`),
		newRule("environment.missing_tool", "Install the tool that the log names and run again.",
			`(?i)executable file not found|command not found|is not recognized as an internal or external command`),
		newRule("environment.disk", "Free disk space and run again.",
			`(?i)no space left on device|\bENOSPC\b|disk quota exceeded`),
		newRule("environment.lock_wait", "Wait for the holder of the lock, or end it.",
			`(?i)index\.lock|database is locked|another git process seems to be running`),
		newRule("environment.memory", "Free memory, or run with fewer workers.",
			`(?i)out of memory|cannot allocate memory|\bENOMEM\b`),
		newRule("environment.missing-env:"+redNamePlaceholder, "Set the environment variable `"+redNamePlaceholder+"`.",
			`(?i:environment variable )([A-Z_][A-Z0-9_]*)(?i: (?:is |was )?(?:not set|required|missing|undefined|unset))`),
		newRule("stale.toolchain", "Install the toolchain version that the repository pins.",
			`(?i)requires go >= |go\.mod requires go|compiled with go\d|different version of go`),
		newRule("stale.build_cache", "Clear the build cache and run again.",
			`(?i)build cache is (?:corrupt|trimmed)|stale object|object is stale|export data is out of date`),
		newRule("stale.generated", "Regenerate the generated files.",
			"(?i)generated\\b.*\\b(?:out of date|stale)|run .?go generate"),
		newRule("stale.build-output", "Rebuild the output and run again.",
			`(?i)could not find a production build|(?:\.next|dist|build)[/\\].*(?:no such file|ENOENT)`),
		newRule("stale.cache", "Clear the cache that the log names and run again.",
			`(?i)bad magic number|(?:corrupt|stale|invalid) cache|cache (?:is )?(?:corrupt|stale|invalid)`),
		newRule("real_bug.timeout", "A single test over its own limit: find the wait that does not end; raise the limit only with a measured reason.",
			`(?i)test timed out after|exceeded timeout of \d+ ?ms for a test|test timeout of \d+ ?ms exceeded`),
		newRule("real_bug.race", "Guard the shared data that the race report names.",
			`WARNING: DATA RACE`),
		newRule("real_bug.panic", "Fix the nil value, index or conversion at `sourceLocation`.",
			`^panic: `),
		newRule("real_bug.compile", "Fix the compiler error at its file:line:col.",
			`\.go:\d+:\d+: .*(?:undefined:|syntax error|cannot use|declared and not used|imported and not used|missing return|not enough arguments|too many arguments)`),
		newRule("real_bug.build", "Fix the build error and run the build step alone.",
			`\[build failed\]`),
		newRule("real_bug.setup", "Fix the test setup or fixture that the failure names.",
			`\[setup failed\]|^ERROR collecting |^ERROR at setup of `),
		newRule("real_bug.no_tests", "Add the test or fix the selection; a run with no test is red (DR-6).",
			`(?i)no tests to run|no tests ran|collected 0 items`),
		newRule("real_bug.finding", "Fix the finding at its file:line, or remove the dead code it names.",
			`unreachable func:|^\S+\.go:\d+:\d+: .*\([a-z][a-z0-9_-]*\)$`),
		newRule("real_bug.assertion", "Fix the product code at `sourceLocation`, or declare the behaviour change in the test and the commit body.",
			`^--- FAIL|AssertionError|^FAILED |^Error Trace:|^FAIL\b`),
	}
}

// ClassifyRed returns the class of a red run from the rule table and never from a rerun
// (SPEC decision L64). The first rule, in table order, that matches a line decides, and the
// verdict names the first line it matched. A run that no rule matches is unknown, except a
// run that ended with exit 127, which is a tool that was not found. A pure function.
func ClassifyRed(run RedRun) RedVerdict {
	for _, r := range redRules() {
		for _, raw := range run.Output {
			line := strings.TrimSpace(clipBytes(raw, redScanMax))
			m := r.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			sub, hint := r.subclass, r.hint
			if strings.Contains(sub, redNamePlaceholder) && len(m) > 1 {
				sub = strings.Replace(sub, redNamePlaceholder, m[1], 1)
				hint = strings.Replace(hint, redNamePlaceholder, m[1], 1)
			}
			return verdict(run.Step, sub, hint, line)
		}
	}
	if run.Exit == redToolExit {
		sub, hint, _ := lookupSubclass("environment.missing_tool")
		return verdict(run.Step, sub, hint, redToolLine)
	}
	return RedVerdict{Step: run.Step, Class: RedUnknown, Subclass: redUnknownSub, Hint: redUnknownHint}
}

func verdict(step, sub, hint, line string) RedVerdict {
	class, _, _ := strings.Cut(sub, ".")
	return RedVerdict{Step: step, Class: RedClass(class), Subclass: sub, Line: showLine(line), Hint: hint}
}

// clipBytes cuts s to at most n bytes.
func clipBytes(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// showLine makes a matched line one short printable line: valid UTF-8, no control characters,
// at most redShowMax runes.
func showLine(s string) string {
	s = strings.ToValidUTF8(s, "?")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= redShowMax {
		return s
	}
	return string([]rune(s)[:redShowMax])
}

// RedSubclasses lists the subclasses of the table in table order. The subclass that takes a
// captured name is listed with redNamePlaceholder in place of the name.
func RedSubclasses() []string {
	rules := redRules()
	subs := make([]string, len(rules))
	for i, r := range rules {
		subs[i] = r.subclass
	}
	return subs
}

// RedFixHint returns the default one-line fix hint of a subclass, and false for a subclass the
// table does not hold. A subclass with a captured name, such as environment.missing-env:PORT,
// gets its hint with the name filled in.
func RedFixHint(subclass string) (string, bool) {
	_, hint, ok := lookupSubclass(subclass)
	return hint, ok
}

// lookupSubclass finds a subclass, or a named subclass with its name filled in, in the table.
func lookupSubclass(subclass string) (sub, hint string, ok bool) {
	for _, r := range redRules() {
		if r.subclass == subclass {
			return r.subclass, r.hint, true
		}
		if head, found := strings.CutSuffix(r.subclass, redNamePlaceholder); found {
			if name, has := strings.CutPrefix(subclass, head); has && redEnvName.MatchString(name) {
				return subclass, strings.Replace(r.hint, redNamePlaceholder, name, 1), true
			}
		}
	}
	return "", "", false
}

// TestRun is one recorded result of one test on one tree: the input of the flaky decision.
type TestRun struct {
	TreeHash string
	TestID   string
	Passed   bool
}

// FlakyTests returns, sorted and without repeats, the ids of the tests that have a green and a
// red record on the same tree hash (SPEC decision L64). A record with no tree hash or no test id
// proves nothing and is skipped. A green on one tree and a red on another is a change, not
// flakiness. A pure function; history is not modified.
func FlakyTests(history []TestRun) []string {
	const (
		sawGreen = 1
		sawRed   = 2
	)
	type key struct{ tree, test string }
	seen := map[key]int{}
	for _, r := range history {
		if r.TreeHash == "" || r.TestID == "" {
			continue
		}
		bit := sawRed
		if r.Passed {
			bit = sawGreen
		}
		seen[key{r.TreeHash, r.TestID}] |= bit
	}
	var flaky []string
	for k, bits := range seen {
		if bits == sawGreen|sawRed {
			flaky = append(flaky, k.test)
		}
	}
	slices.Sort(flaky)
	return slices.Compact(flaky)
}
