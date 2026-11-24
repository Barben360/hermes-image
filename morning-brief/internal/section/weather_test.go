package section

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

type memRec struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (r *memRec) Save(name string, v any) error {
	b, err := json.Marshal(v)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string][]byte{}
	}
	r.m[name] = b
	return err
}
func (r *memRec) Load(string, any) error { return nil }

func zoneSnap(zones ...string) *ha.Snapshot {
	s := &ha.Snapshot{ByID: map[string]ha.State{}}
	for _, z := range zones {
		s.ByID[z] = ha.State{EntityID: z, State: "zoning", Attributes: map[string]any{"latitude": 45.0, "longitude": 5.0, "radius": 100.0}}
	}
	return s
}

// weatherServer serves the fixture and records every request query.
func weatherServer(t *testing.T, status int) (*httptest.Server, *[]string) {
	return weatherServerBody(t, status, nil)
}

// weatherServerBody is weatherServer with the fixture rewritten by edit (a decoded JSON object).
func weatherServerBody(t *testing.T, status int, edit func(m map[string]any)) (*httptest.Server, *[]string) {
	t.Helper()
	body, err := os.ReadFile("../../testdata/openmeteo/forecast.json")
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		edit(m)
		if body, err = json.Marshal(m); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &queries
}

func weatherCfg(url string) *WeatherConfig {
	c := newWeatherConfig()
	c.APIURL = url
	c.Places = map[string]string{"home": "zone.home", "work": "zone.office"}
	return c
}

func weatherRun(t *testing.T, cfg *WeatherConfig, snap *ha.Snapshot, rec brief.Recorder) brief.Result {
	t.Helper()
	env := haEnv(snap, nil)
	env.HTTP = http.DefaultClient
	env.Rec = rec
	return collect(t, "weather", env, cfg)
}

func okWeather(t *testing.T) brief.Result {
	srv, _ := weatherServer(t, 200)
	r := weatherRun(t, weatherCfg(srv.URL), zoneSnap("zone.home", "zone.office"), nil)
	if r.Status != brief.StatusReport {
		t.Fatalf("%s %s", r.Status, r.Err)
	}
	return r
}

func TestWeatherNowAndToday(t *testing.T) {
	r := okWeather(t)
	want(t, r, "now", "3°C (feels 1°C), couvert")
	want(t, r, "today", "min 2°C, max 11°C, wind max 22 km/h, gusts max 37 km/h")
}

func TestWeatherSlots(t *testing.T) {
	r := okWeather(t)
	want(t, r, "slot 07:30-08:30 home", "départ: min 3°C, feels min 1°C, rain p 30%, 0.2 mm, gusts 27 km/h")
	want(t, r, "slot 12:00-14:00 work", "min 9°C, feels min 7°C, rain p 70%, 2 mm, gusts 25 km/h")
	want(t, r, "slot 17:30-18:30 work", "retour: min 8°C, feels min 6°C, rain p 0%, 0 mm, gusts 25 km/h")
}

func TestWeatherRainWindows(t *testing.T) {
	r := okWeather(t)
	want(t, r, "rain home", "10:00-14:00 (p 80%, 4 mm)")
}

func TestWeatherRainWindowsOnlyFromNow(t *testing.T) {
	srv, _ := weatherServer(t, 200)
	env := haEnv(zoneSnap("zone.home", "zone.office"), nil)
	env.HTTP = http.DefaultClient
	env.Now = env.Now.Add(4 * 3600e9) // 11:15: the 10:00 hour is past
	r := collect(t, "weather", env, weatherCfg(srv.URL))
	want(t, r, "rain home", "11:00-14:00 (p 70%, 3 mm)")
}

func TestWeatherTomorrowAndSun(t *testing.T) {
	r := okWeather(t)
	want(t, r, "tomorrow", "min 1°C, max 8°C, rain p 90%, 3.2 mm, wind max 30 km/h, gusts max 50 km/h, midi: pluie faible")
	want(t, r, "sun", "rise 07:55, set 17:20 (tomorrow 07:56, 17:19)")
}

func TestWeatherNoCoordinatesAnywhere(t *testing.T) {
	srv, queries := weatherServer(t, 200)
	rec := &memRec{}
	r := weatherRun(t, weatherCfg(srv.URL), zoneSnap("zone.home", "zone.office"), rec)
	if len(*queries) == 0 || !strings.Contains((*queries)[0], "latitude=45.00") || !strings.Contains((*queries)[0], "longitude=5.00") {
		t.Fatalf("request not made with 2-decimal coordinates: %v", *queries)
	}
	var out bytes.Buffer
	_ = brief.RenderText(&out, brief.Doc{Results: []brief.Result{r}}, &brief.Secrets{})
	all := out.String() + dump(r)
	if len(rec.m) == 0 {
		t.Fatal("nothing recorded")
	}
	for k, v := range rec.m {
		all += k + string(v)
	}
	for _, bad := range []string{"45.0", "45.00", "5.00", "latitude", "longitude", "api_url", srv.URL} {
		if strings.Contains(all, bad) {
			t.Errorf("output or recording holds %q", bad)
		}
	}
}

