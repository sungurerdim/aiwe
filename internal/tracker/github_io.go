package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The IO of the tracker package: the GitHub adapter's HTTPS reads and its `gh` process. Every
// decision it needs lives in tracker.go (SPEC §5.6, §10.1).

// maxIssuesResponseBytes caps the issue list response the adapter reads; a larger body is refused
// before it is parsed (SPEC §10.1).
const maxIssuesResponseBytes = 8 << 20

// issueReadTimeout bounds one issue read, so a tracker that never answers cannot hold intake
// open forever (SPEC §10.1, §5.18).
const issueReadTimeout = 60 * time.Second

// GitHubConfig configures the GitHub adapter. A nil Client is http.DefaultClient and a nil Log is
// slog.Default (SPEC §5.3, §5.6).
type GitHubConfig struct {
	BaseURL string
	Token   string
	Client  *http.Client
	Log     *slog.Logger
}

// GitHub is the GitHub tracker adapter: it reads issues over HTTPS with the standard library and
// the token in the Authorization header (SPEC §10.1).
type GitHub struct {
	baseURL string
	token   string
	client  *http.Client
	log     *slog.Logger
}

// NewGitHub returns the adapter over cfg. A token is held without the whitespace around it, so a
// blank one is no token at all (SPEC §10.1).
func NewGitHub(cfg GitHubConfig) *GitHub {
	g := &GitHub{baseURL: cfg.BaseURL, token: strings.TrimSpace(cfg.Token), log: cfg.Log}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	g.client = httpsOnlyRedirects(client)
	if g.client.Timeout == 0 {
		g.client.Timeout = issueReadTimeout
	}
	if g.log == nil {
		g.log = slog.Default()
	}
	return g
}

// httpsOnlyRedirects returns a copy of client that refuses a redirect whose target is not https:
// the request to that target is never sent, so the Authorization header cannot follow a Location
// to a cleartext host (SPEC §10.1). The copy shares the caller's transport, so the caller's client
// is never mutated in place.
func httpsOnlyRedirects(client *http.Client) *http.Client {
	guarded := *client
	prev := client.CheckRedirect
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("tracker: redirect is not https")
		}
		if prev != nil {
			return prev(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &guarded
}

// ID is the adapter's stable identifier (SPEC §5.1).
func (g *GitHub) ID() string { return "github" }

// ReadUnits reads every issue of repo over HTTPS, page by page, and returns the units among them:
// pull requests the issues endpoint lists stay out, and the read stops at maxIssuePages pages or at
// maxIssuesTotalBytes bytes (SPEC §10.1). A read without a token is refused as ErrUnauthenticated
// before any request is built, and a base URL that is not https is refused before the token is sent
// anywhere.
func (g *GitHub) ReadUnits(ctx context.Context, repo RepoID) ([]Unit, error) {
	if _, err := requireToken(g.token); err != nil {
		return nil, fmt.Errorf("read issues: %s: %w", repo.slug(), err)
	}
	if !httpsBase(g.baseURL) {
		return nil, fmt.Errorf("read issues: %s: base URL is not https", repo.slug())
	}
	units := make([]Unit, 0, issuesPerPage)
	budget := readBudget{}
	next := g.issuesURL(repo) + "?per_page=" + strconv.Itoa(issuesPerPage)
	for page := 0; next != ""; page++ {
		if page == maxIssuePages {
			return nil, fmt.Errorf("read issues: %s: over %d pages", repo.slug(), maxIssuePages)
		}
		pageUnits, following, size, err := g.readPage(ctx, next)
		if err != nil {
			return nil, fmt.Errorf("read issues: %s: %w", repo.slug(), err)
		}
		if err := budget.add(size); err != nil {
			return nil, fmt.Errorf("read issues: %s: %w", repo.slug(), err)
		}
		units = append(units, pageUnits...)
		next = following
	}
	g.log.Debug("read issues", "repo", repo.slug(), "units", len(units))
	return units, nil
}

// readPage reads one page of the issue list: the units it holds, the URL of the page after it,
// empty on the last page, and the size of the page body in bytes.
func (g *GitHub) readPage(ctx context.Context, pageURL string) ([]Unit, string, int, error) {
	body, link, err := g.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, "", 0, err
	}
	issues, err := decodeIssues(body)
	if err != nil {
		return nil, "", 0, fmt.Errorf("parse response: %w", err)
	}
	units, err := unitsOf(issues)
	if err != nil {
		return nil, "", 0, err
	}
	next, err := nextPageURL(link, pageURL, g.baseURL)
	if err != nil {
		return nil, "", 0, err
	}
	return units, next, len(body), nil
}

// fetchPage gets one page of the issue list, with the token in the Authorization header, and
// returns its body and its Link header. A status other than 200, a transport error or a body over
// the cap is refused, and the refusal body is never copied into the error: it can echo the token
// (DR-19, SPEC §10.1). The response body is closed on every path.
func (g *GitHub) fetchPage(ctx context.Context, pageURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", g.redact(err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+g.token)
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, "", g.redact(err)
	}
	body, link, readErr := g.readPageBody(resp)
	_ = resp.Body.Close()
	return body, link, readErr
}

// readPageBody reads the status, the capped body and the Link header of resp: a status other than
// 200 and a body over maxIssuesResponseBytes are refused, and the refusal body is never copied into
// the error, which could echo the token (DR-19, SPEC §10.1).
func (g *GitHub) readPageBody(resp *http.Response) ([]byte, string, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIssuesResponseBytes+1))
	if err != nil {
		return nil, "", g.redact(err)
	}
	if len(body) > maxIssuesResponseBytes {
		return nil, "", fmt.Errorf("response over %d bytes", maxIssuesResponseBytes)
	}
	return body, resp.Header.Get("Link"), nil
}

// issuesURL is the REST issues endpoint of repo under the configured base URL.
func (g *GitHub) issuesURL(repo RepoID) string {
	return strings.TrimRight(g.baseURL, "/") + "/repos/" + url.PathEscape(repo.Owner) + "/" +
		url.PathEscape(repo.Name) + "/issues"
}

// decodeIssues parses the issue list body.
func decodeIssues(body []byte) ([]issue, error) {
	var issues []issue
	if err := json.Unmarshal(body, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

// redact removes the token from an error, so no token value reaches a log or a record (DR-19).
func (g *GitHub) redact(err error) error {
	if err == nil || g.token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), g.token, "redacted"))
}

// GHAuthToken reads `gh auth token`; it is the optional third token source ResolveToken takes. gh
// is never required: a missing or failing gh is only the absence of that source (SPEC §10.1).
func GHAuthToken(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output() //nolint:gosec // G204: gh and its arguments are fixed literals; no caller input reaches the command
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
