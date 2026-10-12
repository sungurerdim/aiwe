package core

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestStateRootLocation catches a state root of a file-system root, of the user's home, of a
// parent of the home or of a canonical repository that OpenOwner accepts, or refuses with a next
// step other than choosing another state root through its override (#224). A non-root user told
// to remove the access of every other user from "/" cannot, and should not, do it.
func TestStateRootLocation(t *testing.T) {
	self := sweepTestSelf(t)
	fsRoot := func(r OwnedRoots) string {
		if runtime.GOOS == "windows" {
			return filepath.VolumeName(r.Temp) + `\`
		}
		return "/"
	}
	for _, c := range []struct {
		name  string
		state func(t *testing.T, r *OwnedRoots) string
	}{
		{"a file-system root", func(_ *testing.T, r *OwnedRoots) string { return fsRoot(*r) }},
		{"the user's home", func(_ *testing.T, r *OwnedRoots) string { return r.Home }},
		{"a parent of the user's home", func(t *testing.T, r *OwnedRoots) string {
			r.Home = filepath.Join(r.Home, "zeynep")
			removeTestRootDir(t, r.Home)
			return filepath.Dir(r.Home)
		}},
		{"a canonical repository holding .git", func(t *testing.T, r *OwnedRoots) string {
			removeTestRootDir(t, filepath.Join(r.Canonical[0], ".git"))
			return r.Canonical[0]
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := removeTestRoots(t, t.TempDir())
			r.State = c.state(t, &r)
			o, err := OpenOwner(r, self)
			if !errors.Is(err, ErrRootUnsafe) || ErrorResult(err).Outcome != "root_unsafe" {
				t.Fatalf("OpenOwner with state root %s (%s): owner %t, error %v; want %v", r.State, c.name, o != nil, err, ErrRootUnsafe)
			}
			d := ErrorResult(err).Detail
			_, next, ok := strings.Cut(d, "; next: ")
			if !ok || !strings.Contains(next, "state root") || !strings.Contains(next, productEnv("HOME")) ||
				strings.Contains(next, "delete") || strings.Contains(next, "access") {
				t.Errorf("refusal detail %q: want a next step that chooses another state root through %s, not one that deletes it or changes its access", d, productEnv("HOME"))
			}
		})
	}
}
