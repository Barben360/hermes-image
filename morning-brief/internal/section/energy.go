package section

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

// EnergyConfig has no site data: the statistic ids come from the Energy panel's own configuration.
type EnergyConfig struct {
	Enabled bool `yaml:"enabled"`
}

func newEnergyConfig() *EnergyConfig { return &EnergyConfig{Enabled: true} }

func (c *EnergyConfig) IsEnabled() bool { return c.Enabled }
func (c *EnergyConfig) Validate() error { return nil }

func init() {
	brief.Register(brief.Section{
		Name:        "energy",
		Description: "Electricity and gas consumption and cost (yesterday, 7-day average) from Home Assistant's long-term statistics",
		Credentials: []string{"ha_token"},
		Default:     func() brief.SectionConfig { return newEnergyConfig() },
		Collect:     collectEnergy,
	})
}

const (
	energyAvgDays   = 7
	energyLookback  = 10 // days fetched before today: the 7-day averages plus Gazpar's lag
	energyDayFormat = "2006-01-02"
)

// energyPair is one energy statistic and its cost statistic ("" when none could be resolved).
type energyPair struct{ energy, cost string }

// energyKind is one commodity. One source without a resolvable cost id makes the whole commodity "cost unknown".
type energyKind struct {
	pairs      []energyPair
	seen       bool // the prefs hold a source of this type, whatever its shape
	noCostInfo bool
}

func (k *energyKind) add(stat string, explicit *string, info *ha.EnergyInfo) {
	if stat == "" {
		return
	}
	// The cost id is what the panel stores, else what energy/info reports: never derived from a name
	// (an orphaned `_cost` sits next to the live `_cost_2`).
	cost := ""
	switch {
	case explicit != nil && *explicit != "":
		cost = *explicit
	case info.CostSensors[stat] != "":
		cost = info.CostSensors[stat]
	default:
		k.noCostInfo = true
	}
	for _, p := range k.pairs {
		if p.energy == stat {
			return
		}
	}
	k.pairs = append(k.pairs, energyPair{stat, cost})
}

