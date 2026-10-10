package core_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// measuredUsage is a ledger-shaped record that carries measured fields (SPEC §8.5).
type measuredUsage struct {
	TokensIn  core.Measured[int64]   `json:"tokensIn"`
	TokensOut core.Measured[int64]   `json:"tokensOut"`
	CostUSD   core.Measured[float64] `json:"costUsd"`
}

func TestMeasured(t *testing.T) {
	t.Run("marshal", func(t *testing.T) {
		tests := []struct {
			name string
			in   any
			want string
		}{
			{"unmeasured token count printed as 0 instead of unknown", core.Measured[int64]{}, `"unknown"`},
			{"unmeasured price printed as 0 instead of unknown", core.Measured[float64]{}, `"unknown"`},
			{"unknown quota leaks a stale value", core.Measured[int64]{Value: 400000, Known: false}, `"unknown"`},
			{"unknown price leaks a stale value", core.Measured[float64]{Value: 4.73, Known: false}, `"unknown"`},
			{"measured zero tokens printed as unknown", core.Measured[int64]{Value: 0, Known: true}, `0`},
			{"measured zero cost printed as unknown", core.Measured[float64]{Value: 0, Known: true}, `0`},
			{"measured token count not printed as a number", core.Measured[int64]{Value: 18342, Known: true}, `18342`},
			{"measured price not printed as a number", core.Measured[float64]{Value: 4.73, Known: true}, `4.73`},
			{"largest token count truncated", core.Measured[int64]{Value: math.MaxInt64, Known: true}, `9223372036854775807`},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got, err := json.Marshal(tc.in)
				if err != nil {
					t.Fatalf("json.Marshal(%#v) error = %v, want nil", tc.in, err)
				}
				if string(got) != tc.want {
					t.Errorf("json.Marshal(%#v) = %s, want %s", tc.in, got, tc.want)
				}
			})
		}
	})

	t.Run("unmarshal int64", func(t *testing.T) {
		tests := []struct {
			name string
			in   string
			want core.Measured[int64]
		}{
			{`"unknown" read back as a known 0`, `"unknown"`, core.Measured[int64]{}},
			{"measured 0 read back as unknown", `0`, core.Measured[int64]{Value: 0, Known: true}},
			{"measured count read back wrong", `18342`, core.Measured[int64]{Value: 18342, Known: true}},
			{"largest count read back wrong", `9223372036854775807`, core.Measured[int64]{Value: math.MaxInt64, Known: true}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				// Start from a known value so a decoder that leaves it untouched is caught.
				got := core.Measured[int64]{Value: 777, Known: true}
				if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
					t.Fatalf("json.Unmarshal(%s) error = %v, want nil", tc.in, err)
				}
				if got != tc.want {
					t.Errorf("json.Unmarshal(%s) = %#v, want %#v", tc.in, got, tc.want)
				}
			})
		}
	})

	t.Run("unmarshal float64", func(t *testing.T) {
		tests := []struct {
			name string
			in   string
			want core.Measured[float64]
		}{
			{`"unknown" price read back as a known 0`, `"unknown"`, core.Measured[float64]{}},
			{"measured zero cost read back as unknown", `0`, core.Measured[float64]{Value: 0, Known: true}},
			{"measured price read back wrong", `4.73`, core.Measured[float64]{Value: 4.73, Known: true}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got := core.Measured[float64]{Value: 9.99, Known: true}
				if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
					t.Fatalf("json.Unmarshal(%s) error = %v, want nil", tc.in, err)
				}
				if got != tc.want {
					t.Errorf("json.Unmarshal(%s) = %#v, want %#v", tc.in, got, tc.want)
				}
			})
		}
	})

	t.Run("unmarshal refused", func(t *testing.T) {
		// Fail-closed (SPEC §4): input that is neither a number nor "unknown" is refused,
		// never read as 0 or as unknown.
		tests := []struct {
			name string
			in   string
		}{
			{"capitalised Unknown accepted", `"Unknown"`},
			{"empty string accepted", `""`},
			{"quoted number accepted as a measured value", `"18342"`},
			{"boolean accepted", `true`},
			{"object accepted", `{}`},
			{"array accepted", `[]`},
			{"fraction accepted as a token count", `1.5`},
			{"token count above int64 accepted", `9223372036854775808`},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				var got core.Measured[int64]
				if err := json.Unmarshal([]byte(tc.in), &got); err == nil {
					t.Errorf("json.Unmarshal(%s) = %#v, nil error; want an error", tc.in, got)
				}
			})
		}
	})

	t.Run("record round trip", func(t *testing.T) {
		tests := []struct {
			name string
			in   measuredUsage
			want string
		}{
			{
				name: "unknown cost beside measured tokens collapses to 0",
				in: measuredUsage{
					TokensIn:  core.Measured[int64]{Value: 18342, Known: true},
					TokensOut: core.Measured[int64]{Value: 0, Known: true},
					CostUSD:   core.Measured[float64]{},
				},
				want: `{"tokensIn":18342,"tokensOut":0,"costUsd":"unknown"}`,
			},
			{
				name: "subscription run with nothing measured printed as zeros",
				in:   measuredUsage{},
				want: `{"tokensIn":"unknown","tokensOut":"unknown","costUsd":"unknown"}`,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				b, err := json.Marshal(tc.in)
				if err != nil {
					t.Fatalf("json.Marshal error = %v, want nil", err)
				}
				if string(b) != tc.want {
					t.Errorf("json.Marshal = %s, want %s", b, tc.want)
				}
				var back measuredUsage
				if err := json.Unmarshal(b, &back); err != nil {
					t.Fatalf("json.Unmarshal(%s) error = %v, want nil", b, err)
				}
				if back != tc.in {
					t.Errorf("round trip of %s = %#v, want %#v", b, back, tc.in)
				}
			})
		}
	})

	t.Run("absent field read as a measured value", func(t *testing.T) {
		// Absence is not evidence (DR-13): a missing key stays unknown.
		var got measuredUsage
		if err := json.Unmarshal([]byte(`{"tokensIn":120}`), &got); err != nil {
			t.Fatalf("json.Unmarshal error = %v, want nil", err)
		}
		if got.TokensOut.Known || got.CostUSD.Known {
			t.Errorf("absent tokensOut/costUsd decoded as known: %#v", got)
		}
		if got.TokensIn != (core.Measured[int64]{Value: 120, Known: true}) {
			t.Errorf("tokensIn = %#v, want known 120", got.TokensIn)
		}
	})
}
