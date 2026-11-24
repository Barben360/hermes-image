package brief

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type dirRecorder struct {
	dir     string
	secrets *Secrets
	replay  bool
}

// NewDirRecorder records source answers as JSON files in dir (replay=false) or serves them back
// (replay=true: Save does nothing, Load reads). Every file is scrubbed of the secrets read so far.
func NewDirRecorder(dir string, s *Secrets, replay bool) Recorder {
	return &dirRecorder{dir: dir, secrets: s, replay: replay}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (r *dirRecorder) file(name string) string {
	return filepath.Join(r.dir, unsafeName.ReplaceAllString(name, "_")+".json")
}

func (r *dirRecorder) Save(name string, v any) error {
	if r.replay {
		return nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}
	b = []byte(r.secrets.Scrub(string(b)))
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}
	return os.WriteFile(r.file(name), b, 0o600)
}

func (r *dirRecorder) Load(name string, v any) error {
	if !r.replay {
		return fmt.Errorf("replay %s: recorder is not replaying", name)
	}
	b, err := os.ReadFile(r.file(name))
	if err != nil {
		return fmt.Errorf("replay %s: not recorded", name)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("replay %s: %w", name, err)
	}
	return nil
}

// recordedHTTP is one recorded exchange: no request header, no query string.
type recordedHTTP struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	Body   string `json:"body"`
}

type recordingTransport struct {
	next   http.RoundTripper
	rec    Recorder
	replay bool
	mu     sync.Mutex
	seen   map[string]int
}

// NewRecordingTransport wraps next (nil = http.DefaultTransport). With a recording recorder it
// forwards and saves each answer; with a replaying one (NewDirRecorder replay=true) it answers
// from the recorded files and never touches the network. Requests are keyed by method and path
// (never the query string, which may carry coordinates), the n-th repeat getting its own file.
func NewRecordingTransport(next http.RoundTripper, rec Recorder) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	dr, _ := rec.(*dirRecorder)
	return &recordingTransport{next: next, rec: rec, replay: dr != nil && dr.replay, seen: map[string]int{}}
}

func (t *recordingTransport) name(req *http.Request) string {
	key := "http_" + req.Method + "_" + req.URL.Host + req.URL.Path
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen[key]++
	if n := t.seen[key]; n > 1 {
		key = fmt.Sprintf("%s_%d", key, n)
	}
	return key
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	name := t.name(req)
	if t.replay {
		var rh recordedHTTP
		if err := t.rec.Load(name, &rh); err != nil {
			return nil, err
		}
		return &http.Response{
			Status: fmt.Sprintf("%d %s", rh.Status, http.StatusText(rh.Status)), StatusCode: rh.Status,
			Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header: http.Header{}, Body: io.NopCloser(strings.NewReader(rh.Body)),
			ContentLength: int64(len(rh.Body)), Request: req,
		}, nil
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	_ = t.rec.Save(name, recordedHTTP{Method: req.Method, Path: req.URL.Path, Status: resp.StatusCode, Body: string(b)})
	return resp, nil
}
