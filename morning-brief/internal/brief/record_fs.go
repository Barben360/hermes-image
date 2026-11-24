package brief

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// WithSection tags ctx with the section on whose behalf requests are made, so recordings are keyed per section.
func WithSection(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, sectionKey{}, name)
}

type sectionKey struct{}

func sectionOf(ctx context.Context) string {
	s, _ := ctx.Value(sectionKey{}).(string)
	return s
}

func shortHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// file maps a name to a file; the hash of the exact name keeps two names that sanitise alike apart.
func (r *dirRecorder) file(name string) string {
	return filepath.Join(r.dir, unsafeName.ReplaceAllString(name, "_")+"-"+shortHash(name)+".json")
}

// scrubValue replaces every secret in every string of a decoded JSON value.
func (r *dirRecorder) scrubValue(v any) any {
	switch x := v.(type) {
	case string:
		return r.secrets.Scrub(x)
	case []any:
		for i := range x {
			x[i] = r.scrubValue(x[i])
		}
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[r.secrets.Scrub(k)] = r.scrubValue(e)
		}
		return out
	}
	return v
}

// Replaying reports whether the recorder serves recorded answers.
func (r *dirRecorder) Replaying() bool { return r.replay }

func (r *dirRecorder) Save(name string, v any) error {
	if r.replay {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}
	// scrub the decoded strings (before JSON escaping can hide a value), then the final text as a net
	b, err := json.MarshalIndent(r.scrubValue(generic), "", "  ")
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

// recordedHTTP is one recorded exchange: no request header, no query string, only the answer
// headers a source reads (replayedHeaders).
type recordedHTTP struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Status int               `json:"status"`
	Header map[string]string `json:"header,omitempty"`
	Body   string            `json:"body"`
}

// replayedHeaders are the answer headers sources read: pcb's SSE Content-Type, GitHub's Link paging.
var replayedHeaders = []string{"Content-Type", "Link"}

type recordingTransport struct {
	next   http.RoundTripper
	rec    Recorder
	replay bool
	keys   *keyCounter // shared with the transports WrapNext derives
}

// keyCounter numbers the repeats of one key.
type keyCounter struct {
	mu   sync.Mutex
	seen map[string]int
}

// NewRecordingTransport wraps next (nil = http.DefaultTransport). With a recording recorder it
// forwards and saves each answer; with a replaying one (NewDirRecorder replay=true) it answers
// from the recorded files and never touches the network. Requests are keyed by method and path
// (never the query string, which may carry coordinates, nor the host, so that a recording replays
// wherever the sources are configured), the n-th repeat getting its own file.
func NewRecordingTransport(next http.RoundTripper, rec Recorder) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &recordingTransport{next: next, rec: rec, replay: Replaying(rec), keys: &keyCounter{seen: map[string]int{}}}
}

// WrapNext returns the same recorder in front of another transport: a source that needs its own
// (pcb's pinned CA) still records, and replays without touching the network.
func (t *recordingTransport) WrapNext(next http.RoundTripper) http.RoundTripper {
	return &recordingTransport{next: next, rec: t.rec, replay: t.replay, keys: t.keys}
}

// dropCoordinates removes numeric top-level latitude/longitude from a JSON object body.
func dropCoordinates(body []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	changed := false
	for _, k := range []string{"latitude", "longitude"} {
		if _, ok := m[k]; ok {
			delete(m, k)
			changed = true
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

func (t *recordingTransport) name(req *http.Request, body []byte) string {
	key := "http_" + sectionOf(req.Context()) + "_" + req.Method + "_" + req.URL.Path + "_" + shortHash(string(body))
	t.keys.mu.Lock()
	defer t.keys.mu.Unlock()
	t.keys.seen[key]++
	if n := t.keys.seen[key]; n > 1 {
		key = fmt.Sprintf("%s_%d", key, n)
	}
	return key
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil && req.Body != http.NoBody {
		var err error
		if reqBody, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	name := t.name(req, reqBody)
	if t.replay {
		var rh recordedHTTP
		if err := t.rec.Load(name, &rh); err != nil {
			return nil, err
		}
		h := http.Header{}
		for k, v := range rh.Header {
			h.Set(k, v)
		}
		return &http.Response{
			Status: fmt.Sprintf("%d %s", rh.Status, http.StatusText(rh.Status)), StatusCode: rh.Status,
			Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header: h, Body: io.NopCloser(strings.NewReader(rh.Body)),
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
	saved := dropCoordinates(b)
	var hdr map[string]string
	for _, k := range replayedHeaders {
		if v := resp.Header.Get(k); v != "" {
			if hdr == nil {
				hdr = map[string]string{}
			}
			hdr[k] = v
		}
	}
	if err := t.rec.Save(name, recordedHTTP{Method: req.Method, Path: req.URL.Path, Status: resp.StatusCode, Header: hdr, Body: string(saved)}); err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("recording failed: %w", err)
	}
	return resp, nil
}
