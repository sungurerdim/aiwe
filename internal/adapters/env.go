package adapters

import (
	"errors"

	"github.com/sungurerdim/aiwe/internal/core"
)

// Env is the environment a provider reads its credential from, by name, at run time; it is passed
// in so a test never touches the process environment (SPEC §5.3, DR-19).
type Env interface {
	Lookup(name string) (string, bool)
}

// ErrKeyMissing reports a credential the environment does not hold: an unset or empty variable is
// key_missing, never an empty key (DR-6, DR-19).
var ErrKeyMissing = errors.New("key_missing")

// redact is what a Secret renders as: a credential that reaches a log line, an error or a report
// is redacted, so its value never leaves the environment (DR-19).
const redact = "redacted"

// Secret is an API key held in memory for one turn only. It renders as redact through fmt and
// JSON, so a credential never reaches a log line, an error or a report (DR-19).
type Secret string

// String renders the credential as the redaction only.
func (s Secret) String() string { return redact }

// MarshalJSON writes the redaction, never the credential.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redact + `"`), nil }

// Key reads the named credential from env at run time; an unset or empty variable is
// ErrKeyMissing (DR-6, DR-19).
func Key(env Env, name string) (Secret, error) {
	if env == nil {
		return "", ErrKeyMissing
	}
	value, ok := env.Lookup(name)
	if !ok || value == "" {
		return "", ErrKeyMissing
	}
	return Secret(value), nil
}

// processEnv is the Env of the running process; every lookup reads the process environment at run
// time, through the one reader of SPEC §5.3.
type processEnv struct{}

// Lookup reads one variable of the process environment.
func (processEnv) Lookup(name string) (string, bool) {
	value := core.LookupEnv(name)
	return value, value != ""
}

// ProcessEnv returns the Env of the running process (SPEC §5.3).
func ProcessEnv() Env { return processEnv{} }
