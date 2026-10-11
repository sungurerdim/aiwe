//go:build !windows

package core

import "os"

// checkOwnerOnly checks nothing outside Windows: there the 0o700 and 0o600 modes a new state root
// and owner key are made with are the access control. The mode of an existing state root or key is
// not checked; Unix key modes are not measured (#109).
func checkOwnerOnly(string) error { return nil }

// mkdirOwnerOnly makes the state root p, and any missing parent, with mode 0o700.
func mkdirOwnerOnly(p string) error { return os.MkdirAll(p, 0o700) }
