package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
)

// TestCanonicalBytesRFC8785 guards against a hash over bytes that are not the RFC 8785 (JCS) form:
// keys out of UTF-16 order, whitespace, or escapes and numbers other than ECMAScript's would give one
// record two hashes, so a verifier written in another language could not recompute a link (L59). The
// numbers are the IEEE 754 vectors of RFC 8785 Appendix B; each expected form was checked against
// ECMAScript JSON.stringify.
func TestCanonicalBytesRFC8785(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"keys sorted by UTF-16 code units, not by UTF-8 bytes (RFC 8785 §3.2.3)",
			`{"€":"Euro Sign","\r":"Carriage Return","דּ":"Hebrew Letter Dalet With Dagesh","1":"One",` +
				`"😀":"Emoji: Grinning Face","\u0080":"Control","ö":"Latin Small Letter O With Diaeresis"}`,
			`{"\r":"Carriage Return","1":"One","` + "\u0080" + `":"Control","` + "ö" +
				`":"Latin Small Letter O With Diaeresis","` + "€" + `":"Euro Sign","` + "\U0001F600" +
				`":"Emoji: Grinning Face","` + "דּ" + `":"Hebrew Letter Dalet With Dagesh"}`},
		{"whitespace between tokens left in, keys of a nested object left unsorted",
			"{ \"b\" : [ 1 , { \"z\" : null , \"a\" : true } ] ,\n\t\"a\" : { } }",
			`{"a":{},"b":[1,{"a":true,"z":null}]}`},
		{"a literal or an empty array written in another form", `[ true , false , null , [ ] ]`, `[true,false,null,[]]`},
		{"string escapes other than JSON.stringify's: upper-case hex, an escaped slash, an escaped U+2028",
			`"A\/é\u001F\b\f\n\r\t\"\\ \u007f\u0000"`,
			`"A/` + "é" + `\u001f\b\f\n\r\t\"\\` + " \u007f" + `\u0000"`},
		{"a raw non-ASCII character or U+FFFD escaped", "\"ö�\"", "\"ö�\""},
		{"a surrogate pair not joined into one character", `"😀"`, "\"\U0001F600\""},
		{"1e21 not in exponent form with a plus sign", `1e21`, `1e+21`},
		{"1e-7 with Go's two-digit exponent 1e-07", `1e-7`, `1e-7`},
		{"negative zero written as -0", `-0`, `0`},
		{"0.000001 in exponent form", `0.000001`, `0.000001`},
		{"a number below 1e21 in exponent form", `1.2345678901234568e20`, `123456789012345680000`},
		{"an integer float64 holds exactly written in another form", `100`, `100`},
		{"nesting at the depth bound refused", strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth),
			strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth)},
	}
	vectors := []struct {
		name, in, want string
	}{
		{"zero from a zero exponent", `0e+0`, `0`},
		{"the smallest subnormal", `5e-324`, `5e-324`},
		{"the negative smallest subnormal", `-5e-324`, `-5e-324`},
		{"the largest double", `1.7976931348623157e+308`, `1.7976931348623157e+308`},
		{"the negative largest double", `-1.7976931348623157e+308`, `-1.7976931348623157e+308`},
		{"2^53 in exponent form", `9.007199254740992e+15`, `9007199254740992`},
		{"-2^53 in exponent form", `-9.007199254740992e+15`, `-9007199254740992`},
		{"2^68 not rounded to 17 digits", `2.9514790517935283e+20`, `295147905179352830000`},
		{"just below 1e23", `9.999999999999997e+22`, `9.999999999999997e+22`},
		{"1e23", `1e+23`, `1e+23`},
		{"just above 1e23", `1.0000000000000001e+23`, `1.0000000000000001e+23`},
		{"the double before the one before 1e21", `9.999999999999997e+20`, `999999999999999700000`},
		{"the double before 1e21", `9.999999999999999e+20`, `999999999999999900000`},
		{"1e21 from its exponent form", `1e+21`, `1e+21`},
		{"the double before 1e-6", `9.999999999999997e-7`, `9.999999999999997e-7`},
		{"1e-6 in exponent form", `1e-6`, `0.000001`},
		{"a fraction of 16 digits", `3.333333333333332e+8`, `333333333.3333332`},
		{"a fraction of 17 digits", `3.3333333333333325e+8`, `333333333.33333325`},
		{"the next double", `3.333333333333333e+8`, `333333333.3333333`},
		{"the double after it", `3.333333333333334e+8`, `333333333.3333334`},
		{"a fraction of 17 digits after it", `3.3333333333333343e+8`, `333333333.33333343`},
		{"a negative number just above 1e-6", `-3.3333333333333333e-6`, `-0.0000033333333333333333`},
		{"a large number with one fraction digit", `1.4249539237812062e+15`, `1424953923781206.2`},
	}
	for _, v := range vectors {
		cases = append(cases, struct{ name, in, want string }{"RFC 8785 Appendix B: " + v.name, v.in, v.want})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalBytes([]byte(tc.in))
			if err != nil {
				t.Fatalf("canonicalBytes(%q) error = %v, want %q", tc.in, err, tc.want)
			}
			if string(got) != tc.want {
				t.Errorf("canonicalBytes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	t.Run("a record canonicalized in another form than encoding/json's sorted, compact one", func(t *testing.T) {
		for _, rec := range []Record{ledgerTestRun(), ledgerTestCheck(), ledgerTestEvent(EventVerdict)} {
			line, err := json.Marshal(rec)
			if err != nil {
				t.Fatalf("json.Marshal(%T) error = %v", rec, err)
			}
			got, err := canonicalBytes(line)
			if err != nil {
				t.Fatalf("canonicalBytes(%T) error = %v, want nil", rec, err)
			}
			if want := oracleBytes(t, line); !bytes.Equal(got, want) {
				t.Errorf("canonicalBytes(%T) = %s, want %s", rec, got, want)
			}
		}
	})
}

