package engine

import (
	"cmp"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The user-data safety guarantees of the slot pool (#470, SPEC §6.5, DR-10, DR-11), one table per guarantee
// (G1 to G4, see each test); each row names the failure it catches.

// The restore helper dumps its environment to the file and plants a filter that appends to the marker these name.
func slotSafetyDumpEnv() string  { return strings.ToUpper(core.Product) + "_SLOT_SAFETY_DUMP" }
func slotSafetyPlantEnv() string { return strings.ToUpper(core.Product) + "_SLOT_SAFETY_PLANT" }

// slotSafetyPool opens a pool of one slot whose restore runs TestSlotSafetyRestoreHelper.
func slotSafetyPool(t *testing.T, fx *slotTestFixture) *SlotPool {
	t.Helper()
	spec := fx.spec(1, newSlotTestQueue())
	spec.Restore = []string{os.Args[0], "-test.run=^TestSlotSafetyRestoreHelper$"}
	p, err := OpenSlotPool(spec)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// slotSafetyOS skips a row bound to another OS, with the reason.
func slotSafetyOS(t *testing.T, only string) {
	t.Helper()
	switch {
	case only == "windows" && runtime.GOOS != "windows":
		t.Skip("a junction exists only on Windows; the symbolic link row covers this OS")
	case only == "unix" && runtime.GOOS == "windows":
		t.Skip("a symbolic link needs a privilege on Windows; the junction row covers this OS")
	}
}

// slotSafetyLink makes link lead to target: a junction on Windows, a symbolic link elsewhere.
func slotSafetyLink(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		return
	}
	if out, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
}

// slotSafetyTry leases a slot for h at base and returns what Lease returned.
func slotSafetyTry(t *testing.T, p *SlotPool, h SlotHolder, base string) (*Slot, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), slotTestWait)
	defer cancel()
	return p.Lease(ctx, h, base)
}

// slotSafetyCmd is a filter command that appends to marker and passes its input through.
func slotSafetyCmd(m string) string    { return "echo ran >> '" + filepath.ToSlash(m) + "'; cat" }
func slotSafetyRecord(n string) string { return `{"name":` + strconv.Quote(n) + `}` }

// slotSafetyFilter sets the smudge and clean commands of the filter "planted" with git config opts.
func slotSafetyFilter(t *testing.T, dir, cmd string, opts ...string) {
	t.Helper()
	for _, k := range []string{"smudge", "clean"} {
		engineTestGit(t, dir, append(append([]string{"config"}, opts...), "filter.planted."+k, cmd)...)
	}
}

// slotSafetyAssign assigns the filter "planted" to every path through the slot's info/attributes.
func slotSafetyAssign(t *testing.T, dir string) {
	t.Helper()
	gitDir := engineTestGit(t, dir, "rev-parse", "--absolute-git-dir")
	engineTestWrite(t, filepath.Join(gitDir, "info", "attributes"), "* filter=planted\n")
}

// slotSafetyArmed proves with raw git that the planted filter is live in dir, then clears marker.
func slotSafetyArmed(t *testing.T, dir, marker string) {
	t.Helper()
	engineTestGit(t, dir, "hash-object", "--path=README.md", "README.md")
	if engineTestRan(t, marker) == "" || os.Remove(marker) != nil {
		t.Fatalf("fixture not armed: raw git ran no planted filter in %s", dir)
	}
}

// TestSlotSafetyRestoreHelper is the safety tests' restore, run by a pool as a child: it dumps its environment,
// or plants a filter in its slot and moves the README's time back, so git status must run the filter.
func TestSlotSafetyRestoreHelper(t *testing.T) {
	if dump := os.Getenv(slotSafetyDumpEnv()); dump != "" {
		engineTestWrite(t, dump, strings.Join(os.Environ(), "\n")+"\n")
	}
	if marker := os.Getenv(slotSafetyPlantEnv()); marker != "" {
		slotSafetyFilter(t, ".", slotSafetyCmd(marker))
		slotSafetyAssign(t, ".")
		engineTestTouch(t, "README.md")
	}
}

