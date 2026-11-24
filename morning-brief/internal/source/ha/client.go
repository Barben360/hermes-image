package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// Recorder is brief.Recorder's method set; it is repeated here because package brief imports this one.
type Recorder interface {
	Save(name string, v any) error
	Load(name string, v any) error
}

// Client talks to Home Assistant's REST API with a long-lived token.
type Client struct {
	BaseURL, Token string // BaseURL without scheme means https
	HTTP           *http.Client
	Rec            Recorder
	Dial           func(ctx context.Context, network, addr string) (net.Conn, error) // WebSocket only; nil = default
}

const maxBody = 32 << 20

func (c *Client) base() string {
	u := strings.TrimRight(c.BaseURL, "/")
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	return u
}

func (c *Client) do(ctx context.Context, method, p string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base()+p, body)
	if err != nil {
		return nil, fmt.Errorf("Home Assistant: bad URL")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Home Assistant: unreachable (%s)", urlErrReason(err))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("Home Assistant: read failed")
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("Home Assistant: 401 (token wrong or expired)")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("Home Assistant: HTTP %d", resp.StatusCode)
	}
	return b, nil
}

// urlErrReason keeps the cause of a transport error without the URL (it may hold nothing secret, but stays short).
func urlErrReason(err error) string {
	if ue, ok := err.(interface{ Unwrap() error }); ok && ue.Unwrap() != nil {
		err = ue.Unwrap()
	}
	return err.Error()
}

type wireState struct {
	EntityID     string         `json:"entity_id"`
	State        string         `json:"state"`
	Attributes   map[string]any `json:"attributes"`
	LastChanged  string         `json:"last_changed"`
	LastUpdated  string         `json:"last_updated"`
	LastReported string         `json:"last_reported"`
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

// States fetches every entity (GET /api/states).
func (c *Client) States(ctx context.Context) (*Snapshot, error) {
	b, err := c.do(ctx, http.MethodGet, "/api/states", nil)
	if err != nil {
		return nil, err
	}
	var wire []wireState
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, fmt.Errorf("Home Assistant: answer is not a state list")
	}
	snap := &Snapshot{At: time.Now(), ByID: make(map[string]State, len(wire))}
	for _, w := range wire {
		st := State{EntityID: w.EntityID, State: w.State, Attributes: w.Attributes}
		for _, f := range []struct {
			in  string
			out *time.Time
		}{{w.LastChanged, &st.LastChanged}, {w.LastUpdated, &st.LastUpdated}, {w.LastReported, &st.LastReported}} {
			if *f.out, err = parseTime(f.in); err != nil {
				return nil, fmt.Errorf("Home Assistant: bad time on %s", w.EntityID)
			}
		}
		snap.ByID[w.EntityID] = st
	}
	if c.Rec != nil {
		_ = c.Rec.Save("ha_states", json.RawMessage(b))
	}
	return snap, nil
}

// Service calls domain.service with ?return_response and returns the raw answer.
func (c *Client) Service(ctx context.Context, domain, service string, body any) (json.RawMessage, error) {
	if body == nil {
		body = map[string]any{}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("Home Assistant: bad service body")
	}
	b, err := c.do(ctx, http.MethodPost, "/api/services/"+domain+"/"+service+"?return_response", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// Glob returns the states whose entity id matches the pattern (path.Match syntax), sorted by id.
func (s *Snapshot) Glob(pattern string) []State {
	var out []State
	for id, st := range s.ByID {
		if ok, _ := path.Match(pattern, id); ok {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntityID < out[j].EntityID })
	return out
}
