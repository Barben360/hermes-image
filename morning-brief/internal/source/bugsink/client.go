// Package bugsink is a read-only client for Bugsink's canonical API.
package bugsink

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxPages = 10

// Client talks to Bugsink at BaseURL, presenting Host as the Host header.
type Client struct {
	BaseURL string
	Host    string
	Token   string
	HTTP    *http.Client
}

// Project is one Bugsink project.
type Project struct {
	ID   string
	Name string
}

// Issue is one issue of a project.
type Issue struct {
	ID         string
	Title      string
	FirstSeen  time.Time
	LastSeen   time.Time
	EventCount int
}

type page struct {
	Next    *string            `json:"next"`
	Results *[]json.RawMessage `json:"results"`
}

func (c *Client) getPage(ctx context.Context, path string, q url.Values) (page, error) {
	var p page
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return p, fmt.Errorf("bugsink: bad request")
	}
	if c.Host != "" {
		req.Host = c.Host
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return p, fmt.Errorf("bugsink: request failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return p, fmt.Errorf("bugsink: reading answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return p, fmt.Errorf("bugsink: HTTP %d on %s", resp.StatusCode, path)
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Results == nil {
		return p, fmt.Errorf("bugsink: bad answer on %s", path)
	}
	return p, nil
}

// cursorOf extracts the cursor of a "next" link; only the cursor is reused, so the token
// is never sent to a host named by the server's answer.
func cursorOf(next *string) string {
	if next == nil || *next == "" {
		return ""
	}
	u, err := url.Parse(*next)
	if err != nil {
		return ""
	}
	return u.Query().Get("cursor")
}

// all follows the cursor pages of path; fn returns false to stop early, or an error.
// Running out of pages while a next page exists is an error, never a silent cut.
func (c *Client) all(ctx context.Context, path string, q url.Values, fn func(json.RawMessage) (bool, error)) error {
	for i := 0; i < maxPages; i++ {
		p, err := c.getPage(ctx, path, q)
		if err != nil {
			return err
		}
		for _, r := range *p.Results {
			more, err := fn(r)
			if err != nil {
				return err
			}
			if !more {
				return nil
			}
		}
		cur := cursorOf(p.Next)
		if cur == "" {
			return nil
		}
		q.Set("cursor", cur)
	}
	return fmt.Errorf("bugsink: more than %d pages on %s, answer truncated", maxPages, path)
}

func idString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// Projects lists the projects the token can read.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := c.all(ctx, "/api/canonical/0/projects/", url.Values{}, func(r json.RawMessage) (bool, error) {
		var p struct {
			ID   json.RawMessage `json:"id"`
			Name string          `json:"name"`
		}
		if json.Unmarshal(r, &p) != nil || len(p.ID) == 0 {
			return false, fmt.Errorf("bugsink: unreadable project row")
		}
		out = append(out, Project{ID: idString(p.ID), Name: p.Name})
		return true, nil
	})
	return out, err
}

func parseTime(s string) time.Time {
	for _, l := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", time.RFC3339} {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// IssuesSince lists the issues of a project first seen at or after since. Issues are read
// most recently seen first; an issue first seen in the window was also last seen in it,
// so reading stops at the first issue last seen before since.
func (c *Client) IssuesSince(ctx context.Context, project string, since time.Time) ([]Issue, error) {
	var out []Issue
	q := url.Values{"project": {project}, "sort": {"last_seen"}, "order": {"desc"}}
	err := c.all(ctx, "/api/canonical/0/issues/", q, func(r json.RawMessage) (bool, error) {
		var v struct {
			ID           json.RawMessage `json:"id"`
			Title        string          `json:"title"`
			Type         string          `json:"calculated_type"`
			Value        string          `json:"calculated_value"`
			FirstSeen    string          `json:"first_seen"`
			LastSeen     string          `json:"last_seen"`
			DigestedHits int             `json:"digested_event_count"`
		}
		if json.Unmarshal(r, &v) != nil || len(v.ID) == 0 {
			return false, fmt.Errorf("bugsink: unreadable issue row")
		}
		first := parseTime(v.FirstSeen)
		if first.IsZero() {
			return false, fmt.Errorf("bugsink: unreadable first_seen on an issue")
		}
		last := parseTime(v.LastSeen)
		if !last.IsZero() && last.Before(since) {
			return false, nil
		}
		if first.Before(since) {
			return true, nil
		}
		title := strings.TrimSpace(strings.Trim(v.Type+": "+v.Value, ": "))
		if title == "" {
			title = v.Title
		}
		if title == "" {
			title = "(untitled)"
		}
		out = append(out, Issue{ID: idString(v.ID), Title: title, FirstSeen: first, LastSeen: last, EventCount: v.DigestedHits})
		return true, nil
	})
	return out, err
}
