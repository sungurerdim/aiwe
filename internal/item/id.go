package item

import (
	"fmt"
	"io"
	"time"
)

const (
	// idPrefix starts every id (SPEC §8.4).
	idPrefix = "W-"
	// crockford is the Crockford base32 alphabet: digits and letters without I, L, O and U.
	crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	// idSymbols is the number of base32 symbols after the prefix; each carries 5 bits.
	idSymbols = 6
	// idTimeBits is how many of the 30 id bits come from the clock; the other 16 come from the random source.
	idTimeBits = 14
	// idRandomBytes is the number of bytes drawn from the random source.
	idRandomBytes = 2
)

// NewID returns an id: the prefix W- and 6 Crockford base32 characters, such as W-7K3F9Q. Its 30 bits
// are the low 14 bits of the clock in Unix milliseconds followed by 16 bits read from random. The caller
// passes both, so the id is a function of its inputs and a test fixes them; the caller keeps the
// id for the life of the item, because an id never changes. A random source that cannot give its two
// bytes is an error and no id.
func NewID(now time.Time, random io.Reader) (string, error) {
	var buf [idRandomBytes]byte
	if _, err := io.ReadFull(random, buf[:]); err != nil {
		return "", fmt.Errorf("item id: read random bits: %w", err)
	}
	timeBits := uint32(now.UnixMilli() & (1<<idTimeBits - 1))
	bits := timeBits<<(8*idRandomBytes) | uint32(buf[0])<<8 | uint32(buf[1])
	id := make([]byte, 0, len(idPrefix)+idSymbols)
	id = append(id, idPrefix...)
	for i := range idSymbols {
		shift := 5 * (idSymbols - 1 - i)
		id = append(id, crockford[bits>>shift&31])
	}
	return string(id), nil
}
