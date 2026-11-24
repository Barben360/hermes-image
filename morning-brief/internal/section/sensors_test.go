package section

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

func sensorsCfg() *SensorsConfig {
	c := newSensorsConfig()
	c.Include = []string{"sensor.room_*"}
	return c
}

func loadSnapshot(t *testing.T, file string) *ha.Snapshot {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var wire []map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	snap := &ha.Snapshot{ByID: map[string]ha.State{}}
	for _, m := range wire {
		id := m["entity_id"].(string)
		st := ha.State{EntityID: id, State: m["state"].(string), Attributes: m["attributes"].(map[string]any)}
		st.LastChanged, _ = time.Parse(time.RFC3339, m["last_changed"].(string))
		st.LastUpdated, _ = time.Parse(time.RFC3339, m["last_updated"].(string))
		st.LastReported, _ = time.Parse(time.RFC3339, m["last_reported"].(string))
		snap.ByID[id] = st
	}
	return snap
}

// ageSnap: Home Assistant up for 30 h (a marker entity), one sensor last reported `age` ago.
func ageSnap(env *brief.Env, state string, age time.Duration, attrs map[string]any) *ha.Snapshot {
	snap := &ha.Snapshot{ByID: map[string]ha.State{}}
	start := env.Now.Add(-30 * time.Hour)
	snap.ByID["sun.sun"] = ha.State{EntityID: "sun.sun", State: "above_horizon", LastChanged: start, LastReported: start}
	if attrs == nil {
		attrs = map[string]any{}
	}
	attrs["friendly_name"] = "Room X"
	rep := env.Now.Add(-age)
	snap.ByID["sensor.room_x"] = ha.State{EntityID: "sensor.room_x", State: state, Attributes: attrs, LastChanged: rep, LastUpdated: rep, LastReported: rep}
	return snap
}

func sensorNames(r brief.Result) string {
	var b strings.Builder
	for _, it := range r.Items {
		for _, f := range it.Facts {
			b.WriteString(f.Key + "=" + f.Value + ";")
		}
	}
	return b.String()
}

func TestSensorsStaleThreshold(t *testing.T) {
	env := haEnv(nil, nil)
	env.HA = func(ctx context.Context) (*ha.Snapshot, error) { return ageSnap(env, "20", 23*time.Hour, nil), nil }
	r := collect(t, "sensors", env, sensorsCfg())
	if r.Status != brief.StatusOK || len(r.Items) != 0 {
		t.Errorf("23 h listed: %s", sensorNames(r))
	}
	env.HA = func(ctx context.Context) (*ha.Snapshot, error) { return ageSnap(env, "20", 25*time.Hour, nil), nil }
	r = collect(t, "sensors", env, sensorsCfg())
	if r.Status != brief.StatusReport || !strings.Contains(sensorNames(r), "status=no data since 2026-11-24 06:15;") {
		t.Errorf("%s %s", r.Status, sensorNames(r))
	}
	c := sensorsCfg()
	c.StaleAfterHours = 12
	env.HA = func(ctx context.Context) (*ha.Snapshot, error) { return ageSnap(env, "20", 13*time.Hour, nil), nil }
	if r = collect(t, "sensors", env, c); len(r.Items) != 1 {
		t.Errorf("stale_after_hours ignored: %s", sensorNames(r))
	}
}

func TestSensorsUnknownIsStale(t *testing.T) {
	for _, s := range []string{"unknown", "unavailable"} {
		env := haEnv(nil, nil)
		env.HA = func(ctx context.Context) (*ha.Snapshot, error) { return ageSnap(env, s, time.Minute, nil), nil }
		r := collect(t, "sensors", env, sensorsCfg())
		if !strings.Contains(sensorNames(r), "status=no data ("+s+");") {
			t.Errorf("%s: %s", s, sensorNames(r))
		}
	}
}

func TestSensorsNeverBatteryPercent(t *testing.T) {
	env := haEnv(nil, nil)
	env.HA = func(ctx context.Context) (*ha.Snapshot, error) {
		return ageSnap(env, "3", time.Minute, map[string]any{"device_class": "battery", "unit_of_measurement": "%"}), nil
	}
	r := collect(t, "sensors", env, sensorsCfg())
	if r.Status != brief.StatusOK || len(r.Items) != 0 || strings.Contains(dump(r), "3") {
		t.Errorf("%s %s", r.Status, sensorNames(r))
	}
}

func TestSensorsLastSeenWins(t *testing.T) {
	env := haEnv(nil, nil)
	seen := env.Now.Add(-40 * time.Hour).Format(time.RFC3339)
	env.HA = func(ctx context.Context) (*ha.Snapshot, error) {
		return ageSnap(env, "20", time.Minute, map[string]any{"last_seen": seen}), nil
	}
	if r := collect(t, "sensors", env, sensorsCfg()); len(r.Items) != 1 {
		t.Errorf("last_seen ignored: %s", sensorNames(r))
	}
}