// TestSlotSafetyStaysInCacheRoot guards G1: a slot whose record or tree leads out of the cache root is
// refused, a link inside a slot is never followed, and the user repository keeps HEAD and files. A record's
// {v} is the user repository, {r} its work tree, {b} the base of {v}; link root, tree or inside links to them.
func TestSlotSafetyStaysInCacheRoot(t *testing.T) {
	for _, tc := range []struct {
		name, only, link, rec string
		gitfile               bool // the slot's .git becomes a gitfile into the user repository
	}{
		{name: "catches: a record naming a user repository by an absolute path", rec: `{"name":"{v}"}`},
		{name: "catches: a record naming a user work tree by an absolute path", rec: `{"name":"{r}"}`},
		{name: "catches: a record name that climbs out with ..", rec: `{"name":"../{b}"}`},
		{name: "catches: a record name that climbs out with a backslash", rec: `{"name":"..\\{b}"}`},
		{name: "catches: a slot-shaped record name that climbs out", rec: `{"name":"slot-1-0123456789abcdef/../../{b}"}`},
		{name: "catches: a record naming a plain directory inside a user repository", rec: `{"name":"{r}/docs"}`},
		{name: "catches: a relative record name of a plain directory inside a user repository", rec: `{"name":"../{b}/clone/docs"}`},
		{name: "catches: a record of the old format with an absolute root and work tree", rec: `{"root":"{v}","dir":"{r}"}`},
		{name: "catches: a slot whose .git is a gitfile into a user repository", gitfile: true},
		{name: "catches: a slot root that is a junction to a user repository", only: "windows", link: "root"},
		{name: "catches: a slot root that is a symbolic link to a user repository", only: "unix", link: "root"},
		{name: "catches: a slot work tree that is a junction to a user work tree", only: "windows", link: "tree"},
		{name: "catches: a slot work tree that is a symbolic link to a user work tree", only: "unix", link: "tree"},
		{name: "catches: a clean that follows a junction in the work tree out of the slot", only: "windows", link: "inside"},
		{name: "catches: a clean that follows a symbolic link in the work tree out of the slot", only: "unix", link: "inside"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slotSafetyOS(t, tc.only)
			fx := slotTestSetup(t)
			victim, repo, head := slotTestVictim(t, fx)
			draft := filepath.Join(repo, "docs", "draft.md")
			engineTestWrite(t, draft, "untracked notes of the user\n")
			p := fx.open(t, 1, newSlotTestQueue())
			a := fx.lease(t, p, fx.holder(slotTestSession, 5101), fx.base)
			fx.release(t, p, a)
			moved, target := a.Root, victim
			switch {
			case tc.rec != "":
				esc := func(s string) string { q := strconv.Quote(filepath.ToSlash(s)); return q[1 : len(q)-1] }
				rec := strings.NewReplacer("{v}", esc(victim), "{r}", esc(repo), "{b}", filepath.Base(victim)).Replace(tc.rec)
				engineTestWrite(t, filepath.Join(fx.leaseDir, "s1.slot"), rec)
			case tc.gitfile:
				moved = filepath.Join(a.Dir, ".git")
			case tc.link == "tree":
				moved, target = a.Dir, repo
			case tc.link == "inside":
				moved, target = filepath.Join(a.Dir, "vendor-link"), repo
			}
			if (tc.gitfile || tc.link == "root" || tc.link == "tree") && os.Rename(moved, moved+"-moved") != nil {
				t.Fatalf("move %s aside", moved)
			}
			if tc.gitfile {
				engineTestWrite(t, moved, "gitdir: "+filepath.ToSlash(filepath.Join(repo, ".git"))+"\n")
			} else if tc.link != "" {
				slotSafetyLink(t, moved, target)
			}
			s, err := slotSafetyTry(t, p, fx.holder(slotTestSecondSession, 5102), fx.base)
			if tc.link != "inside" && (err == nil || s != nil) {
				t.Errorf("Lease = slot %v, err %v; want the slot refused", s, err)
			}
			slotTestVictimIntact(t, repo, head)
			if !slotTestExists(t, draft) {
				t.Error("an untracked file in a plain directory of the user repository was removed")
			}
			if tc.link != "inside" && slotTestExists(t, fx.leaseFile(1)) {
				t.Error("a refused slot kept its lease file")
			}
		})
	}
}

