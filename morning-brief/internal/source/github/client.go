// Package github is a read-only client for the few GitHub REST calls the brief needs.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Client talks to the GitHub REST API with one token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Issue is one search hit (an issue or a pull request).
type Issue struct {
	RepositoryURL string `json:"repository_url"`
	Number        int    `json:"number"`
	Title         string `json:"title"`
	HTMLURL       string `json:"html_url"`
	CreatedAt     string `json:"created_at"`
	User          struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullRequest *json.RawMessage `json:"pull_request"`
}

// Repo returns "owner/name" from the repository_url.
func (i Issue) Repo() string {
	parts := strings.Split(strings.TrimRight(i.RepositoryURL, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (*http.Response, []byte, error) {
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("github: bad request")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("github: request failed: %v", urlErrorReason(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("github: reading answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("github: HTTP %d on %s", resp.StatusCode, path)
	}
	return resp, body, nil
}

func urlErrorReason(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}

// Search runs an issues/PRs search (first 100 hits, newest first).
func (c *Client) Search(ctx context.Context, query string) ([]Issue, error) {
	q := url.Values{"q": {query}, "per_page": {"100"}, "sort": {"created"}, "order": {"desc"}}
	_, body, err := c.get(ctx, "/search/issues", q)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []Issue `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("github: bad search answer")
	}
	return out.Items, nil
}

var lastPage = regexp.MustCompile(`[?&]page=(\d+)[^>]*>;\s*rel="last"`)

// VisibleRepos counts the repositories the token can see (per_page=1, last page of the Link header).
func (c *Client) VisibleRepos(ctx context.Context) (int, error) {
	resp, body, err := c.get(ctx, "/user/repos", url.Values{"per_page": {"1"}})
	if err != nil {
		return 0, err
	}
	if m := lastPage.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
		return strconv.Atoi(m[1])
	}
	var repos []json.RawMessage
	if err := json.Unmarshal(body, &repos); err != nil {
		return 0, fmt.Errorf("github: bad repos answer")
	}
	return len(repos), nil
}
