package roles

import (
	"github.com/sungurerdim/aiwe/internal/core"
	"strings"
)

const findingSep = " · "

type Finding struct {
	Location string
	Rule     string
	Evidence string
	Impact   string
	Class    string
	Severity string
}
type Judgement struct {
	Affected []string
	Findings []Finding
	FixRound bool
}
type parsed struct {
	affected    []string
	sawAffected bool
	stated      string
	sawSeverity bool
	red         bool
	findings    []Finding
	prose       []string
}

func ParseJudgement(text string, criteria []string) (Judgement, error) {
	p, err := parseReturn(text)
	if err != nil {
		return Judgement{}, err
	}
	cited := returnCited(text, criteria)
	if p.red && !cited && len(p.findings) == 0 {
		p.findings = append(p.findings, uncitedFinding(p.prose))
	}
	if err := assignSeverity(p.findings, p.affected, p.stated, p.sawSeverity); err != nil {
		return Judgement{}, err
	}
	return Judgement{Affected: p.affected, Findings: p.findings, FixRound: p.red && cited}, nil
}
func parseReturn(text string) (parsed, error) {
	var p parsed
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" {
			continue
		}
		if err := takeLine(&p, line); err != nil {
			return parsed{}, err
		}
	}
	return p, nil
}
func takeLine(p *parsed, line string) error {
	switch {
	case strings.HasPrefix(line, "Affected:"):
		return takeAffected(p, strings.TrimSpace(line[len("Affected:"):]))
	case strings.HasPrefix(line, "Severity:"):
		return takeStated(p, strings.TrimSpace(line[len("Severity:"):]))
	case line == "VERDICT: RED":
		p.red = true
		return nil
	case strings.HasPrefix(line, "finding:"):
		return takeFinding(p, strings.TrimSpace(line[len("finding:"):]))
	default:
		p.prose = append(p.prose, line)
		return nil
	}
}
func takeAffected(p *parsed, raw string) error {
	names, err := splitAffected(raw)
	if err != nil {
		return err
	}
	if _, err := Severity(names, "must"); err != nil {
		return err
	}
	if p.sawAffected && !sameParties(p.affected, names) {
		return unitInvalid("one Affected line", "two Affected lines",
			"keep a single Affected line")
	}
	p.affected = names
	p.sawAffected = true
	return nil
}
func splitAffected(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, unitInvalid("an affected party", "a blank party",
				"list parties separated by commas, with no empty entry")
		}
		names = append(names, name)
	}
	return names, nil
}
func sameParties(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func takeStated(p *parsed, value string) error {
	if p.sawSeverity && p.stated != value {
		return unitInvalid("one Severity line", "two Severity lines",
			"keep a single Severity line")
	}
	p.stated = value
	p.sawSeverity = true
	return nil
}
func takeFinding(p *parsed, rest string) error {
	f, err := parseFinding(rest)
	if err != nil {
		return err
	}
	for _, got := range p.findings {
		if got.Location == f.Location && got.Rule == f.Rule {
			return nil
		}
	}
	p.findings = append(p.findings, f)
	return nil
}
func parseFinding(rest string) (Finding, error) {
	parts := strings.Split(rest, findingSep)
	if len(parts) != 5 {
		return Finding{}, unitInvalid("finding: location · rule · evidence · impact · class",
			maskSecrets(rest), "write five fields separated by · with class must or nice")
	}
	f := Finding{
		Location: strings.TrimSpace(parts[0]),
		Rule:     strings.TrimSpace(parts[1]),
		Evidence: strings.TrimSpace(parts[2]),
		Impact:   strings.TrimSpace(parts[3]),
		Class:    strings.TrimSpace(parts[4]),
	}
	if blankFinding(f) || (f.Class != "must" && f.Class != "nice") {
		return Finding{}, unitInvalid("a non-blank location, rule, evidence and impact, and class must or nice",
			maskSecrets(rest), "write five fields separated by · with class must or nice")
	}
	return maskFinding(f), nil
}
func blankFinding(f Finding) bool {
	return f.Location == "" || f.Rule == "" || f.Evidence == "" || f.Impact == ""
}
func maskFinding(f Finding) Finding {
	f.Location = maskSecrets(f.Location)
	f.Rule = maskSecrets(f.Rule)
	f.Evidence = maskSecrets(f.Evidence)
	f.Impact = maskSecrets(f.Impact)
	return f
}
func uncitedFinding(prose []string) Finding {
	evidence := strings.Join(prose, "\n")
	if strings.TrimSpace(evidence) == "" {
		evidence = "VERDICT: RED"
	}
	return Finding{
		Location: "return",
		Rule:     "uncited-red",
		Evidence: maskSecrets(evidence),
		Impact:   "a RED that cites no done criterion and no DR opens no fix round",
		Class:    "must",
	}
}
func assignSeverity(findings []Finding, affected []string, stated string, saw bool) error {
	for i := range findings {
		sev, err := Severity(affected, findings[i].Class)
		if err != nil {
			return err
		}
		findings[i].Severity = sev
		if saw && sev != stated {
			return unitInvalid("severity "+sev, "severity "+maskSecrets(stated),
				"remove the Severity line, or set it to the severity derived from the affected parties and the class")
		}
	}
	return nil
}
func returnCited(text string, criteria []string) bool {
	if drCite.MatchString(text) {
		return true
	}
	for _, id := range criteria {
		if id != "" && hasIdent(text, id) {
			return true
		}
	}
	return false
}
func hasIdent(text, id string) bool {
	from := 0
	for {
		i := strings.Index(text[from:], id)
		if i < 0 {
			return false
		}
		i += from
		if identBounded(text, i, i+len(id)) {
			return true
		}
		from = i + len(id)
	}
}
func identBounded(text string, from, to int) bool {
	if from > 0 && isIdentByte(text[from-1]) {
		return false
	}
	return to >= len(text) || !isIdentByte(text[to])
}
func isIdentByte(b byte) bool {
	return b == '-' || b == '_' || b == '.' ||
		(b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
func maskSecrets(s string) string {
	s = pemPrivateKey.ReplaceAllString(s, core.ConfigMasked)
	s = githubToken.ReplaceAllString(s, core.ConfigMasked)
	return maskURLPasswords(s)
}
func maskURLPasswords(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "://")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i+3])
		s = s[i+3:]
		end := authorityEnd(s)
		b.WriteString(maskAuthority(s[:end]))
		s = s[end:]
	}
}
func authorityEnd(s string) int {
	for j, r := range s {
		if r == '/' || r == '?' || r == '#' || r <= ' ' || r == '"' || r == '\'' || r == '<' || r == '>' {
			return j
		}
	}
	return len(s)
}
func maskAuthority(authority string) string {
	at := strings.LastIndex(authority, "@")
	if at <= 0 {
		return authority
	}
	userinfo := authority[:at]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		return authority
	}
	return userinfo[:colon+1] + core.ConfigMasked + authority[at:]
}