// TestSlotSafetyNoRepoProgram guards G2: a filter the slot's repository configures after the last
// settle, or a restore plants during one, never runs from an engine git call; the slot is dirty (and
// recloned) or refused. Each plant is proven live with raw git first.
func TestSlotSafetyNoRepoProgram(t *testing.T) {
	configInfo := func(t *testing.T, d, c, _ string) { slotSafetyFilter(t, d, c); slotSafetyAssign(t, d) }
	for _, tc := range []struct {
		name    string
		foreign bool // a new pool leases: the slot's creator is gone
		plant   func(t *testing.T, dir, cmd, outside string)
	}{
		{"catches: a filter in .git/config assigned by info/attributes", false, configInfo},
		{"catches: a filter in .git/config of a slot whose creator is gone", true, configInfo},
		{"catches: a filter in .git/config assigned by a work-tree .gitattributes", false, func(t *testing.T, d, c, _ string) {
			slotSafetyFilter(t, d, c)
			engineTestWrite(t, filepath.Join(d, ".gitattributes"), "* filter=planted\n")
		}},
		{"catches: a filter in config.worktree assigned by info/attributes", false, func(t *testing.T, d, c, _ string) {
			engineTestGit(t, d, "config", "extensions.worktreeConfig", "true")
			slotSafetyFilter(t, d, c, "--worktree")
			slotSafetyAssign(t, d)
		}},
		{"catches: a filter in a file outside .git that .git/config includes", false, func(t *testing.T, d, c, o string) {
			inc, q := filepath.Join(o, "planted.gitconfig"), strconv.Quote(c)
			engineTestWrite(t, inc, "[filter \"planted\"]\n\tsmudge = "+q+"\n\tclean = "+q+"\n")
			engineTestGit(t, d, "config", "include.path", filepath.ToSlash(inc))
			slotSafetyAssign(t, d)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			p := fx.open(t, 1, newSlotTestQueue())
			a := fx.lease(t, p, fx.holder(slotTestSession, 5301), fx.base)
			marker := filepath.Join(fx.outside, "filter-ran.txt")
			tc.plant(t, a.Dir, slotSafetyCmd(marker), fx.outside)
			slotSafetyArmed(t, a.Dir, marker)
			engineTestWrite(t, filepath.Join(a.Dir, "README.md"), "# greeter\n\nedited by the run\n")
			fx.release(t, p, a)
			if tc.foreign {
				p = fx.open(t, 1, newSlotTestQueue())
			}
			s, err := slotSafetyTry(t, p, fx.holder(slotTestSecondSession, 5302), fx.base)
			if got := engineTestRan(t, marker); got != "" {
				t.Errorf("an engine git call ran the planted filter: %q", got)
			}
			if err == nil {
				slotTestAssertClean(t, s, fx.base)
				if cfg := engineTestGit(t, s.Dir, "config", "--list"); strings.Contains(cfg, "filter.planted") {
					t.Error("a slot handed out with the planted filter still configured")
				}
			}
		})
	}

	t.Run("catches: a filter the restore plants during the settle, which the verify then runs", func(t *testing.T) {
		fx := slotTestSetup(t)
		marker := filepath.Join(fx.outside, "filter-ran.txt")
		t.Setenv(slotSafetyPlantEnv(), marker)
		p := slotSafetyPool(t, fx)
		a, err := slotSafetyTry(t, p, fx.holder(slotTestSession, 5303), fx.base)
		if err == nil {
			fx.release(t, p, a)
			_, err = slotSafetyTry(t, p, fx.holder(slotTestSecondSession, 5304), fx.base)
		}
		t.Logf("lease err: %v", err)
		if got := engineTestRan(t, marker); got != "" {
			t.Errorf("an engine git call ran the filter the restore planted: %q", got)
		}
	})
}

