package section

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/grafana"
)

// AlertsConfig is the alerts section's configuration.
type AlertsConfig struct {
	Enabled           bool     `yaml:"enabled"`
	ExcludeAlertnames []string `yaml:"exclude_alertnames"`
	IncludeInfo       bool     `yaml:"include_info"`
}

func (c *AlertsConfig) IsEnabled() bool { return c.Enabled }
func (c *AlertsConfig) Validate() error { return nil }

func init() {
	brief.Register(brief.Section{
		Name:        "alerts",
		Description: "Active homelab alerts from Alertmanager via Grafana; info alerts are news",
		Credentials: []string{"grafana_token"},
		Default: func() brief.SectionConfig {
			return &AlertsConfig{Enabled: true, ExcludeAlertnames: []string{"Watchdog"}, IncludeInfo: true}
		},
		Collect: collectAlerts,
	})
}

func collectAlerts(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	cfg := sc.(*AlertsConfig)
	res := brief.Result{Name: "alerts"}
	fail := func(err error) brief.Result {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	if env.Sources.GrafanaURL == "" {
		return fail(errors.New("set sources.grafana_url (morning-brief config set …)"))
	}
	tok, err := env.Secrets.Read("grafana_token")
	if err != nil {
		return fail(err)
	}
	hc := env.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	alerts, err := (&grafana.Client{BaseURL: env.Sources.GrafanaURL, Token: tok, HTTP: hc}).Alerts(ctx)
	if err != nil {
		return fail(err)
	}
	excl := map[string]bool{}
	for _, n := range cfg.ExcludeAlertnames {
		excl[n] = true
	}
	loc := env.Loc
	if loc == nil {
		loc = time.UTC
	}
	var firing, news []grafana.Alert
	for _, a := range alerts {
		if a.Labels["alertname"] == "" || a.StartsAt.IsZero() {
			continue // malformed entry
		}
		if excl[a.Labels["alertname"]] {
			continue
		}
		if a.Status.State != "" && a.Status.State != "active" {
			continue
		}
		if strings.EqualFold(a.Labels["severity"], "info") {
			if cfg.IncludeInfo {
				news = append(news, a)
			}
			continue
		}
		firing = append(firing, a)
	}
	if len(firing)+len(news) == 0 {
		res.Status = brief.StatusOK
		return res
	}
	res.Status = brief.StatusReport
	add := func(kind string, as []grafana.Alert) {
		sort.SliceStable(as, func(i, j int) bool { return as[i].StartsAt.Before(as[j].StartsAt) })
		for _, a := range as {
			since := a.StartsAt.In(loc)
			facts := []brief.Fact{
				{Key: "kind", Value: kind},
				brief.Untrusted("alert", a.Labels["alertname"]),
			}
			if k := a.Labels["key"]; k != "" {
				facts = append(facts, brief.Untrusted("key", k))
			}
			if s := a.Annotations["summary"]; s != "" {
				facts = append(facts, brief.Untrusted("summary", s))
			}
			facts = append(facts, brief.Fact{Key: "since", Value: since.Format("2006-01-02 15:04"), AsOf: since})
			res.Items = append(res.Items, brief.Item{Facts: facts})
		}
	}
	add("alert", firing)
	add("news", news)
	return res
}
