package section

import (
	"strings"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

func feederCfg() *PetFeederConfig {
	c := newPetFeederConfig()
	c.FoodLevel, c.Fault, c.FeedSuccess = "sensor.feeder_food", "sensor.feeder_fault", "event.feeder_feedsuccess"
	return c
}

func feederRun(t *testing.T, mod map[string]string) brief.Result {
	t.Helper()
	st := map[string]string{
		"sensor.feeder_food": "Normal", "sensor.feeder_fault": "No Faults",
		"event.feeder_feedsuccess": "2026-11-24T07:15:00+01:00", // 24 h before the fixed now
		"sensor.feeder_desiccant":  "0",
	}
	for k, v := range mod {
		st[k] = v
	}
	return collect(t, "pet_feeder", haEnv(snapOf(st, nil), nil), feederCfg())
}

func TestPetFeederFoodLow(t *testing.T) {
	if r := feederRun(t, nil); len(r.Facts) != 0 {
		t.Errorf("normal reported: %s", dump(r))
	}
	for _, s := range []string{"Low", "Empty"} {
		r := feederRun(t, map[string]string{"sensor.feeder_food": s})
		want(t, r, "food", s)
		if r.Status != brief.StatusReport {
			t.Errorf("%s: status %s", s, r.Status)
		}
	}
	for _, s := range []string{"unknown", "unavailable"} {
		want(t, feederRun(t, map[string]string{"sensor.feeder_food": s}), "food", "no data")
	}
	c := feederCfg()
	c.FoodOKStates = []string{"Normal", "Low"}
	r := collect(t, "pet_feeder", haEnv(snapOf(map[string]string{"sensor.feeder_food": "Low", "sensor.feeder_fault": "OK", "event.feeder_feedsuccess": "2026-11-25T06:00:00+01:00"}, nil), nil), c)
	if len(r.Facts) != 0 {
		t.Errorf("food_ok_states ignored: %s", dump(r))
	}
}

func TestPetFeederFault(t *testing.T) {
	for _, ok := range []string{"No Faults", "OK"} {
		if r := feederRun(t, map[string]string{"sensor.feeder_fault": ok}); len(r.Facts) != 0 {
			t.Errorf("%s reported: %s", ok, dump(r))
		}
	}
	want(t, feederRun(t, map[string]string{"sensor.feeder_fault": "Error"}), "fault", "Error")
	want(t, feederRun(t, map[string]string{"sensor.feeder_fault": "Timeout"}), "fault", "Timeout")
	want(t, feederRun(t, map[string]string{"sensor.feeder_fault": "unavailable"}), "fault", "no data")
	// the desiccant at 0 days is never reported (owner's call)
	r := feederRun(t, map[string]string{"sensor.feeder_desiccant": "0"})
	if strings.Contains(dump(r), "desiccant") || len(r.Facts) != 0 {
		t.Errorf("%s", dump(r))
	}
}

func TestPetFeederNoFeed(t *testing.T) {
	// now is 2026-11-25 07:15
	if r := feederRun(t, map[string]string{"event.feeder_feedsuccess": "2026-11-24T06:15:00+01:00"}); len(r.Facts) != 0 {
		t.Errorf("25 h reported: %s", dump(r))
	}
	r := feederRun(t, map[string]string{"event.feeder_feedsuccess": "2026-11-24T04:15:00+01:00"})
	want(t, r, "feed", "no successful feed since 2026-11-24 04:15")
	want(t, feederRun(t, map[string]string{"event.feeder_feedsuccess": "unknown"}), "feed", "no feed recorded")
	want(t, feederRun(t, map[string]string{"event.feeder_feedsuccess": "unavailable"}), "feed", "no data")
	// UTC timestamps are shown in local time
	r = feederRun(t, map[string]string{"event.feeder_feedsuccess": "2026-11-24T03:15:00.250+00:00"})
	want(t, r, "feed", "no successful feed since 2026-11-24 04:15")
}

func TestPetFeederOkWhenNothing(t *testing.T) {
	if r := feederRun(t, nil); r.Status != brief.StatusOK {
		t.Errorf("%s %s", r.Status, r.Err)
	}
}

func TestPetFeederValidate(t *testing.T) {
	err := newPetFeederConfig().Validate()
	if err == nil || !strings.Contains(err.Error(), "set pet_feeder.food_level") {
		t.Errorf("%v", err)
	}
	if err := feederCfg().Validate(); err != nil {
		t.Error(err)
	}
}

func TestPetFeederMissingEntity(t *testing.T) {
	st := map[string]string{"sensor.feeder_food": "Normal", "sensor.feeder_fault": "OK"}
	r := collect(t, "pet_feeder", haEnv(snapOf(st, nil), nil), feederCfg())
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "event.feeder_feedsuccess") {
		t.Errorf("%s %q", r.Status, r.Err)
	}
}

func TestPetFeederExactBoundary(t *testing.T) {
	// now 2026-11-25 07:15; exactly 26 h ago is fine, one second more is not
	if r := feederRun(t, map[string]string{"event.feeder_feedsuccess": "2026-11-24T05:15:00+01:00"}); len(r.Facts) != 0 {
		t.Errorf("26 h reported: %s", dump(r))
	}
	r := feederRun(t, map[string]string{"event.feeder_feedsuccess": "2026-11-24T05:14:59+01:00"})
	want(t, r, "feed", "no successful feed since 2026-11-24 05:14")
}
