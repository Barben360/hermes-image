package section

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/deepseek"
)

// DeepSeekConfig configures the deepseek section.
type DeepSeekConfig struct {
	Enabled   bool    `yaml:"enabled"`
	WarnBelow float64 `yaml:"warn_below"`
	BaseURL   string  `yaml:"base_url"`
}

func deepseekDefault() *DeepSeekConfig {
	return &DeepSeekConfig{Enabled: true, WarnBelow: 5, BaseURL: "https://api.deepseek.com"}
}

func (c *DeepSeekConfig) IsEnabled() bool { return c.Enabled }

func (c *DeepSeekConfig) Validate() error {
	if c.WarnBelow < 0 {
		return fmt.Errorf("deepseek.warn_below must not be negative")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("set deepseek.base_url (morning-brief config set deepseek.base_url https://api.deepseek.com)")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "deepseek",
		Description: "DeepSeek API balance, warning when low or unavailable",
		Credentials: []string{"deepseek_key"},
		Default:     func() brief.SectionConfig { return deepseekDefault() },
		Collect:     collectDeepSeek,
	})
}

func collectDeepSeek(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	cfg := sc.(*DeepSeekConfig)
	res := brief.Result{Name: "deepseek"}
	fail := func(err error) brief.Result {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	key, err := env.Secrets.Read("deepseek_key")
	if err != nil {
		return fail(err)
	}
	b, err := (&deepseek.Client{BaseURL: cfg.BaseURL, Key: key, HTTP: env.HTTP}).Balance(ctx)
	if err != nil {
		return fail(err)
	}
	if b.IsAvailable && len(b.BalanceInfos) == 0 {
		return fail(fmt.Errorf("deepseek: no balance in the answer"))
	}
	var warnings []string
	if !b.IsAvailable {
		warnings = append(warnings, "account unavailable (API calls will fail)")
	}
	for _, i := range b.BalanceInfos {
		v, perr := strconv.ParseFloat(i.TotalBalance, 64)
		if perr != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return fail(fmt.Errorf("deepseek: unreadable %s balance", brief.Sanitize(i.Currency)))
		}
		cur := brief.Sanitize(i.Currency)
		res.Facts = append(res.Facts, brief.Fact{Key: "balance " + cur, Value: i.TotalBalance})
		if v < cfg.WarnBelow {
			warnings = append(warnings, fmt.Sprintf("%s balance %s is below %g", cur, i.TotalBalance, cfg.WarnBelow))
		}
	}
	res.Status = brief.StatusOK
	if len(warnings) > 0 {
		res.Status = brief.StatusReport
		res.Facts = append(res.Facts, brief.Fact{Key: "warning", Value: strings.Join(warnings, "; ")})
	}
	return res
}
