package core_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

func TestText(t *testing.T) {
	// fixtures are the DR-27 fixtures: text in any human language must pass through unchanged.
	// Invisible code points are written as escapes so the fixture is reviewable.
	fixtures := []struct {
		name string
		in   []byte
	}{
		{"Turkish dotted and dotless i, s-cedilla, g-breve", []byte("İıŞşĞğ — İstanbul'da ılık bir gün")},
		{"CJK ideographs, kana and hangul", []byte("漢字とかな、한글 텍스트")},
		{"RTL Hebrew and Arabic with direction marks", []byte("שלום \u200fمرحبا بالعالم\u200e")},
		{"emoji with ZWJ sequence, skin tone and flag", []byte("\U0001F468\u200d\U0001F469\u200d\U0001F467 \U0001F44D\U0001F3FD \U0001F1F9\U0001F1F7")},
		{"combining marks kept decomposed, not normalised", []byte("e\u0301 a\u0308 I\u0307 n\u0303")},
		{"byte order mark kept at the start", []byte("\ufeffİçerik")},
		{"CRLF and lone CR kept as written", []byte("satır bir\r\nsatır iki\rson\n")},
		{"NUL and tab are valid UTF-8 and kept", []byte("a\x00b\tc")},
		{"empty input", []byte{}},
	}

	// locales are settings a careless implementation might read; none may change a byte.
	locales := []map[string]string{
		{"LANG": "C", "LC_ALL": "C", "LC_CTYPE": "C"},
		{"LANG": "tr_TR.UTF-8", "LC_ALL": "tr_TR.UTF-8", "LC_CTYPE": "tr_TR.UTF-8"},
		{"LANG": "tr_TR.ISO-8859-9", "LC_ALL": "tr_TR.ISO-8859-9", "LC_CTYPE": "tr_TR.ISO-8859-9"},
		{"LANG": "ja_JP.SJIS", "LC_ALL": "ja_JP.SJIS", "LC_CTYPE": "ja_JP.SJIS"},
	}

	t.Run("valid UTF-8 round-trips byte-exact", func(t *testing.T) {
		for _, tc := range fixtures {
			t.Run(tc.name, func(t *testing.T) {
				// Guards against a reader that normalises, case-maps, strips a BOM or rewrites line ends.
				got, err := core.ReadText(tc.in)
				if err != nil {
					t.Fatalf("ReadText(%q) error = %v, want nil", tc.in, err)
				}
				if !bytes.Equal([]byte(got), tc.in) {
					t.Fatalf("ReadText(%q) = %q, want the same bytes", tc.in, got)
				}
			})
		}
	})

	t.Run("result does not depend on the locale", func(t *testing.T) {
		for _, env := range locales {
			t.Run(env["LANG"], func(t *testing.T) {
				// Guards against text handling that reads LANG, LC_ALL or LC_CTYPE (DR-27).
				for k, v := range env {
					t.Setenv(k, v)
				}
				for _, tc := range fixtures {
					got, err := core.ReadText(tc.in)
					if err != nil {
						t.Fatalf("%s: ReadText(%q) error = %v under %v", tc.name, tc.in, err, env)
					}
					if !bytes.Equal([]byte(got), tc.in) {
						t.Fatalf("%s: ReadText(%q) = %q under %v, want the same bytes", tc.name, tc.in, got, env)
					}
				}
			})
		}
	})

	t.Run("invalid UTF-8 is refused with invalid_utf8", func(t *testing.T) {
		invalid := []struct {
			name string
			in   []byte
		}{
			{"lone 0xFF byte", []byte("ok\xffok")},
			{"truncated two-byte sequence at the end", []byte("İ\xc4")},
			{"overlong encoding of slash", []byte("\xc0\xaf")},
			{"UTF-16 surrogate encoded as UTF-8", []byte("\xed\xa0\x80")},
			{"code point above U+10FFFF", []byte("\xf4\x90\x80\x80")},
			{"ISO-8859-9 Turkish bytes", []byte("\xdd\xfd\xde\xfe\xd0\xf0")},
			{"UTF-16LE byte order mark", []byte("\xff\xfeI\x00")},
		}
		for _, tc := range invalid {
			t.Run(tc.name, func(t *testing.T) {
				// Guards against invalid input passed on as text or silently replaced with U+FFFD (DR-13).
				got, err := core.ReadText(tc.in)
				if !errors.Is(err, core.ErrInvalidUTF8) {
					t.Fatalf("ReadText(%q) error = %v, want core.ErrInvalidUTF8", tc.in, err)
				}
				if got != "" {
					t.Fatalf("ReadText(%q) = %q with an error, want empty text", tc.in, got)
				}
			})
		}
	})
}
