//go:build !windows

package core

import (
	"runtime"
	"testing"
)

// JunctionForTest fails the test: directory junctions exist only on Windows, and every caller
// checks the OS before it asks for one.
func JunctionForTest(t *testing.T, target, link string) {
	t.Helper()
	t.Fatalf("junction %s -> %s: no directory junctions on %s", link, target, runtime.GOOS)
}
