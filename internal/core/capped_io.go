package core

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ReadRegularCapped reads the file name of root, refusing anything but a regular file of at most
// limit bytes. The file is opened once, without blocking on Unix, so a FIFO cannot hang the open;
// its type is checked on that handle, and at most limit+1 bytes are read through it, so a larger
// file is refused without being read whole (#199).
func ReadRegularCapped(root *os.Root, name string, limit int64) (b []byte, err error) {
	f, err := root.OpenFile(name, ownerKeyOpenFlag, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", info.Mode().Type())
	}
	b, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return b, nil
}
