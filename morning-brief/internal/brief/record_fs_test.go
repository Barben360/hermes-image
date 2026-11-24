package brief

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestReplayKeysBySectionAndBodyUnderConcurrency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Write([]byte("answer-to-" + string(b)))
	}))
	dir := t.TempDir()
	type call struct{ section, body string }
	calls := []call{{"a", "one"}, {"b", "two"}, {"a", "three"}, {"b", "one"}}
	do := func(c *http.Client, k call) string {
		ctx := WithSection(context.Background(), k.section)
		req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/same", strings.NewReader(k.body))
		resp, err := c.Do(req)
		if err != nil {
			t.Error(err)
			return ""
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	live := &http.Client{Transport: NewRecordingTransport(nil, NewDirRecorder(dir, NewSecrets(t.TempDir()), false))}
	for _, k := range calls {
		do(live, k)
	}
	srv.Close()
	for round := 0; round < 20; round++ {
		rp := &http.Client{Transport: NewRecordingTransport(nil, NewDirRecorder(dir, NewSecrets(t.TempDir()), true))}
		var wg sync.WaitGroup
		for _, k := range calls {
			wg.Add(1)
			go func(k call) {
				defer wg.Done()
				if got := do(rp, k); got != "answer-to-"+k.body {
					t.Errorf("%s/%s got %q", k.section, k.body, got)
				}
			}(k)
		}
		wg.Wait()
	}
}

func TestRecordedBodyDropsCoordinatesAndFailsLoudlyOnSaveError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"latitude":45.0,"longitude":5.0,"hourly":{"t":[1]}}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	c := &http.Client{Transport: NewRecordingTransport(nil, NewDirRecorder(dir, NewSecrets(t.TempDir()), false))}
	resp, err := c.Get(srv.URL + "/v1/forecast")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if all := readAll(t, files); strings.Contains(all, "latitude") || !strings.Contains(all, "hourly") {
		t.Errorf("recorded: %s", all)
	}
	// a directory that cannot be written makes the request fail rather than record nothing
	bad := filepath.Join(dir, "afile")
	os.WriteFile(bad, nil, 0o600)
	c2 := &http.Client{Transport: NewRecordingTransport(nil, NewDirRecorder(filepath.Join(bad, "sub"), NewSecrets(t.TempDir()), false))}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv2.Close()
	if resp, err := c2.Get(srv2.URL + "/x"); err == nil {
		resp.Body.Close()
		t.Error("record failure must fail the request")
	}
}

// A recording replays whatever host the sources are configured at: the key is section, method and
// path (a morpheus recording replays on a laptop, a fake's random port does not matter).
func TestReplayIgnoresSourceHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("from-fake")) }))
	dir := t.TempDir()
	live := &http.Client{Transport: NewRecordingTransport(nil, NewDirRecorder(dir, NewSecrets(t.TempDir()), false))}
	resp, err := live.Get(srv.URL + "/api/states")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	srv.Close()
	rp := &http.Client{Transport: NewRecordingTransport(nil, NewDirRecorder(dir, NewSecrets(t.TempDir()), true))}
	resp, err = rp.Get("http://ha.example.test:8123/api/states")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "from-fake" {
		t.Errorf("replay on another host: %q", b)
	}
}

func TestReplaying(t *testing.T) {
	if !Replaying(NewDirRecorder(t.TempDir(), nil, true)) || Replaying(NewDirRecorder(t.TempDir(), nil, false)) || Replaying(nil) {
		t.Error("Replaying is wrong")
	}
}

type countingRT struct{ n int }

func (c *countingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n++
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("inner")), Header: http.Header{}, Request: r}, nil
}

// A source that needs its own transport (pcb: a pinned CA) keeps recording and replaying through WrapNext.
func TestWrapNextKeepsRecording(t *testing.T) {
	dir := t.TempDir()
	wrap := func(rec Recorder, inner http.RoundTripper) http.RoundTripper {
		w, ok := NewRecordingTransport(nil, rec).(interface {
			WrapNext(http.RoundTripper) http.RoundTripper
		})
		if !ok {
			t.Fatal("the recording transport has no WrapNext")
		}
		return w.WrapNext(inner)
	}
	inner := &countingRT{}
	c := &http.Client{Transport: wrap(NewDirRecorder(dir, NewSecrets(t.TempDir()), false), inner)}
	resp, err := c.Get("https://pcb.example.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if inner.n != 1 {
		t.Fatalf("inner transport called %d times", inner.n)
	}
	inner2 := &countingRT{}
	c = &http.Client{Transport: wrap(NewDirRecorder(dir, NewSecrets(t.TempDir()), true), inner2)}
	resp, err = c.Get("https://pcb.example.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "inner" || inner2.n != 0 {
		t.Errorf("replay through WrapNext: %q, inner called %d times", b, inner2.n)
	}
}

// The headers a source reads (pcb's SSE Content-Type, GitHub's Link paging) replay too; no other
// header is kept (cookies, session ids).
func TestReplayKeepsContentTypeAndLinkOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Link", `<https://api.example.test/user/repos?page=7>; rel="last"`)
		w.Header().Set("Set-Cookie", "session=cookie-value-1")
		w.Header().Set("Mcp-Session-Id", "sess-value-2")
		w.Write([]byte("data: {}\n\n"))
	}))
	dir := t.TempDir()
	live := &http.Client{Transport: NewRecordingTransport(nil, NewDirRecorder(dir, NewSecrets(t.TempDir()), false))}
	resp, err := live.Get(srv.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	srv.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if all := readAll(t, files); strings.Contains(all, "cookie-value-1") || strings.Contains(all, "sess-value-2") {
		t.Errorf("recorded a header it must not: %s", all)
	}
	rp := &http.Client{Transport: NewRecordingTransport(nil, NewDirRecorder(dir, NewSecrets(t.TempDir()), true))}
	resp, err = rp.Get(srv.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(resp.Header.Get("Link"), "page=7") {
		t.Errorf("replayed headers %v", resp.Header)
	}
}
