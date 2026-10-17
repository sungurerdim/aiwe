//go:build !windows

package core

import (
	"runtime"
	"testing"
)

// ownerKeyTestSID fails the test: SDDL principals exist only on Windows, and every caller checks
// the OS before it reads an ACL. It reports with Errorf, not Fatalf: staticcheck reads Fatalf as a
// call that never returns, so the values the caller checked before it would count as dead (#342).
func ownerKeyTestSID(t *testing.T, s string) string {
	t.Helper()
	t.Errorf("SDDL principal %q: no SIDs on %s", s, runtime.GOOS)
	return ""
}
