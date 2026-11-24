package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const maxWSMessage = 32 << 20

// WSConn is an authenticated Home Assistant WebSocket session. Commands run one at a time.
type WSConn struct {
	conn   *websocket.Conn
	id     int
	rec    Recorder
	replay bool // answers come from rec, conn is nil
}

// replaying reports whether rec serves recorded answers (brief.Replaying, repeated: brief imports this package).
func replaying(rec Recorder) bool {
	r, ok := rec.(interface{ Replaying() bool })
	return ok && r.Replaying()
}

// DayValue is one day's change of a statistic, the day starting at Start.
type DayValue struct {
	Start  time.Time
	Change float64
}

// wsURL turns the base URL into the WebSocket endpoint.
func (c *Client) wsURL() string {
	u := c.base()
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + "/api/websocket"
}

// WS connects and authenticates with the token, dialing through c.Dial when set (Env.Dial).
// In replay (c.Rec replaying) it connects to nothing: every command answers from the recording.
func (c *Client) WS(ctx context.Context) (*WSConn, error) {
	if replaying(c.Rec) {
		return &WSConn{rec: c.Rec, replay: true}, nil
	}
	hc := &http.Client{}
	if c.HTTP != nil {
		hc.Transport = c.HTTP.Transport // same TLS settings as the REST calls
	}
	if c.Dial != nil {
		tr := &http.Transport{DialContext: c.Dial}
		if c.HTTP != nil {
			if base, ok := c.HTTP.Transport.(*http.Transport); ok && base.TLSClientConfig != nil {
				tr.TLSClientConfig = base.TLSClientConfig.Clone()
			}
		}
		hc.Transport = tr
	}
	conn, _, err := websocket.Dial(ctx, c.wsURL(), &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		return nil, fmt.Errorf("Home Assistant: WebSocket unreachable")
	}
	conn.SetReadLimit(maxWSMessage)
	w := &WSConn{conn: conn, rec: c.Rec}
	ok := false
	defer func() {
		if !ok {
			conn.CloseNow()
		}
	}()
	var hello struct{ Type string }
	if err := w.read(ctx, &hello); err != nil || hello.Type != "auth_required" {
		return nil, fmt.Errorf("Home Assistant: WebSocket handshake failed")
	}
	if err := w.write(ctx, map[string]any{"type": "auth", "access_token": c.Token}); err != nil {
		return nil, err
	}
	var reply struct{ Type string }
	if err := w.read(ctx, &reply); err != nil {
		return nil, fmt.Errorf("Home Assistant: WebSocket handshake failed")
	}
	if reply.Type != "auth_ok" {
		return nil, fmt.Errorf("Home Assistant: WebSocket authentication failed (token wrong or expired)")
	}
	ok = true
	return w, nil
}

// Close ends the session.
func (w *WSConn) Close() {
	if w.conn != nil {
		_ = w.conn.Close(websocket.StatusNormalClosure, "")
	}
}

func (w *WSConn) write(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("Home Assistant: bad WebSocket command")
	}
	if err := w.conn.Write(ctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("Home Assistant: WebSocket write failed")
	}
	return nil
}