func TestWeatherErrorHasNoURL(t *testing.T) {
	srv, _ := weatherServer(t, 500)
	r := weatherRun(t, weatherCfg(srv.URL), zoneSnap("zone.home", "zone.office"), nil)
	if r.Status != brief.StatusError {
		t.Fatalf("status %s", r.Status)
	}
	if !strings.Contains(r.Err, "HTTP 500") || strings.Contains(r.Err, "latitude") || strings.Contains(r.Err, srv.URL) {
		t.Errorf("err %q", r.Err)
	}
}

func TestWeatherMissingZoneIsError(t *testing.T) {
	srv, _ := weatherServer(t, 200)
	r := weatherRun(t, weatherCfg(srv.URL), zoneSnap("zone.home"), nil)
	if r.Status != brief.StatusReport {
		t.Fatalf("%s %s", r.Status, r.Err)
	}
	want(t, r, "slot 07:30-08:30 home", "départ: min 3°C, feels min 1°C, rain p 30%, 0.2 mm, gusts 27 km/h")
	want(t, r, "slot 12:00-14:00 work", "error: entity zone.office not found in Home Assistant")
	want(t, r, "slot 17:30-18:30 work", "error: entity zone.office not found in Home Assistant")
}

func TestWeatherEmptyPlaceIsSkippedNotError(t *testing.T) {
	srv, queries := weatherServer(t, 200)
	c := weatherCfg(srv.URL)
	c.Places["work"] = ""
	r := weatherRun(t, c, zoneSnap("zone.home"), nil)
	if r.Status != brief.StatusReport {
		t.Fatalf("%s %s", r.Status, r.Err)
	}
	f, _ := fact(r, "slot 12:00-14:00 work")
	if !strings.HasPrefix(f.Value, "skipped: set weather.places") {
		t.Errorf("value %q", f.Value)
	}
	if len(*queries) != 1 {
		t.Errorf("%d requests, want 1 (home only)", len(*queries))
	}
}

func TestWeatherSamePlaceFetchedOnce(t *testing.T) {
	srv, queries := weatherServer(t, 200)
	c := weatherCfg(srv.URL)
	c.Places["work"] = "zone.home"
	weatherRun(t, c, zoneSnap("zone.home"), nil)
	if len(*queries) != 1 {
		t.Errorf("%d requests, want 1", len(*queries))
	}
}

func TestWeatherZoneWithoutCoordinatesIsError(t *testing.T) {
	srv, _ := weatherServer(t, 200)
	snap := zoneSnap("zone.home", "zone.office")
	snap.ByID["zone.office"] = ha.State{EntityID: "zone.office", Attributes: map[string]any{}}
	r := weatherRun(t, weatherCfg(srv.URL), snap, nil)
	f, _ := fact(r, "slot 12:00-14:00 work")
	if !strings.HasPrefix(f.Value, "error: zone.office has no coordinates") {
		t.Errorf("value %q", f.Value)
	}
}

func TestWeatherValidate(t *testing.T) {
	c := newWeatherConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults must validate (empty work zone is not an error): %v", err)
	}
	if c.Places["home"] != "zone.home" || c.Places["work"] != "" {
		t.Errorf("places %v", c.Places)
	}
	c.Slots[0].Place = "gym"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "weather.places") {
		t.Errorf("unknown place: %v", err)
	}
	c = newWeatherConfig()
	c.Slots[0].To = "07:00"
	if c.Validate() == nil {
		t.Error("to before from must fail")
	}
	c = newWeatherConfig()
	c.Slots[0].From = "7h30"
	if c.Validate() == nil {
		t.Error("bad time must fail")
	}
}

func TestWeatherConditionWords(t *testing.T) {
	for code, w := range map[int]string{0: "dégagé", 3: "couvert", 63: "pluie", 95: "orage", 71: "neige faible", 12345: "code 12345"} {
		if got := wmoCondition(code); got != w {
			t.Errorf("%d = %q, want %q", code, got, w)
		}
	}
}

