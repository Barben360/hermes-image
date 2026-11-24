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

const fakeLat, fakeLon = "45", "5"

// haFixture loads testdata/ha/states.json with overrides {entity id: state}.
func haFixture(t *testing.T, override map[string]string) *ha.Snapshot {
	t.Helper()
	b, err := os.ReadFile("../../testdata/ha/states.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	snap := &ha.Snapshot{ByID: map[string]ha.State{}}
	for _, m := range raw {
		id := m["entity_id"].(string)
		st := ha.State{EntityID: id, State: m["state"].(string), Attributes: m["attributes"].(map[string]any)}
		st.LastChanged, _ = time.Parse(time.RFC3339, m["last_changed"].(string))
		st.LastUpdated, _ = time.Parse(time.RFC3339, m["last_updated"].(string))
		st.LastReported, _ = time.Parse(time.RFC3339, m["last_reported"].(string))
		if v, ok := override[id]; ok {
			st.State = v
		}
		snap.ByID[id] = st
	}
	return snap
}

func haEnv(snap *ha.Snapshot, err error) *brief.Env {
	loc, _ := time.LoadLocation("Europe/Paris")
	return &brief.Env{
		Now: time.Date(2026, 11, 25, 7, 15, 0, 0, loc),
		Loc: loc,
		HA:  func(context.Context) (*ha.Snapshot, error) { return snap, err },
	}
}

func collect(t *testing.T, name string, env *brief.Env, cfg brief.SectionConfig) brief.Result {
	t.Helper()
	for _, s := range brief.Sections() {
		if s.Name == name {
			return s.Collect(context.Background(), env, cfg)
		}
	}
	t.Fatalf("section %s not registered", name)
	return brief.Result{}
}

func fact(r brief.Result, key string) (brief.Fact, bool) {
	for _, f := range r.Facts {
		if f.Key == key {
			return f, true
		}
	}
	return brief.Fact{}, false
}

func dump(r brief.Result) string {
	var b strings.Builder
	for _, f := range r.Facts {
		b.WriteString(f.Key + "=" + f.Value + "\n")
	}
	return b.String()
}
