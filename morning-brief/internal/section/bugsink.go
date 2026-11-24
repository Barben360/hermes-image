package section

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/bugsink"
)

// BugsinkConfig configures the bugsink section.
type BugsinkConfig struct {
	Enabled bool `yaml:"enabled"`
	Hours   int  `yaml:"hours"`
}

func bugsinkDefault() *BugsinkConfig { return &BugsinkConfig{Enabled: true, Hours: 24} }

func (c *BugsinkConfig) IsEnabled() bool { return c.Enabled }

func (c *BugsinkConfig) Validate() error {
	if c.Hours <= 0 {
		return fmt.Errorf("bugsink.hours must be positive")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "bugsink",
		Description: "Issues first seen in Bugsink during the last hours",
		ThirdParty:  true,
		Credentials: []string{"bugsink_token"},
		Default:     func() brief.SectionConfig { return bugsinkDefault() },
		Collect:     collectBugsink,
	})
}

func collectBugsink(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	cfg := sc.(*BugsinkConfig)
	res := brief.Result{Name: "bugsink"}
	fail := func(err error) brief.Result {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	if env.Sources.BugsinkURL == "" {
		return fail(fmt.Errorf("set sources.bugsink_url (morning-brief config set sources.bugsink_url <url>)"))
	}
	tok, err := env.Secrets.Read("bugsink_token")
	if err != nil {
		return fail(err)
	}
	c := &bugsink.Client{BaseURL: env.Sources.BugsinkURL, Host: env.Sources.BugsinkHost, Token: tok, HTTP: env.HTTP}
	since := env.Now.Add(-time.Duration(cfg.Hours) * time.Hour).UTC()
	projects, err := c.Projects(ctx)
	if err != nil {
		return fail(err)
	}
	host := env.Sources.BugsinkHost
	if host == "" {
		host = strings.TrimPrefix(strings.TrimPrefix(env.Sources.BugsinkURL, "https://"), "http://")
	}
	for _, p := range projects {
		issues, err := c.IssuesSince(ctx, p.ID, since)
		if err != nil {
			return fail(err)
		}
		for _, i := range issues {
			res.Items = append(res.Items, brief.Item{Facts: []brief.Fact{
				brief.Untrusted("project", p.Name),
				brief.Untrusted("title", i.Title),
				{Key: "events", Value: strconv.Itoa(i.EventCount)},
				{Key: "first_seen", Value: i.FirstSeen.UTC().Format(time.RFC3339)},
				{Key: "url", Value: "https://" + host + "/issues/issue/" + i.ID + "/"},
			}})
		}
	}
	res.Facts = append(res.Facts, brief.Fact{Key: "visible_projects", Value: strconv.Itoa(len(projects))})
	res.Status = brief.StatusOK
	if len(res.Items) > 0 {
		res.Status = brief.StatusReport
	}
	return res
}