func TestWeatherStaleForecastIsNotNoRain(t *testing.T) {
	srv, _ := weatherServerBody(t, 200, func(m map[string]any) {
		h := m["hourly"].(map[string]any)
		ts := h["time"].([]any)
		for i := range ts { // shift every hour to 2026-11-20/21
			ts[i] = strings.Replace(ts[i].(string), "2026-11-2", "2026-11-1", 1)
			ts[i] = strings.Replace(ts[i].(string), "2026-11-15", "2026-11-20", 1)
			ts[i] = strings.Replace(ts[i].(string), "2026-11-16", "2026-11-21", 1)
		}
	})
	r := weatherRun(t, weatherCfg(srv.URL), zoneSnap("zone.home", "zone.office"), nil)
	if f, ok := fact(r, "rain home"); ok {
		t.Errorf("printed rain home = %q from a forecast without today", f.Value)
	}
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "no entry for today") {
		t.Errorf("%s %q", r.Status, r.Err)
	}
}

func TestWeatherStaleForecastAloneIsSectionError(t *testing.T) {
	srv, _ := weatherServerBody(t, 200, func(m map[string]any) {
		h := m["hourly"].(map[string]any)
		ts := h["time"].([]any)
		for i := range ts {
			ts[i] = "2026-11-01T" + ts[i].(string)[11:]
		}
	})
	c := weatherCfg(srv.URL)
	c.Slots = nil
	r := weatherRun(t, c, zoneSnap("zone.home"), nil)
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "no entry") {
		t.Errorf("%s %q", r.Status, r.Err)
	}
}

func TestWeatherPrimaryPlaceFallsBackToFirstSlot(t *testing.T) {
	srv, _ := weatherServer(t, 200)
	c := weatherCfg(srv.URL)
	c.Places = map[string]string{"base": "zone.home", "work": "zone.office"}
	c.Slots[0].Place = "base"
	r := weatherRun(t, c, zoneSnap("zone.home", "zone.office"), nil)
	want(t, r, "rain base", "10:00-14:00 (p 80%, 4 mm)")
	if _, ok := fact(r, "now"); !ok {
		t.Error("now missing")
	}
}

func TestWeatherRequestAsksKmh(t *testing.T) {
	srv, queries := weatherServer(t, 200)
	weatherRun(t, weatherCfg(srv.URL), zoneSnap("zone.home", "zone.office"), nil)
	if len(*queries) == 0 || !strings.Contains((*queries)[0], "wind_speed_unit=kmh") {
		t.Errorf("queries %v", *queries)
	}
}

func TestWeatherNullProbabilityIsUnknownNotFatal(t *testing.T) {
	srv, _ := weatherServerBody(t, 200, func(m map[string]any) {
		p := m["hourly"].(map[string]any)["precipitation_probability"].([]any)
		p[7], p[8] = nil, nil // the départ slot
		p[20] = nil           // later today, outside every slot
	})
	r := weatherRun(t, weatherCfg(srv.URL), zoneSnap("zone.home", "zone.office"), nil)
	if r.Status != brief.StatusReport {
		t.Fatalf("%s %s", r.Status, r.Err)
	}
	want(t, r, "slot 07:30-08:30 home", "départ: min 3°C, feels min 1°C, rain p ?, 0.2 mm, gusts 27 km/h")
	want(t, r, "rain home", "10:00-14:00 (p 80%, 4 mm); p unknown for 3 h")
	want(t, r, "slot 12:00-14:00 work", "min 9°C, feels min 7°C, rain p 70%, 2 mm, gusts 25 km/h")
}

func TestWeatherNullTemperatureStillFails(t *testing.T) {
	srv, _ := weatherServerBody(t, 200, func(m map[string]any) {
		m["hourly"].(map[string]any)["temperature_2m"].([]any)[3] = nil
	})
	r := weatherRun(t, weatherCfg(srv.URL), zoneSnap("zone.home", "zone.office"), nil)
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "incomplete") {
		t.Errorf("%s %q", r.Status, r.Err)
	}
}

func TestWeatherPartialFactWhenSlotsFail(t *testing.T) {
	srv, _ := weatherServer(t, 200)
	r := weatherRun(t, weatherCfg(srv.URL), zoneSnap("zone.home"), nil)
	want(t, r, "partial", "2 slot(s) failed")
	if r.Facts[0].Key != "partial" {
		t.Errorf("partial is not the first fact: %s", dump(r))
	}
	ok := okWeather(t)
	if _, has := fact(ok, "partial"); has {
		t.Error("partial printed on a clean run")
	}
	c := weatherCfg(srv.URL)
	c.Places["work"] = ""
	if _, has := fact(weatherRun(t, c, zoneSnap("zone.home"), nil), "partial"); has {
		t.Error("a skipped (unconfigured) slot is not a failure")
	}
}
