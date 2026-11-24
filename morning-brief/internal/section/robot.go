package section

import (
	"context"
	"fmt"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

// RobotConfig names the robot vacuum's Home Assistant entities. Every id is empty by default (site data).
type RobotConfig struct {
	Enabled         bool     `yaml:"enabled"`
	LastCleanEnd    string   `yaml:"last_clean_end"`    // sensor (timestamp): end of the last cleaning
	Parts           []string `yaml:"parts"`             // sensors: hours left before a part is worn out
	DockError       string   `yaml:"dock_error"`        // enum sensor: "ok" when fine
	WaterProblems   []string `yaml:"water_problems"`    // binary sensors (problem): on = tank problem
	VacuumError     string   `yaml:"vacuum_error"`      // enum sensor: "none" when fine
	RemindAfterDays float64  `yaml:"remind_after_days"` // remind to run it after this many days without cleaning
	Person          string   `yaml:"person"`            // optional person entity, for owner_away_today
}

func newRobotConfig() *RobotConfig { return &RobotConfig{Enabled: true, RemindAfterDays: 3} }

func (c *RobotConfig) IsEnabled() bool { return c.Enabled }

func (c *RobotConfig) Validate() error {
	if c.LastCleanEnd == "" {
		return fmt.Errorf("set robot.last_clean_end (morning-brief config set robot.last_clean_end <entity id>)")
	}
	if c.RemindAfterDays <= 0 {
		return fmt.Errorf("robot.remind_after_days must be above 0")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "robot",
		Description: "Robot vacuum: days since the last cleaning (reminder), worn-out parts, dock, water tank and vacuum errors (Home Assistant)",
		Credentials: []string{"ha_token"},
		Default:     func() brief.SectionConfig { return newRobotConfig() },
		Collect:     collectRobot,
	})
}

func collectRobot(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	res := brief.Result{Name: "robot"}
	cfg := sc.(*RobotConfig)
	snap, err := haSnapshot(ctx, env)
	if err != nil {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	problem := false
	add := func(k, v string) { res.Facts = append(res.Facts, brief.Fact{Key: k, Value: v}) }

	end, err := needState(snap, cfg.LastCleanEnd)
	if err != nil {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	if t, perr := time.Parse(time.RFC3339, end.State); perr != nil {
		add("last_clean", "unknown — reminder")
		problem = true
	} else {
		add("last_clean", stamp(t, env.Loc))
		days := env.Now.Sub(t).Hours() / 24
		if days >= cfg.RemindAfterDays {
			add("reminder", fmt.Sprintf("yes (%.1f days)", days))
			problem = true
		} else {
			add("reminder", "no")
		}
	}
	if cfg.Person != "" {
		p, err := needState(snap, cfg.Person)
		if err != nil {
			res.Status, res.Err = brief.StatusError, err.Error()
			return res
		}
		add("owner_away_today", yesNo(p.State != "home"))
	}

	var worn []string
	for _, id := range cfg.Parts {
		st, err := needState(snap, id)
		if err != nil {
			res.Status, res.Err = brief.StatusError, err.Error()
			return res
		}
		h, ok := number(st)
		switch {
		case !ok:
			res.Facts = append(res.Facts, brief.Untrusted("part", entityName(st)+": no data"))
			problem = true
		case h <= 0:
			worn = append(worn, entityName(st))
		}
	}
	if len(worn) > 0 {
		res.Facts = append(res.Facts, brief.Untrusted("worn_out", joinNames(worn)))
		problem = true
	}

	enum := func(key, id, fine string) error {
		if id == "" {
			return nil
		}
		st, err := needState(snap, id)
		if err != nil {
			return err
		}
		switch {
		case unavailable(st.State):
			add(key, "no data")
			problem = true
		case st.State != fine:
			add(key, brief.Sanitize(st.State))
			problem = true
		}
		return nil
	}
	if err := enum("dock_error", cfg.DockError, "ok"); err != nil {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	if err := enum("vacuum_error", cfg.VacuumError, "none"); err != nil {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	for _, id := range cfg.WaterProblems {
		st, err := needState(snap, id)
		if err != nil {
			res.Status, res.Err = brief.StatusError, err.Error()
			return res
		}
		switch {
		case unavailable(st.State):
			res.Facts = append(res.Facts, brief.Untrusted("water_tank", entityName(st)+": no data"))
			problem = true
		case st.State == "on":
			res.Facts = append(res.Facts, brief.Untrusted("water_tank", entityName(st)))
			problem = true
		}
	}
	res.Status = brief.StatusOK
	if problem {
		res.Status = brief.StatusReport
	}
	return res
}

func joinNames(ns []string) string {
	out := ""
	for i, n := range ns {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
