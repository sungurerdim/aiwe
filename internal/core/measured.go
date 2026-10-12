package core

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// unknownJSON is the JSON form of a value that was not measured (DR-6).
const unknownJSON = `"unknown"`

// Measured is a price, limit, quota or metric that is either measured or unknown (DR-6, SPEC
// §8.5). Unknown is a separate state from 0: the zero Measured is unknown, never a measured 0.
// The reason for an unknown value lives in the record that holds it, not here.
type Measured[T int64 | float64] struct {
	Value T
	Known bool
}

// MarshalJSON writes a known value as a JSON number and an unknown one as "unknown"; an unknown
// value never leaks its stale Value.
func (m Measured[T]) MarshalJSON() ([]byte, error) {
	if !m.Known {
		return []byte(unknownJSON), nil
	}
	return json.Marshal(m.Value)
}

// UnmarshalJSON reads a JSON number as a known value and "unknown" as an unknown one. Any other
// input, null included, is refused (fail-closed, SPEC §4): it is never read as 0 or as unknown.
func (m *Measured[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte(unknownJSON)) {
		*m = Measured[T]{}
		return nil
	}
	if len(data) == 0 || data[0] == '"' || bytes.Equal(data, []byte("null")) {
		return fmt.Errorf("measured value: expected a number or %s, found %s", unknownJSON, data)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("measured value: expected a number or %s, found %s: %w", unknownJSON, data, err)
	}
	*m = Measured[T]{Value: v, Known: true}
	return nil
}
