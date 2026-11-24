package section

import (
	"context"
	"fmt"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

// PresenceConfig names the person entity; empty by default (site data).
type PresenceConfig struct {
	Enabled bool   `yaml:"enabled"`
	Person  string `yaml:"person"` // person.<name>: its state is the zone name
}

func newPresenceConfig() *PresenceConfig { return &PresenceConfig{Enabled: true} }

func (c *PresenceConfig) IsEnabled() bool { return c.Enabled }

func (c *PresenceConfig) Validate() error {
	if c.Person == "" {
		return fmt.Errorf("set presence.person (morning-brief config set presence.person <entity id>)")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "presence",
		Description: "Which zone the owner is in and since when (zone name only, never coordinates)",
		Credentials: []string{"ha_token"},
		Default:     func() brief.SectionConfig { return newPresenceConfig() },
		Collect:     collectPresence,
	})
}

func collectPresence(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	res := brief.Result{Name: "presence"}
	cfg := sc.(*PresenceConfig)
	snap, err := haSnapshot(ctx, env)
	if err == nil {
		var st = snap.ByID[cfg.Person]
		if _, ok := snap.ByID[cfg.Person]; !ok {
			err = fmt.Errorf("entity %s not found in Home Assistant", cfg.Person)
		} else {
			v := st.State
			if !st.LastChanged.IsZero() {
				v += " since " + st.LastChanged.In(env.Loc).Format("2006-01-02 15:04")
			}
			res.Facts = []brief.Fact{{Key: "zone", Value: v}}
			res.Status = brief.StatusReport
			return res
		}
	}
	res.Status, res.Err = brief.StatusError, err.Error()
	return res
}
