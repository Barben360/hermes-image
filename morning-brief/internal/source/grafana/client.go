// Package grafana reads Alertmanager's active alerts through Grafana.
package grafana

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	query = "?active=true&silenced=false&inhibited=false"
	// PrimaryPath is Grafana's Alertmanager API for the built-in "alertmanager" datasource.
	PrimaryPath = "/api/alertmanager/alertmanager/api/v2/alerts"
	// FallbackPath is the datasource proxy, used when a Viewer is refused on PrimaryPath.
	FallbackPath = "/api/datasources/proxy/uid/alertmanager/api/v2/alerts"
)

// Alert is one Alertmanager v2 alert.
type Alert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	Status      struct {
		State string `json:"state"`
	} `json:"status"`
}

// Client talks to Grafana with a Bearer token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Alerts returns the active, unsilenced, uninhibited alerts.
func (c *Client) Alerts(ctx context.Context) ([]Alert, error) {
	as, code, err := c.get(ctx, PrimaryPath)
	if err != nil && (code == http.StatusForbidden || code == http.StatusNotFound) {
		var err2 error
		as, _, err2 = c.get(ctx, FallbackPath)
		if err2 != nil {
			return nil, fmt.Errorf("%v; fallback: %v", err, err2)
		}
		return as, nil
	}
	return as, err
}

func (c *Client) get(ctx context.Context, path string) ([]Alert, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path+query, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("grafana %s: bad request", path)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("grafana %s: unreachable", path)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("grafana %s: HTTP %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("grafana %s: read failed", path)
	}
	var as []Alert
	if err := json.Unmarshal(body, &as); err != nil || as == nil { // `null` decodes to a nil slice: not an answer
		return nil, 0, fmt.Errorf("grafana %s: invalid JSON", path)
	}
	return as, resp.StatusCode, nil
}
