package section

import (
	"context"
	"fmt"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

// PetFeederConfig names the feeder's Home Assistant entities. Ids are empty by default (site data).
// The desiccant sensor is deliberately not configurable: the owner does not want it reported.
type PetFeederConfig struct {
	Enabled          bool     `yaml:"enabled"`
	FoodLevel        string   `yaml:"food_level"`   // enum sensor: food left
	Fault            string   `yaml:"fault"`        // enum sensor: device fault
	FeedSuccess      string   `yaml:"feed_success"` // event entity: its state is the last successful feed's timestamp
	NoFeedAfterHours float64  `yaml:"no_feed_after_hours"`
	FoodOKStates     []string `yaml:"food_ok_states"`
	FaultOKStates    []string `yaml:"fault_ok_states"`
}

func newPetFeederConfig() *PetFeederConfig {
	return &PetFeederConfig{
		Enabled: true, NoFeedAfterHours: 26,
		FoodOKStates: []string{"Normal"}, FaultOKStates: []string{"No Faults", "OK"},
	}
}

func (c *PetFeederConfig) IsEnabled() bool { return c.Enabled }

func (c *PetFeederConfig) Validate() error {
	required := []struct{ key, id string }{
		{"food_level", c.FoodLevel}, {"fault", c.Fault}, {"feed_success", c.FeedSuccess},
	}
	for _, r := range required {
		if r.id == "" {
			return fmt.Errorf("set pet_feeder.%s (morning-brief config set %s)", r.key, setHint("pet_feeder", required))
		}
	}
	if c.NoFeedAfterHours <= 0 {
		return fmt.Errorf("pet_feeder.no_feed_after_hours must be above 0")
	}
	if len(c.FoodOKStates) == 0 || len(c.FaultOKStates) == 0 {
		return fmt.Errorf("pet_feeder.food_ok_states and pet_feeder.fault_ok_states must not be empty")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "pet_feeder",
		Description: "Pet feeder: food level not normal, device fault, no successful feed for too long (Home Assistant)",
		Credentials: []string{"ha_token"},
		Default:     func() brief.SectionConfig { return newPetFeederConfig() },
		Collect:     collectPetFeeder,
	})
}

func collectPetFeeder(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	res := brief.Result{Name: "pet_feeder"}
	cfg := sc.(*PetFeederConfig)
	fail := func(err error) brief.Result {
		res.Status, res.Err, res.Facts = brief.StatusError, err.Error(), nil
		return res
	}
	snap, err := haSnapshot(ctx, env)
	if err != nil {
		return fail(err)
	}
	food, err := needState(snap, cfg.FoodLevel)
	if err != nil {
		return fail(err)
	}
	fault, err := needState(snap, cfg.Fault)
	if err != nil {
		return fail(err)
	}
	feed, err := needState(snap, cfg.FeedSuccess)
	if err != nil {
		return fail(err)
	}
	add := func(k, v string) { res.Facts = append(res.Facts, brief.Fact{Key: k, Value: v}) }

	check := func(key string, st string, ok []string) {
		switch {
		case unavailable(st):
			add(key, "no data")
		case !inList(ok, st):
			add(key, brief.Sanitize(st))
		}
	}
	check("food", food.State, cfg.FoodOKStates)
	check("fault", fault.State, cfg.FaultOKStates)

	switch {
	case feed.State == "unknown":
		add("feed", "no feed recorded")
	case unavailable(feed.State):
		add("feed", "no data")
	default:
		t, perr := time.Parse(time.RFC3339, feed.State)
		switch {
		case perr != nil:
			add("feed", "no data")
		case env.Now.Sub(t).Hours() > cfg.NoFeedAfterHours:
			add("feed", "no successful feed since "+stamp(t, env.Loc))
		}
	}
	res.Status = brief.StatusOK
	if len(res.Facts) > 0 {
		res.Status = brief.StatusReport
	}
	return res
}
