package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestResolveTokenOrder proves the token sources are tried in order — GH_TOKEN, then GITHUB_TOKEN,
// then `gh auth token` — and that gh is optional: with no variable set and gh absent or failing the
// resolution is refused as unauthenticated, never a crash.
func TestResolveTokenOrder(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name         string
		env          map[string]string
		ghPresent    bool
		ghOutput     string
		ghError      error
		want         string
		wantErr      error
		wantGHCalled bool
	}{
		{name: "GH_TOKEN wins over GITHUB_TOKEN", env: map[string]string{"GH_TOKEN": "gh-first", "GITHUB_TOKEN": "gh-second"}, ghPresent: true, ghOutput: "gh-cli\n", want: "gh-first"},
		{name: "GITHUB_TOKEN when GH_TOKEN is unset", env: map[string]string{"GITHUB_TOKEN": "gh-second"}, ghPresent: true, ghOutput: "gh-cli\n", want: "gh-second"},
		{name: "gh auth token when both are unset", ghPresent: true, ghOutput: "gh-cli\n", want: "gh-cli", wantGHCalled: true},
		{name: "gh absent and no variable", wantErr: ErrUnauthenticated},
		{name: "gh present but failing and no variable", ghPresent: true, ghError: errors.New("gh: not logged in"), wantErr: ErrUnauthenticated, wantGHCalled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			var gh func(context.Context) (string, error)
			if tt.ghPresent {
				gh = func(context.Context) (string, error) {
					called = true
					return tt.ghOutput, tt.ghError
				}
			}
			env := func(name string) string { return tt.env[name] }
			got, err := ResolveToken(ctx, env, gh)
			if called != tt.wantGHCalled {
				t.Fatalf("gh called = %v, want %v", called, tt.wantGHCalled)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolveToken error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveToken: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveToken = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestGitHubReadsIssuesOverHTTPS proves the adapter reads the issues of a repository over TLS with
// the token in the Authorization header, parses their bodies, and writes no token to its log.
func TestGitHubReadsIssuesOverHTTPS(t *testing.T) {
	const secret = "ghp_intake-read-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	issues, err := json.Marshal([]struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		State  string `json:"state"`
	}{
		{Number: 25, Title: "Intake reads a unit issue", Body: "## Problem\nread the issue over HTTPS", State: "open"},
	})
	if err != nil {
		t.Fatalf("marshal fixture issues: %v", err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/sungurerdim/engine/issues" {
			t.Errorf("request path = %q, want %q", r.URL.Path, "/repos/sungurerdim/engine/issues")
		}
		if r.TLS == nil {
			t.Error("request arrived without TLS")
		}
		if auth := r.Header.Get("Authorization"); !strings.Contains(auth, secret) {
			t.Errorf("Authorization = %q, want it to carry the token", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(issues)
	}))
	defer srv.Close()

	gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: secret, Client: srv.Client(), Log: logger})
	units, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err != nil {
		t.Fatalf("ReadUnits: %v", err)
	}
	if len(units) != 1 || units[0].Number != 25 || !strings.Contains(units[0].Body, "read the issue over HTTPS") {
		t.Fatalf("ReadUnits = %+v, want one unit #25 with its body", units)
	}
	if strings.Contains(logs.String(), secret) {
		t.Errorf("token leaked into the log: %s", logs.String())
	}
}

// TestGitHubTokenNeverReachesErrorOrLog proves a token value cannot reach an error or a log: the
// server here echoes the Authorization header into its refusal body, so an adapter that copies the
// body verbatim into its error would leak the token.
func TestGitHubTokenNeverReachesErrorOrLog(t *testing.T) {
	const secret = "ghp_redact-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"message":"Bad credentials for %s"}`, r.Header.Get("Authorization"))
	}))
	defer srv.Close()

	gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: secret, Client: srv.Client(), Log: logger})
	_, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err == nil {
		t.Fatal("ReadUnits on a 401 = nil error, want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("token leaked into the error: %v", err)
	}
	if strings.Contains(logs.String(), secret) {
		t.Errorf("token leaked into the log: %s", logs.String())
	}
}

// leakingTransport is an HTTP transport that echoes the request's Authorization header into its
// error, standing for a proxy that reports a failed request together with its headers (DR-19).
type leakingTransport struct{}

