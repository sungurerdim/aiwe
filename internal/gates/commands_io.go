package gates

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sungurerdim/aiwe/internal/core"
)

// engineOwner opens the engine owner under the resolved roots, with repo as the canonical
// repository it must keep out of reach (SPEC §6.4, §5.15). Every check runs in roots this owner
// makes, so a gate with no owner refuses: an unsafe or unresolved root keeps its named outcome.
func engineOwner(repo string) (*core.Owner, error) {
	roots, err := core.UserRoots()
	if err != nil {
		return nil, ownerRefusal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ownerRefusal(fmt.Errorf("%w: home: %w", core.ErrRootUnsafe, err))
	}
	self, err := core.CurrentProcess()
	if err != nil {
		return nil, ownerRefusal(err)
	}
	owner, err := core.OpenOwner(core.OwnedRoots{State: roots.State, Temp: roots.Temp, Cache: roots.Cache,
		Home: home, Canonical: []string{repo}}, self)
	if err != nil {
		return nil, ownerRefusal(err)
	}
	return owner, nil
}

// readDefinition reads and parses gate.json at the repository root. A missing file is an error,
// never a green gate (DR-6). The read goes through an os.Root: a gate.json symlink or junction that
// leads out of repo is refused. A hardlink to a file outside repo, on the same volume, is read; the
// root cannot see it (SPEC §5.6).
func readDefinition(repo string) (Definition, error) {
	path, err := core.ResolvePath(repo, "gate.json")
	if err != nil {
		return Definition{}, err
	}
	data, err := readInRoot(repo, "gate.json")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Definition{}, &core.Error{Outcome: core.OutcomeError, Expected: "a gate.json at the repository root",
			Found: fmt.Sprintf("no file at %q", path), Next: "add gate.json (SPEC §8.3), or pass the --repo that holds it",
			Err: err}
	case err != nil:
		return Definition{}, &core.Error{Outcome: core.OutcomeError, Expected: "a readable gate.json inside the repository",
			Found: err.Error(), Next: "make gate.json a readable file inside --repo, then run the gate again", Err: err}
	}
	return ParseDefinition(data)
}

// maxGateDefinitionBytes caps gate.json; a larger file is refused unread (#199).
const maxGateDefinitionBytes = 1 << 20

// readInRoot reads the file name under dir; a name or link that leads out of dir, a non-regular
// file or a file over maxGateDefinitionBytes is an error that names the path.
func readInRoot(dir, name string) (data []byte, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	data, err = core.ReadRegularCapped(root, name, maxGateDefinitionBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, name), err)
	}
	return data, nil
}
