// Package stacks holds the stack profiles and the role path tables of SPEC §8.2: data rows a stack
// detection, a restore and a gate derivation read (SPEC §5.1: a new stack is a row). A row is plain
// data, so the package imports nothing beyond core (SPEC §6.1).
package stacks

// Profile is one stack profile of SPEC §8.2: the cells the engine reads for a repository of that
// stack. An empty cell is not measured for the stack, never a default a detection made up (DR-6).
type Profile struct {
	ID            string   // the stack id
	Detect        []string // the files whose presence detects the stack
	Restore       []string // the argv that restores the stack's dependencies in a run clone (SPEC §6.5)
	Gate          []Step   // the gate steps the stack derives (SPEC §8.3)
	TestFiles     string   // the glob of the stack's test files
	SourceExt     []string // the file extensions of its source files
	LockFiles     []string // the lock files whose digest keys a check (SPEC §7.8)
	Complexity    string   // the tool of SPEC §5.17 that measures its complexity
	SecurityPaths []string // its paths that add the security-reviewer role (SPEC §8.2, DR-15)
	UIPaths       []string // its paths that add the ux-reviewer role (SPEC §8.2, DR-15)
	UIRunner      string   // its UI runner, empty when it has none
}

// Step is one gate step a profile derives: its id, its argv, and the output pattern a green run of
// it must not print (SPEC §8.2, §8.3). Command is argv, never a shell string.
type Step struct {
	ID            string
	Command       []string
	OutputRejects string
}

// Profiles returns the stack profile table (SPEC §8.2). The skeleton ships the Go row, whose gate
// steps are the ones §8.2 names; another stack is another row (SPEC §5.1).
func Profiles() []Profile {
	return []Profile{
		{
			ID:      "go",
			Detect:  []string{"go.mod"},
			Restore: []string{"go", "mod", "download"},
			Gate: []Step{
				{ID: "vet", Command: []string{"go", "vet", "./..."}},
				{ID: "format", Command: []string{"gofmt", "-l", "."}, OutputRejects: `\S`},
				{ID: "test", Command: []string{"go", "test", "./..."}},
			},
			TestFiles:     "*_test.go",
			SourceExt:     []string{".go"},
			LockFiles:     []string{"go.sum"},
			Complexity:    "gocognit",
			SecurityPaths: []string{"*handler*.go"},
		},
	}
}

// ProfileByID returns the profile of id and whether the table holds one. A stack the table does not
// hold is unknown, and its cells read not_measured (SPEC §8.2, DR-6).
func ProfileByID(id string) (Profile, bool) {
	for _, p := range Profiles() {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// SecuritySegments returns the path segments that add the security-reviewer role (default, SPEC
// §8.2, DR-15): a diff path whose segment contains one of them, or whose base name matches the
// stack's SecurityPaths, needs the security review of that unit.
func SecuritySegments() []string {
	return []string{"auth", "login", "session", "token", "secret", "credential", "password",
		"crypto", "permission", "oauth", "webhook"}
}