func (w *WSConn) read(ctx context.Context, v any) error {
	_, b, err := w.conn.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// call sends one command and returns the raw result of its answer.
func (w *WSConn) call(ctx context.Context, cmd map[string]any) (json.RawMessage, error) {
	w.id++
	cmd["id"] = w.id
	if err := w.write(ctx, cmd); err != nil {
		return nil, err
	}
	for {
		var msg struct {
			ID      int             `json:"id"`
			Type    string          `json:"type"`
			Success bool            `json:"success"`
			Result  json.RawMessage `json:"result"`
			Error   struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := w.read(ctx, &msg); err != nil {
			return nil, fmt.Errorf("Home Assistant: WebSocket read failed (%v)", ctxOrGeneric(ctx))
		}
		if msg.Type != "result" || msg.ID != w.id {
			continue
		}
		if !msg.Success {
			return nil, fmt.Errorf("Home Assistant: %v failed (%s)", cmd["type"], msg.Error.Code)
		}
		return msg.Result, nil
	}
}

func ctxOrGeneric(ctx context.Context) string {
	if err := ctx.Err(); err != nil {
		return err.Error()
	}
	return "connection closed"
}

// command runs cmd and records its answer under name; in replay it returns the recorded answer.
func (w *WSConn) command(ctx context.Context, name string, cmd map[string]any) (json.RawMessage, error) {
	if w.replay {
		var raw json.RawMessage
		if err := w.rec.Load(name, &raw); err != nil {
			return nil, fmt.Errorf("Home Assistant: %v", err)
		}
		return raw, nil
	}
	raw, err := w.call(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if w.rec != nil {
		_ = w.rec.Save(name, raw)
	}
	return raw, nil
}

// FlowFrom is one consumption source of the Energy panel's grid.
type FlowFrom struct {
	StatEnergyFrom string  `json:"stat_energy_from"`
	StatCost       *string `json:"stat_cost"`
}

// EnergySource is one entry of energy/get_prefs' energy_sources; which fields are set depends on Type.
type EnergySource struct {
	Type           string     `json:"type"`
	FlowFrom       []FlowFrom `json:"flow_from"`
	StatEnergyFrom string     `json:"stat_energy_from"`
	StatCost       *string    `json:"stat_cost"`
}

// EnergyPrefs is the Energy panel's configuration.
type EnergyPrefs struct {
	EnergySources []EnergySource `json:"energy_sources"`
}

// EnergyInfo is energy/info: the cost sensors HA created, by energy statistic id.
type EnergyInfo struct {
	CostSensors map[string]string `json:"cost_sensors"`
}

// EnergyPrefs reads energy/get_prefs.
func (w *WSConn) EnergyPrefs(ctx context.Context) (*EnergyPrefs, error) {
	raw, err := w.command(ctx, "ha_energy_prefs", map[string]any{"type": "energy/get_prefs"})
	if err != nil {
		return nil, err
	}
	var p EnergyPrefs
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("Home Assistant: energy/get_prefs answer unreadable")
	}
	return &p, nil
}

// EnergyInfo reads energy/info.
func (w *WSConn) EnergyInfo(ctx context.Context) (*EnergyInfo, error) {
	raw, err := w.command(ctx, "ha_energy_info", map[string]any{"type": "energy/info"})
	if err != nil {
		return nil, err
	}
	var i EnergyInfo
	if err := json.Unmarshal(raw, &i); err != nil {
		return nil, fmt.Errorf("Home Assistant: energy/info answer unreadable")
	}
	return &i, nil
}

// DailyChange returns, per statistic id, the change of each day in [from, to). A statistic with no data is absent.
func (w *WSConn) DailyChange(ctx context.Context, statIDs []string, from, to time.Time) (map[string][]DayValue, error) {
	raw, err := w.command(ctx, "ha_energy_stats", map[string]any{
		"type":          "recorder/statistics_during_period",
		"start_time":    from.UTC().Format(time.RFC3339),
		"end_time":      to.UTC().Format(time.RFC3339),
		"statistic_ids": statIDs,
		"period":        "day",
		"types":         []string{"change"},
		"units":         map[string]string{},
	})
	if err != nil {
		return nil, err
	}
	var wire map[string][]struct {
		Start  json.RawMessage `json:"start"`
		Change *float64        `json:"change"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("Home Assistant: statistics answer unreadable")
	}
	if len(statIDs) > 0 && len(wire) == 0 {
		return nil, fmt.Errorf("Home Assistant: statistics answer is empty (no recorded data for the requested ids)")
	}
	out := make(map[string][]DayValue, len(wire))
	for id, rows := range wire {
		for _, r := range rows {
			if r.Change == nil { // no value for that day: not a data day
				continue
			}
			t, err := parseStatTime(r.Start)
			if err != nil {
				return nil, fmt.Errorf("Home Assistant: bad statistics time on %s", id)
			}
			out[id] = append(out[id], DayValue{Start: t, Change: *r.Change})
		}
	}
	return out, nil
}

// parseStatTime reads a start as milliseconds since the epoch or as an ISO 8601 string.
func parseStatTime(raw json.RawMessage) (time.Time, error) {
	var ms float64
	if err := json.Unmarshal(raw, &ms); err == nil {
		return time.UnixMilli(int64(math.Round(ms))), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, s)
}
