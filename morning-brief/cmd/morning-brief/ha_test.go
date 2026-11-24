package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

func TestHAIsMemoisedAndReadsToken(t *testing.T) {
	var calls atomic.Int32
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		auth.Store(r.Header.Get("Authorization"))
		w.Write([]byte(`[{"entity_id":"a.b","state":"1","attributes":{}}]`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ha_token"), []byte("secret\n"), 0o600)
	env := newEnv(time.Now(), time.UTC, brief.NewSecrets(dir), brief.Sources{HAURL: srv.URL})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, err := env.HA(context.Background()); err != nil || s.ByID["a.b"].State != "1" {
				t.Errorf("%v", err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || auth.Load() != "Bearer secret" {
		t.Errorf("calls %d auth %v", calls.Load(), auth.Load())
	}
}

func TestHAMissingTokenIsError(t *testing.T) {
	env := newEnv(time.Now(), time.UTC, brief.NewSecrets(t.TempDir()), brief.Sources{HAURL: "x"})
	if _, err := env.HA(context.Background()); err == nil {
		t.Error("want error")
	}
}
