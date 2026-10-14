package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// envKeyVar and envSecret are the credential fixtures: the variable OpenCode Go reads its key from
// by name at run time, and the key value itself, which must never render anywhere (SPEC §10.2,
// DR-19).
const (
	envKeyVar = "OPENCODE_API_KEY"
	envSecret = "sk-live-3f6c9b7e1d2a4c8f"
)

// fixtureEnv is an Env over NAME=value pairs; the first pair of a name wins.
type fixtureEnv []string

// Lookup returns the value of name in the pairs.
func (e fixtureEnv) Lookup(name string) (string, bool) {
	for _, kv := range e {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == name {
			return v, true
		}
	}
	return "", false
}

// recordingEnv records every variable name the code under test looks up, so a test proves that a
// credential is read by name and that no tracker token is read at all (DR-13, DR-19).
type recordingEnv struct {
	pairs []string
	names []string
}

// Lookup returns the value of name and records the lookup.
func (e *recordingEnv) Lookup(name string) (string, bool) {
	e.names = append(e.names, name)
	return fixtureEnv(e.pairs).Lookup(name)
}

// TestKeyReadsByNameAtRunTime catches a key read once, enumerated from the environment or taken from
// another variable: Key looks its name up on every call, and a variable that is unset or empty is
// key_missing, never an empty key (DR-6, DR-19).
func TestKeyReadsByNameAtRunTime(t *testing.T) {
	env := &recordingEnv{pairs: []string{"HOME=/tmp", envKeyVar + "=" + envSecret, "GH_TOKEN=ghp_test"}}
	got, err := Key(env, envKeyVar)
	if err != nil {
		t.Fatalf("Key(%s): %v", envKeyVar, err)
	}
	if string(got) != envSecret {
		t.Errorf("Key(%s) read %q, want the value that variable holds (DR-19)", envKeyVar, got)
	}
	if len(env.names) != 1 || env.names[0] != envKeyVar {
		t.Errorf("Key looked up %v, want exactly [%s]", env.names, envKeyVar)
	}
	if _, err := Key(env, envKeyVar+"_ABSENT"); !errors.Is(err, ErrKeyMissing) {
		t.Errorf("Key on an unset variable: got %v, want %v (DR-6)", err, ErrKeyMissing)
	}
	if _, err := Key(fixtureEnv{envKeyVar + "="}, envKeyVar); !errors.Is(err, ErrKeyMissing) {
		t.Errorf("Key on an empty variable: got %v, want %v, so an empty key never authenticates (DR-6)",
			err, ErrKeyMissing)
	}
}

// TestSecretNeverRenders catches a credential that leaks through text, JSON or a log line: a Secret
// renders as the redaction only (DR-19).
func TestSecretNeverRenders(t *testing.T) {
	log, lines := testLog(t)
	key, err := Key(fixtureEnv{envKeyVar + "=" + envSecret}, envKeyVar)
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	log.Info("turn", "detail", fmt.Sprintf("authorization %v and %s and %q", key, key, key))
	holder := struct {
		Key Secret
	}{key}
	encoded, err := json.Marshal(holder)
	if err != nil {
		t.Fatalf("marshal of a Secret: %v", err)
	}
	view := lines.String() + string(encoded)
	if strings.Contains(view, envSecret) {
		t.Errorf("the credential rendered: %s (DR-19)", view)
	}
	if !strings.Contains(view, redact) {
		t.Errorf("a credential rendered as %s, want %s (DR-19)", view, redact)
	}
}

// TestRunReadsNoTrackerToken catches an adapter that falls back to a tracker token or enumerates the
// environment: with GH_TOKEN and GITHUB_TOKEN set and no key, the run fails with key_missing having
// read exactly its own variable (DR-13, DR-19).
func TestRunReadsNoTrackerToken(t *testing.T) {
	log, _ := testLog(t)
	env := &recordingEnv{pairs: []string{"HOME=/tmp", "GH_TOKEN=ghp_test", "GITHUB_TOKEN=ghp_test"}}
	client := &http.Client{Transport: noDial{t: t}}
	p := NewOpenCodeGo(OpenCodeGoConfig{Env: env, Log: log, HTTP: client})
	if _, err := p.Run(context.Background(), fixtureRequest("")); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("Run without a key: got %v, want %v (DR-6, DR-19)", err, ErrKeyMissing)
	}
	if len(env.names) != 1 || env.names[0] != envKeyVar {
		t.Errorf("Run read %v, want exactly [%s]: a tracker token is never a provider credential (DR-13)",
			env.names, envKeyVar)
	}
}
