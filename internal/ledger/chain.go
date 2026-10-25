package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// genesis is the prev of the first record of a ledger that continues no other: 64 zeros (L59).
const genesis = "0000000000000000000000000000000000000000000000000000000000000000"

// maxDepth bounds the nesting canonicalBytes reads, as in encoding/json, so the stack never runs out.
const maxDepth = 10000

// isHash reports whether s has the form of a link of the chain: 64 lowercase hex digits (SPEC §8.5).
func isHash(s string) bool {
	return len(s) == 2*sha256.Size && strings.Trim(s, "0123456789abcdef") == ""
}

// hashLine returns the link to the record written as line: the lowercase hex SHA-256 of its RFC 8785
// canonical bytes (L59, SPEC §8.5). A line with no canonical form is refused, never hashed otherwise.
func hashLine(line []byte) (string, error) {
	canonical, err := canonicalBytes(line)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalBytes returns the RFC 8785 (JCS) bytes of the one JSON value in data: keys sorted by UTF-16
// code units, no whitespace, strings and numbers in the form of ECMAScript JSON.stringify (SPEC §17).
// Input outside I-JSON (RFC 7493), which RFC 8785 requires, is refused, never canonicalized loosely:
// non-UTF-8 bytes, a lone surrogate, a key twice, a number beyond float64 or an integer it rewrites.
func canonicalBytes(data []byte) ([]byte, error) {
	p := &jcsParser{data: data}
	out, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.data) {
		return nil, p.refuse("one JSON value", "data after the value")
	}
	return out, nil
}

// jcsParser reads JSON text and writes each value it reads in RFC 8785 form.
type jcsParser struct {
	data []byte
	pos  int
}

// refuse returns the refusal of the input at the current byte; it names the byte, never the value.
func (p *jcsParser) refuse(expected, found string) error {
	return refuse("RFC 8785 input: "+expected, fmt.Sprintf("%s at byte %d", found, p.pos))
}

// skipSpace moves past the whitespace JSON allows between tokens (RFC 8259 §2).
func (p *jcsParser) skipSpace() {
	for p.pos < len(p.data) && strings.IndexByte(" \t\n\r", p.data[p.pos]) >= 0 {
		p.pos++
	}
}

