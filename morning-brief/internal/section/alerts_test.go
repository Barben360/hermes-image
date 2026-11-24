package section

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/grafana"
)

const amJSON = `[
 {"labels":{"alertname":"HomelabAlert","key":"backup:matrix","severity":"warning"},"annotations":{"summary":"backup failed"},"startsAt":"2026-11-25T02:12:00Z","status":{"state":"active"}},
 {"labels":{"alertname":"Watchdog","severity":"none"},"annotations":{},"startsAt":"2026-11-01T00:00:00Z","status":{"state":"active"}},
 {"labels":{"alertname":"HomelabImageOutdated","key":"outdated:img:1.0","severity":"info"},"annotations":{"summary":"newer tag 1.1"},"startsAt":"2026-11-23T07:00:00Z","status":{"state":"active"}}
]`

func testEnv(t *testing.T, url string, secrets map[string]string) *brief.Env {
	t.Helper()
	dir := t.TempDir()
	for k, v := range secrets {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loc, _ := time.LoadLocation("Europe/Paris")
	return &brief.Env{
		Now: time.Date(2026, 11, 25, 7, 15, 0, 0, loc), Loc: loc,
		HTTP: http.DefaultClient, Secrets: brief.NewSecrets(dir),
		Sources: brief.Sources{GrafanaURL: url},
	}
}

func alertsCfg() *AlertsConfig { return brief.Sections()[indexOf("alerts")].Default().(*AlertsConfig) }

func indexOf(name string) int {
	for i, s := range brief.Sections() {
		if s.Name == name {
			return i
		}
	}
	return -1
}

func factMap(it brief.Item) map[string]string {
	m := map[string]string{}
	for _, f := range it.Facts {
		m[f.Key] = f.Value
	}
	return m
}

func amServer(t *testing.T, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != grafana.PrimaryPath || r.Header.Get("Authorization") != "Bearer tok123" ||
			r.URL.RawQuery != "active=true&silenced=false&inhibited=false" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		w.Write([]byte(body))
	}))
}

func TestAlertsActiveOnly(t *testing.T) {
	srv := amServer(t, amJSON)
	defer srv.Close()
	cfg := alertsCfg()
	cfg.IncludeInfo = false
	r := collectAlerts(context.Background(), testEnv(t, srv.URL, map[string]string{"grafana_token": "tok123\n"}), cfg)
	if r.Status != brief.StatusReport || len(r.Items) != 1 {
		t.Fatalf("status %s items %d err %q", r.Status, len(r.Items), r.Err)
	}
	m := factMap(r.Items[0])
	if m["alert"] != "HomelabAlert" || m["key"] != "backup:matrix" || m["since"] != "2026-11-25 03:12" || m["kind"] != "alert" {
		t.Fatalf("item %v", m)
	}
	for _, it := range r.Items {
		if strings.Contains(factMap(it)["alert"], "Watchdog") {
			t.Fatal("Watchdog must be excluded")
		}
	}
}

func TestAlertsInfoAsNews(t *testing.T) {
	srv := amServer(t, amJSON)
	defer srv.Close()
	r := collectAlerts(context.Background(), testEnv(t, srv.URL, map[string]string{"grafana_token": "tok123"}), alertsCfg())
	if len(r.Items) != 2 {
		t.Fatalf("items %d", len(r.Items))
	}
	if k := factMap(r.Items[0])["kind"]; k != "alert" {
		t.Fatalf("first item kind %q", k)
	}
	if m := factMap(r.Items[1]); m["kind"] != "news" || m["alert"] != "HomelabImageOutdated" {
		t.Fatalf("second item %v", m)
	}
}

func TestAlertsNoneIsOK(t *testing.T) {
	srv := amServer(t, `[{"labels":{"alertname":"Watchdog"},"annotations":{},"startsAt":"2026-11-01T00:00:00Z","status":{"state":"active"}}]`)
	defer srv.Close()
	r := collectAlerts(context.Background(), testEnv(t, srv.URL, map[string]string{"grafana_token": "tok123"}), alertsCfg())
	if r.Status != brief.StatusOK || len(r.Items) != 0 {
		t.Fatalf("status %s items %d", r.Status, len(r.Items))
	}
}

func TestAlertsGrafana403IsError(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.Error(w, "forbidden tok123", http.StatusForbidden)
	}))
	defer srv.Close()
	r := collectAlerts(context.Background(), testEnv(t, srv.URL, map[string]string{"grafana_token": "tok123"}), alertsCfg())
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "403") || strings.Contains(r.Err, "tok123") {
		t.Fatalf("status %s err %q", r.Status, r.Err)
	}
	if len(paths) != 2 || paths[0] != grafana.PrimaryPath || paths[1] != grafana.FallbackPath {
		t.Fatalf("paths %v", paths)
	}
}

func TestAlertsFallbackPath(t *testing.T) {
	if grafana.PrimaryPath != "/api/alertmanager/alertmanager/api/v2/alerts" ||
		grafana.FallbackPath != "/api/datasources/proxy/uid/alertmanager/api/v2/alerts" {
		t.Fatal("paths changed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case grafana.PrimaryPath:
			http.Error(w, "no", http.StatusForbidden)
		case grafana.FallbackPath:
			w.Write([]byte(amJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	r := collectAlerts(context.Background(), testEnv(t, srv.URL, map[string]string{"grafana_token": "tok123"}), alertsCfg())
	if r.Status != brief.StatusReport || len(r.Items) != 2 {
		t.Fatalf("status %s items %d err %q", r.Status, len(r.Items), r.Err)
	}
}

func TestAlertsMissingToken(t *testing.T) {
	r := collectAlerts(context.Background(), testEnv(t, "http://127.0.0.1:1", nil), alertsCfg())
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "grafana_token") {
		t.Fatalf("%s %q", r.Status, r.Err)
	}
}

func TestAlertsMalformedEntriesSkipped(t *testing.T) {
	srv := amServer(t, `[{}]`)
	defer srv.Close()
	r := collectAlerts(context.Background(), testEnv(t, srv.URL, map[string]string{"grafana_token": "tok123"}), alertsCfg())
	if r.Status != brief.StatusOK || len(r.Items) != 0 {
		t.Fatalf("%s %d", r.Status, len(r.Items))
	}
}

func TestAlertsGarbage200IsError(t *testing.T) {
	for _, body := range []string{"<html>hi</html>", "{}", "null"} {
		srv := amServer(t, body)
		r := collectAlerts(context.Background(), testEnv(t, srv.URL, map[string]string{"grafana_token": "tok123"}), alertsCfg())
		srv.Close()
		if r.Status != brief.StatusError {
			t.Fatalf("%q: status %s", body, r.Status)
		}
	}
}