func (k *energyKind) ids() (ids []string) {
	seen := map[string]bool{}
	for _, p := range k.pairs {
		for _, id := range []string{p.energy, p.cost} {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return
}

// dayRows keeps, per local date, the summed change of id and whether it has a row that day.
func dayRows(data map[string][]ha.DayValue, id string, loc *time.Location) map[string]float64 {
	out := map[string]float64{}
	for _, v := range data[id] {
		out[v.Start.In(loc).Format(energyDayFormat)] += v.Change
	}
	return out
}

// commodity is a kind with its fetched data. Meter sources are the energy statistics that moved over the
// window; a subscription's always-present zero-kWh sensor is not one, so it cannot make a day look present.
type commodity struct {
	kind   *energyKind
	energy map[string]float64 // summed over every source
	meter  map[string]bool    // days a meter source has a row
	rows   map[string]map[string]float64
}

func newCommodity(k *energyKind, data map[string][]ha.DayValue, loc *time.Location) commodity {
	c := commodity{kind: k, energy: map[string]float64{}, meter: map[string]bool{}, rows: map[string]map[string]float64{}}
	for _, p := range k.pairs {
		er := dayRows(data, p.energy, loc)
		total := 0.0
		for d, v := range er {
			c.energy[d] += v
			total += math.Abs(v)
		}
		if total > 0 {
			for d := range er {
				c.meter[d] = true
			}
		}
		c.rows[p.energy] = er
		if p.cost != "" {
			c.rows[p.cost] = dayRows(data, p.cost, loc)
		}
	}
	return c
}

// day returns the day's kWh and €, whether a meter has data that day, and whether the cost is complete:
// every source that moved that day (and every subscription) needs a cost row.
func (c commodity) day(d string) (kwh, eur float64, present, costKnown bool) {
	if !c.meter[d] {
		return 0, 0, false, false
	}
	costKnown = !c.kind.noCostInfo
	for _, p := range c.kind.pairs {
		_, moved := c.rows[p.energy][d]
		isMeter := false
		for _, v := range c.rows[p.energy] {
			if v != 0 {
				isMeter = true
				break
			}
		}
		if p.cost == "" {
			continue
		}
		v, has := c.rows[p.cost][d]
		if has {
			eur += v
		} else if !isMeter || moved {
			costKnown = false
		}
	}
	return c.energy[d], eur, true, costKnown
}

func fmtAmount(kwh, eur float64, costKnown bool) string {
	if !costKnown {
		return fmt.Sprintf("%.1f kWh, cost unknown", kwh)
	}
	return fmt.Sprintf("%.1f kWh, %.2f €", kwh, eur)
}

func collectEnergy(ctx context.Context, env *brief.Env, _ brief.SectionConfig) brief.Result {
	res := brief.Result{Name: "energy"}
	fail := func(err error) brief.Result {
		res.Status, res.Err, res.Facts = brief.StatusError, err.Error(), nil
		return res
	}
	if env.Secrets == nil {
		return fail(fmt.Errorf("Home Assistant: not configured"))
	}
	token, err := env.Secrets.Read("ha_token")
	if err != nil {
		return fail(err)
	}
	c := &ha.Client{BaseURL: env.Sources.HAURL, Token: token, HTTP: env.HTTP, Rec: env.Rec, Dial: env.Dial}
	ws, err := c.WS(ctx)
	if err != nil {
		return fail(err)
	}
	defer ws.Close()
	prefs, err := ws.EnergyPrefs(ctx)
	if err != nil {
		return fail(err)
	}
	info, err := ws.EnergyInfo(ctx)
	if err != nil {
		return fail(err)
	}

	var elec, gas energyKind
	for _, s := range prefs.EnergySources {
		switch s.Type {
		case "grid":
			elec.seen = true
			for _, f := range s.FlowFrom { // older HA
				elec.add(f.StatEnergyFrom, f.StatCost, info)
			}
			elec.add(s.StatEnergyFrom, s.StatCost, info) // newer HA: flat, no flow_from
		case "gas":
			gas.seen = true
			gas.add(s.StatEnergyFrom, s.StatCost, info)
		}
	}
	if !elec.seen && !gas.seen {
		return fail(fmt.Errorf("Home Assistant: the Energy panel has no grid or gas source"))
	}

	loc := env.Loc
	today := time.Date(env.Now.In(loc).Year(), env.Now.In(loc).Month(), env.Now.In(loc).Day(), 0, 0, 0, 0, loc)
	from := today.AddDate(0, 0, -energyLookback)
	ids := append(elec.ids(), gas.ids()...)
	data, err := ws.DailyChange(ctx, ids, from, today)
	if err != nil {
		return fail(err)
	}
	e, g := newCommodity(&elec, data, loc), newCommodity(&gas, data, loc)
	add := func(k, v string) { res.Facts = append(res.Facts, brief.Fact{Key: k, Value: v}) }

	yesterday := today.AddDate(0, 0, -1)
	if elec.seen && len(elec.pairs) == 0 {
		add("electricity", "no data (no readable grid source in the Energy panel)")
	}
	if len(elec.pairs) > 0 {
		ykey := yesterday.Format(energyDayFormat)
		if kwh, eur, ok, known := e.day(ykey); ok {
			add("electricity yesterday", fmtAmount(kwh, eur, known))
		} else {
			add("electricity yesterday", "no data for "+ykey)
		}
		if kwh, eur, known, n := average(e, yesterday, false); n == 0 {
			add("electricity avg 7d", "no data")
		} else {
			add("electricity avg 7d", fmtAmount(kwh, eur, known))
		}
	}
	if gas.seen && len(gas.pairs) == 0 {
		add("gas", "no data (no readable gas source in the Energy panel)")
	}
	if len(gas.pairs) > 0 {
		var days []string
		for d := range g.meter {
			if g.energy[d] > 0 {
				days = append(days, d)
			}
		}
		sort.Strings(days)
		if len(days) == 0 {
			add("gas latest complete day", fmt.Sprintf("no data in the last %d days", energyLookback))
		} else {
			last := days[len(days)-1]
			kwh, eur, _, known := g.day(last)
			add("gas latest complete day "+last, fmtAmount(kwh, eur, known))
			lastDay, _ := time.ParseInLocation(energyDayFormat, last, loc)
			if kwh, eur, known, n := average(g, lastDay, true); n == 0 {
				add("gas avg 7d", "no data")
			} else {
				add("gas avg 7d", fmtAmount(kwh, eur, known))
			}
		}
	}
	res.Status = brief.StatusReport
	return res
}

// average is the mean per day over the 7 days before ref (ref excluded), over days a meter has data
// (positive only when positiveOnly: Gazpar reports 0 for days it has not delivered yet). The cost is
// unknown when any averaged day has an incomplete cost.
func average(c commodity, ref time.Time, positiveOnly bool) (kwh, eur float64, costKnown bool, n int) {
	costKnown = true
	for i := 1; i <= energyAvgDays; i++ {
		d := ref.AddDate(0, 0, -i).Format(energyDayFormat)
		k, e, ok, known := c.day(d)
		if !ok || (positiveOnly && k <= 0) {
			continue
		}
		kwh += k
		eur += e
		costKnown = costKnown && known
		n++
	}
	if n > 0 {
		kwh, eur = kwh/float64(n), eur/float64(n)
	}
	return
}