// accept moves past c and reports whether the input held it at the current byte.
func (p *jcsParser) accept(c byte) bool {
	if p.pos < len(p.data) && p.data[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

// skip moves past any whitespace and then c, and reports whether c was there.
func (p *jcsParser) skip(c byte) bool {
	p.skipSpace()
	return p.accept(c)
}

// digits moves past a run of decimal digits and returns how many there were.
func (p *jcsParser) digits() int {
	start := p.pos
	for p.pos < len(p.data) && '0' <= p.data[p.pos] && p.data[p.pos] <= '9' {
		p.pos++
	}
	return p.pos - start
}

// value reads one JSON value at depth and returns its canonical bytes.
func (p *jcsParser) value(depth int) ([]byte, error) {
	p.skipSpace()
	if p.pos == len(p.data) {
		return nil, p.refuse("a JSON value", "the end of the input")
	}
	c := p.data[p.pos]
	switch {
	case (c == '{' || c == '[') && depth >= maxDepth:
		return nil, p.refuse(fmt.Sprintf("objects and arrays nested at most %d deep", maxDepth), "a deeper one")
	case c == '{':
		return p.object(depth + 1)
	case c == '[':
		return p.array(depth + 1)
	case c == '"':
		s, err := p.str()
		return quoteJCS(s), err
	case c == '-' || ('0' <= c && c <= '9'):
		return p.number()
	}
	return p.literal()
}

// literal reads true, false or null.
func (p *jcsParser) literal() ([]byte, error) {
	for _, lit := range []string{"true", "false", "null"} {
		if bytes.HasPrefix(p.data[p.pos:], []byte(lit)) {
			p.pos += len(lit)
			return []byte(lit), nil
		}
	}
	return nil, p.refuse("a JSON value", "a character that starts none")
}

// elements reads the elements of an object or array, from its opening byte to closing, with read.
func (p *jcsParser) elements(closing byte, read func() error) error {
	p.pos++
	if p.skip(closing) {
		return nil
	}
	for {
		if err := read(); err != nil {
			return err
		}
		if p.skip(closing) {
			return nil
		}
		if !p.skip(',') {
			return p.refuse(fmt.Sprintf("a comma or %c", closing), "another character")
		}
	}
}

// jcsMember is one member of an object: its key in UTF-16 code units (RFC 8785 §3.2.3) and its bytes.
type jcsMember struct {
	units []uint16
	bytes []byte
}

// object reads an object and writes its members sorted by key; a key twice is refused (I-JSON).
func (p *jcsParser) object(depth int) ([]byte, error) {
	var members []jcsMember
	seen := make(map[string]bool)
	err := p.elements('}', func() error {
		key, value, err := p.member(depth)
		if err != nil {
			return err
		}
		if seen[key] {
			return p.refuse("each key once in an object (I-JSON)", "a key twice")
		}
		seen[key] = true
		members = append(members, jcsMember{units: utf16.Encode([]rune(key)),
			bytes: slices.Concat(quoteJCS(key), []byte{':'}, value)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(members, func(a, b jcsMember) int { return slices.Compare(a.units, b.units) })
	parts := make([][]byte, len(members))
	for i, m := range members {
		parts[i] = m.bytes
	}
	return slices.Concat([]byte{'{'}, bytes.Join(parts, []byte{','}), []byte{'}'}), nil
}

// member reads one key and its value; the value is at depth.
func (p *jcsParser) member(depth int) (string, []byte, error) {
	p.skipSpace()
	if p.pos == len(p.data) || p.data[p.pos] != '"' {
		return "", nil, p.refuse("a key string", "another value")
	}
	key, err := p.str()
	if err != nil {
		return "", nil, err
	}
	if !p.skip(':') {
		return "", nil, p.refuse("a colon after the key", "another character")
	}
	value, err := p.value(depth)
	return key, value, err
}

// array reads an array and writes its values in their order.
func (p *jcsParser) array(depth int) ([]byte, error) {
	var parts [][]byte
	err := p.elements(']', func() error {
		value, err := p.value(depth)
		parts = append(parts, value)
		return err
	})
	if err != nil {
		return nil, err
	}
	return slices.Concat([]byte{'['}, bytes.Join(parts, []byte{','}), []byte{']'}), nil
}

// str reads the string that opens at the current byte. A raw control character, an undefined escape,
// non-UTF-8 bytes and a lone surrogate are refused (RFC 8259 §7, I-JSON), never read as U+FFFD.
func (p *jcsParser) str() (string, error) {
	var b strings.Builder
	for p.pos++; p.pos < len(p.data); {
		c := p.data[p.pos]
		switch {
		case c == '"':
			p.pos++
			return b.String(), nil
		case c == '\\':
			if err := p.escape(&b); err != nil {
				return "", err
			}
		case c < 0x20:
			return "", p.refuse("a control character written as an escape", "a raw control character")
		default:
			if err := p.char(&b); err != nil {
				return "", err
			}
		}
	}
	return "", p.refuse("a closing quote", "the end of the input")
}

// char moves past one UTF-8 character of a string and writes it to b.
func (p *jcsParser) char(b *strings.Builder) error {
	r, size := utf8.DecodeRune(p.data[p.pos:])
	if r == utf8.RuneError && size == 1 {
		return p.refuse("UTF-8 text", "a byte that is not UTF-8")
	}
	b.Write(p.data[p.pos : p.pos+size])
	p.pos += size
	return nil
}

// escape moves past the escape at the current byte and writes the character it stands for to b.
func (p *jcsParser) escape(b *strings.Builder) error {
	p.pos++
	if p.pos == len(p.data) {
		return p.refuse("an escape", "the end of the input")
	}
	c := p.data[p.pos]
	p.pos++
	if c == 'u' {
		r, err := p.unicodeEscape()
		b.WriteRune(r)
		return err
	}
	i := strings.IndexByte(`"\/bfnrt`, c)
	if i < 0 {
		return p.refuse("an escape JSON defines", "another escape")
	}
	b.WriteRune([]rune("\"\\/\b\f\n\r\t")[i])
	return nil
}

// unicodeEscape reads the digits after \u; a surrogate is read only as the high half of a pair.
func (p *jcsParser) unicodeEscape() (rune, error) {
	r, err := p.hex4()
	switch {
	case err != nil:
		return 0, err
	case 0xd800 <= r && r < 0xdc00:
		return p.lowSurrogate(r)
	case utf16.IsSurrogate(r):
		return 0, p.refuse("a low surrogate after a high one", "a lone low surrogate")
	}
	return r, nil
}

// lowSurrogate reads the escape of the low surrogate after high and returns the character of the pair.
func (p *jcsParser) lowSurrogate(high rune) (rune, error) {
	if bytes.HasPrefix(p.data[p.pos:], []byte(`\u`)) {
		p.pos += 2
		low, err := p.hex4()
		if r := utf16.DecodeRune(high, low); err != nil || r != utf8.RuneError {
			return r, err
		}
	}
	return 0, p.refuse("a low surrogate after a high one", "a lone high surrogate")
}

// hex4 reads four hex digits.
func (p *jcsParser) hex4() (rune, error) {
	if len(p.data)-p.pos >= 4 {
		if b, err := hex.DecodeString(string(p.data[p.pos : p.pos+4])); err == nil {
			p.pos += 4
			return rune(b[0])<<8 | rune(b[1]), nil
		}
	}
	return 0, p.refuse(`four hex digits after \u`, "fewer")
}

// number reads a number in the grammar of RFC 8259 §6 and returns it in ECMAScript form.
func (p *jcsParser) number() ([]byte, error) {
	start := p.pos
	p.accept('-')
	if !p.accept('0') && p.digits() == 0 {
		return nil, p.refuse("a digit", "another character")
	}
	integerEnd := p.pos
	if p.accept('.') && p.digits() == 0 {
		return nil, p.refuse("a digit after the decimal point", "another character")
	}
	if (p.accept('e') || p.accept('E')) && p.exponentDigits() == 0 {
		return nil, p.refuse("a digit in the exponent", "another character")
	}
	return p.numberForm(string(p.data[start:p.pos]), p.pos == integerEnd)
}

// exponentDigits moves past the sign and digits of an exponent and returns how many digits it read.
func (p *jcsParser) exponentDigits() int {
	if !p.accept('+') {
		p.accept('-')
	}
	return p.digits()
}

// numberForm returns the ECMAScript form of lit (RFC 8785 §3.2.2.3); integer: no fraction or exponent.
// A number beyond float64 range is refused, and so is an integer float64 writes in another form, such
// as an int64 above 2^53, which it rounds: two counts would have one hash (I-JSON, RFC 7493 §2.2).
func (p *jcsParser) numberForm(lit string, integer bool) ([]byte, error) {
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return nil, p.refuse("a number within float64 range (I-JSON)", "a number beyond it")
	}
	form := esNumber(f)
	if integer && lit != "-0" && form != lit {
		return nil, p.refuse("an integer float64 holds in the same form (I-JSON)", "an integer it would round or rewrite")
	}
	return []byte(form), nil
}

// esNumber writes the finite f as ECMAScript Number.prototype.toString does (RFC 8785 §3.2.2.3): the
// shortest decimal that reads back as f, in exponent form below 1e-6 and from 1e21 on; -0 is 0.
func esNumber(f float64) string {
	if f == 0 {
		return "0"
	}
	format := byte('f')
	if abs := math.Abs(f); abs < 1e-6 || abs >= 1e21 {
		format = 'e'
	}
	s := strconv.FormatFloat(f, format, -1, 64)
	// Go writes a one-digit negative exponent with a leading zero (1e-07); ECMAScript does not.
	if n := len(s); format == 'e' && n >= 4 && s[n-4] == 'e' && s[n-3] == '-' && s[n-2] == '0' {
		s = s[:n-2] + s[n-1:]
	}
	return s
}

// quoteJCS writes the UTF-8 s as JSON.stringify does (RFC 8785 §3.2.2.2): only a quote, a backslash
// and a control character are escaped, a control character in short form or as lowercase \u00xx.
func quoteJCS(s string) []byte {
	out := []byte{'"'}
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			out = append(out, '\\')
		case r < 0x20:
			out = append(out, controlEscape(r)...)
			continue
		}
		out = utf8.AppendRune(out, r)
	}
	return append(out, '"')
}

// controlEscape returns the JSON.stringify escape of the control character r.
func controlEscape(r rune) string {
	if i := strings.IndexRune("\b\f\n\r\t", r); i >= 0 {
		return `\` + string("bfnrt"[i])
	}
	return fmt.Sprintf(`\u%04x`, r)
}
