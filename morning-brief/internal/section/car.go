package section

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

// CarConfig names the Home Assistant entities of the car. Every id is empty by default: they are site data.
type CarConfig struct {
	Enabled             bool   `yaml:"enabled"`
	Battery             string `yaml:"battery"`               // sensor: state of charge in %
	BatteryTimestamp    string `yaml:"battery_timestamp"`     // sensor, optional: ISO time of the battery data (a cloud poll moves last_reported, not the data)
	Range               string `yaml:"range"`                 // sensor: remaining range in km
	MaxRange            string `yaml:"max_range"`             // sensor, optional: range at 100 %
	Plug                string `yaml:"plug"`                  // sensor: unplugged / plugged / plugged_waiting_for_charge…
	Charging            string `yaml:"charging"`              // binary sensor: on while charging
	ChargeState         string `yaml:"charge_state"`          // sensor, optional: the maker's charge state enum
	Target              string `yaml:"target"`                // number: target charge level in %
	ChargeTimeRemaining string `yaml:"charge_time_remaining"` // sensor, optional: minutes left to the target
	Tracker             string `yaml:"tracker"`               // device_tracker: its state is the zone name
	HVACThreshold       string `yaml:"hvac_threshold"`        // number, optional: battery % under which pre-heating stops
	WarnBelowPct        int    `yaml:"warn_below_pct"`        // low battery warning
}

func newCarConfig() *CarConfig { return &CarConfig{Enabled: true, WarnBelowPct: 20} }

func (c *CarConfig) IsEnabled() bool { return c.Enabled }

// setHint is the multi-key form of `config set` for the required ids of a section.
func setHint(section string, ids []struct{ key, id string }) string {
	parts := make([]string, len(ids))
	for i, r := range ids {
		parts[i] = section + "." + r.key + "=<id>"
	}
	return strings.Join(parts, " ")
}

func (c *CarConfig) Validate() error {
	required := []struct{ key, id string }{
		{"battery", c.Battery}, {"range", c.Range}, {"plug", c.Plug},
		{"charging", c.Charging}, {"target", c.Target}, {"tracker", c.Tracker},
	}
	for _, r := range required {
		if r.id == "" {
			return fmt.Errorf("set car.%s (morning-brief config set %s)", r.key, setHint("car", required))
		}
	}
	seen := map[string]string{}
	all := append(required, []struct{ key, id string }{
		{"battery_timestamp", c.BatteryTimestamp}, {"max_range", c.MaxRange}, {"charge_state", c.ChargeState},
		{"charge_time_remaining", c.ChargeTimeRemaining}, {"hvac_threshold", c.HVACThreshold},
	}...)
	for _, r := range all {
		if r.id == "" {
			continue
		}
		if prev, dup := seen[r.id]; dup {
			return fmt.Errorf("car.%s and car.%s are both %s", prev, r.key, r.id)
		}
		seen[r.id] = r.key
	}
	if c.WarnBelowPct < 0 || c.WarnBelowPct > 100 {
		return fmt.Errorf("car.warn_below_pct must be between 0 and 100")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "car",
		Description: "Car battery, range, plug and charge state, zone, low-battery warning (Home Assistant)",
		Credentials: []string{"ha_token"},
		Default:     func() brief.SectionConfig { return newCarConfig() },
		Collect:     collectCar,
	})
}

func collectCar(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	res := brief.Result{Name: "car"}
	fail := func(err error) brief.Result {
		res.Status, res.Err, res.Facts = brief.StatusError, err.Error(), nil
		return res
	}
	cfg := sc.(*CarConfig)
	snap, err := haSnapshot(ctx, env)
	if err != nil {
		return fail(err)
	}
	get := func(id string) (ha.State, error) { return needState(snap, id) }
	var (
		battery, rng, plug, charging, target, tracker ha.State
		batteryTime, maxRange, chargeState, remaining ha.State
		hvac                                          ha.State
	)
	for _, l := range []struct {
		dst *ha.State
		id  string
	}{{&battery, cfg.Battery}, {&rng, cfg.Range}, {&plug, cfg.Plug}, {&charging, cfg.Charging}, {&target, cfg.Target}, {&tracker, cfg.Tracker}} {
		if *l.dst, err = get(l.id); err != nil {
			return fail(err)
		}
	}
	for _, l := range []struct {
		dst *ha.State
		id  string
	}{{&batteryTime, cfg.BatteryTimestamp}, {&maxRange, cfg.MaxRange}, {&chargeState, cfg.ChargeState}, {&remaining, cfg.ChargeTimeRemaining}, {&hvac, cfg.HVACThreshold}} {
		if l.id == "" {
			continue
		}
		if *l.dst, err = get(l.id); err != nil {
			return fail(err)
		}
	}
	add := func(k, v string) { res.Facts = append(res.Facts, brief.Fact{Key: k, Value: v}) }
	withUnit := func(st ha.State, unit string) string {
		if f, ok := number(st); ok {
			return trim(f) + unit
		}
		return "unavailable"
	}

	bat, batOK := number(battery)
	asOf := reported(battery)
	if cfg.BatteryTimestamp != "" {
		if t, err := time.Parse(time.RFC3339, batteryTime.State); err == nil {
			asOf = t
		}
	}
	res.Facts = append(res.Facts, brief.Fact{Key: "battery", Value: withUnit(battery, "%"), AsOf: asOf.In(env.Loc)})
	add("range", withUnit(rng, " km"))
	if cfg.MaxRange != "" {
		add("max_range", withUnit(maxRange, " km"))
	}
	isPlugged := plug.State == "plugged" || plug.State == "plugged_waiting_for_charge"
	isCharging := charging.State == "on"
	add("plugged", yesNo(isPlugged))
	add("charging", yesNo(isCharging))
	if cfg.ChargeState != "" {
		add("charge_state", chargeState.State)
	}
	add("target", withUnit(target, "%"))
	if cfg.ChargeTimeRemaining != "" && isCharging {
		if m, ok := number(remaining); ok {
			add("charge", "done in "+minutesText(int(m)))
		}
	}
	add("zone", tracker.State)
	if !batOK {
		add("warning", "unknown (no battery data)")
	} else {
		if bat < float64(cfg.WarnBelowPct) {
			add("warning", fmt.Sprintf("yes (below %d%%)", cfg.WarnBelowPct))
		} else {
			add("warning", "no")
		}
		if cfg.HVACThreshold != "" {
			if th, ok := number(hvac); ok && bat < th {
				add("hvac", fmt.Sprintf("below threshold (%s%%)", trim(th)))
			}
		}
	}
	res.Status = brief.StatusReport
	return res
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// minutesText prints minutes as "2 h 12", or "45 min" under an hour.
func minutesText(min int) string {
	if min < 60 {
		return fmt.Sprintf("%d min", min)
	}
	return fmt.Sprintf("%d h %02d", min/60, min%60)
}