// RoundTrip fails every request, naming the credentials it saw.
func (leakingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("proxy refused %s %s with %s", req.Method, req.URL, req.Header.Get("Authorization"))
}

// TestGitHubRedactsTokenFromTransportError proves a token carried into a transport error by a
// failing client is replaced by the redaction marker before the error and the log leave the
// adapter; the marker proves the redaction ran and is not skipped (DR-19).
func TestGitHubRedactsTokenFromTransportError(t *testing.T) {
	const secret = "ghp_transport-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	gh := NewGitHub(GitHubConfig{
		BaseURL: "https://api.github.com",
		Token:   secret,
		Client:  &http.Client{Transport: leakingTransport{}},
		Log:     logger,
	})
	_, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err == nil {
		t.Fatal("ReadUnits over a failing transport = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "redacted") {
		t.Errorf("error = %v, want the token replaced by the redaction marker", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("token leaked into the error: %v", err)
	}
	if strings.Contains(logs.String(), secret) {
		t.Errorf("token leaked into the log: %s", logs.String())
	}
}

// TestGitHubRefusesOversizedResponse proves the adapter stops at its response cap instead of
// buffering whatever a server sends (SPEC §10.1).
func TestGitHubRefusesOversizedResponse(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Repeat("a", maxIssuesResponseBytes+1)))
	}))
	defer srv.Close()

	gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: "ghp_limit-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3",
		Client: srv.Client(), Log: logger})
	_, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err == nil {
		t.Fatalf("ReadUnits over %d bytes = nil error, want a refusal", maxIssuesResponseBytes+1)
	}
	if want := fmt.Sprintf("over %d bytes", maxIssuesResponseBytes); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to name %q", err, want)
	}
}

// TestGitHubFollowsIssuePages proves the adapter reads every page of an issue list, not only the
// first: the first response links a next page, the request carries the page size, and the units of
// both pages arrive in order (SPEC §10.1).
func TestGitHubFollowsIssuePages(t *testing.T) {
	const secret = "ghp_pages-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != strconv.Itoa(issuesPerPage) {
			t.Errorf("per_page = %q, want %d", got, issuesPerPage)
		}
		w.Header().Set("Content-Type", "application/json")
		switch page := r.URL.Query().Get("page"); page {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(
				`<https://%s/repos/sungurerdim/engine/issues?per_page=%d&page=2>; rel="next"`,
				r.Host, issuesPerPage))
			_, _ = w.Write([]byte(`[{"number":25,"title":"First","body":"a"}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"number":26,"title":"Second","body":"b"}]`))
		default:
			t.Errorf("unexpected page %q", page)
		}
	}))
	defer srv.Close()

	gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: secret, Client: srv.Client()})
	units, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err != nil {
		t.Fatalf("ReadUnits: %v", err)
	}
	want := []Unit{{Number: 25, Title: "First", Body: "a"}, {Number: 26, Title: "Second", Body: "b"}}
	if !reflect.DeepEqual(units, want) {
		t.Errorf("ReadUnits = %+v, want %+v", units, want)
	}
}

// TestGitHubRefusesEndlessPages proves the adapter stops at its page cap: a server that links a
// next page on every response is refused after maxIssuePages reads instead of being followed
// forever (SPEC §5.13, §10.1).
func TestGitHubRefusesEndlessPages(t *testing.T) {
	const secret = "ghp_endless-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", fmt.Sprintf(
			`<https://%s/repos/sungurerdim/engine/issues?per_page=%d&page=%d>; rel="next"`,
			r.Host, issuesPerPage, requests+1))
		_, _ = w.Write([]byte(`[{"number":25,"title":"Always more","body":"a"}]`))
	}))
	defer srv.Close()

	gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: secret, Client: srv.Client()})
	_, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err == nil {
		t.Fatalf("ReadUnits over an endless list = nil error, want the %d-page refusal", maxIssuePages)
	}
	if want := fmt.Sprintf("over %d pages", maxIssuePages); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to name %q", err, want)
	}
	if requests != maxIssuePages {
		t.Errorf("requests = %d, want %d: the read stops at the page cap", requests, maxIssuePages)
	}
}

