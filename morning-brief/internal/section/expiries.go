package section

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

// ExpiriesConfig is the expiries section's configuration.
type ExpiriesConfig struct {
	Enabled        bool     `yaml:"enabled"`
	Files          []string `yaml:"files"`
	WarnWithinDays int      `yaml:"warn_within_days"`
}

func (c *ExpiriesConfig) IsEnabled() bool { return c.Enabled }
func (c *ExpiriesConfig) Validate() error {
	if c.WarnWithinDays < 0 {
		return fmt.Errorf("expiries.warn_within_days must be >= 0")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "expiries",
		Description: "Credentials whose expiry date (YYYY-MM-DD in a secret file) is near",
		Credentials: []string{"github_token_expires"},
		Default: func() brief.SectionConfig {
			return &ExpiriesConfig{Enabled: true, Files: []string{"github_token_expires"}, WarnWithinDays: 30}
		},
		Collect: collectExpiries,
	})
}

func collectExpiries(_ context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	cfg := sc.(*ExpiriesConfig)
	res := brief.Result{Name: "expiries"}
	loc := env.Loc
	if loc == nil {
		loc = time.UTC
	}
	today := time.Date(env.Now.In(loc).Year(), env.Now.In(loc).Month(), env.Now.In(loc).Day(), 0, 0, 0, 0, time.UTC)
	var errs []string
	for _, name := range cfg.Files {
		raw, err := env.Secrets.Read(name)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		d, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: not a YYYY-MM-DD date", name))
			continue
		}
		days := int(d.Sub(today).Hours() / 24)
		if days > cfg.WarnWithinDays {
			continue
		}
		val := fmt.Sprintf("%s in %d days", d.Format("2006-01-02"), days)
		if days < 0 {
			val = fmt.Sprintf("%s expired %d days ago", d.Format("2006-01-02"), -days)
		} else if days == 0 {
			val = d.Format("2006-01-02") + " expires today"
		}
		res.Items = append(res.Items, brief.Item{Facts: []brief.Fact{
			{Key: "credential", Value: name}, {Key: "expires", Value: val},
		}})
	}
	switch {
	case len(errs) > 0:
		res.Status, res.Err = brief.StatusError, strings.Join(errs, "; ")
	case len(res.Items) > 0:
		res.Status = brief.StatusReport
	default:
		res.Status = brief.StatusOK
	}
	return res
}
