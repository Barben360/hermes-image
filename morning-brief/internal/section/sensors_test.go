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

// restartSnap: the post-restart fixture (Home Assistant started 2026-11-25 03:01:20 UTC by the
// nightly backup) moved by d; any report after now becomes the restart's replay (start + 2 min).
func restartSnap(t *testing.T, d time.Duration, now time.Time) *ha.Snapshot {
	t.Helper()
	snap := loadSnapshot(t, "../../testdata/ha/states-after-restart.json")
	start := time.Date(2026, 11, 25, 3, 1, 20, 0, time.UTC).Add(d)
	for id, st := range snap.ByID {
		for _, p := range []*time.Time{&st.LastChanged, &st.LastUpdated, &st.LastReported} {
			*p = p.Add(d)
			if p.After(now) {
				*p = start.Add(2 * time.Minute)
			}
		}
		snap.ByID[id] = st
	}
	return snap
}

func restartCfg() *SensorsConfig {
	c := sensorsCfg()
	c.Include = []string{"sensor.room_*_temperature", "sensor.room_*_humidity"}
	return c
}

func restartRun(t *testing.T, d time.Duration, now time.Time) brief.Result {
	t.Helper()
	env := haEnv(nil, nil)
	env.Now = now.In(env.Loc)
	snap := restartSnap(t, d, now)
	env.HA = func(context.Context) (*ha.Snapshot, error) { return snap, nil }
	return collect(t, "sensors", env, restartCfg())
}

// summer: the same nightly restart at 03:01 UTC, moved to a July day (Paris = UTC+2).
const toSummer = -133 * 24 * time.Hour // 2026-11-25 → 2026-07-15

// TestSensorsDeadAcrossRestart: Home Assistant restarted at 03:01 UTC by the backup; Zigbee2MQTT's
// retained replay gave every sensor a fresh state, so a dead device looks 2 h old at 07:15. An MQTT
// entity whose value did not change is not reported again: Room A's humidity is as old as the
// replay, but its temperature moved, so Room A is alive.
func TestSensorsDeadAcrossRestart(t *testing.T) {
	for _, tc := range []struct {
		name, at string
		d        time.Duration
		now      time.Time
	}{
		{"winter 06:15 UTC", "04:01", 0, time.Date(2026, 11, 25, 6, 15, 0, 0, time.UTC)},
		{"summer 05:15 UTC", "05:01", toSummer, time.Date(2026, 7, 15, 5, 15, 0, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := restartRun(t, tc.d, tc.now)
			got := sensorNames(r)
			wantItems := `sensor=Room B;status=not heard since the HA restart at ` + tc.at + `;` +
				`sensor=Room D Humidity;status=no data (unknown);`
			if r.Status != brief.StatusReport || got != wantItems {
				t.Errorf("%s\n got %s\nwant %s", r.Status, got, wantItems)
			}
			if _, ok := fact(r, "restart"); ok {
				t.Errorf("restart fact: %s", dump(r))
			}
		})
	}
}

// TestSensorsTooSoonAfterRestart: on an image-update day Home Assistant restarts again at 04:31 UTC;
// at 05:15 UTC (summer brief) it has been up 44 min, every sensor still looks like the replay, and
// the brief says the sensors cannot be judged yet instead of saying nothing.
func TestSensorsTooSoonAfterRestart(t *testing.T) {
	r := restartRun(t, toSummer+90*time.Minute, time.Date(2026, 7, 15, 5, 15, 0, 0, time.UTC))
	if r.Status != brief.StatusReport {
		t.Fatalf("%s %s", r.Status, sensorNames(r))
	}
	want(t, r, "restart", "HA restarted at 06:31, dead sensors cannot be told yet")
	if got := sensorNames(r); got != `sensor=Room D Humidity;status=no data (unknown);` {
		t.Errorf("judged too early: %s", got)
	}
}

// TestSensorsRestartBoundary: the device judgement starts at exactly 75 min of uptime.
func TestSensorsRestartBoundary(t *testing.T) {
	start := time.Date(2026, 11, 25, 3, 1, 20, 0, time.UTC)
	r := restartRun(t, 0, start.Add(75*time.Minute))
	if _, ok := fact(r, "restart"); ok || !strings.Contains(sensorNames(r), `sensor=Room B;status=not heard since the HA restart at 04:01;`) {
		t.Errorf("75 min: %s / %s", dump(r), sensorNames(r))
	}
	r = restartRun(t, 0, start.Add(75*time.Minute-time.Second))
	if strings.Contains(sensorNames(r), `sensor=Room B;`) {
		t.Errorf("75 min - 1 s judged: %s", sensorNames(r))
	}
	want(t, r, "restart", "HA restarted at 04:01, dead sensors cannot be told yet")
}

func TestSensorsDeviceKeyAndName(t *testing.T) {
	for id, k := range map[string]string{
		"sensor.room_a_temperature": "room_a", "sensor.room_a_humidity": "room_a",
		"sensor.kitchen": "kitchen", "binary_sensor.door_contact": "door",
	} {
		if got := deviceKey(id); got != k {
			t.Errorf("deviceKey(%s) = %s, want %s", id, got, k)
		}
	}
	st := func(n string) ha.State { return ha.State{Attributes: map[string]any{"friendly_name": n}} }
	for want, sts := range map[string][]ha.State{
		"Chambre":             {st("Chambre température"), st("Chambre humidité")},
		"chambre":             {st("Température chambre"), st("Humidité chambre")},
		"Salon":               {st("Salon")},
		"Capteur 1 / Sonde 2": {st("Capteur 1"), st("Sonde 2")},
	} {
		if got := deviceName(sts); got != want {
			t.Errorf("deviceName = %q, want %q", got, want)
		}
	}
}

// TestSensorsDeviceAllListedNotRepeated: a single-entity device already listed as unknown is not
// listed a second time for the restart.
func TestSensorsDeviceAllListedNotRepeated(t *testing.T) {
	r := restartRun(t, 0, time.Date(2026, 11, 25, 6, 15, 0, 0, time.UTC))
	if n := strings.Count(sensorNames(r), "Room D"); n != 1 {
		t.Errorf("Room D listed %d times: %s", n, sensorNames(r))
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
