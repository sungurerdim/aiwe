package engine

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestCollect guards DR-11 and SPEC §6.5: the run branch is fetched into a run ref of the canonical
// repository before the clone goes, and the residue of the clone is recorded. It catches a run
// whose commits are lost with its clone.
func TestCollect(t *testing.T) {
	ctx := t.Context()
	roots := engineTestRoots(t)
	owner := engineTestOwner(t, roots)
	r := engineTestRepoNew(t, roots.Canonical[0])

	clone, err := NewClone(ctx, owner, CloneSpec{Canonical: r.dir, Base: r.base, RunID: engineTestRunID})
	if err != nil {
		t.Fatalf("NewClone: %v", err)
	}

	// The run commits on its branch and leaves one path uncommitted.
	const branch = "unit/35-run-clone"
	const leftover = "notes.txt"
	engineTestGit(t, clone.Dir, "config", "user.name", "Ada Lovelace")
	engineTestGit(t, clone.Dir, "config", "user.email", "ada.lovelace@example.com")
	engineTestGit(t, clone.Dir, "config", "commit.gpgsign", "false")
	engineTestGit(t, clone.Dir, "checkout", "-q", "-b", branch)
	engineTestWrite(t, filepath.Join(clone.Dir, filepath.FromSlash(engineTestSource)), "package engine\n\n// the claim\n")
	engineTestGit(t, clone.Dir, "add", engineTestSource)
	engineTestGit(t, clone.Dir, "commit", "-q", "-m", "feat(engine): claim a unit")
	tip := engineTestGit(t, clone.Dir, "rev-parse", "HEAD")
	engineTestWrite(t, filepath.Join(clone.Dir, leftover), "scratch of the run\n")

	residue, err := Collect(ctx, owner, clone, branch)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	t.Run("the run branch is fetched to a run ref before the clone goes", func(t *testing.T) {
		// Catches a clone that is removed with its commits in it.
		if residue.Ref != RunRef(engineTestRunID) {
			t.Errorf("residue ref = %q, want %q", residue.Ref, RunRef(engineTestRunID))
		}
		if residue.RunID != engineTestRunID {
			t.Errorf("residue run id = %q, want %q", residue.RunID, engineTestRunID)
		}
		if got := engineTestGit(t, r.dir, "rev-parse", residue.Ref); got != tip {
			t.Errorf("%s in the canonical repository = %s, want the run tip %s", residue.Ref, got, tip)
		}
		if residue.Tip != tip {
			t.Errorf("residue tip = %s, want %s", residue.Tip, tip)
		}
		if _, err := os.Lstat(clone.Dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the run clone %s still exists after Collect (err %v)", clone.Dir, err)
		}
	})

	t.Run("the residue of the clone is recorded", func(t *testing.T) {
		// Catches residue that loses the paths the agent left in the clone.
		if !slices.Contains(residue.Uncommitted, leftover) {
			t.Errorf("residue uncommitted paths = %v, want %q", residue.Uncommitted, leftover)
		}
		if !strings.Contains(residue.DiffStat, engineTestSource) {
			t.Errorf("residue diff stat = %q, want the changed %s", residue.DiffStat, engineTestSource)
		}
	})

	t.Run("the owner's tree is untouched", func(t *testing.T) {
		// Catches collection that writes into the owner's checkout.
		if got := engineTestGit(t, r.dir, "rev-parse", "HEAD"); got != r.base {
			t.Errorf("the owner's HEAD = %s after Collect, want %s", got, r.base)
		}
		if got := engineTestGit(t, r.dir, "status", "--porcelain"); got != "" {
			t.Errorf("the owner's work tree is dirty after Collect: %q", got)
		}
		if got := engineTestRead(t, filepath.Join(r.dir, engineTestReadme)); got != engineTestReadmeBase {
			t.Errorf("the owner's %s = %q, want the base content %q", engineTestReadme, got, engineTestReadmeBase)
		}
	})
}
