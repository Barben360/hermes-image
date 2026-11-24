package section

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

func carCfg() *CarConfig {
	c := newCarConfig()
	c.Battery, c.Range, c.MaxRange = "sensor.car_battery", "sensor.car_range", "sensor.car_max_range"
	c.Plug, c.Charging, c.ChargeState = "sensor.car_plug", "binary_sensor.car_charging", "sensor.car_charge_state"
	c.Target, c.ChargeTimeRemaining = "number.car_target", "sensor.car_charge_time"
	c.Tracker, c.HVACThreshold = "device_tracker.car", "number.car_hvac_threshold"
	return c
}

func carRun(t *testing.T, override map[string]string, cfg *CarConfig) brief.Result {
	t.Helper()
	return collect(t, "car", haEnv(haFixture(t, override), nil), cfg)
}

func want(t *testing.T, r brief.Result, key, val string) {
	t.Helper()
	f, ok := fact(r, key)
	if !ok || f.Value != val {
		t.Errorf("%s = %q (present %v), want %q\n%s", key, f.Value, ok, val, dump(r))
	}
}

func TestCarReport(t *testing.T) {
	r := carRun(t, nil, carCfg())
	if r.Status != brief.StatusReport {
		t.Fatalf("%s %s", r.Status, r.Err)
	}
	want(t, r, "battery", "25%")
	want(t, r, "range", "87 km")
	want(t, r, "max_range", "348 km")
	want(t, r, "plugged", "no")
	want(t, r, "target", "80%")
	want(t, r, "zone", "home")
	want(t, r, "warning", "no")
}

func TestCarWarningBelow20(t *testing.T) {
	want(t, carRun(t, map[string]string{"sensor.car_battery": "19"}, carCfg()), "warning", "yes (below 20%)")
	want(t, carRun(t, map[string]string{"sensor.car_battery": "20"}, carCfg()), "warning", "no")
}

func TestCarWarnBelowIsConfigurable(t *testing.T) {
	c := carCfg()
	c.WarnBelowPct = 30
	want(t, carRun(t, nil, c), "warning", "yes (below 30%)")
}