// TestSlotSafetyUnusableSkipped guards G3: an unusable slot (dirty of a gone creator, or an untrusted
// record) is skipped while another slot is usable; a lease fails, promptly, only when none is.
func TestSlotSafetyUnusableSkipped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dirty  []int
		record bool // slot 1 gets a record naming no slot directory
		want   int  // the slot leased, 0 for a refused lease
	}{
		{"catches: a dirty first slot of a gone creator that fails the lease while slot 2 is clean", []int{1}, false, 2},
		{"catches: a dirty second slot of a gone creator that fails the lease while slot 1 is clean", []int{2}, false, 1},
		{"catches: an untrusted record of slot 1 that fails the lease while slot 2 is clean", nil, true, 2},
		{"catches: a lease that succeeds, waits or leaves a lease file when every slot is unusable", []int{1, 2}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			p1 := fx.open(t, 2, newSlotTestQueue())
			slots := []*Slot{fx.lease(t, p1, fx.holder(slotTestSession, 5401), fx.base),
				fx.lease(t, p1, fx.holder(slotTestSecondSession, 5402), fx.base)}
			var planted []string
			for _, k := range tc.dirty {
				f := filepath.Join(slots[k-1].Dir, "node_modules", "left-pad", "postinstall.js")
				engineTestWrite(t, f, "require('child_process')\n")
				planted = append(planted, f)
			}
			for _, s := range slots {
				fx.release(t, p1, s)
			}
			if tc.record {
				engineTestWrite(t, filepath.Join(fx.leaseDir, "s1.slot"), slotSafetyRecord("../elsewhere"))
			}
			s, err := slotSafetyTry(t, fx.open(t, 2, newSlotTestQueue()), fx.holder(slotTestThirdSession, 5403), fx.base)
			switch {
			case tc.want == 0 && (err == nil || s != nil || errors.Is(err, context.DeadlineExceeded)):
				t.Errorf("Lease = slot %v, err %v; want a prompt refusal", s, err)
			case tc.want != 0 && err != nil:
				t.Errorf("Lease = %v; want slot %d while it is usable", err, tc.want)
			case tc.want != 0 && s.K != tc.want:
				t.Errorf("Lease got slot %d, want the usable slot %d", s.K, tc.want)
			case tc.want != 0:
				slotTestAssertClean(t, s, fx.base)
			}
			for _, f := range planted {
				if !slotTestExists(t, f) {
					t.Error("a pool removed a slot another process created (DR-11)")
				}
			}
			for _, n := range slotTestNames(t, fx.leaseDir) {
				if tc.want == 0 && (strings.HasSuffix(n, leaseSuffix) || strings.HasSuffix(n, ticketSuffix)) {
					t.Errorf("a refused lease left %s in the lease directory", n)
				}
			}
		})
	}
}

// TestSlotSafetyRefusalHidesValue guards G4: a refusal names what it refused and never repeats the
// refused value: a path, a commit id or a holder.
func TestSlotSafetyRefusalHidesValue(t *testing.T) {
	const canary = "canary-5e1f"
	absent := "c0ffee5e1f" + strings.Repeat("0", 30) // a well-formed commit id in no repository
	for _, tc := range []struct {
		name, value           string
		edit                  func(fx *slotTestFixture, s *SlotPoolSpec)
		recorded              bool // lease and release once first, so the slot has a record
		record, session, base string
	}{
		{name: "catches: a KeepDirs entry repeated", value: canary,
			edit: func(_ *slotTestFixture, s *SlotPoolSpec) { s.KeepDirs = []string{"../" + canary} }},
		{name: "catches: a lock file repeated", value: canary,
			edit: func(_ *slotTestFixture, s *SlotPoolSpec) { s.LockFiles = []string{".git/" + canary} }},
		{name: "catches: a canonical repository path that is no repository repeated", value: canary,
			edit: func(fx *slotTestFixture, s *SlotPoolSpec) { s.Canonical = filepath.Join(fx.outside, canary) }},
		{name: "catches: a holder session repeated", value: canary, session: "../" + canary},
		{name: "catches: a base that is no commit id repeated", value: canary, base: "--" + canary},
		{name: "catches: an absent base commit id repeated by a fresh clone", value: absent[:12], base: absent},
		{name: "catches: an absent base commit id repeated by a reset", value: absent[:12], base: absent, recorded: true},
		{name: "catches: a record path outside the cache root repeated", value: canary, recorded: true,
			record: slotSafetyRecord(filepath.Join(os.TempDir(), canary))},
		{name: "catches: a record name that climbs out repeated", value: canary, recorded: true,
			record: slotSafetyRecord("../" + canary)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			spec := fx.spec(1, newSlotTestQueue())
			if tc.edit != nil {
				tc.edit(fx, &spec)
			}
			p, err := OpenSlotPool(spec)
			if err == nil {
				if tc.recorded {
					fx.release(t, p, fx.lease(t, p, fx.holder(slotTestSecondSession, 5501), fx.base))
				}
				if tc.record != "" {
					engineTestWrite(t, filepath.Join(fx.leaseDir, "s1.slot"), tc.record)
				}
				h := fx.holder(slotTestSession, 5502)
				h.Session = cmp.Or(tc.session, h.Session)
				_, err = slotSafetyTry(t, p, h, cmp.Or(tc.base, fx.base))
			}
			if err == nil || strings.Contains(err.Error(), tc.value) {
				t.Errorf("refusal = %v; want an error without %q", err, tc.value)
			}
		})
	}
}