// TestGitHubSkipsPullRequestsAndRefusesAnotherShape proves the entries the issues endpoint returns
// for pull requests are no units, and that an entry which carries no number or no title is refused
// instead of becoming a unit (SPEC §8.4, §10.1).
func TestGitHubSkipsPullRequestsAndRefusesAnotherShape(t *testing.T) {
	const secret = "ghp_shape-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	tests := []struct {
		name    string
		body    string
		want    []Unit
		refused bool
	}{
		{
			name: "a pull request is no unit",
			body: `[{"number":25,"title":"Issue","body":"a"},` +
				`{"number":26,"title":"PR","body":"b","pull_request":{"url":"https://api.github.com/x"}}]`,
			want: []Unit{{Number: 25, Title: "Issue", Body: "a"}},
		},
		{name: "an entry without a number is refused", body: `[{"title":"no number","body":"b"}]`, refused: true},
		{name: "an entry without a title is refused", body: `[{"number":27,"title":" ","body":"c"}]`, refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: secret, Client: srv.Client()})
			units, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
			if tt.refused {
				if err == nil {
					t.Fatalf("ReadUnits(%s) = %+v, want a refusal", tt.body, units)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadUnits: %v", err)
			}
			if !reflect.DeepEqual(units, tt.want) {
				t.Errorf("ReadUnits = %+v, want %+v", units, tt.want)
			}
		})
	}
}

// TestGitHubRefusesCleartextBaseURL proves the adapter never sends the token to a base URL that is
// not https: the read is refused and no request leaves the adapter (SPEC §10.1).
func TestGitHubRefusesCleartextBaseURL(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer srv.Close()

	gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: "ghp_cleartext-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"})
	_, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err == nil {
		t.Fatal("ReadUnits over an http base URL = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error = %v, want it to name https", err)
	}
	if requests != 0 {
		t.Errorf("requests = %d, want none: the token must not leave over http", requests)
	}
}

// TestNextPageURL covers the Link header decision: a next page is resolved against the current
// page, and a next link that is not https or that names another host is refused so the token never
// follows it (SPEC §10.1).
func TestNextPageURL(t *testing.T) {
	const (
		current = "https://api.github.com/repos/sungurerdim/engine/issues?per_page=100"
		base    = "https://api.github.com"
	)
	if _, err := nextPageURL(`<http://api.github.com/x>; rel="next"`, current, base); err == nil {
		t.Error("nextPageURL(cleartext next link) = nil error, want a refusal")
	}
	if _, err := nextPageURL(`<https://evil.example/x>; rel="next"`, current, base); err == nil {
		t.Error("nextPageURL(next link on another host) = nil error, want a refusal")
	}
	got, err := nextPageURL(`<https://api.github.com/repos/sungurerdim/engine/issues?page=2>; rel="next", `+
		`<https://api.github.com/last>; rel="last"`, current, base)
	if err != nil {
		t.Fatalf("nextPageURL: %v", err)
	}
	if want := "https://api.github.com/repos/sungurerdim/engine/issues?page=2"; got != want {
		t.Errorf("nextPageURL = %q, want %q", got, want)
	}
	if next, err := nextPageURL(`<https://api.github.com/last>; rel="last"`, current, base); err != nil || next != "" {
		t.Errorf("nextPageURL(header without a next link) = %q, %v, want no page", next, err)
	}
}

// TestNextPageURLRefusesEmptyNextReference proves a `rel="next"` link with an empty reference is
// refused instead of ending the page list: fail-open would silently drop every page after this one
// (SPEC §10.1).
func TestNextPageURLRefusesEmptyNextReference(t *testing.T) {
	const (
		current = "https://api.github.com/repos/sungurerdim/engine/issues?per_page=100"
		base    = "https://api.github.com"
	)
	if next, err := nextPageURL(`<>; rel="next"`, current, base); err == nil {
		t.Errorf("nextPageURL(empty next reference) = %q, nil error, want a refusal", next)
	}
}

