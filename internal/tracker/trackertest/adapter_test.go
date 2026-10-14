package trackertest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sungurerdim/aiwe/internal/tracker"
)

func TestGitHubAdapterPassesContract(t *testing.T) {
	fx := fixture()
	srv := httptest.NewTLSServer(githubIssues(fx))
	defer srv.Close()
	tr := tracker.NewGitHub(tracker.GitHubConfig{
		BaseURL: srv.URL,
		Token:   "ghp_contract-7Qm2Xv9Rt4Lp8Zc1Wn6Bh3",
		Client:  srv.Client(),
	})
	Run(t, tr, fx)
}

// githubIssues serves the fixture's issues in the REST shape the GitHub adapter reads.
func githubIssues(fx Fixture) http.Handler {
	type issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		State  string `json:"state"`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+fx.Repo.Owner+"/"+fx.Repo.Name+"/issues",
		func(w http.ResponseWriter, _ *http.Request) {
			out := make([]issue, 0, len(fx.Units))
			for _, u := range fx.Units {
				out = append(out, issue{Number: u.Number, Title: u.Title, Body: u.Body, State: "open"})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		})
	return mux
}