// oracleBytes returns the RFC 8785 form of a ledger line without canonicalBytes: encoding/json
// writes a map with its keys sorted, no whitespace and its numbers in ECMAScript form, which is the
// RFC 8785 form of a line whose keys are ASCII and whose strings hold no U+2028 or U+2029, as the
// test records are.
func oracleBytes(t *testing.T, line []byte) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal(line, &value); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", line, err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		t.Fatalf("encode %s error = %v", line, err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
}

// TestCanonicalRefuses guards against a record hashed loosely: input that is not I-JSON (RFC 7493,
// which RFC 8785 requires) has no single canonical form, so it is refused, never canonicalized in
// some form of its own (fail closed, L59). Each row names the failure it catches.
func TestCanonicalRefuses(t *testing.T) {
	deep := strings.Repeat("[", maxDepth+1) + strings.Repeat("]", maxDepth+1)
	cases := []struct {
		name, in string
	}{
		{"an empty input (no value hashed as nothing)", ``},
		{"data after the value (a second record hidden behind the first)", `{} {}`},
		{"a value nested deeper than the bound (stack exhaustion)", deep},
		{"a word that is no literal", `nul`},
		{"a sign with no digit", `-`},
		{"a number with a leading zero", `01`},
		{"a fraction with no digit", `1.`},
		{"an exponent with no digit", `1e+`},
		{"a number beyond float64 range (read as infinity)", `1e400`},
		{"an integer above 2^53 that float64 rounds (two counts, one hash)", `9007199254740993`},
		{"an integer float64 writes in another form", `1000000000000000000000`},
		{"a string with no closing quote", `"abc`},
		{"a raw control character in a string", "\"a\tb\""},
		{"a byte that is not UTF-8 (replaced by U+FFFD)", "\"\xff\""},
		{"a surrogate encoded in UTF-8", "\"\xed\xa0\x80\""},
		{"an escape JSON does not define", `"\x"`},
		{"a backslash at the end of the input", `"\`},
		{`a \u escape with fewer than four digits`, `"\u12`},
		{`a \u escape with a digit that is not hex`, `"\u12zz"`},
		{"a lone low surrogate (replaced by U+FFFD)", `"\udc00"`},
		{"a lone high surrogate at the end of a string", `"\ud800"`},
		{"a high surrogate before an escape that is no low one", `"\ud800A"`},
		{"a high surrogate before a short escape", `"\ud800\u12"`},
		{"a key twice (I-JSON; encoding/json keeps the last)", `{"a":1,"a":2}`},
		{"a key twice, once escaped", `{"a":1,"a":2}`},
		{"a key that is not a string", `{1:2}`},
		{"a member with no colon", `{"a" 1}`},
		{"a member with no value", `{"a":`},
		{"an object with a trailing comma", `{"a":1,}`},
		{"an object with no comma between members", `{"a":1 "b":2}`},
		{"an object with no closing brace", `{"a":1`},
		{"an array with no comma between values", `[1 2]`},
		{"an array with a trailing comma", `[1,]`},
		{"an array with no closing bracket", `[1`},
	}
	for _, tc := range cases {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			got, err := canonicalBytes([]byte(tc.in))
			if err == nil {
				t.Fatalf("canonicalBytes(%q) = %q, nil error, want a refusal", tc.in, got)
			}
			var refusal *core.Error
			if !errors.As(err, &refusal) || refusal.Outcome != core.OutcomeError {
				t.Errorf("canonicalBytes(%q) error = %v, want a core.Error with outcome error", tc.in, err)
			}
		})
	}
	t.Run("a refusal does not echo the value it refuses", func(t *testing.T) {
		const secret = "alice@example.com"
		for _, in := range []string{`{"` + secret + `":1,"` + secret + `":2}`, `"` + secret + `\x"`,
			"\"" + secret + "\t\"", `{"` + secret + `" 1}`} {
			_, err := canonicalBytes([]byte(in))
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Errorf("canonicalBytes(%q) error = %v, want a refusal that does not repeat the value", in, err)
			}
		}
	})
}

// FuzzCanonical guards the canonicalizer, which parses bytes read from the ledger file: it never
// panics, it accepts nothing encoding/json refuses, and what it writes is valid JSON that it reads
// back to the same bytes, so a link recomputed from canonical bytes never changes (L59).
func FuzzCanonical(f *testing.F) {
	for _, seed := range []string{`{"b":[1,{"a":"é"}],"a":null}`, `1e21`, `-0`, `9007199254740993`,
		`"😀"`, `"\ud800"`, `{"a":1,"a":2}`, `[`, `{"a":1,}`, "\"\xff\"", `1.2345678901234568e20`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := canonicalBytes(data)
		if err != nil {
			var refusal *core.Error
			if !errors.As(err, &refusal) {
				t.Fatalf("canonicalBytes(%q) error = %v, want a core.Error", data, err)
			}
			return
		}
		if !json.Valid(data) {
			t.Fatalf("canonicalBytes accepted %q, which encoding/json refuses", data)
		}
		if !utf8.Valid(out) || !json.Valid(out) {
			t.Fatalf("canonicalBytes(%q) = %q, which is not valid JSON in UTF-8", data, out)
		}
		again, err := canonicalBytes(out)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("canonical form %q reads back as %q, %v; want the same bytes", out, again, err)
		}
	})
}
