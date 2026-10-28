package ledger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// openLedger opens the ledger file at path for reading and appending, creating it when absent. It
// opens through the directory of the file, so a link in the name cannot lead out of that directory
// (SPEC §5.6).
func openLedger(path string) (ledgerFile, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open the directory of %s: %w", path, err)
	}
	f, err := root.OpenFile(filepath.Base(path), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open %s: %w", path, err), root.Close())
	}
	if err := root.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("close the directory of %s: %w", path, err), f.Close())
	}
	return f, nil
}
