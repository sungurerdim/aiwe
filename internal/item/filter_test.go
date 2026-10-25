package item_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/item"
)

// bidiControls are the twelve controls of the Unicode property Bidi_Control (UAX #9): the marks,
// the embeddings, the overrides and the isolates. STD-22 refuses every one.
func bidiControls() []rune {
	return []rune{
		'\u061C', // arabic letter mark
		'\u200E', // left-to-right mark
		'\u200F', // right-to-left mark
		'\u202A', // left-to-right embedding
		'\u202B', // right-to-left embedding
		'\u202C', // pop directional formatting
		'\u202D', // left-to-right override
		'\u202E', // right-to-left override
		'\u2066', // left-to-right isolate
		'\u2067', // right-to-left isolate
		'\u2068', // first strong isolate
		'\u2069', // pop directional isolate
	}
}

func TestFilterRefusesBidiControls(t *testing.T) {
	for _, c := range bidiControls() {
		code := fmt.Sprintf("U+%04X", c)
		rows := []struct{ name, in string }{
			{"alone", string(c)},
			{"inside a word", "adm" + string(c) + "in"},
			{"inside a comment", "a <!-- " + string(c) + " --> b"},
			{"after a comment", "<!-- x -->" + string(c)},
		}
		for _, row := range rows {
			t.Run(code+" "+row.name, func(t *testing.T) {
				got, err := item.FilterText(row.in)
				if !errors.Is(err, item.ErrHiddenText) {
					t.Fatalf("FilterText(%q) = %q, %v; want ErrHiddenText", row.in, got, err)
				}
				if !strings.Contains(err.Error(), code) {
					t.Errorf("the refusal %q does not name %s", err, code)
				}
				if got != "" {
					t.Errorf("a refused text returns %q, want nothing", got)
				}
			})
		}
	}
}

// TestFilterRefusesInvalidUTF8 pins the refusal of a text that is not UTF-8 before anything is
// removed: removal must never join bytes into a control the scan did not see.
func TestFilterRefusesInvalidUTF8(t *testing.T) {
	rows := []struct{ name, in string }{
		{"U+202E split by a comment joins once the comment goes", "A\xe2\x80<!-- x -->\xaeB"},
		{"U+202E split after its first byte", "A\xe2<!-- x -->\x80\xaeB"},
		{"a lone continuation byte", "a\x80b"},
		{"an overlong slash", "\xc0\xaf"},
		{"a surrogate half", "\xed\xa0\x80"},
		{"a byte that is never UTF-8", "ok\xff"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got, err := item.FilterText(row.in)
			if !errors.Is(err, core.ErrInvalidUTF8) {
				t.Fatalf("FilterText(%q) = %q, %v; want core.ErrInvalidUTF8", row.in, got, err)
			}
			if got != "" {
				t.Errorf("a refused text returns %q, want nothing", got)
			}
		})
	}
}

func TestFilterKeepsRightToLeftText(t *testing.T) {
	in := "שלום עולם, مرحبا بالعالم"
	got, err := item.FilterText(in)
	if err != nil || got != in {
		t.Fatalf("FilterText(%q) = %q, %v; want the text unchanged", in, got, err)
	}
}

// TestFilterRefusesHidingText pins the refusal of a text that can hide content from its reader
// (SPEC-contracts §8.4): an HTML comment, declaration or processing-instruction opener, and any
// Unicode format control (category Cf). Each refused text holds the word secret, which the refusal
// must not repeat.
func TestFilterRefusesHidingText(t *testing.T) {
	rows := []struct{ name, in string }{
		{"an HTML comment hides its text", "a <!-- secret --> b"},
		{"a declaration hides its text", "<!DOCTYPE secret>"},
		{"a processing instruction hides its text", "<?php secret ?>"},
		{"</ before a space opens a bogus comment", "</ secret>"},
		{"an end tag with no name is dropped by the reader", "a</>secret"},
		{"</ at the end is no end tag", "secret </"},
	}
	for _, c := range []struct {
		name string
		r    rune
	}{
		{"U+202E right-to-left override reverses the text", 0x202E},
		{"U+2066 left-to-right isolate reorders the text", 0x2066},
		{"U+200B zero-width space hides a break", 0x200B},
		{"U+200D zero-width joiner hides a join", 0x200D},
		{"U+2060 word joiner hides a join", 0x2060},
		{"U+FEFF zero-width no-break space hides a break", 0xFEFF},
		{"U+00AD soft hyphen is invisible", 0x00AD},
		{"U+E0049 tag character carries invisible text", 0xE0049},
	} {
		rows = append(rows, struct{ name, in string }{c.name, "secret" + string(c.r) + "word"})
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got, err := item.FilterText(row.in)
			if !errors.Is(err, item.ErrHiddenText) {
				t.Fatalf("FilterText(%q) = %q, %v; want ErrHiddenText", row.in, got, err)
			}
			if got != "" {
				t.Errorf("a refused text returns %q, want nothing", got)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("the refusal %q repeats the refused text", err)
			}
		})
	}
}

