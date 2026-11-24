package section

import (
	"strings"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

func presenceCfg() *PresenceConfig {
	c := newPresenceConfig()
	c.Person = "person.alex"
	return c
}

func TestPresenceZoneNameOnly(t *testing.T) {
	r := collect(t, "presence", haEnv(haFixture(t, nil), nil), presenceCfg())
	if r.Status != brief.StatusReport {
		t.Fatalf("%s %s", r.Status, r.Err)
	}
	want(t, r, "zone", "home since 2026-11-24 18:30")
	for _, f := range r.Facts {
		for _, bad := range []string{fakeLat, fakeLon, "gps", "latitude", "longitude"} {
			if strings.Contains(f.Key+f.Value, bad) {
				t.Errorf("fact %s=%s holds %q", f.Key, f.Value, bad)
			}
		}
	}
}

func TestPresenceAway(t *testing.T) {
	r := collect(t, "presence", haEnv(haFixture(t, map[string]string{"person.alex": "not_home"}), nil), presenceCfg())
	want(t, r, "zone", "not_home since 2026-11-24 18:30")
}

func TestPresenceMissingEntityIsError(t *testing.T) {
	c := presenceCfg()
	c.Person = "person.nobody"
	r := collect(t, "presence", haEnv(haFixture(t, nil), nil), c)
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "person.nobody") {
		t.Errorf("%s %q", r.Status, r.Err)
	}
}

func TestPresenceValidate(t *testing.T) {
	if err := newPresenceConfig().Validate(); err == nil || !strings.Contains(err.Error(), "set presence.person (morning-brief config set") {
		t.Errorf("%v", err)
	}
	if err := presenceCfg().Validate(); err != nil {
		t.Error(err)
	}
}

func TestPresenceHAError(t *testing.T) {
	r := collect(t, "presence", haEnv(nil, errString("Home Assistant: HTTP 500")), presenceCfg())
	if r.Status != brief.StatusError || r.Err != "Home Assistant: HTTP 500" {
		t.Errorf("%s %q", r.Status, r.Err)
	}
}
