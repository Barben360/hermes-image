package section

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/openmeteo"
)

// WeatherSlot is a time range of today spent at a named place.
type WeatherSlot struct {
	From  string `yaml:"from"`
	To    string `yaml:"to"`
	Place string `yaml:"place"`
	Label string `yaml:"label,omitempty"`
}

// WeatherConfig: places map a name to a Home Assistant zone id (its coordinates are the forecast point).
type WeatherConfig struct {
	Enabled bool              `yaml:"enabled"`
	APIURL  string            `yaml:"api_url"`
	Places  map[string]string `yaml:"places"`
	Slots   []WeatherSlot     `yaml:"slots"`
	RainMin float64           `yaml:"rain_min_pct"` // an hour counts as rainy from this probability
}

func newWeatherConfig() *WeatherConfig {
	return &WeatherConfig{
		Enabled: true,
		APIURL:  openmeteo.DefaultURL,
		Places:  map[string]string{"home": "zone.home", "work": ""}, // the work zone is site data
		Slots: []WeatherSlot{
			{From: "07:30", To: "08:30", Place: "home", Label: "départ"},
			{From: "12:00", To: "14:00", Place: "work"},
			{From: "17:30", To: "18:30", Place: "work", Label: "retour"},
		},
		RainMin: 50,
	}
}

func (c *WeatherConfig) IsEnabled() bool { return c.Enabled }

func (c *WeatherConfig) Validate() error {
	if c.APIURL == "" {
		return fmt.Errorf("weather.api_url is empty (morning-brief config unset weather.api_url)")
	}
	for i, s := range c.Slots {
		if _, ok := c.Places[s.Place]; !ok {
			return fmt.Errorf("weather.slots[%d] uses place %q: add it to weather.places (morning-brief config set weather.places '{home: zone.home, %s: <zone id>}')", i, s.Place, s.Place)
		}
		from, e1 := parseClock(s.From)
		to, e2 := parseClock(s.To)
		if e1 != nil || e2 != nil || to <= from {
			return fmt.Errorf("weather.slots[%d]: from and to must be HH:MM with to after from", i)
		}
	}
	return nil
}

// parseClock returns minutes since midnight.
func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "weather",
		Description: "Open-Meteo forecast at Home Assistant zones: now, today, commute slots, rain windows, tomorrow, sun",
		Credentials: []string{"ha_token"},
		Default:     func() brief.SectionConfig { return newWeatherConfig() },
		Collect:     collectWeather,
	})
}

// wmoCondition is the French word for a WMO weather code.
func wmoCondition(code int) string {
	m := map[int]string{
		0: "dégagé", 1: "plutôt dégagé", 2: "partiellement nuageux", 3: "couvert",
		45: "brouillard", 48: "brouillard givrant",
		51: "bruine faible", 53: "bruine", 55: "forte bruine", 56: "bruine verglaçante", 57: "forte bruine verglaçante",
		61: "pluie faible", 63: "pluie", 65: "forte pluie", 66: "pluie verglaçante", 67: "forte pluie verglaçante",
		71: "neige faible", 73: "neige", 75: "forte neige", 77: "grains de neige",
		80: "averses faibles", 81: "averses", 82: "fortes averses", 85: "averses de neige", 86: "fortes averses de neige",
		95: "orage", 96: "orage avec grêle", 99: "orage avec forte grêle",
	}
	if w, ok := m[code]; ok {
		return w
	}
	return "code " + strconv.Itoa(code)
}

func r1(f float64) string   { return trim(math.Round(f*10) / 10) }
func temp(f float64) string { return r1(f) + "°C" }
func kmh(f float64) string  { return r1(f) + " km/h" }
func mm(f float64) string   { return r1(f) + " mm" }
func pct(f float64) string  { return r1(f) + "%" }

type placeData struct {
	fc  *openmeteo.Forecast
	err error
}