// TestFilterKeepsVisibleText pins that the filter removes nothing: a text it accepts comes back byte
// for byte, Markdown code, generics and comparisons included.
func TestFilterKeepsVisibleText(t *testing.T) {
	rows := []struct{ name, in string }{
		{"a generic type", "List<T> and Map<K, V>"},
		{"a comparison", "a < b and c <= d"},
		{"an end tag", "</div>"},
		{"two arrows", "x <- y and a --> b"},
		{"a code span", "Run `go test ./...` first."},
		{"a backtick fence", "```go\nif a < b {\n}\n```"},
		{"a fence in a list", "- ```\n  code\n  ```"},
		{"a fence in a quote", "> ```\n> code\n> ```"},
		{"indented code", "    if a < b {\n        return\n    }"},
		{"Turkish text", "Çalışma öğesi: ığüşöç İĞÜŞÖÇ"},
		{"emoji without a joiner", "Ship it " + string(rune(0x1F680)) + " " + string(rune(0x2705))},
		{"an empty text", ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got, err := item.FilterText(row.in)
			if err != nil || got != row.in {
				t.Fatalf("FilterText(%q) = %q, %v; want the text unchanged", row.in, got, err)
			}
		})
	}
}

// hiddenAt returns the byte offset of the first opener or format control that the filter must
// refuse in s, or -1.
func hiddenAt(s string) int {
	for i, r := range s {
		if unicode.Is(unicode.Cf, r) || r == '<' && opensHidden(s[i+1:]) {
			return i
		}
	}
	return -1
}

// opensHidden reports whether rest, the text after a <, makes it an opener that hides text.
func opensHidden(rest string) bool {
	if rest == "" {
		return false
	}
	switch rest[0] {
	case '!', '?':
		return true
	case '/':
		return len(rest) == 1 || (rest[1] < 'a' || rest[1] > 'z') && (rest[1] < 'A' || rest[1] > 'Z')
	}
	return false
}

// FuzzFilterText feeds untrusted text to the filter: it must not panic, and it either refuses the
// text or returns it unchanged, and then the text holds no hiding opener and no format control.
func FuzzFilterText(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "a <!-- b --> c", "<!", "<?", "</", "</a>", "</ x>", "</>", "List<T>", "x <- y",
		"```\n<!-- x -->\n```", string([]byte{0xff, 0xfe}) + "<!--",
		"A" + string([]byte{0xe2, 0x80}) + "<!-- x -->" + string([]byte{0xae}) + "B",
	} {
		f.Add(seed)
	}
	for _, c := range bidiControls() {
		f.Add("adm" + string(c) + "in")
	}
	f.Add("tag" + string(rune(0xE0049)))
	f.Fuzz(func(t *testing.T, in string) {
		got, err := item.FilterText(in)
		if err != nil {
			if got != "" {
				t.Fatalf("FilterText(%q) refused but returned %q", in, got)
			}
			return
		}
		if got != in {
			t.Fatalf("FilterText(%q) = %q, want the text unchanged", in, got)
		}
		if !utf8.ValidString(in) {
			t.Fatalf("FilterText(%q) accepted a text that is not UTF-8", in)
		}
		if i := hiddenAt(got); i >= 0 {
			t.Fatalf("FilterText(%q) accepted a text that hides content at byte %d", in, i)
		}
	})
}