func TestSensorsIncludeExclude(t *testing.T) {
	env := haEnv(nil, nil)
	env.HA = func(ctx context.Context) (*ha.Snapshot, error) { return ageSnap(env, "unknown", time.Minute, nil), nil }
	c := sensorsCfg()
	c.Exclude = []string{"sensor.room_x"}
	if r := collect(t, "sensors", env, c); len(r.Items) != 0 {
		t.Errorf("excluded sensor listed")
	}
	c = sensorsCfg()
	c.Include = []string{"sensor.other_*"}
	r := collect(t, "sensors", env, c)
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "sensor.other_*") {
		t.Errorf("zero-match include: %s %q", r.Status, r.Err)
	}
}

// TestSensorsDeadAcrossRestart: Home Assistant restarted at 03:00; Zigbee2MQTT's retained replay gave
// every sensor a fresh state, so a dead one looks only 4 h old at 07:15.
func TestSensorsDeadAcrossRestart(t *testing.T) {
	snap := loadSnapshot(t, "../../testdata/ha/states-after-restart.json")
	env := haEnv(snap, nil) // 2026-11-25 07:15 Europe/Paris
	c := sensorsCfg()
	c.Include = []string{"sensor.room_*_temperature", "sensor.room_*_humidity"}
	r := collect(t, "sensors", env, c)
	got := sensorNames(r)
	for _, dead := range []string{"Room B Temperature", "Room B Humidity"} {
		if !strings.Contains(got, "sensor=\""+dead+"\"") && !strings.Contains(got, "sensor="+dead+";") {
			t.Errorf("dead %s not listed: %s", dead, got)
		}
	}
	for _, live := range []string{"Room A Temperature", "Room A Humidity", "Room C Temperature"} {
		if strings.Contains(got, live) {
			t.Errorf("live %s listed: %s", live, got)
		}
	}
	if len(r.Items) != 2 {
		t.Errorf("%d items: %s", len(r.Items), got)
	}
	// right after the restart nothing is judged yet
	env.Now = time.Date(2026, 11, 25, 3, 30, 0, 0, env.Loc)
	if r = collect(t, "sensors", env, c); len(r.Items) != 1 { // only the unknown one
		t.Errorf("judged too early: %s", sensorNames(r))
	}
}

func TestSensorsValidate(t *testing.T) {
	err := newSensorsConfig().Validate()
	if err == nil || !strings.Contains(err.Error(), "set sensors.include") {
		t.Errorf("%v", err)
	}
	c := sensorsCfg()
	c.Include = []string{"sensor.[x"}
	if c.Validate() == nil {
		t.Error("bad glob accepted")
	}
}

func TestSensorsExactBoundary(t *testing.T) {
	env := haEnv(nil, nil)
	for age, wantN := range map[time.Duration]int{24 * time.Hour: 0, 24*time.Hour + time.Second: 1} {
		env.HA = func(ctx context.Context) (*ha.Snapshot, error) { return ageSnap(env, "20", age, nil), nil }
		if r := collect(t, "sensors", env, sensorsCfg()); len(r.Items) != wantN {
			t.Errorf("age %s: %d items", age, len(r.Items))
		}
	}
}

func TestSensorsReportBeforeRestartNotFlaggedByReplayClause(t *testing.T) {
	env := haEnv(nil, nil)
	env.HA = func(ctx context.Context) (*ha.Snapshot, error) {
		snap := ageSnap(env, "20", 31*time.Hour, nil) // reported 1 h before HA's start (30 h ago), within 24 h? no: 31 h
		st := snap.ByID["sensor.room_x"]
		st.LastReported = env.Now.Add(-time.Hour) // HA start 30 h ago: report is after; shift start instead
		snap.ByID["sun.sun"] = ha.State{EntityID: "sun.sun", State: "x", LastChanged: env.Now.Add(-5 * time.Hour)}
		st.LastReported = env.Now.Add(-5*time.Hour - time.Minute) // 1 min BEFORE the start
		st.LastChanged = env.Now.Add(-time.Hour)
		snap.ByID["sensor.room_x"] = st
		return snap, nil
	}
	if r := collect(t, "sensors", env, sensorsCfg()); len(r.Items) != 0 {
		t.Errorf("flagged: %s", sensorNames(r))
	}
}

func TestSensorsCapsList(t *testing.T) {
	env := haEnv(nil, nil)
	env.HA = func(ctx context.Context) (*ha.Snapshot, error) {
		snap := ageSnap(env, "unknown", time.Minute, nil)
		for i := 0; i < 24; i++ {
			id := "sensor.room_" + string(rune('a'+i))
			snap.ByID[id] = ha.State{EntityID: id, State: "unknown", Attributes: map[string]any{}}
		}
		return snap, nil
	}
	r := collect(t, "sensors", env, sensorsCfg())
	if len(r.Items) != 20 {
		t.Errorf("%d items", len(r.Items))
	}
	want(t, r, "more", "+4 more")
}
