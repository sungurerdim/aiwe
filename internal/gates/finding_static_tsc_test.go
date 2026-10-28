package gates

import "testing"

func TestStaticFindingFromTscText(t *testing.T) {
	const fix = anyFix
	in := "src/a.ts(10,5): error TS2322: Type 'string' is not assignable to type 'number'.\r\n" +
		"  Types of property 'x' are incompatible.\n" +
		"C:\\w\\b(1).ts(3,1): warning TS6133: 'y' is declared but never used.\n" +
		"c.ts(2,2): message TS80001: File is a CommonJS module.\n" +
		"error TS5083: Cannot read file 'tsconfig.json'.\n" +
		"\n"
	runFindingCases(t, ParseTscText, []findingCase{
		{"located and global lines", "a CR kept in the message, a continuation line read as a finding, a path with parentheses cut, message level not a note, a global error dropped", in, []StaticFinding{
			sf("tsc", "src/a.ts", 10, 5, "TS2322", "error", "Type 'string' is not assignable to type 'number'.", fix),
			sf("tsc", `C:\w\b(1).ts`, 3, 1, "TS6133", "warning", "'y' is declared but never used.", fix),
			sf("tsc", "c.ts", 2, 2, "TS80001", "note", "File is a CommonJS module.", fix),
			sf("tsc", "", 0, 0, "TS5083", "error", "Cannot read file 'tsconfig.json'.", fix),
		}},
		{"clean", "tsc's empty output (no errors) read as a failure", "", nil},
		{"summary line", "the trailing summary read as an unknown line", "Found 0 errors.\n", nil},
	})
}

func TestStaticFindingRejectsMalformedTscText(t *testing.T) {
	p := ParseTscText
	runErrCases(t, []errCase{
		{"prose", "a tool crash message read as clean", "Segmentation fault\n", "line 1 is not a diagnostic", p},
		{"json", "JSON given to the text reader read as clean", `{"errors":[]}`, "line 1 is not a diagnostic", p},
		{"second line", "the failing line number lost", "a.ts(1,1): error TS1: m\nboom\n", "line 2", p},
	})
}

func FuzzParseTscText(f *testing.F) {
	f.Add([]byte("a.ts(1,2): error TS2322: m\n  more\nerror TS5083: x\n"))
	f.Add([]byte("a.ts(99999999999,2): error TS1: m"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := ParseTscText(data)
		checkFuzzFindings(t, got, err)
	})
}
