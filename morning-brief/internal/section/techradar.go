package section

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

// TechRadarConfig configures the tech_radar section.
type TechRadarConfig struct {
	Enabled bool `yaml:"enabled"`
}

func techRadarDefault() *TechRadarConfig { return &TechRadarConfig{Enabled: true} }

func (c *TechRadarConfig) IsEnabled() bool { return c.Enabled }
func (c *TechRadarConfig) Validate() error { return nil }

func init() {
	brief.Register(brief.Section{
		Name:        "tech_radar",
		Description: "Whether today's tech radar note is in the vault",
		Default:     func() brief.SectionConfig { return techRadarDefault() },
		Collect:     collectTechRadar,
	})
}

func collectTechRadar(_ context.Context, env *brief.Env, _ brief.SectionConfig) brief.Result {
	res := brief.Result{Name: "tech_radar"}
	if env.Sources.VaultDir == "" {
		res.Status, res.Err = brief.StatusError, "set sources.vault_dir (morning-brief config set sources.vault_dir <dir>)"
		return res
	}
	now := env.Now
	if env.Loc != nil {
		now = now.In(env.Loc)
	}
	rel := fmt.Sprintf("Tech/Tech Radar/Dailies/%s/%s/%s.md", now.Format("2006"), now.Format("01"), now.Format("2006-01-02"))
	st, err := os.Stat(filepath.Join(env.Sources.VaultDir, filepath.FromSlash(rel)))
	if err == nil && st.Mode().IsRegular() {
		res.Status = brief.StatusReport
		res.Facts = []brief.Fact{{Key: "today's note", Value: rel}}
		return res
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		res.Status, res.Err = brief.StatusError, "tech radar note: cannot stat: "+brief.Sanitize(err.Error())
		return res
	}
	res.Status = brief.StatusOK
	res.Facts = []brief.Fact{{Key: "note", Value: "no note yet"}}
	return res
}
