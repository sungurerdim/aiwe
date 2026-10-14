// Package tracker holds the tracker port, the GitHub adapter and the data the intake parser
// produces (SPEC §6.1, §10.1). Only this package knows GitHub; trackertest holds the fake. The
// adapter's HTTP and process IO lives in github_io.go, its decisions here (SPEC §5.6).
package tracker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// ErrUnauthenticated is returned when no token source yields a token (SPEC §7.4, §10.1).
var ErrUnauthenticated = errors.New("tracker: no token")

// issuesPerPage is the page size of one issue list read; 100 is GitHub's maximum (SPEC §10.1).
const issuesPerPage = 100

// maxIssuePages caps the pages ReadUnits follows, so a tracker that never stops linking a next
// page cannot make the read run forever (SPEC §10.1).
const maxIssuePages = 100

// maxIssuesTotalBytes caps the bytes one ReadUnits call reads across all its pages, so page size
// times page count is never the ceiling of what the read holds in memory (SPEC §10.1).
const maxIssuesTotalBytes = 64 << 20

// readBudget counts the bytes a read has taken in; a total over maxIssuesTotalBytes is refused
// (SPEC §10.1).
type readBudget struct{ total int }

// add counts n more bytes and refuses the total over the cap.
func (b *readBudget) add(n int) error {
	b.total += n
	if b.total > maxIssuesTotalBytes {
		return fmt.Errorf("response total over %d bytes", maxIssuesTotalBytes)
	}
	return nil
}

// RepoID names one repository (SPEC §10.1).
type RepoID struct {
	Owner string
	Name  string
}

// slug renders repo as owner/name for a log field or an error.
func (r RepoID) slug() string { return r.Owner + "/" + r.Name }

// Unit is one issue of a repository: its number, title and body (SPEC §8.4).
type Unit struct {
	Number int
	Title  string
	Body   string
}

// issue is the subset of the GitHub issue resource the adapter reads (SPEC §10.1).
type issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	// PullRequest is present on the entries the issues endpoint returns for pull requests.
	PullRequest *struct{} `json:"pull_request"`
}

// unitsOf maps the issues of one page to units: a pull request the issues endpoint lists is no
// unit, and every entry that stays must carry a number and a title, so a response of another
// shape is refused instead of becoming a unit (SPEC §8.4, §10.1).
func unitsOf(issues []issue) ([]Unit, error) {
	units := make([]Unit, 0, len(issues))
	for _, is := range issues {
		if is.PullRequest != nil {
			continue
		}
		if is.Number <= 0 || strings.TrimSpace(is.Title) == "" {
			return nil, errors.New("an issue without a number or a title")
		}
		units = append(units, Unit{Number: is.Number, Title: is.Title, Body: is.Body})
	}
	return units, nil
}

// httpsBase reports whether raw is an https URL with a host; the token is only ever sent over TLS,
// so a cleartext or hostless base URL is refused before any request is built (SPEC §10.1).
func httpsBase(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u != nil && u.Scheme == "https" && u.Host != ""
}

// linkNext returns the reference of the `rel=next` relation of a Link header and whether the header
// names that relation, quoted or not. A part that names the relation without a usable reference is
// still a named next page, so nextPageURL refuses it instead of reading it as the end of the list
// (SPEC §10.1).
func linkNext(link string) (string, bool) {
	for _, part := range strings.Split(link, ",") {
		ref, params, ok := strings.Cut(part, ";")
		if !ok {
			ref, params = "", part
		}
		if nextRel(params) {
			return strings.Trim(strings.TrimSpace(ref), "<>"), true
		}
	}
	return "", false
}

// nextRel reports whether a Link parameter list names the next relation, unquoted as `rel=next` or
// quoted as `rel="next"` (SPEC §10.1).
func nextRel(params string) bool {
	for _, param := range strings.Split(params, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
		if ok && strings.EqualFold(strings.TrimSpace(name), "rel") &&
			strings.EqualFold(strings.Trim(strings.TrimSpace(value), `"'`), "next") {
			return true
		}
	}
	return false
}

// nextPageURL returns the absolute URL of the page after current: the `rel="next"` link of the
// Link header, resolved against current. An absent next link means the list is complete; a next
// link that is empty, that is not https or that names another host is refused, so a page is never
// silently dropped and the token only ever follows a link to the tracker it was given for
// (SPEC §10.1). The reference is never copied into the error, so a token it carries cannot leave
// the adapter (DR-19).
func nextPageURL(link, current, trackerBase string) (string, error) {
	ref, ok := linkNext(link)
	if !ok {
		return "", nil
	}
	if ref == "" {
		return "", errors.New("tracker: next page: empty reference")
	}
	next, err := url.Parse(ref)
	if err != nil {
		return "", errors.New("tracker: next page: invalid reference")
	}
	if cur, err := url.Parse(current); err == nil {
		next = cur.ResolveReference(next)
	}
	if !httpsBase(next.String()) {
		return "", errors.New("tracker: next page is not https")
	}
	if !sameHost(next.String(), trackerBase) {
		return "", errors.New("tracker: next page is on another host")
	}
	return next.String(), nil
}

// sameHost reports whether raw names the same host as base: a next link to another host would carry
// the Authorization header to a server the token was never given for (SPEC §10.1).
func sameHost(raw, base string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, b.Host)
}

// Tracker is the tracker port; engine code depends on it, never on an adapter (SPEC §5.1, §10.1).
// This unit needs only ReadUnits; the write, permission and CI calls follow in later units.
type Tracker interface {
	core.Identified
	ReadUnits(ctx context.Context, repo RepoID) ([]Unit, error)
}

// requireToken returns the token a tracker read must carry: an empty token is refused as
// ErrUnauthenticated, so no adapter read leaves without credentials and an anonymous read can never
// stand in for the `tracker_unauthenticated` stop of SPEC §7.4 (SPEC §10.1, DR-19).
func requireToken(token string) (string, error) {
	if token = strings.TrimSpace(token); token != "" {
		return token, nil
	}
	return "", ErrUnauthenticated
}

// ResolveToken returns the tracker token in order: GH_TOKEN, then GITHUB_TOKEN, then the gh CLI
// when gh is present. No source yields ErrUnauthenticated; gh is never required (SPEC §10.1).
func ResolveToken(ctx context.Context, env func(string) string,
	gh func(context.Context) (string, error)) (string, error) {
	if token := strings.TrimSpace(env("GH_TOKEN")); token != "" {
		return token, nil
	}
	if token := strings.TrimSpace(env("GITHUB_TOKEN")); token != "" {
		return token, nil
	}
	if gh != nil {
		if token, err := gh(ctx); err == nil {
			if token = strings.TrimSpace(token); token != "" {
				return token, nil
			}
		}
	}
	return "", ErrUnauthenticated
}