// TestSlotSafetyForeignRepoEnv guards that an engine environment pointing git at another repository
// stops the pool before any git call, without repeating the path.
func TestSlotSafetyForeignRepoEnv(t *testing.T) {
	for _, name := range repoEnvNames() {
		t.Run("catches: "+name+" aiming the slot's git at another repository", func(t *testing.T) {
			fx := slotTestSetup(t)
			t.Setenv(name, filepath.Join(fx.canonical, ".git", "canary-5e1f"))
			p, err := OpenSlotPool(fx.spec(1, newSlotTestQueue()))
			if p != nil || !errors.Is(err, ErrForeignRepoEnv) || strings.Contains(err.Error(), "canary-5e1f") {
				t.Errorf("OpenSlotPool = %v, %v; want ErrForeignRepoEnv without the path", p, err)
			}
		})
	}
}

// TestSlotSafetyRestoreEnv guards that the restore, which runs dependency scripts, inherits no
// credential: each row is a credential the engine's environment holds, with its own value.
func TestSlotSafetyRestoreEnv(t *testing.T) {
	creds := [][2]string{{"GITHUB_TOKEN", "cred-gh"}, {"AWS_SECRET_ACCESS_KEY", "cred-aws"}, {"DB_PASSWORD", "cred-db"},
		{"ANTHROPIC_API_KEY", "cred-api"}, {"NPM_CONFIG__AUTH", "cred-npm"}, {"GITLAB_PAT", "cred-pat"},
		{"GOOGLE_APPLICATION_CREDENTIALS", "cred-gcp"}, {"HTTPS_PROXY", "http://ada:cred-proxy@proxy.example:8080"},
		{"DATABASE_URL", "postgres://ada:cred-dsn@db.example/app"}}
	fx := slotTestSetup(t)
	dump := filepath.Join(fx.outside, "restore-env.txt")
	neutral := strings.ToUpper(core.Product) + "_SLOT_SAFETY_NEUTRAL"
	t.Setenv(slotSafetyDumpEnv(), dump)
	t.Setenv(neutral, "plain-5e1f")
	for _, c := range creds {
		t.Setenv(c[0], c[1])
	}
	fx.lease(t, slotSafetyPool(t, fx), fx.holder(slotTestSession, 5601), fx.base)
	dumped := engineTestRead(t, dump)
	env := strings.Split(dumped, "\n")
	if engineTestEnvValue(env, neutral) != "plain-5e1f" {
		t.Fatal("catches: a dump that proves nothing: the neutral variable did not reach the restore")
	}
	for _, c := range creds {
		t.Run("catches: "+c[0]+" reaching the restore", func(t *testing.T) {
			if _, ok := engineTestEnvEntry(env, c[0]); ok || strings.Contains(dumped, c[1]) {
				t.Errorf("the restore inherited %s or its value", c[0])
			}
		})
	}
}

