package core

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	markerFile     = ".owner"
	markerDomain   = "owned_marker"
	maxMarkerBytes = 4096
)

// ownedMarker is the content of the .owner file inside an owned directory.
type ownedMarker struct {
	Path  string    `json:"path"`
	Owner string    `json:"owner"`
	PID   int       `json:"pid"`
	Start time.Time `json:"start"`
	HMAC  string    `json:"hmac"`
}

// errMarkerShape is the refusal of a marker that is not a plain file of at most maxMarkerBytes.
var errMarkerShape = fmt.Errorf("not a plain file of at most %d bytes", maxMarkerBytes)

// matchMarker checks that the marker bytes b, read from p, match rec under the key.
func (o *Owner) matchMarker(rootPath, p string, b []byte, rec ownedRecord) error {
	var m ownedMarker
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("%w: marker %q: %w", ErrNotOwned, p, err)
	}
	if m.Path != rec.Path || m.Owner != rec.Owner || m.PID != rec.PID || !m.Start.Equal(rec.Start) ||
		!o.validMAC(markerDomain, rootPath, rec, m.HMAC) {
		return fmt.Errorf("%w: marker %q does not match its owned_dir record", ErrNotOwned, p)
	}
	return nil
}
