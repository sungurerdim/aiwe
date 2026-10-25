package item_test

import (
	"bytes"
	"regexp"
	"testing"
	"testing/iotest"
	"time"

	"github.com/sungurerdim/aiwe/internal/item"
)

// idShape is the SPEC §8.4 id: the prefix W- and 6 Crockford base32 characters (no I, L, O or U).
var idShape = regexp.MustCompile(`^W-[0-9A-HJKMNP-TV-Z]{6}$`)

// idNow is a fixed instant, so every id in these tests is reproducible.
var idNow = time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC)

// idRandom is a fixed random source of the two bytes an id draws.
func idRandom(b0, b1 byte) *bytes.Reader { return bytes.NewReader([]byte{b0, b1}) }

// TestIDFormat proves an id is W- plus 6 Crockford base32 characters. The rows guard against a wrong
// alphabet (RFC 4648 base32 or the letters I, L, O, U), a wrong length, a lost prefix and an id that
// is not the same for the same inputs.
func TestIDFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		now  time.Time
		b0   byte
		b1   byte
		want string
	}{
		{name: "a typical clock and random pair", now: idNow, b0: 0x3A, b1: 0xC5, want: "W-YW0EP5"},
		{name: "the epoch with no random bits is all zeros, not a short id", now: time.UnixMilli(0), b0: 0, b1: 0,
			want: "W-000000"},
		{name: "every drawn bit set reaches the last symbol of the alphabet", now: time.UnixMilli(0x3FFF),
			b0: 0xFF, b1: 0xFF, want: "W-ZZZZZZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := item.NewID(tt.now, idRandom(tt.b0, tt.b1))
			if err != nil {
				t.Fatalf("NewID: %v", err)
			}
			if !idShape.MatchString(got) {
				t.Fatalf("id %q does not match W- plus 6 Crockford base32 characters", got)
			}
			if got != tt.want {
				t.Fatalf("id = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIDFormatAcrossManyDraws proves the shape holds for every value of the random byte pair that a
// caller can pass, so no input produces an id outside the alphabet.
func TestIDFormatAcrossManyDraws(t *testing.T) {
	t.Parallel()
	for b0 := 0; b0 < 256; b0 += 5 {
		for b1 := 0; b1 < 256; b1 += 7 {
			got, err := item.NewID(idNow.Add(time.Duration(b0*b1)*time.Millisecond), idRandom(byte(b0), byte(b1)))
			if err != nil {
				t.Fatalf("NewID(%d, %d): %v", b0, b1, err)
			}
			if !idShape.MatchString(got) {
				t.Fatalf("NewID(%d, %d) = %q, not W- plus 6 Crockford base32 characters", b0, b1, got)
			}
		}
	}
}

// TestIDDrawsOnClockAndRandom proves the id changes when only the clock changes and when only the
// random source changes. It guards against an id that ignores one of its two inputs, which would
// collide for every item made in one instant or for every item drawn from one random source.
func TestIDDrawsOnClockAndRandom(t *testing.T) {
	t.Parallel()
	base, err := item.NewID(idNow, idRandom(0x3A, 0xC5))
	if err != nil {
		t.Fatalf("NewID base: %v", err)
	}
	again, err := item.NewID(idNow, idRandom(0x3A, 0xC5))
	if err != nil {
		t.Fatalf("NewID again: %v", err)
	}
	if again != base {
		t.Fatalf("the same clock and random source gave %q then %q, want one id", base, again)
	}
	tests := []struct {
		name string
		now  time.Time
		b0   byte
		b1   byte
		want string
	}{
		{name: "the clock moves by one millisecond", now: idNow.Add(time.Millisecond), b0: 0x3A, b1: 0xC5,
			want: "W-YW2EP5"},
		{name: "the clock moves by one hour", now: idNow.Add(time.Hour), b0: 0x3A, b1: 0xC5, want: "W-P40EP5"},
		{name: "the low random byte changes", now: idNow, b0: 0x3A, b1: 0xC6, want: "W-YW0EP6"},
		{name: "the high random byte changes", now: idNow, b0: 0x3B, b1: 0xC5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := item.NewID(tt.now, idRandom(tt.b0, tt.b1))
			if err != nil {
				t.Fatalf("NewID: %v", err)
			}
			if got == base {
				t.Fatalf("id = %q, equal to the base id %q although only one input changed", got, base)
			}
			if tt.want != "" && got != tt.want {
				t.Fatalf("id = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIDRefusesShortRandomSource proves a random source that cannot give its bytes yields an error and
// no id. It guards against an id made from partial or zero bits when the source fails.
func TestIDRefusesShortRandomSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source *bytes.Reader
	}{
		{name: "the source is empty", source: bytes.NewReader(nil)},
		{name: "the source ends after one byte", source: bytes.NewReader([]byte{0x3A})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := item.NewID(idNow, tt.source)
			if err == nil {
				t.Fatalf("NewID = %q with no error, want an error for a short random source", got)
			}
			if got != "" {
				t.Fatalf("NewID returned %q with an error, want an empty id", got)
			}
		})
	}
	t.Run("the source fails", func(t *testing.T) {
		t.Parallel()
		got, err := item.NewID(idNow, iotest.ErrReader(iotest.ErrTimeout))
		if err == nil || got != "" {
			t.Fatalf("NewID = %q, %v; want an empty id and the source error", got, err)
		}
	})
}