func collectWeather(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	res := brief.Result{Name: "weather"}
	cfg := sc.(*WeatherConfig)
	snap, err := haSnapshot(ctx, env)
	if err != nil {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	// Distinct zone ids are fetched once, whatever the places naming them.
	byZone := map[string]placeData{}
	fetch := func(place string) (*openmeteo.Forecast, error) {
		id := cfg.Places[place]
		if id == "" {
			return nil, fmt.Errorf("skipped: set weather.places (morning-brief config set weather.places '{home: zone.home, %s: <zone id>}')", place)
		}
		if d, ok := byZone[id]; ok {
			return d.fc, d.err
		}
		var d placeData
		st, e := needState(snap, id)
		if e == nil {
			lat, ok1 := st.Attributes["latitude"].(float64)
			lon, ok2 := st.Attributes["longitude"].(float64)
			if !ok1 || !ok2 {
				e = fmt.Errorf("%s has no coordinates", id)
			} else {
				d.fc, e = openmeteo.Fetch(ctx, env.HTTP, env.Rec, cfg.APIURL, place, lat, lon)
			}
		}
		d.err = e
		byZone[id] = d
		return d.fc, d.err
	}
	failText := func(err error) string {
		if strings.HasPrefix(err.Error(), "skipped:") {
			return err.Error()
		}
		return "error: " + err.Error()
	}

	okCount := 0
	var errs []string
	wall := wallClock(env.Now.In(env.Loc))

	if hf, herr := fetch("home"); herr != nil {
		res.Facts = append(res.Facts, brief.Fact{Key: "home", Value: failText(herr)})
		errs = append(errs, herr.Error())
	} else {
		okCount++
		res.Facts = append(res.Facts, homeFacts(hf, wall, cfg)...)
	}
	for _, s := range cfg.Slots {
		key := "slot " + s.From + "-" + s.To + " " + s.Place
		fc, err := fetch(s.Place)
		if err != nil {
			res.Facts = append(res.Facts, brief.Fact{Key: key, Value: failText(err)})
			errs = append(errs, err.Error())
			continue
		}
		okCount++
		v, err := slotText(fc, wall, s)
		if err != nil {
			res.Facts = append(res.Facts, brief.Fact{Key: key, Value: failText(err)})
			errs = append(errs, err.Error())
			continue
		}
		res.Facts = append(res.Facts, brief.Fact{Key: key, Value: v})
	}
	if okCount == 0 {
		res.Status, res.Err = brief.StatusError, strings.Join(dedupe(errs), "; ")
		res.Facts = nil
		return res
	}
	res.Status = brief.StatusReport
	return res
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// wallClock keeps t's clock reading and labels it UTC, the convention of openmeteo.Hour.Time.
func wallClock(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func dayHours(fc *openmeteo.Forecast, day time.Time) []openmeteo.Hour {
	var out []openmeteo.Hour
	for _, h := range fc.Hourly {
		if sameDay(h.Time, day) {
			out = append(out, h)
		}
	}
	return out
}

func dayOf(fc *openmeteo.Forecast, day time.Time) (openmeteo.Day, bool) {
	for _, d := range fc.Daily {
		if sameDay(d.Date, day) {
			return d, true
		}
	}
	return openmeteo.Day{}, false
}

func maxOf(hs []openmeteo.Hour, f func(openmeteo.Hour) float64) float64 {
	m := math.Inf(-1)
	for _, h := range hs {
		m = math.Max(m, f(h))
	}
	return m
}

func minOf(hs []openmeteo.Hour, f func(openmeteo.Hour) float64) float64 {
	m := math.Inf(1)
	for _, h := range hs {
		m = math.Min(m, f(h))
	}
	return m
}

func sumOf(hs []openmeteo.Hour, f func(openmeteo.Hour) float64) float64 {
	s := 0.0
	for _, h := range hs {
		s += f(h)
	}
	return s
}

func homeFacts(fc *openmeteo.Forecast, now time.Time, cfg *WeatherConfig) []brief.Fact {
	var out []brief.Fact
	add := func(k, v string) { out = append(out, brief.Fact{Key: k, Value: v}) }
	today := dayHours(fc, now)
	tomorrow := dayHours(fc, now.AddDate(0, 0, 1))
	for _, h := range today {
		if h.Time.Hour() == now.Hour() {
			add("now", fmt.Sprintf("%s (feels %s), %s", temp(h.Temp), temp(h.Feels), wmoCondition(h.Code)))
		}
	}
	if d, ok := dayOf(fc, now); ok && len(today) > 0 {
		add("today", fmt.Sprintf("min %s, max %s, wind max %s, gusts max %s", temp(d.Min), temp(d.Max),
			kmh(maxOf(today, func(h openmeteo.Hour) float64 { return h.Wind })),
			kmh(maxOf(today, func(h openmeteo.Hour) float64 { return h.Gust }))))
	}
	add("rain home", rainWindows(today, now, cfg.RainMin))
	if d, ok := dayOf(fc, now.AddDate(0, 0, 1)); ok && len(tomorrow) > 0 {
		v := fmt.Sprintf("min %s, max %s, rain p %s, %s, wind max %s, gusts max %s", temp(d.Min), temp(d.Max),
			pct(maxOf(tomorrow, func(h openmeteo.Hour) float64 { return h.PrecipProb })),
			mm(sumOf(tomorrow, func(h openmeteo.Hour) float64 { return h.Precip })),
			kmh(maxOf(tomorrow, func(h openmeteo.Hour) float64 { return h.Wind })),
			kmh(maxOf(tomorrow, func(h openmeteo.Hour) float64 { return h.Gust })))
		for _, h := range tomorrow {
			if h.Time.Hour() == 12 {
				v += ", midi: " + wmoCondition(h.Code)
			}
		}
		add("tomorrow", v)
	}
	if d, ok := dayOf(fc, now); ok {
		v := "rise " + d.Sunrise.Format("15:04") + ", set " + d.Sunset.Format("15:04")
		if n, ok := dayOf(fc, now.AddDate(0, 0, 1)); ok {
			v += " (tomorrow " + n.Sunrise.Format("15:04") + ", " + n.Sunset.Format("15:04") + ")"
		}
		add("sun", v)
	}
	return out
}

// rainWindows lists runs of consecutive hours, from now's hour, whose rain probability is at least min.
func rainWindows(today []openmeteo.Hour, now time.Time, min float64) string {
	var wins []string
	var run []openmeteo.Hour
	flush := func() {
		if len(run) == 0 {
			return
		}
		end := run[len(run)-1].Time.Add(time.Hour)
		wins = append(wins, fmt.Sprintf("%s-%s (p %s, %s)", run[0].Time.Format("15:04"), end.Format("15:04"),
			pct(maxOf(run, func(h openmeteo.Hour) float64 { return h.PrecipProb })),
			mm(sumOf(run, func(h openmeteo.Hour) float64 { return h.Precip }))))
		run = nil
	}
	for _, h := range today {
		if h.Time.Hour() < now.Hour() || h.PrecipProb < min {
			flush()
			continue
		}
		run = append(run, h)
	}
	flush()
	if len(wins) == 0 {
		return "none"
	}
	return strings.Join(wins, "; ")
}

// slotText summarises the hours overlapping [from, to) today.
func slotText(fc *openmeteo.Forecast, now time.Time, s WeatherSlot) (string, error) {
	from, _ := parseClock(s.From)
	to, _ := parseClock(s.To)
	var hs []openmeteo.Hour
	for _, h := range dayHours(fc, now) {
		start := h.Time.Hour() * 60
		if start < to && start+60 > from {
			hs = append(hs, h)
		}
	}
	if len(hs) == 0 {
		return "", fmt.Errorf("no forecast hours for the slot")
	}
	v := fmt.Sprintf("min %s, feels min %s, rain p %s, %s, gusts %s",
		temp(minOf(hs, func(h openmeteo.Hour) float64 { return h.Temp })),
		temp(minOf(hs, func(h openmeteo.Hour) float64 { return h.Feels })),
		pct(maxOf(hs, func(h openmeteo.Hour) float64 { return h.PrecipProb })),
		mm(sumOf(hs, func(h openmeteo.Hour) float64 { return h.Precip })),
		kmh(maxOf(hs, func(h openmeteo.Hour) float64 { return h.Gust })))
	if s.Label != "" {
		v = s.Label + ": " + v
	}
	return v, nil
}