// TestSlotSafetyGoneCreator guards R2-1: a slot whose creator process is gone is recloned when dirty or never
// settled, and a shrink over the ceiling removes it, through core.Owner.OpenOwned (DR-11).
func TestSlotSafetyGoneCreator(t *testing.T) {
	for _, tc := range []struct {
		name    string
		restore string // an extra restore argument of the gone creator's pool
		shrink  bool
	}{
		{name: "catches: a dirty slot of a gone creator that no later process reclones"},
		{name: "catches: a slot whose first settle a gone creator never finished", restore: "-test.count=not-a-number"},
		{name: "catches: an idle slot of a gone creator that a shrink over the ceiling cannot remove", shrink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := slotTestSetup(t)
			spec := fx.goneSpec(t, 1)
			if tc.restore != "" {
				spec.Restore = append(spec.Restore, tc.restore)
			}
			pg, err := OpenSlotPool(spec)
			if err != nil {
				t.Fatal(err)
			}
			a, err := slotSafetyTry(t, pg, fx.holder(slotTestSession, 5601), fx.base)
			planted := filepath.Join(fx.roots.Cache, "none")
			if err == nil {
				planted = filepath.Join(a.Dir, "node_modules", "is-odd", "index.js")
				engineTestWrite(t, planted, "module.exports = n => n % 2 === 1\n")
				fx.release(t, pg, a)
			}
			p := fx.open(t, 1, newSlotTestQueue())
			if tc.shrink {
				if removed, err := p.Shrink(t.Context(), slotTestOverCache()); err != nil || removed != 1 || slotTestExists(t, a.Root) {
					t.Errorf("Shrink over an idle slot of a gone creator = %d, %v; want it removed", removed, err)
				}
				return
			}
			slotTestAssertClean(t, fx.lease(t, p, fx.holder(slotTestSecondSession, 5602), fx.base), fx.base)
			if slotTestExists(t, planted) {
				t.Error("the dirty slot of a gone creator was handed out")
			}
		})
	}
}

// TestSlotSafetyTrustRows guards R2-4: a filter that info/attributes alone assigns to a driver the last
// settle trusted never runs.
func TestSlotSafetyTrustRows(t *testing.T) {
	t.Run("catches: a filter info/attributes assigns to a driver the last settle trusted", func(t *testing.T) {
		fx := slotTestSetup(t)
		p := fx.open(t, 1, newSlotTestQueue())
		a := fx.lease(t, p, fx.holder(slotTestSession, 5611), fx.base)
		marker := filepath.Join(fx.outside, "filter-ran.txt")
		slotSafetyFilter(t, a.Dir, slotSafetyCmd(marker))
		fx.setRecord(t, a.K, func(r *slotRecord) {
			var err error
			if r.Ctl, err = controlDigest(a.Dir); err != nil {
				t.Fatal(err)
			}
		})
		slotSafetyAssign(t, a.Dir)
		slotSafetyArmed(t, a.Dir, marker)
		engineTestWrite(t, filepath.Join(a.Dir, "README.md"), "# greeter\n\nedited by the run\n")
		fx.release(t, p, a)
		_, err := slotSafetyTry(t, p, fx.holder(slotTestSecondSession, 5612), fx.base)
		if got := engineTestRan(t, marker); got != "" {
			t.Errorf("an engine git call ran the filter info/attributes assigned (lease err %v): %q", err, got)
		}
	})
}

// TestSlotSafetyDoubleAdopt guards R3-2: two pools that meet one slot of a gone creator never both take it
// over, so the shrink of the first never fails on a slot the second removed and cloned anew (DR-11).
func TestSlotSafetyDoubleAdopt(t *testing.T) {
	t.Logf("catches: a takeover without the slot's lease, which strands the shrink of the first adopter")
	fx := slotTestSetup(t)
	pg, err := OpenSlotPool(fx.goneSpec(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	fx.release(t, pg, fx.lease(t, pg, fx.holder(slotTestSession, 5701), fx.base))
	pa, pb := fx.open(t, 1, newSlotTestQueue()), fx.open(t, 1, newSlotTestQueue())
	if _, err := pa.Shrink(t.Context(), core.DefaultFootprintCeilings()); err != nil {
		t.Fatal(err)
	}
	if removed, err := pb.Shrink(t.Context(), slotTestOverCache()); err != nil || removed != 1 {
		t.Fatalf("Shrink of the second pool = %d, %v; want the gone creator's slot removed", removed, err)
	}
	fx.release(t, pb, fx.lease(t, pb, fx.holder(slotTestSecondSession, 5702), fx.base))
	if removed, err := pa.Shrink(t.Context(), slotTestOverCache()); err != nil || removed != 0 {
		t.Errorf("Shrink of the first pool = %d, %v; want 0 and no error: the slot is the second pool's", removed, err)
	}
}
