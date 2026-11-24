package section

import (
	"context"
	"fmt"
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

// energyKind is one commodity: its energy statistics and the cost statistic of each.
type energyKind struct {
	energy, cost []string
}

func (k *energyKind) add(stat string, explicit *string, info *ha.EnergyInfo, missing *bool) {
	if stat == "" {
		return
	}
	k.energy = appendOnce(k.energy, stat)
	// The cost id is what the panel stores, else what energy/info reports: never derived from a name
	// (an orphaned `_cost` sits next to the live `_cost_2`).
	switch {
	case explicit != nil && *explicit != "":
		k.cost = appendOnce(k.cost, *explicit)
	case info.CostSensors[stat] != "":
		k.cost = appendOnce(k.cost, info.CostSensors[stat])
	default:
		*missing = true
	}
}

func appendOnce(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

// dayTotals sums the daily changes of ids, by local date.
func dayTotals(data map[string][]ha.DayValue, ids []string, loc *time.Location) map[string]float64 {
	out := map[string]float64{}
	for _, id := range ids {
		for _, v := range data[id] {
			out[v.Start.In(loc).Format(energyDayFormat)] += v.Change
		}
	}
	return out
}

type commodity struct {
	energy, cost map[string]float64
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
	var elecNoCost, gasNoCost bool
	for _, s := range prefs.EnergySources {
		switch s.Type {
		case "grid":
			for _, f := range s.FlowFrom {
				elec.add(f.StatEnergyFrom, f.StatCost, info, &elecNoCost)
			}
		case "gas":
			gas.add(s.StatEnergyFrom, s.StatCost, info, &gasNoCost)
		}
	}
	if len(elec.energy) == 0 && len(gas.energy) == 0 {
		return fail(fmt.Errorf("Home Assistant: the Energy panel has no grid or gas source"))
	}

	loc := env.Loc
	today := time.Date(env.Now.In(loc).Year(), env.Now.In(loc).Month(), env.Now.In(loc).Day(), 0, 0, 0, 0, loc)
	from := today.AddDate(0, 0, -energyLookback)
	ids := append(append(append(append([]string{}, elec.energy...), elec.cost...), gas.energy...), gas.cost...)
	data, err := ws.DailyChange(ctx, ids, from, today)
	if err != nil {
		return fail(err)
	}
	e := commodity{dayTotals(data, elec.energy, loc), dayTotals(data, elec.cost, loc)}
	g := commodity{dayTotals(data, gas.energy, loc), dayTotals(data, gas.cost, loc)}
	add := func(k, v string) { res.Facts = append(res.Facts, brief.Fact{Key: k, Value: v}) }

	yesterday := today.AddDate(0, 0, -1)
	if len(elec.energy) > 0 {
		ykey := yesterday.Format(energyDayFormat)
		if _, ok := e.energy[ykey]; ok {
			add("electricity yesterday", fmtAmount(e.energy[ykey], e.cost[ykey], !elecNoCost))
		} else {
			add("electricity yesterday", "no data for "+ykey)
		}
		kwh, eur, n := average(e, yesterday, false)
		if n == 0 {
			add("electricity avg 7d", "no data")
		} else {
			add("electricity avg 7d", fmtAmount(kwh, eur, !elecNoCost))
		}
	}
	if len(gas.energy) > 0 {
		var days []string
		for d, v := range g.energy {
			if v > 0 {
				days = append(days, d)
			}
		}
		sort.Strings(days)
		if len(days) == 0 {
			add("gas latest complete day", fmt.Sprintf("no data in the last %d days", energyLookback))
		} else {
			last := days[len(days)-1]
			add("gas latest complete day "+last, fmtAmount(g.energy[last], g.cost[last], !gasNoCost))
			lastDay, _ := time.ParseInLocation(energyDayFormat, last, loc)
			kwh, eur, n := average(g, lastDay, true)
			if n == 0 {
				add("gas avg 7d", "no data")
			} else {
				add("gas avg 7d", fmtAmount(kwh, eur, !gasNoCost))
			}
		}
	}
	res.Status = brief.StatusReport
	return res
}

// average is the mean per day over the 7 days before ref (ref excluded), over days with energy data
// (positive only when positiveOnly: Gazpar reports 0 for days it has not delivered yet).
func average(c commodity, ref time.Time, positiveOnly bool) (kwh, eur float64, n int) {
	for i := 1; i <= energyAvgDays; i++ {
		d := ref.AddDate(0, 0, -i).Format(energyDayFormat)
		v, ok := c.energy[d]
		if !ok || (positiveOnly && v <= 0) {
			continue
		}
		kwh += v
		eur += c.cost[d]
		n++
	}
	if n > 0 {
		kwh, eur = kwh/float64(n), eur/float64(n)
	}
	return
}
