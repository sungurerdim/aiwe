package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// ownerKeyTestGrant gives Authenticated Users (SID S-1-5-11) the access perm on p with icacls.
func ownerKeyTestGrant(t *testing.T, p, perm string) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "icacls", p, "/grant", "*S-1-5-11:"+perm).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %s /grant *S-1-5-11:%s: %v: %s", p, perm, err, out)
	}
}

// ownerKeyTestPermissiveParent gives Authenticated Users modify on base, inherited by every entry
// made below it, so the test does not depend on how open the machine's temp dir is.
func ownerKeyTestPermissiveParent(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	ownerKeyTestGrant(t, base, "(OI)(CI)(M)")
	return base
}

// ownerKeyTestSDDL returns the current user's SID and the security descriptor of p in SDDL, read
// by PowerShell, apart from the code under test.
func ownerKeyTestSDDL(t *testing.T, p string) (string, string) {
	t.Helper()
	script := "[Security.Principal.WindowsIdentity]::GetCurrent().User.Value; (Get-Acl -LiteralPath '" +
		strings.ReplaceAll(p, "'", "''") + "').Sddl"
	cmd := exec.CommandContext(t.Context(), "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	// Under PowerShell 7 (the GitHub Actions default shell) PSModulePath names PowerShell 7's
	// module folders first; Windows PowerShell 5.1 then fails to load Get-Acl's module. Without
	// the variable, 5.1 builds its own default path.
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return strings.EqualFold(k, "PSModulePath")
	})
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read the ACL of %s: %v: %s", p, err, out)
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		t.Fatalf("read the ACL of %s: want a SID and an SDDL line, got %q", p, out)
	}
	return f[0], f[1]
}

var (
	ownerKeyTestOwner = regexp.MustCompile(`^O:(.+?)G:`)
	ownerKeyTestDACL  = regexp.MustCompile(`D:([A-Z]*)((?:\([^)]*\))*)`)
	ownerKeyTestACE   = regexp.MustCompile(`\(([^)]*)\)`)
)

// ownerKeyTestAllowed returns the SIDs that the DACL of sddl allows, its DACL flags and its owner.
func ownerKeyTestAllowed(t *testing.T, p, sddl string) (allowed []string, flags, owner string) {
	t.Helper()
	o := ownerKeyTestOwner.FindStringSubmatch(sddl)
	d := ownerKeyTestDACL.FindStringSubmatch(sddl)
	if o == nil || d == nil {
		t.Fatalf("%s: SDDL %q has no owner or no DACL", p, sddl)
	}
	for _, m := range ownerKeyTestACE.FindAllStringSubmatch(d[2], -1) {
		f := strings.Split(m[1], ";")
		if len(f) != 6 || (f[0] != "A" && f[0] != "D") {
			t.Fatalf("%s: ACE %q is neither a plain allow nor a plain deny ACE", p, m[1])
		}
		if f[0] == "A" {
			allowed = append(allowed, f[5])
		}
	}
	return allowed, d[1], o[1]
}

// ownerKeyTestOwnerOnly fails the test when p is owned by, or allows, a principal other than the
// current user, SYSTEM or Administrators, or, when protected is set, still inherits its parent's
// ACEs.
func ownerKeyTestOwnerOnly(t *testing.T, p string, protected bool) {
	t.Helper()
	user, sddl := ownerKeyTestSDDL(t, p)
	// SDDL prints a well-known principal as an alias, such as LA for the RID-500 user the GitHub
	// runner uses, so each principal is compared by its SID value.
	trusted := map[string]bool{user: true, "S-1-5-18": true, "S-1-5-32-544": true, "S-1-3-0": true}
	allowed, flags, owner := ownerKeyTestAllowed(t, p, sddl)
	if !trusted[ownerKeyTestSID(t, owner)] {
		t.Errorf("%s is owned by %s; want the current user (%s), SYSTEM or Administrators", p, owner, user)
	}
	if protected && !strings.Contains(flags, "P") {
		t.Errorf("%s inherits its parent's ACEs (DACL flags %q, SDDL %s); want inheritance cut", p, flags, sddl)
	}
	for _, sid := range allowed {
		if !trusted[ownerKeyTestSID(t, sid)] {
			t.Errorf("%s allows %s (SDDL %s); want only the current user, SYSTEM and Administrators", p, sid, sddl)
		}
	}
}

// ownerKeyTestReal returns the real path of p, the spelling a refusal detail names: a temp dir given
// as an 8.3 short name or in another letter case resolves to its long, on-disk name.
func ownerKeyTestReal(t *testing.T, p string) string {
	t.Helper()
	rp, err := realPath(p)
	if err != nil {
		t.Fatal(err)
	}
	return rp
}

