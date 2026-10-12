//go:build !windows

package core

import (
	"runtime"
	"testing"
)

// ownerKeyTestSID fails the test: SDDL principals exist only on Windows, and every caller checks
// the OS before it reads an ACL.
func ownerKeyTestSID(t *testing.T, s string) string {
	t.Helper()
	t.Fatalf("SDDL principal %q: no SIDs on %s", s, runtime.GOOS)
	return ""
}
