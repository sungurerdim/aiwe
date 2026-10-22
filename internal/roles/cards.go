package roles

// Cards returns the skeleton cards, in id order. Each call builds the cards again, so a caller
// cannot change the shipped set.
func Cards() []Card {
	return []Card{auditorCard(), engineerCard(), securityReviewerCard(), testAuthorCard()}
}

// Lookup returns the shipped card for id. A role this unit does not ship is missing.
func Lookup(id string) (Card, bool) {
	for _, c := range Cards() {
		if c.ID == id {
			return c, true
		}
	}
	return Card{}, false
}

func auditorCard() Card {
	return Card{
		ID:      "auditor",
		Purpose: "Judges work it did not write.",
		Trigger: "a writing run this role did not write has finished",
		Needs:   "the done criteria and the recorded evidence",
		Output:  "PASS or FAIL for each done criterion, with one evidence line",
		Tier:    "raised",
		May:     []string{"judge the recorded evidence against the done criteria"},
		MayNot: []Limit{
			{Text: "share a run with the writer", Gate: "DR-5"},
			{Text: "share a context with the writer", Gate: "D14"},
			{Text: "write VERDICT: into a branch commit", Gate: "verdict_in_branch"},
			{Text: "The architect and the auditor do not propose a §15 item unless new evidence " +
				"meets its trigger.", Gate: "§15"},
		},
		Body: "the writer's return is a claim; each Done criterion gets PASS or FAIL with " +
			"one evidence line; it looks for silent scope change, weakened assertion, " +
			"removed check, compatibility shim, scratch file and fail-open reasoning; a " +
			"test that only matches source text proves nothing; it never fixes.",
	}
}

func engineerCard() Card {
	return Card{
		ID:      "engineer",
		Purpose: "Implements the unit inside the file set.",
		Trigger: "a unit whose work is code in the declared file set",
		Needs:   "the unit text, its file set and the recorded red result",
		Output:  "the change on the run branch",
		Tier:    "base",
		May: []string{
			"edit inside the declared file set",
			"commit on the run branch",
		},
		MayNot: []Limit{
			{Text: "touch the test-author's files", Gate: "D1"},
			{Text: "leave the file set", Gate: "DR-9"},
			{Text: "weaken tests", Gate: "D2"},
			{Text: "ignore an error", Gate: "D4"},
			{Text: "add a TODO marker", Gate: "D7"},
			{Text: "lower the assertion count", Gate: "D9"},
			{Text: "change the test count", Gate: "D15"},
			{Text: "add secrets", Gate: "D16"},
			{Text: "add an unregistered dependency", Gate: "D17"},
		},
		Body: "\"red before\" only from a recorded result; a structural change removes no " +
			"check (DR-12); a deliberate behaviour change goes under Scope deviation; " +
			"three reds with no new cause → stop.",
	}
}

func securityReviewerCard() Card {
	return Card{
		ID:      "security-reviewer",
		Purpose: "Attacks the change; proves each finding.",
		Trigger: "a diff path in the security table",
		Needs:   "the diff, the recorded results and the security table",
		Output:  "a one-line threat model and one proved finding per attack",
		Tier:    "raised",
		May:     []string{"attack the change and prove each finding"},
		MayNot: []Limit{
			{Text: "fix the finding it reports", Gate: "DR-8"},
			{Text: "run a reproduction itself", Gate: "DR-8"},
		},
		Body: "a one-line threat model; a finding = location · reproduction the engine " +
			"runs · result · severity; unreproduced → not_measured.",
	}
}

func testAuthorCard() Card {
	return Card{
		ID:      "test-author",
		Purpose: "Writes the unit's tests before code, one commit.",
		Trigger: "a risky unit whose tests are written before code",
		Needs:   "the unit text and its done criteria",
		Output:  "one test commit that fails on base for the missing behaviour",
		Tier:    "base",
		May:     []string{"add tests that check the done criteria"},
		MayNot: []Limit{
			{Text: "write production code", Gate: "D1"},
			{Text: "add a skipped or focused test", Gate: "D10"},
			{Text: "add an empty test", Gate: "D11"},
		},
		Body: "tests check the done entries through the production entry point and fail on " +
			"base for the missing behaviour, never for an import, syntax or fixture error; " +
			"they are written at admission, as soon as intake accepts the unit (R6).",
	}
}
