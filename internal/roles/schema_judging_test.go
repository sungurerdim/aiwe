package roles

import (
	"errors"
	"github.com/sungurerdim/aiwe/internal/core"
	"reflect"
	"strings"
	"testing"
)

func citedCriteria() []string {
	return []string{
		"a-finding-line-becomes-one-deduplicated-finding-with-derived-severity",
		"a-red-without-a-cited-criterion-or-dr-opens-no-fix-round",
	}
}
func findingLine(location, rule, evidence, impact, class string) string {
	return "finding: " + location + " · " + rule + " · " + evidence + " · " + impact + " · " + class
}
func roleReturn(affected []string, severity, body string) string {
	var b strings.Builder
	if len(affected) > 0 {
		b.WriteString("Affected: " + strings.Join(affected, ", ") + "\n")
	}
	if severity != "" {
		b.WriteString("Severity: " + severity + "\n")
	}
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
func mustJudgement(t *testing.T, text string) Judgement {
	t.Helper()
	got, err := ParseJudgement(text, citedCriteria())
	if err != nil {
		t.Fatalf("ParseJudgement: %v", err)
	}
	return got
}
func findingByKey(findings []Finding, location, rule string) (Finding, bool) {
	for _, f := range findings {
		if f.Location == location && f.Rule == rule {
			return f, true
		}
	}
	return Finding{}, false
}
func assertDerived(t *testing.T, affected []string, f Finding) {
	t.Helper()
	want := "high"
	if len(affected) == 0 {
		want = "unknown"
	} else if f.Class == "nice" {
		want = "low"
	}
	if f.Severity != want {
		t.Fatalf("severity %q, want %q derived from affected × %s", f.Severity, want, f.Class)
	}
}
func TestFindingLineDedupesByLocationAndRule(t *testing.T) {
	t.Parallel()
	affected := []string{"developer", "system"}
	first := findingLine("internal/roles/schema.go:42:7", "DR-12",
		"got nil, want an error", "a weakened assertion can ship", "must")
	dup := findingLine("internal/roles/schema.go:42:7", "DR-12",
		"got a pass, want a failure", "the same check was counted twice", "must")
	otherRule := findingLine("internal/roles/schema.go:42:7", "DR-9",
		"got an edit outside the file set, want a refusal", "a change leaves the declared file set", "must")
	otherLoc := findingLine("internal/engine/unit.go:81", "DR-9",
		"got 6 source files, want at most 5", "a wide unit is dispatched", "nice")
	got := mustJudgement(t, roleReturn(affected, "", first+"\n"+dup+"\n\n"+otherRule+"\n"+otherLoc))
	if got.FixRound {
		t.Fatal("a return with no VERDICT opened a fix round")
	}
	if !reflect.DeepEqual(got.Affected, affected) {
		t.Fatalf("Affected = %#v, want %#v", got.Affected, affected)
	}
	if len(got.Findings) != 3 {
		t.Fatalf("findings = %d, want 3 after collapsing the duplicate location and rule", len(got.Findings))
	}
	kept, ok := findingByKey(got.Findings, "internal/roles/schema.go:42:7", "DR-12")
	if !ok || kept.Evidence != "got nil, want an error" || kept.Impact != "a weakened assertion can ship" || kept.Class != "must" {
		t.Fatalf("first finding = %#v, want the first line kept", kept)
	}
	assertDerived(t, affected, kept)
	byRule, ok := findingByKey(got.Findings, "internal/roles/schema.go:42:7", "DR-9")
	if !ok || byRule.Evidence != "got an edit outside the file set, want a refusal" || byRule.Class != "must" {
		t.Fatalf("same location, other rule = %#v", byRule)
	}
	assertDerived(t, affected, byRule)
	byLoc, ok := findingByKey(got.Findings, "internal/engine/unit.go:81", "DR-9")
	if !ok || byLoc.Class != "nice" || byLoc.Evidence != "got 6 source files, want at most 5" {
		t.Fatalf("other location = %#v", byLoc)
	}
	assertDerived(t, affected, byLoc)
}
func TestSeverityFollowsAffectedAndClass(t *testing.T) {
	t.Parallel()
	must, err := Severity([]string{"developer", "system"}, "must")
	if err != nil || must != "high" {
		t.Fatalf("Severity(developer, system, must) = %q, %v, want high", must, err)
	}
	flipped, err := Severity([]string{"system", "developer"}, "must")
	if err != nil || flipped != "high" {
		t.Fatalf("Severity(system, developer, must) = %q, %v, want high", flipped, err)
	}
	nice, err := Severity([]string{"developer", "system"}, "nice")
	if err != nil || nice != "low" {
		t.Fatalf("Severity(developer, system, nice) = %q, %v, want low", nice, err)
	}
	if got, err := Severity([]string{"owner"}, "must"); err != nil || got != "high" {
		t.Fatalf("Severity(owner, must) = %q, %v, want high", got, err)
	}
	unknown, err := Severity(nil, "must")
	if err != nil || unknown != "unknown" {
		t.Fatalf("Severity(no affected, must) = %q, %v, want unknown", unknown, err)
	}
	if _, err := Severity([]string{"developer"}, "critical"); err == nil {
		t.Fatal("accepted class critical")
	}
	lone := mustJudgement(t, findingLine("internal/roles/schema.go:42", "DR-12",
		"got nil, want an error", "a weakened assertion can ship", "must"))
	if len(lone.Findings) != 1 || lone.FixRound {
		t.Fatalf("lone return = %#v, want one finding and no fix round", lone)
	}
	f := lone.Findings[0]
	if f.Location != "internal/roles/schema.go:42" || f.Rule != "DR-12" || f.Class != "must" ||
		f.Evidence != "got nil, want an error" || f.Impact != "a weakened assertion can ship" || f.Severity != "unknown" {
		t.Fatalf("lone finding = %#v, want the parsed line and severity unknown", f)
	}
}
func TestContradictingSeverityIsUnitInvalid(t *testing.T) {
	t.Parallel()
	affected := []string{"developer", "system"}
	line := findingLine("internal/roles/schema.go:42", "DR-12",
		"got nil, want an error", "a weakened assertion can ship", "must")
	t.Run("matches derived", func(t *testing.T) {
		t.Parallel()
		got := mustJudgement(t, roleReturn(affected, "high", line))
		if len(got.Findings) != 1 || got.Findings[0].Severity != "high" {
			t.Fatalf("finding = %#v, want severity high", got.Findings)
		}
	})
	for _, stated := range []string{"low", "critical"} {
		t.Run("contradicts with "+stated, func(t *testing.T) {
			t.Parallel()
			_, err := ParseJudgement(roleReturn(affected, stated, line), citedCriteria())
			var e *core.Error
			if !errors.As(err, &e) || e.Outcome != "unit_invalid" {
				t.Fatalf("stated %q: got %v, want outcome unit_invalid", stated, err)
			}
		})
	}
}
func TestFindingLineMasksSecrets(t *testing.T) {
	t.Parallel()
	const token = "ghp_7Hq2xKfLm9Pz3RtWvYb4Nc8Jd1Ae5Gs6Uo0I"
	const password = "correct-horse-battery-staple"
	line := findingLine(
		"internal/roles/schema.go:42",
		"DR-19",
		"the log line carried "+token+" and stayed",
		"the client sent https://dev:"+password+"@github.com/example/repo.git",
		"must",
	)
	got := mustJudgement(t, roleReturn([]string{"developer"}, "", line))
	if got.FixRound || len(got.Findings) != 1 {
		t.Fatalf("return = %#v, want one finding and no fix round", got)
	}
	f := got.Findings[0]
	blob := f.Location + "\n" + f.Rule + "\n" + f.Evidence + "\n" + f.Impact + "\n" + f.Class + "\n" + f.Severity
	if strings.Contains(blob, token) || strings.Contains(blob, password) || strings.Contains(blob, "ghp_") {
		t.Fatalf("parsed finding still carries a secret: %q", blob)
	}
	if f.Location != "internal/roles/schema.go:42" || f.Rule != "DR-19" || f.Class != "must" ||
		!strings.Contains(f.Evidence, "the log line carried") || !strings.Contains(f.Evidence, "and stayed") ||
		!strings.Contains(f.Evidence, core.ConfigMasked) || !strings.Contains(f.Impact, "the client sent") ||
		!strings.Contains(f.Impact, core.ConfigMasked) {
		t.Fatalf("finding = %#v, want the words around each secret and %s", f, core.ConfigMasked)
	}
	assertDerived(t, []string{"developer"}, f)
}
func TestRedFixRoundFollowsCitation(t *testing.T) {
	t.Parallel()
	got := mustJudgement(t, roleReturn([]string{"developer", "system"}, "",
		"VERDICT: RED\nThe diff adds an unmeasured helper.\n"))
	if got.FixRound || len(got.Findings) != 1 || !reflect.DeepEqual(got.Affected, []string{"developer", "system"}) ||
		!strings.Contains(got.Findings[0].Evidence, "unmeasured helper") {
		t.Fatalf("uncited RED = %#v, want one finding and no fix round", got)
	}
	assertDerived(t, got.Affected, got.Findings[0])
	stdLine := findingLine("internal/roles/schema.go:42", "STD-25",
		"got a token in the log, want it masked", "a secret can leave in the return", "must")
	got = mustJudgement(t, roleReturn([]string{"system"}, "", "VERDICT: RED\n"+stdLine))
	f, ok := findingByKey(got.Findings, "internal/roles/schema.go:42", "STD-25")
	if got.FixRound || !ok || len(got.Findings) != 1 {
		t.Fatalf("STD-25 RED = %#v, want that finding and no fix round", got)
	}
	assertDerived(t, got.Affected, f)
	cited := "VERDICT: RED\nFAIL a-finding-line-becomes-one-deduplicated-finding-with-derived-severity: the second line was kept.\n"
	if g := mustJudgement(t, roleReturn([]string{"system"}, "", cited)); !g.FixRound {
		t.Fatalf("cited criterion = %#v, want a fix round", g)
	}
	if g := mustJudgement(t, roleReturn([]string{"developer"}, "", "VERDICT: RED\nThe skipped test breaks DR-12.\n")); !g.FixRound {
		t.Fatalf("cited DR = %#v, want a fix round", g)
	}
	line := findingLine("internal/roles/schema.go:42", "DR-12",
		"got nil, want an error", "a weakened assertion can ship", "must")
	got = mustJudgement(t, roleReturn([]string{"system"}, "", "VERDICT: RED\n"+line))
	if _, ok := findingByKey(got.Findings, "internal/roles/schema.go:42", "DR-12"); !got.FixRound || !ok || len(got.Findings) != 1 {
		t.Fatalf("RED finding line = %#v, want one finding and a fix round", got)
	}
}
func TestFindingLineRefusesMalformed(t *testing.T) {
	t.Parallel()
	bad := []struct{ name, line string }{
		{"too few fields", "finding: internal/roles/schema.go:42 · DR-12 · got nil, want an error"},
		{"class is a severity word", findingLine("internal/roles/schema.go:42", "DR-12",
			"got nil, want an error", "a weakened assertion can ship", "critical")},
		{"class has the wrong case", findingLine("internal/roles/schema.go:42", "DR-12",
			"got nil, want an error", "a weakened assertion can ship", "Must")},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseJudgement(roleReturn([]string{"system"}, "", tc.line), citedCriteria()); err == nil {
				t.Fatalf("accepted a finding line with %s", tc.name)
			}
		})
	}
}
func TestParseJudgementEmptyInput(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"", " \n\t\r\n "} {
		got, err := ParseJudgement(text, citedCriteria())
		var e *core.Error
		if !errors.As(err, &e) || e.Outcome != "unit_invalid" || !strings.Contains(e.Expected, "judgement") {
			t.Fatalf("text %q: got %#v, %v; want unit_invalid naming the missing judgement", text, got, err)
		}
		if !reflect.DeepEqual(got, Judgement{}) {
			t.Fatalf("text %q: got %#v with the error, want a zero Judgement", text, got)
		}
	}
}
func FuzzParseJudgement(f *testing.F) {
	f.Add("")
	f.Add(roleReturn([]string{"developer", "system"}, "", findingLine("internal/roles/schema.go:42", "DR-12",
		"got nil, want an error", "a weakened assertion can ship", "must")))
	f.Add("VERDICT: RED\nThe diff adds an unmeasured helper.\nAffected: system\n")
	f.Fuzz(func(t *testing.T, text string) {
		_, _ = ParseJudgement(text, citedCriteria())
	})
}
