package section

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
	// Judge "nothing since the replay" only once Home Assistant has been up this long. An MQTT
	// entity whose value did not change is not reported again, so the judgement is per device:
	// one live entity (its temperature moved) clears the device. The brief can run 1 h 45 after
	// the nightly backup's restart, and 45 min after an image update's.
	restartSilence = 75 * time.Minute
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

// deviceKey groups the entities of one device: the object id without its last `_<word>`
// (sensor.room_a_temperature and sensor.room_a_humidity → room_a); an object id without `_` is
// its own device.
func deviceKey(id string) string {
	obj := id[strings.IndexByte(id, '.')+1:]
	if i := strings.LastIndexByte(obj, '_'); i > 0 {
		return obj[:i]
	}
	return obj
}

// deviceName is the words the entities' friendly names share at their start (else at their end),
// else the names joined; a single entity's name is its own.
func deviceName(sts []ha.State) string {
	if len(sts) == 1 {
		return entityName(sts[0])
	}
	names := make([][]string, len(sts))
	for i, st := range sts {
		names[i] = strings.Fields(entityName(st))
	}
	common := func(at func(ws []string, i int) string) int {
		n := 0
		for ; ; n++ {
			for _, ws := range names {
				if n >= len(ws) || at(ws, n) != at(names[0], n) {
					return n
				}
			}
		}
	}
	if n := common(func(ws []string, i int) string { return ws[i] }); n > 0 && n < len(names[0]) {
		return strings.Join(names[0][:n], " ")
	}
	if n := common(func(ws []string, i int) string { return ws[len(ws)-1-i] }); n > 0 && n < len(names[0]) {
		return strings.Join(names[0][len(names[0])-n:], " ")
	}
	parts := make([]string, len(sts))
	for i, st := range sts {
		parts[i] = entityName(st)
	}
	return strings.Join(parts, " / ")
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
	up := env.Now.Sub(start)
	judgeRestart := !start.IsZero() && up >= restartSilence
	if !start.IsZero() && up >= 0 && up < restartSilence {
		res.Facts = append(res.Facts, brief.Fact{Key: "restart",
			Value: "HA restarted at " + start.In(env.Loc).Format("15:04") + ", dead sensors cannot be told yet"})
	}
	devices := map[string][]string{}
	var keys []string
	for id := range snap.ByID {
		if matchAny(cfg.Include, id) && !matchAny(cfg.Exclude, id) {
			k := deviceKey(id)
			if devices[k] == nil {
				keys = append(keys, k)
			}
			devices[k] = append(devices[k], id)
		}
	}
	sort.Strings(keys)
	limit := time.Duration(cfg.StaleAfterHours * float64(time.Hour))
	for _, k := range keys {
		ids := devices[k]
		sort.Strings(ids)
		sts := make([]ha.State, len(ids))
		listed, replayOnly := 0, judgeRestart
		for i, id := range ids {
			st := snap.ByID[id]
			sts[i] = st
			heard := lastHeard(st)
			if d := heard.Sub(start); heard.IsZero() || d < 0 || d > restartReplayWindow {
				replayOnly = false
			}
			// unknown / unavailable and the 24 h rule: per entity
			var why string
			switch {
			case st.State == "unavailable" || st.State == "unknown":
				why = "no data (" + st.State + ")"
			case heard.IsZero() || env.Now.Sub(heard) > limit:
				why = "no data"
				if !heard.IsZero() {
					why = "no data since " + stamp(heard, env.Loc)
				}
			}
			if why == "" {
				continue
			}
			listed++
			res.Items = append(res.Items, brief.Item{Facts: []brief.Fact{
				brief.Untrusted("sensor", entityName(st)), {Key: "status", Value: why},
			}})
		}
		// Every entity of the device heard only in the restart's replay: the device is silent.
		// Nothing to add when each of its entities is already listed.
		if replayOnly && listed < len(ids) {
			res.Items = append(res.Items, brief.Item{Facts: []brief.Fact{
				brief.Untrusted("sensor", deviceName(sts)),
				{Key: "status", Value: "not heard since the HA restart at " + start.In(env.Loc).Format("15:04")},
			}})
		}
	}
	capItems(&res)
	res.Status = brief.StatusOK
	if len(res.Items) > 0 || len(res.Facts) > 0 {
		res.Status = brief.StatusReport
	}
	return res
}
