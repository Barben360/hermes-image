package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

// httpSection is a fake section whose only input is an HTTP call, as the real ones.
func httpSection() brief.Section {
	return brief.Section{
		Name: "fakeHTTP", Description: "http-backed", Credentials: []string{"tok"},
		Default: func() brief.SectionConfig { return &fakeCfg{Enabled: true} },
		Collect: func(ctx context.Context, env *brief.Env, c brief.SectionConfig) brief.Result {
			tok, err := env.Secrets.Read("tok")
			if err != nil {
				return brief.Result{Status: brief.StatusError, Err: err.Error()}
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", env.Sources.HAURL+"/api/thing?x=1", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := env.HTTP.Do(req)
			if err != nil {
				return brief.Result{Status: brief.StatusError, Err: "fetch failed"}
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return brief.Result{Status: brief.StatusOK, Facts: []brief.Fact{{Key: "answer", Value: string(b)}}}
		},
	}
}

func TestReplayReproducesRun(t *testing.T) {
	defer brief.UseSectionsForTest(httpSection())()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("forty-two"))
	}))
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tok"), []byte("tok-QQ99"), 0o600)
	cfg := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfg, []byte("sources:\n  ha_url: "+srv.URL+"\n"), 0o600)
	rdir := filepath.Join(dir, "rec")
	base := []string{"run", "--config", cfg, "--secrets-dir", dir, "--now", "2026-11-25T07:15:00+01:00"}
	run := func(extra ...string) string {
		var out, errb bytes.Buffer
		if code := realMain(append(append([]string{}, base...), extra...), &out, &errb); code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		return out.String()
	}
	live := run("--record", rdir)
	if !bytes.Contains([]byte(live), []byte("forty-two")) {
		t.Fatalf("live run did not use the server: %s", live)
	}
	srv.Close()
	if got := run("--replay", rdir); got != live {
		t.Errorf("replay differs.\nlive:\n%s\nreplay:\n%s", live, got)
	}
	var out, errb bytes.Buffer
	if code := realMain(append(append([]string{}, base...), "--record", rdir, "--replay", rdir), &out, &errb); code != 2 {
		t.Errorf("--record with --replay: exit %d", code)
	}
}

func TestReplayNeverDials(t *testing.T) {
	env := newEnv(time.Now(), time.UTC, brief.NewSecrets(t.TempDir()), brief.Sources{})
	useRecorder(env, brief.NewDirRecorder(t.TempDir(), env.Secrets, true), true)
	conn, err := env.Dial(context.Background(), "tcp", "127.0.0.1:1")
	if err == nil || conn != nil || !strings.Contains(err.Error(), "not recorded in replay") {
		t.Errorf("dial in replay: %v %v", conn, err)
	}
}