// TestGitHubRefusesUnparsableNextLinkWithoutLeakingToken proves a Link header whose next reference
// cannot be parsed is refused with a fixed error: the raw reference is never copied into the read
// error, so a token it carries cannot escape the adapter (DR-19, SPEC §10.1).
func TestGitHubRefusesUnparsableNextLinkWithoutLeakingToken(t *testing.T) {
	const secret = "ghp_badlink-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", fmt.Sprintf(`<https://api.github.com:%s>; rel="next"`, secret))
		_, _ = w.Write([]byte(`[{"number":25,"title":"First","body":"a"}]`))
	}))
	defer srv.Close()

	gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: secret, Client: srv.Client()})
	_, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err == nil {
		t.Fatal("ReadUnits with an unparsable next link = nil error, want a refusal")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("token leaked into the error: %v", err)
	}
}

// TestGitHubRefusesCleartextRedirect proves a redirect that would leave TLS is refused before the
// request is sent: the http target receives nothing, so the Authorization header never follows a
// Location off https (SPEC §10.1).
func TestGitHubRefusesCleartextRedirect(t *testing.T) {
	const secret = "ghp_redirect-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	requests := 0
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`[]`))
	}))
	defer plain.Close()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/repos/sungurerdim/engine/issues", http.StatusFound)
	}))
	defer srv.Close()

	gh := NewGitHub(GitHubConfig{BaseURL: srv.URL, Token: secret, Client: srv.Client()})
	_, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err == nil {
		t.Fatal("ReadUnits over a cleartext redirect = nil error, want a refusal")
	}
	if requests != 0 {
		t.Errorf("requests to the http target = %d, want none: the token must not follow the redirect", requests)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("token leaked into the error: %v", err)
	}
}

// paddingReader yields 'a' bytes, so a test can serve a large body without holding one.
type paddingReader struct{}

// Read fills p with 'a'.
func (paddingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// cappedTransport serves one full-size issue page per request in the shape the REST endpoint uses,
// each linked to the next page on the same host, so a read can be driven past its total cap without
// a real tracker; RoundTrip is that one page.
type cappedTransport struct{ requests int }

// RoundTrip returns one full-size page and a next link to the page after this request.
func (c *cappedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.requests++
	const head, tail = `[{"number":1,"title":"x","body":"`, `"}]`
	body := io.MultiReader(strings.NewReader(head),
		io.LimitReader(paddingReader{}, int64(maxIssuesResponseBytes-len(head)-len(tail))),
		strings.NewReader(tail))
	link := fmt.Sprintf(`<https://api.github.com/repos/sungurerdim/engine/issues?page=%d>; rel="next"`, c.requests+1)
	resp := new(http.Response)
	resp.StatusCode = http.StatusOK
	resp.Body = io.NopCloser(body)
	resp.Header = http.Header{"Link": []string{link}}
	return resp, nil
}

// TestGitHubRefusesOverTheTotalBytes proves the read is bounded in total and not only per page: a
// server that links page after page of full-size bodies is refused once their sum passes the cap,
// so page size times page count is never the memory the read holds (SPEC §10.1).
func TestGitHubRefusesOverTheTotalBytes(t *testing.T) {
	transport := &cappedTransport{}
	gh := NewGitHub(GitHubConfig{
		BaseURL: "https://api.github.com",
		Token:   "ghp_total-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3",
		Client:  &http.Client{Transport: transport},
	})
	units, err := gh.ReadUnits(context.Background(), RepoID{Owner: "sungurerdim", Name: "engine"})
	if err == nil {
		t.Fatalf("ReadUnits over %d pages of %d bytes = %d units, nil error, want the total-bytes refusal",
			transport.requests, maxIssuesResponseBytes, len(units))
	}
	if want := fmt.Sprintf("over %d bytes", maxIssuesTotalBytes); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to name %q", err, want)
	}
	if want := maxIssuesTotalBytes/maxIssuesResponseBytes + 1; transport.requests != want {
		t.Errorf("requests = %d, want %d: the read stops at the total cap", transport.requests, want)
	}
}

// TestGHAuthTokenWithoutGh proves the third token source is optional through the real exec path:
// with no gh on the PATH the helper fails instead of returning a token or hanging, so a machine
// without gh still resolves its token from the two variables (SPEC §10.1).
func TestGHAuthTokenWithoutGh(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	token, err := GHAuthToken(context.Background())
	if err == nil {
		t.Fatalf("GHAuthToken with no gh on the PATH = %q, nil error, want an error", token)
	}
	if token != "" {
		t.Errorf("GHAuthToken with no gh on the PATH = %q, want no token", token)
	}
}
