package ledger

import "fmt"

// maxFailures is how many failures a check record keeps; the rest are counted in failuresDropped
// (L76, SPEC-contracts §8.5).
const maxFailures = 100

// Attachment is one piece of evidence of a failure: the path of a Playwright trace or screenshot,
// or a log excerpt.
type Attachment struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
	Text string `json:"text,omitempty"`
}

// Failure is one entry of failures[] in a check record (L76, SPEC-contracts §8.5): a failing test
// or a lint, type or format finding, in one shape. TestLocation is the file:line where the test
// failed, or the file:line:col of a finding; SourceLocation is the deepest product frame of a test,
// or the finding's own place. A finding holds its rule as Kind and its fix hint in Message. The
// type lives here and not in package gates, which imports this package and converts into it.
type Failure struct {
	Test           string       `json:"test,omitempty"`
	Kind           string       `json:"kind"`
	Message        string       `json:"message"`
	TestLocation   string       `json:"testLocation"`
	SourceLocation string       `json:"sourceLocation,omitempty"`
	Got            string       `json:"got,omitempty"`
	Want           string       `json:"want,omitempty"`
	Rerun          []string     `json:"rerun,omitempty"`
	Attachments    []Attachment `json:"attachments,omitempty"`
}

// validate refuses an entry that names no kind, which no reader could place (DR-6).
func (f Failure) validate() error {
	if f.Kind == "" {
		return refuse("a kind for every failure", "a failure with an empty kind")
	}
	return nil
}

// SetFailures sets the failures of c to the first maxFailures of fs, in order, and the count of the
// rest as failuresDropped. A nil or empty fs is a measured empty list and a dropped count of 0, as a
// green step writes; a check that parsed no failure leaves both fields unknown and never calls this.
func (c *Check) SetFailures(fs []Failure) {
	kept := make([]Failure, min(len(fs), maxFailures))
	copy(kept, fs)
	c.Failures = Field[[]Failure]{Value: kept, Known: true}
	c.FailuresDropped.Value, c.FailuresDropped.Known = int64(len(fs)-len(kept)), true
}

// validateFailures refuses failures a check could not have set: more than maxFailures entries, an
// entry that does not validate, or a dropped count that is negative or sits beside an unknown list.
func (c Check) validateFailures() error {
	if c.FailuresDropped.Known && (!c.Failures.Known || c.FailuresDropped.Value < 0) {
		return refuse("a failuresDropped of 0 or more beside a known failures list",
			fmt.Sprintf("failuresDropped %d with failures known %t", c.FailuresDropped.Value, c.Failures.Known))
	}
	if len(c.Failures.Value) > maxFailures {
		return refuse(fmt.Sprintf("at most %d failures", maxFailures), fmt.Sprintf("%d failures", len(c.Failures.Value)))
	}
	for _, f := range c.Failures.Value {
		if err := f.validate(); err != nil {
			return err
		}
	}
	return nil
}
