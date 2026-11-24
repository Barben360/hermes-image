package section

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

// HomeConfig covers Home Assistant updates and the shopping list. The list is empty by default
// (no shopping list read) because its entity id is site data.
type HomeConfig struct {
	Enabled        bool     `yaml:"enabled"`
	ShoppingList   string   `yaml:"shopping_list"`   // todo entity; empty = not read
	UpdatesExclude []string `yaml:"updates_exclude"` // update.* globs to ignore
}

func newHomeConfig() *HomeConfig { return &HomeConfig{Enabled: true} }

func (c *HomeConfig) IsEnabled() bool { return c.Enabled }

func (c *HomeConfig) Validate() error {
	for _, p := range c.UpdatesExclude {
		if _, err := matchGlob(p, "x"); err != nil {
			return fmt.Errorf("home: bad glob %q", p)
		}
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "home",
		Description: "Home Assistant updates available and the open items of the shopping list",
		Credentials: []string{"ha_token"},
		Default:     func() brief.SectionConfig { return newHomeConfig() },
		Collect:     collectHome,
	})
}

func collectHome(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	res := brief.Result{Name: "home"}
	cfg := sc.(*HomeConfig)
	snap, err := haSnapshot(ctx, env)
	if err != nil {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	for _, st := range snap.Glob("update.*") {
		if st.State != "on" || matchAny(cfg.UpdatesExclude, st.EntityID) {
			continue
		}
		inst, _ := st.Attributes["installed_version"].(string)
		latest, _ := st.Attributes["latest_version"].(string)
		res.Items = append(res.Items, brief.Item{Facts: []brief.Fact{
			brief.Untrusted("update", entityName(st)+" "+inst+" → "+latest),
		}})
	}
	if cfg.ShoppingList != "" {
		items, err := shoppingItems(ctx, env, cfg.ShoppingList)
		if err != nil {
			res.Facts = append(res.Facts, brief.Fact{Key: "shopping_list", Value: "unavailable (" + err.Error() + ")"})
		}
		for _, s := range items {
			res.Items = append(res.Items, brief.Item{Facts: []brief.Fact{brief.Untrusted("shopping", s)}})
		}
	}
	res.Status = brief.StatusOK
	if len(res.Items) > 0 {
		res.Status = brief.StatusReport
	}
	return res
}

// shoppingItems returns the summaries of the open items of a todo entity (todo.get_items).
func shoppingItems(ctx context.Context, env *brief.Env, entity string) ([]string, error) {
	token, err := env.Secrets.Read("ha_token")
	if err != nil {
		return nil, err
	}
	c := &ha.Client{BaseURL: env.Sources.HAURL, Token: token, HTTP: env.HTTP}
	raw, err := c.Service(ctx, "todo", "get_items", map[string]any{"entity_id": entity, "status": "needs_action"})
	if err != nil {
		return nil, err
	}
	var out struct {
		Response map[string]struct {
			Items []struct {
				Summary string `json:"summary"`
			} `json:"items"`
		} `json:"service_response"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("answer is not a todo list")
	}
	list, ok := out.Response[entity]
	if !ok {
		return nil, fmt.Errorf("entity %s not in the answer", entity)
	}
	var items []string
	for _, it := range list.Items {
		items = append(items, it.Summary)
	}
	sort.Strings(items)
	return items, nil
}
