package section

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

// SensorsConfig selects the room sensors to watch for silence. Include is empty by default (site data).
type SensorsConfig struct {
	Enabled         bool     `yaml:"enabled"`
	Include         []string `yaml:"include"`           // entity id globs (path.Match)
	Exclude         []string `yaml:"exclude"`           // entity id globs removed from include
	StaleAfterHours float64  `yaml:"stale_after_hours"` // silent for longer than this = stopped reporting
}

func newSensorsConfig() *SensorsConfig { return &SensorsConfig{Enabled: true, StaleAfterHours: 24} }

func (c *SensorsConfig) IsEnabled() bool { return c.Enabled }

func (c *SensorsConfig) Validate() error {
	if len(c.Include) == 0 {
		return fmt.Errorf("set sensors.include (morning-brief config set sensors.include [sensor.room_*_temperature])")
	}
	for _, p := range append(append([]string(nil), c.Include...), c.Exclude...) {
		if _, err := matchGlob(p, "x"); err != nil {
			return fmt.Errorf("sensors: bad glob %q", p)
		}
	}
	if c.StaleAfterHours <= 0 {
		return fmt.Errorf("sensors.stale_after_hours must be above 0")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "sensors",
		Description: "Room sensors that stopped reporting (never their battery level) (Home Assistant)",
		Credentials: []string{"ha_token"},
		Default:     func() brief.SectionConfig { return newSensorsConfig() },
		Collect:     collectSensors,
	})
}

const (
	// Right after Home Assistant starts, Zigbee2MQTT's retained messages give every MQTT sensor a
	// fresh state and last_reported, dead or alive. A report that close to the start is only that replay.
	restartReplayWindow = 10 * time.Minute
	// Judge "nothing since the replay" only once Home Assistant has been up this long: a live sensor
	// can stay quiet for a couple of hours.
	restartSilence = 3 * time.Hour
)

// restartAt estimates when Home Assistant started: every state is rewritten at start, so the
// oldest last_changed of the whole snapshot is the start.
func restartAt(snap *ha.Snapshot) time.Time {
	var min time.Time
	for _, st := range snap.ByID {
		if st.LastChanged.IsZero() {
			continue
		}
		if min.IsZero() || st.LastChanged.Before(min) {
			min = st.LastChanged
		}
	}
	return min
}

// lastHeard is when the sensor itself last spoke: Zigbee2MQTT's last_seen attribute when exposed,
// else Home Assistant's last_reported.
func lastHeard(st ha.State) time.Time {
	if s, ok := st.Attributes["last_seen"].(string); ok {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return reported(st)
}

func collectSensors(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	res := brief.Result{Name: "sensors"}
	cfg := sc.(*SensorsConfig)
	snap, err := haSnapshot(ctx, env)
	if err != nil {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	for _, p := range cfg.Include {
		found := false
		for id := range snap.ByID {
			if ok, _ := matchGlob(p, id); ok {
				found = true
				break
			}
		}
		if !found {
			res.Status, res.Err = brief.StatusError, fmt.Sprintf("sensors.include %q matches no entity in Home Assistant", p)
			return res
		}
	}
	start := restartAt(snap)
	var ids []string
	for id := range snap.ByID {
		if matchAny(cfg.Include, id) && !matchAny(cfg.Exclude, id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	limit := time.Duration(cfg.StaleAfterHours * float64(time.Hour))
	for _, id := range ids {
		st := snap.ByID[id]
		heard := lastHeard(st)
		var why string
		switch {
		case st.State == "unavailable" || st.State == "unknown":
			why = "no data (" + st.State + ")"
		case heard.IsZero() || env.Now.Sub(heard) > limit:
			why = "no data"
			if !heard.IsZero() {
				why = "no data since " + stamp(heard, env.Loc)
			}
		case !start.IsZero() && heard.Sub(start) >= 0 && heard.Sub(start) <= restartReplayWindow && env.Now.Sub(start) >= restartSilence:
			why = "no data since " + stamp(start, env.Loc) + " (only the Home Assistant restart)"
		}
		if why == "" {
			continue
		}
		res.Items = append(res.Items, brief.Item{Facts: []brief.Fact{
			brief.Untrusted("sensor", entityName(st)), {Key: "status", Value: why},
		}})
	}
	capItems(&res)
	res.Status = brief.StatusOK
	if len(res.Items) > 0 {
		res.Status = brief.StatusReport
	}
	return res
}
