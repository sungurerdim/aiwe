package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewGitHubBoundsTheRead proves the adapter bounds an issue read even when the caller passes no
// client of its own: a tracker that never answers cannot hold intake open (SPEC §10.1, §5.18). The
// client of the caller that does pass one is never mutated in place.
func TestNewGitHubBoundsTheRead(t *testing.T) {
	caller := &http.Client{}
	gh := NewGitHub(GitHubConfig{BaseURL: "https://api.github.com", Token: "x", Client: caller})
	if gh.client.Timeout <= 0 {
		t.Errorf("adapter timeout over a caller client = %v, want a positive bound", gh.client.Timeout)
	}
	if caller.Timeout != 0 {
		t.Errorf("caller client timeout = %v, want the caller's client untouched", caller.Timeout)
	}
	if def := NewGitHub(GitHubConfig{BaseURL: "https://api.github.com", Token: "x"}); def.client.Timeout <= 0 {
		t.Errorf("adapter timeout over the default client = %v, want a positive bound", def.client.Timeout)
	}
}

// TestGitHubRedactsTokenFromRequestError proves a token cannot escape in the error of a request the
// adapter could not even build: the error of an unparsable URL copies that URL, which carries the
// credentials, so it is redacted before it leaves the adapter (DR-19, SPEC §10.1).
func TestGitHubRedactsTokenFromRequestError(t *testing.T) {
	const secret = "ghp_request-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	gh := NewGitHub(GitHubConfig{BaseURL: "https://api.github.com", Token: secret})
	_, _, err := gh.fetchPage(context.Background(), "https://api.github.com/"+secret+"\x7f")
	if err == nil {
		t.Fatal("fetchPage over an unparsable URL = nil error, want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("token leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "redacted") {
		t.Errorf("fetchPage error = %v, want the token replaced by the redaction marker", err)
	}
}

// TestNextPageURLReadsAnUnquotedNextRelation proves a Link header whose next relation carries no
// quotes names a next page too, so no page after this one is dropped for a missing quote; a relation
// that names the next page without a usable reference is refused instead of ending the list
// (SPEC §10.1).
func TestNextPageURLReadsAnUnquotedNextRelation(t *testing.T) {
	const (
		current = "https://api.github.com/repos/sungurerdim/engine/issues?per_page=100"
		base    = "https://api.github.com"
		second  = "https://api.github.com/repos/sungurerdim/engine/issues?page=2"
	)
	next, err := nextPageURL("<"+second+">; rel=next", current, base)
	if err != nil || next != second {
		t.Errorf("nextPageURL(unquoted next relation) = %q, %v, want %q", next, err, second)
	}
	if _, ok := linkNext("<" + second + ">; rel=last"); ok {
		t.Error("linkNext(a last relation) = a next page, want none")
	}
	if next, err := nextPageURL("rel=next", current, base); err == nil {
		t.Errorf("nextPageURL(a next relation with no reference) = %q, nil error, want a refusal", next)
	}
}

// TestGitHubFollowsAnUnquotedNextLink proves the adapter reads the page a Link header names without
// quotes: the units of both pages arrive, so an unquoted relation never shortens a read (SPEC §10.1).
func TestGitHubFollowsAnUnquotedNextLink(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`[{"number":26,"title":"Second","body":"b"}]`))
			return
		}
		w.Header().Set("Link", "<https://"+r.Host+"/repos/sungurerdim/engine/issues?page=2>; rel=next")
		_, _ = w.Write([]byte(`[{"number":25,"title":"First","body":"a"}]`))
	}))
	defer srv.Close()

	gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: "ghp_unquoted-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3", Client: srv.Client()})
	units, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err != nil {
		t.Fatalf("ReadUnits: %v", err)
	}
	if len(units) != 2 || units[0].Number != 25 || units[1].Number != 26 {
		t.Errorf("ReadUnits = %+v, want the units of both pages", units)
	}
}
