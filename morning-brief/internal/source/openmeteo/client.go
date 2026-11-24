// Package openmeteo is the Open-Meteo forecast client (no key). It never keeps the request URL
// (its query holds coordinates) in an error or a recording.
package openmeteo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultURL is the public forecast endpoint.
const DefaultURL = "https://api.open-meteo.com/v1/forecast"

// Recorder is brief.Recorder's method set (package brief imports sources).
type Recorder interface {
	Save(name string, v any) error
	Load(name string, v any) error
}

// Hour is one forecast hour. Times are wall-clock Europe/Paris labelled UTC: compare them with a wall-clock "now".
type Hour struct {
	Time                                        time.Time
	Temp, Feels, PrecipProb, Precip, Wind, Gust float64
	Code                                        int
	ProbUnknown                                 bool // precipitation_probability was null: PrecipProb is 0 and meaningless
}

// Day is one forecast day.
type Day struct {
	Date, Sunrise, Sunset time.Time
	Min, Max              float64
}

// Forecast is two days of hourly values and two daily summaries. It holds no coordinates.
type Forecast struct {
	Hourly []Hour
	Daily  []Day
}

type wire struct {
	Hourly struct {
		Time   []string   `json:"time"`
		Temp   []*float64 `json:"temperature_2m"`
		Feels  []*float64 `json:"apparent_temperature"`
		Prob   []*float64 `json:"precipitation_probability"`
		Precip []*float64 `json:"precipitation"`
		Wind   []*float64 `json:"wind_speed_10m"`
		Gust   []*float64 `json:"wind_gusts_10m"`
		Code   []*float64 `json:"weather_code"`
	} `json:"hourly"`
	Daily struct {
		Time    []string   `json:"time"`
		Min     []*float64 `json:"temperature_2m_min"`
		Max     []*float64 `json:"temperature_2m_max"`
		Sunrise []string   `json:"sunrise"`
		Sunset  []string   `json:"sunset"`
	} `json:"daily"`
}

const wallLayout = "2006-01-02T15:04"

// Forecast fetches the forecast at lat, lon rounded to 2 decimals. baseURL "" means DefaultURL.
// name is the recording's name (a place label, never coordinates).
func Fetch(ctx context.Context, hc *http.Client, rec Recorder, baseURL, name string, lat, lon float64) (*Forecast, error) {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.2f", lat))
	q.Set("longitude", fmt.Sprintf("%.2f", lon))
	q.Set("hourly", "temperature_2m,apparent_temperature,precipitation_probability,precipitation,wind_speed_10m,wind_gusts_10m,weather_code")
	q.Set("daily", "temperature_2m_min,temperature_2m_max,sunrise,sunset")
	q.Set("wind_speed_unit", "kmh")
	q.Set("forecast_days", "2")
	q.Set("timezone", "Europe/Paris")
	sep := "?"
	if strings.Contains(baseURL, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+sep+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("Open-Meteo: bad URL")
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Open-Meteo: unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Open-Meteo: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("Open-Meteo: read failed")
	}
	f, err := parse(b)
	if err != nil {
		return nil, err
	}
	if rec != nil {
		_ = rec.Save("openmeteo_"+name, f) // parsed form: the raw body echoes the coordinates
	}
	return f, nil
}

func parse(b []byte) (*Forecast, error) {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("Open-Meteo: unreadable answer")
	}
	h := w.Hourly
	n := len(h.Time)
	if n == 0 {
		return nil, fmt.Errorf("Open-Meteo: no hourly data")
	}
	cols := [][]*float64{h.Temp, h.Feels, h.Precip, h.Wind, h.Gust, h.Code}
	if len(h.Prob) != n {
		return nil, fmt.Errorf("Open-Meteo: incomplete forecast")
	}
	for _, c := range cols {
		if len(c) != n {
			return nil, fmt.Errorf("Open-Meteo: incomplete forecast")
		}
		for _, p := range c {
			if p == nil {
				return nil, fmt.Errorf("Open-Meteo: incomplete forecast (gaps)")
			}
		}
	}
	f := &Forecast{}
	for i, ts := range h.Time {
		t, err := time.Parse(wallLayout, ts)
		if err != nil {
			return nil, fmt.Errorf("Open-Meteo: bad time")
		}
		hr := Hour{Time: t, Temp: *h.Temp[i], Feels: *h.Feels[i],
			Precip: *h.Precip[i], Wind: *h.Wind[i], Gust: *h.Gust[i], Code: int(*h.Code[i])}
		if h.Prob[i] == nil {
			hr.ProbUnknown = true
		} else {
			hr.PrecipProb = *h.Prob[i]
		}
		f.Hourly = append(f.Hourly, hr)
	}
	d := w.Daily
	m := len(d.Time)
	if len(d.Min) != m || len(d.Max) != m || len(d.Sunrise) != m || len(d.Sunset) != m {
		return nil, fmt.Errorf("Open-Meteo: incomplete daily data")
	}
	for i := range d.Time {
		if d.Min[i] == nil || d.Max[i] == nil {
			return nil, fmt.Errorf("Open-Meteo: incomplete daily data (gaps)")
		}
		day := Day{Min: *d.Min[i], Max: *d.Max[i]}
		var e1, e2, e3 error
		day.Date, e1 = time.Parse("2006-01-02", d.Time[i])
		day.Sunrise, e2 = time.Parse(wallLayout, d.Sunrise[i])
		day.Sunset, e3 = time.Parse(wallLayout, d.Sunset[i])
		if e1 != nil || e2 != nil || e3 != nil {
			return nil, fmt.Errorf("Open-Meteo: bad daily time")
		}
		f.Daily = append(f.Daily, day)
	}
	return f, nil
}