func TestCarPrintsAsOf(t *testing.T) {
	r := carRun(t, nil, carCfg())
	var out bytes.Buffer
	err := brief.RenderText(&out, brief.Doc{Now: haEnv(nil, nil).Now, Results: []brief.Result{r}}, brief.NewSecrets(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	// the renderer joins date and time with T
	if !strings.Contains(out.String(), "battery: 25% (as_of 2026-11-24T18:49)") {
		t.Errorf("%s", out.String())
	}
}

func TestCarHVACThreshold(t *testing.T) {
	r := carRun(t, map[string]string{"sensor.car_battery": "7", "number.car_hvac_threshold": "8"}, carCfg())
	want(t, r, "hvac", "below threshold (8%)")
	r = carRun(t, map[string]string{"sensor.car_battery": "9", "number.car_hvac_threshold": "8"}, carCfg())
	if _, ok := fact(r, "hvac"); ok {
		t.Errorf("hvac warning at 9%%\n%s", dump(r))
	}
}

func TestCarChargeTimeOnlyWhileCharging(t *testing.T) {
	r := carRun(t, nil, carCfg()) // unplugged, 132 min remaining
	if _, ok := fact(r, "charge"); ok {
		t.Errorf("charge time while unplugged\n%s", dump(r))
	}
	r = carRun(t, map[string]string{"sensor.car_plug": "plugged", "binary_sensor.car_charging": "on", "sensor.car_charge_state": "charge_in_progress"}, carCfg())
	want(t, r, "charge", "done in 2 h 12")
	want(t, r, "plugged", "yes")
	want(t, r, "charging", "yes")
	want(t, r, "charge_state", "charge_in_progress")
}

func TestCarUnavailableBatteryWarnsUnknown(t *testing.T) {
	for _, v := range []string{"unavailable", "unknown", "n/a"} {
		r := carRun(t, map[string]string{"sensor.car_battery": v, "number.car_hvac_threshold": "8"}, carCfg())
		want(t, r, "battery", "unavailable")
		want(t, r, "warning", "unknown (no battery data)")
		if _, ok := fact(r, "hvac"); ok {
			t.Errorf("%s: hvac judged without a battery level", v)
		}
	}
}

// TestCarBatteryTimestampIsAsOf: a cloud integration polls the car, so the battery entity's
// last_reported is the poll; the data's own time is a separate timestamp entity.
func TestCarBatteryTimestampIsAsOf(t *testing.T) {
	c := carCfg()
	c.BatteryTimestamp = "sensor.car_battery_activity"
	for state, asOf := range map[string]string{
		"2026-11-24T16:12:00+00:00":        "2026-11-24T17:12", // UTC data time, printed in Paris time
		"2026-11-24T16:12:00.123456+00:00": "2026-11-24T17:12",
		"unknown":                          "2026-11-24T18:49", // unreadable: last_reported, as without the key
	} {
		snap := haFixture(t, nil)
		snap.ByID[c.BatteryTimestamp] = ha.State{EntityID: c.BatteryTimestamp, State: state,
			Attributes: map[string]any{"device_class": "timestamp"}}
		r := collect(t, "car", haEnv(snap, nil), c)
		f, ok := fact(r, "battery")
		if !ok || f.AsOf.Format("2006-01-02T15:04") != asOf || f.AsOf.Location().String() != "Europe/Paris" {
			t.Errorf("%s: as_of %s (%s), want %s", state, f.AsOf, f.AsOf.Location(), asOf)
		}
	}
	// a configured timestamp entity that does not exist is an error, like every configured id
	r := collect(t, "car", haEnv(haFixture(t, nil), nil), c)
	if r.Status != brief.StatusError || !strings.Contains(r.Err, c.BatteryTimestamp) {
		t.Errorf("%s %q", r.Status, r.Err)
	}
	c.BatteryTimestamp = c.Battery
	if c.Validate() == nil {
		t.Error("battery_timestamp = battery accepted")
	}
}

func TestCarNoCoordinates(t *testing.T) {
	r := carRun(t, nil, carCfg())
	if s := dump(r); strings.Contains(s, "45.0") || strings.Contains(s, "gps") {
		t.Errorf("%s", s)
	}
}

func TestCarDuplicateEntityFailsValidate(t *testing.T) {
	c := carCfg()
	c.Range = c.Battery
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "sensor.car_battery") {
		t.Errorf("%v", err)
	}
}

func TestCarEmptyRequiredFailsValidate(t *testing.T) {
	err := newCarConfig().Validate()
	if err == nil || !strings.Contains(err.Error(), "set car.battery (morning-brief config set") {
		t.Errorf("%v", err)
	}
	if err := carCfg().Validate(); err != nil {
		t.Errorf("full config: %v", err)
	}
	c := carCfg()
	c.MaxRange, c.ChargeTimeRemaining, c.HVACThreshold, c.ChargeState = "", "", "", ""
	if err := c.Validate(); err != nil {
		t.Errorf("optional ids empty: %v", err)
	}
}

func TestCarDefaultsHoldNoEntityIDs(t *testing.T) {
	c := newCarConfig()
	if c.Battery+c.Range+c.MaxRange+c.Plug+c.Charging+c.ChargeState+c.Target+c.ChargeTimeRemaining+c.Tracker+c.HVACThreshold != "" || c.WarnBelowPct != 20 || !c.Enabled {
		t.Errorf("%+v", c)
	}
}

func TestCarMissingEntityIsError(t *testing.T) {
	snap := haFixture(t, nil)
	delete(snap.ByID, "sensor.car_range")
	r := collect(t, "car", haEnv(snap, nil), carCfg())
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "sensor.car_range") {
		t.Errorf("%s %q", r.Status, r.Err)
	}
}

func TestCarHAErrorIsError(t *testing.T) {
	r := collect(t, "car", haEnv(nil, errString("Home Assistant: 401 (token wrong or expired)")), carCfg())
	if r.Status != brief.StatusError || r.Err != "Home Assistant: 401 (token wrong or expired)" {
		t.Errorf("%s %q", r.Status, r.Err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestCarRegistered(t *testing.T) {
	for _, s := range brief.Sections() {
		if s.Name == "car" {
			if len(s.Credentials) != 1 || s.Credentials[0] != "ha_token" || s.ThirdParty || s.Description == "" {
				t.Errorf("%+v", s)
			}
			return
		}
	}
	t.Error("not registered")
}