// ownerKeyTestRefused fails the test when OpenOwner of p, an open or foreign root or owner key, is
// not refused as root_unsafe with the step that fixes it (#123): a file is deleted first; a directory
// (dir), which may be a home or "/", is never named for delete. The row promises no step a detail lacks.
func ownerKeyTestRefused(t *testing.T, what, p string, o *Owner, err error, dir, foreign bool) {
	t.Helper()
	if !errors.Is(err, ErrRootUnsafe) || ErrorResult(err).Outcome != "root_unsafe" {
		t.Errorf("OpenOwner with %s %s: owner %t, error %v; want %v (§5.15)", p, what, o != nil, err, ErrRootUnsafe)
		return
	}
	if next := outcomeRow("root_unsafe").Next; !strings.Contains(next, "other user") || strings.Contains(next, "detail names the step") {
		t.Errorf("root_unsafe next action %q does not name the other users' access, or promises a step a detail lacks", next)
	}
	q, d := strconv.Quote(p), ErrorResult(err).Detail
	want := map[[2]bool]string{
		{false, false}: "; next: delete " + q + " so the next " + Product + " run makes it owner-only, or remove the access of every other user from it",
		{false, true}:  "; next: delete " + q + ", or give its ownership to the user that runs " + Product,
		{true, false}:  "; next: remove the access of every other user from " + q,
		{true, true}:   "; next: give its ownership to the user that runs " + Product + ", or set the root to a directory that user owns",
	}[[2]bool{dir, foreign}]
	if !strings.HasSuffix(d, want) || (dir && strings.Contains(d, "delete")) {
		t.Errorf("refusal detail %q (directory %t) does not end with the step that fixes it, %q", d, dir, want)
	}
}

// TestOwnerKeyACL catches an owner key, or a state dir, that a principal other than the owner,
// SYSTEM or Administrators can read or write and that OpenOwner still loads on Windows, where the
// 0o600/0o700 modes do nothing (security review of #6, medium). It also catches a state dir and key
// that OpenOwner makes under an open parent and leaves open by inheritance, so aiwe refuses its own
// state on a machine whose temp dir lets other users in (#109), and on every OS a key swapped (#124).
func TestOwnerKeyACL(t *testing.T) {
	self := sweepTestSelf(t)
	t.Run("key swapped for the owner-only key after it is taken", func(t *testing.T) {
		r := removeTestRoots(t, t.TempDir())
		removeTestOpen(t, r)
		key := filepath.Join(r.State, ownerKeyFile)
		if err := errors.Join(os.Rename(key, key+".good"), writeNewFile(key, []byte(strings.Repeat("5a", ownerKeySize))), os.Chmod(key, 0o644)); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "windows" {
			ownerKeyTestGrant(t, key, "(R)")
		}
		swap := errors.New("the taken hook never ran")
		_, err := ownerKeyLoader{taken: func() { swap = errors.Join(os.Rename(key, key+".open"), os.Rename(key+".good", key)) }}.load(r.State)
		if swap != nil {
			t.Errorf("the taken hook did not swap the key open to others for the owner-only key: %v", swap)
		}
		ownerKeyTestRefused(t, "open to others, swapped for the owner-only key after it was taken", key, nil, err, false, false)
	})
	if runtime.GOOS != "windows" {
		t.Skip("the key ACL check is Windows-only; Unix key modes are not measured (#109)")
	}

	t.Run("state dir and key made under an open parent are owner-only", func(t *testing.T) {
		base := ownerKeyTestPermissiveParent(t)
		_, parent := ownerKeyTestSDDL(t, base)
		if allowed, _, _ := ownerKeyTestAllowed(t, base, parent); !slices.Contains(allowed, "AU") && !slices.Contains(allowed, "S-1-5-11") {
			t.Fatalf("parent %s does not allow Authenticated Users (SDDL %s); the case proves nothing", base, parent)
		}
		r := removeTestRoots(t, base)
		if _, err := OpenOwner(r, self); err != nil {
			t.Fatalf("OpenOwner with a missing state root under an open parent: %v; want it made owner-only and loaded", err)
		}
		ownerKeyTestOwnerOnly(t, r.State, true)
		ownerKeyTestOwnerOnly(t, filepath.Join(r.State, ownerKeyFile), false)
	})

	t.Run("state dir made beforehand under an open parent", func(t *testing.T) {
		r := removeTestRoots(t, ownerKeyTestPermissiveParent(t))
		removeTestMkdir(t, r.State)
		o, err := OpenOwner(r, self)
		ownerKeyTestRefused(t, "inheriting Authenticated Users modify", ownerKeyTestReal(t, r.State), o, err, true, false)
		if removeTestExists(t, filepath.Join(r.State, ownerKeyFile)) {
			t.Errorf("OpenOwner refused %s yet wrote a key into it", r.State)
		}
	})

	for _, c := range []struct{ name, target, perm string }{
		{"key readable by Authenticated Users", ownerKeyFile, "(R)"},
		{"key writable by Authenticated Users", ownerKeyFile, "(M)"},
		{"state dir writable by Authenticated Users", ".", "(OI)(CI)(M)"},
		{"temp root writable by Authenticated Users", "../temp", "(OI)(CI)(M)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := removeTestRoots(t, t.TempDir())
			// The first open makes the roots and key and loads them: the grant alone makes the refusal.
			removeTestOpen(t, r)
			target := filepath.Join(r.State, c.target)
			ownerKeyTestGrant(t, target, c.perm)
			o, err := OpenOwner(r, self)
			ownerKeyTestRefused(t, "granting Authenticated Users "+c.perm, ownerKeyTestReal(t, target), o, err, c.target != ownerKeyFile, false)
		})
	}
}
