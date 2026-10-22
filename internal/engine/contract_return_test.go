package engine

import (
	"errors"
	"github.com/sungurerdim/aiwe/internal/core"
	"strconv"
	"strings"
	"testing"
)

const returnTipText = "package engine\n" +
	"\n" +
	"// CheckReturn is not declared here.\n" +
	"func BuildContract() string { return \"CheckReturn\" }\n"

func returnTip() map[string]string {
	return map[string]string{"internal/engine/contract.go": returnTipText}
}
func TestReturnRefusesUnresolvedPathOrIdentifier(t *testing.T) {
	t.Parallel()
	const missing = "internal/engine/missing.go:3"
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"a path the tip does not have", "IMPLEMENTED: BuildContract " + missing + "\n", missing},
		{"a non-ASCII path the tip does not have", "IMPLEMENTED: internal/engine/sözleşme.go:3\n",
			"internal/engine/sözleşme.go:3"},
		{"a line past the end of the file", "IMPLEMENTED: BuildContract internal/engine/contract.go:80\n",
			"internal/engine/contract.go:80"},
		{"an identifier that is not declared", "IMPLEMENTED: CheckReturn internal/engine/contract.go:4\n",
			"CheckReturn"},
		{"a green verdict with an undeclared identifier",
			"VERDICT: GREEN\nCheckReturn internal/engine/contract.go:4\n", "CheckReturn"},
		{"a red verdict with a missing path", "VERDICT: RED\n" + missing + "\n", missing},
		{"a resolved path and a declared identifier",
			"IMPLEMENTED: BuildContract internal/engine/contract.go:4\n", ""},
		{"a return that cites nothing", "IMPLEMENTED: the change is in place\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertReturnCheck(t, tc.text, returnTip(), tc.want)
		})
	}
}
func TestReturnNumberWithoutCommandIsUnverifiedFinding(t *testing.T) {
	t.Parallel()
	const (
		head = "IMPLEMENTED: BuildContract internal/engine/contract.go:4\n"
		cmd  = "`go test ./internal/engine/`"
	)
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"a number with no command", head + "passed 12\n", "12"},
		{"a bare zero with no command", head + "exited 0\n", "0"},
		{"an exit code beside its command", head + cmd + " exited 0\n", ""},
		{"a percent names no denominator", head + cmd + " passed 75%\n", "75"},
		{"a ratio that writes its denominator", head + cmd + " passed 3 of 4\n", ""},
		{"a ratio with no command", head + "passed 3 of 4\n", "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertReturnCheck(t, tc.text, returnTip(), tc.want)
		})
	}
}
func assertReturnCheck(t *testing.T, text string, tip map[string]string, want string) {
	t.Helper()
	err := CheckReturn(text, tip)
	if want == "" {
		if err != nil {
			t.Fatalf("CheckReturn refused a resolved return: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("CheckReturn accepted a return, want unverified_finding naming %q", want)
	}
	var e *core.Error
	if !errors.As(err, &e) {
		t.Fatalf("CheckReturn err = %T, want *core.Error", err)
	}
	if e.Outcome != "unverified_finding" {
		t.Fatalf("outcome = %q, want unverified_finding", e.Outcome)
	}
	if got := core.ErrorResult(err); got.Outcome != "unverified_finding" {
		t.Fatalf("ErrorResult outcome = %q, want unverified_finding", got.Outcome)
	}
	if !returnCites(err.Error(), want) {
		t.Fatalf("refusal %q does not name %q", err.Error(), want)
	}
}
func TestReturnResolvesDotlessPathAllCapsIdentAndInterfaceMethod(t *testing.T) {
	t.Parallel()
	const cmd = "`go test ./internal/engine/`"
	readme := map[string]string{
		"internal/engine/contract.go": returnTipText,
		"README":                      "one\ntwo\nthree\n",
	}
	withEOF := map[string]string{
		"internal/engine/contract.go": "package engine\n\nconst EOF = \"end\"\n",
	}
	iface := map[string]string{
		"internal/engine/contract.go": "package engine\n\ntype Store interface {\n\tSave(path string) error\n}\n",
	}
	structTip := map[string]string{
		"internal/engine/contract.go": "package engine\n\ntype Contract struct {\n\tBytes int\n}\n",
	}
	unicode := map[string]string{"internal/engine/sözleşme.go": "package engine\n"}
	broken := map[string]string{
		"internal/engine/contract.go": "package engine\nfunc (\n",
	}
	for _, tc := range []struct {
		name string
		text string
		tip  map[string]string
		want string
	}{
		{"a dotless path the tip does not have", "IMPLEMENTED: README:3 " + cmd + "\n", returnTip(), "README:3"},
		{"a dotless path and line the tip has", "IMPLEMENTED: README:3 " + cmd + "\n", readme, ""},
		{"a dotless path line past the end", "IMPLEMENTED: README:9 " + cmd + "\n", readme, "README:9"},
		{"a non-ASCII path and line the tip has", "IMPLEMENTED: internal/engine/sözleşme.go:1\n", unicode, ""},
		{"an all-caps identifier the tip does not declare", "IMPLEMENTED: EOF is unset\n", returnTip(), "EOF"},
		{"an all-caps copy of the verdict word", "EOF VERDICT: EOF\n", returnTip(), "EOF"},
		{"the word after VERDICT is not an identifier", "VERDICT: EOF\n", returnTip(), ""},
		{"an all-caps identifier the tip declares", "IMPLEMENTED: EOF\n", withEOF, ""},
		{"a struct field the tip declares", "IMPLEMENTED: Bytes\n", structTip, ""},
		{"a name that is not a field of the struct", "IMPLEMENTED: Text\n", structTip, "Text"},
		{"an interface method the tip declares", "IMPLEMENTED: Save internal/engine/contract.go:4\n", iface, ""},
		{"a name that is not an interface method", "IMPLEMENTED: Load internal/engine/contract.go:4\n", iface, "Load"},
		{"a tip file that does not parse", "IMPLEMENTED: the change is in place\n", broken, "a tip file that parses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertReturnCheck(t, tc.text, tc.tip, tc.want)
		})
	}
}
func returnCites(msg, want string) bool {
	n, err := strconv.Atoi(want)
	if err == nil && strconv.Itoa(n) == want {
		return namesInt(msg, n)
	}
	return strings.Contains(msg, want)
}
