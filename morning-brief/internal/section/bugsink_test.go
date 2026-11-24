package section

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

type bsFake struct {
	mu     sync.Mutex
	host   string
	auth   string
	paths  []string
	issues map[string][]map[string]any // project id -> issues (newest last_seen first)
	page2  bool
}

func bsIssue(id, title, first, last string, n int) map[string]any {
	return map[string]any{"id": id, "title": title, "first_seen": first, "last_seen": last, "digested_event_count": n}
}

func (f *bsFake) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.host, f.auth = r.Host, r.Header.Get("Authorization")
		f.paths = append(f.paths, r.URL.RequestURI())
		switch r.URL.Path {
		case "/api/canonical/0/projects/":
			_ = json.NewEncoder(w).Encode(map[string]any{"next": nil, "results": []map[string]any{
				{"id": 1, "name": "app-one"}, {"id": 2, "name": "app-two"}}})
		case "/api/canonical/0/issues/":
			p := r.URL.Query().Get("project")
			all := f.issues[p]
			if f.page2 && p == "1" {
				if r.URL.Query().Get("cursor") == "" {
					next := "https://elsewhere.invalid/api/canonical/0/issues/?cursor=abc&project=1"
					_ = json.NewEncoder(w).Encode(map[string]any{"next": next, "results": all[:1]})
					return
				}
				all = all[1:]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"next": nil, "results": all})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func bsRun(t *testing.T, f *bsFake) brief.Result {
	srv := f.server(t)
	env := b11Env(t, map[string]string{"bugsink_token": "bs-secret"},
		brief.Sources{BugsinkURL: srv.URL, BugsinkHost: "erreurs.infirlib.fr"})
	return b11Section(t, "bugsink").Collect(t.Context(), env, bugsinkDefault())
}

func TestBugsinkNewIssues(t *testing.T) {
	f := &bsFake{issues: map[string][]map[string]any{
		"1": {
			bsIssue("uuid-new", "ValueError: boom", "2026-11-25T03:00:00Z", "2026-11-25T06:00:00Z", 4),
			bsIssue("uuid-old", "Old: known", "2026-11-01T03:00:00Z", "2026-11-25T05:00:00Z", 900), // seen recently, first seen long ago
			bsIssue("uuid-dead", "Dead: gone", "2026-10-01T00:00:00Z", "2026-11-02T00:00:00Z", 3),
		},
		"2": {},
	}}
	r := bsRun(t, f)
	got := itemsText(r)
	if !strings.Contains(got, "ValueError: boom") || !strings.Contains(got, "https://erreurs.infirlib.fr/issues/issue/uuid-new/") ||
		!strings.Contains(got, "project=app-one") {
		t.Fatalf("new issue missing: %s", got)
	}
	if strings.Contains(got, "Old: known") || strings.Contains(got, "Dead: gone") {
		t.Fatalf("old issue listed: %s", got)
	}
	if r.Status != brief.StatusReport {
		t.Fatalf("status %s", r.Status)
	}
	if f.auth != "Bearer bs-secret" {
		t.Fatalf("auth %q", f.auth)
	}
}

func TestBugsinkNoneIsOK(t *testing.T) {
	f := &bsFake{issues: map[string][]map[string]any{
		"1": {bsIssue("u", "Old: x", "2026-11-01T00:00:00Z", "2026-11-25T05:00:00Z", 2)}, "2": {}}}
	r := bsRun(t, f)
	if r.Status != brief.StatusOK || len(r.Items) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestBugsinkHostHeader(t *testing.T) {
	f := &bsFake{issues: map[string][]map[string]any{}}
	bsRun(t, f)
	if f.host != "erreurs.infirlib.fr" {
		t.Fatalf("Host %q", f.host)
	}
}

func TestBugsinkVisibleProjects(t *testing.T) {
	f := &bsFake{issues: map[string][]map[string]any{}}
	r := bsRun(t, f)
	if v, ok := factVal(r.Facts, "visible_projects"); !ok || v != "2" {
		t.Fatalf("visible_projects %q %v", v, ok)
	}
}

func TestBugsinkFollowsCursorOnlyOnConfiguredHost(t *testing.T) {
	f := &bsFake{page2: true, issues: map[string][]map[string]any{
		"1": {
			bsIssue("a", "A: one", "2026-11-25T03:00:00Z", "2026-11-25T06:00:00Z", 1),
			bsIssue("b", "B: two", "2026-11-25T02:00:00Z", "2026-11-25T05:00:00Z", 1),
		}, "2": {}}}
	r := bsRun(t, f)
	got := itemsText(r)
	if !strings.Contains(got, "A: one") || !strings.Contains(got, "B: two") {
		t.Fatalf("second page not read: %s", got)
	}
	var sawCursor bool
	for _, p := range f.paths {
		if u, _ := url.Parse(p); u.Query().Get("cursor") == "abc" {
			sawCursor = true
		}
	}
	if !sawCursor {
		t.Fatalf("cursor not used: %v", f.paths)
	}
}

func TestBugsinkBadTokenIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	env := b11Env(t, map[string]string{"bugsink_token": "x"}, brief.Sources{BugsinkURL: srv.URL, BugsinkHost: "h"})
	r := b11Section(t, "bugsink").Collect(t.Context(), env, bugsinkDefault())
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "401") {
		t.Fatalf("got %+v", r)
	}
}
