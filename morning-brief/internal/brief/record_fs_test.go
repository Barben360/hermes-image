package brief

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordScrubsSecrets(t *testing.T) {
	sdir := t.TempDir()
	os.WriteFile(filepath.Join(sdir, "tok"), []byte("tok-VALUE-9876\n"), 0o600)
	s := NewSecrets(sdir)
	if _, err := s.Read("tok"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"echo":"tok-VALUE-9876","n":1}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	rec := NewDirRecorder(dir, s, false)
	c := &http.Client{Transport: NewRecordingTransport(nil, rec)}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+"/v1/forecast?latitude=45.0&longitude=5.0", nil)
	req.Header.Set("Authorization", "Bearer tok-VALUE-9876")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "tok-VALUE-9876") {
		t.Fatalf("the live caller must still get the real body: %s", b)
	}
	if err := rec.Save("direct", map[string]string{"k": "tok-VALUE-9876"}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) < 2 {
		t.Fatalf("files: %v", files)
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for _, bad := range []string{"tok-VALUE-9876", "Authorization", "Bearer", "latitude=", "longitude="} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("%s contains %q: %s", f, bad, raw)
			}
		}
	}
	raw, _ := os.ReadFile(files[0])
	if !strings.Contains(string(raw), "[redacted]") && !strings.Contains(readAll(t, files), "[redacted]") {
		t.Error("no [redacted] marker")
	}
}

func readAll(t *testing.T, files []string) string {
	var sb strings.Builder
	for _, f := range files {
		b, _ := os.ReadFile(f)
		sb.Write(b)
	}
	return sb.String()
}

func TestReplayServesRecordedAnswersWithoutNetwork(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(201)
		w.Write([]byte("hello"))
	}))
	dir := t.TempDir()
	rec := NewDirRecorder(dir, NewSecrets(t.TempDir()), false)
	get := func(c *http.Client, url string) (int, string) {
		resp, err := c.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	live := &http.Client{Transport: NewRecordingTransport(nil, rec)}
	url := srv.URL + "/a/b"
	get(live, url)
	srv.Close()
	rp := &http.Client{Transport: NewRecordingTransport(nil, NewDirRecorder(dir, NewSecrets(t.TempDir()), true))}
	if code, body := get(rp, url); code != 201 || body != "hello" {
		t.Errorf("replay: %d %q", code, body)
	}
	if _, err := rp.Get(srv.URL + "/never/recorded"); err == nil {
		t.Error("an unrecorded request must fail in replay")
	}
	if hits != 1 {
		t.Errorf("hits %d", hits)
	}
}
