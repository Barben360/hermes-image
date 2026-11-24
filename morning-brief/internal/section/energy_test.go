package section

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

const energyToken = "tok-energy-secret-123"

type fakeHA struct {
	mu       sync.Mutex
	authFail bool
	emptyAns bool                          // statistics_during_period answers {}
	prefs    string                        // fixture file, default energy-prefs.json
	rawPrefs string                        // inline prefs JSON, overrides prefs
	now      time.Time                     // zero = the default fake clock
	stats    map[string]map[string]float64 // stat id -> "2006-01-02" -> change
	requests []map[string]any              // statistics_during_period requests
}

func readFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("../../testdata/ha/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *fakeHA) serve(t *testing.T, loc *time.Location) *httptest.Server {
	file := f.prefs
	if file == "" {
		file = "energy-prefs.json"
	}
	prefs, info := readFixture(t, file), readFixture(t, "energy-info.json")
	if f.rawPrefs != "" {
		prefs = json.RawMessage(f.rawPrefs)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		send := func(v any) {
			b, _ := json.Marshal(v)
			_ = c.Write(ctx, websocket.MessageText, b)
		}
		recv := func() (map[string]any, bool) {
			_, b, err := c.Read(ctx)
			if err != nil {
				return nil, false
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			return m, true
		}
		send(map[string]any{"type": "auth_required"})
		auth, ok := recv()
		if !ok {
			return
		}
		if f.authFail || auth["access_token"] != energyToken {
			send(map[string]any{"type": "auth_invalid", "message": "Invalid access token " + energyToken})
			return
		}
		send(map[string]any{"type": "auth_ok"})
		for {
			m, ok := recv()
			if !ok {
				return
			}
			res := map[string]any{"id": m["id"], "type": "result", "success": true}
			switch m["type"] {
			case "energy/get_prefs":
				res["result"] = prefs
			case "energy/info":
				res["result"] = info
			case "recorder/statistics_during_period":
				f.mu.Lock()
				f.requests = append(f.requests, m)
				f.mu.Unlock()
				out := map[string][]map[string]any{}
				ids, _ := m["statistic_ids"].([]any)
				for _, id := range ids {
					days := f.stats[id.(string)]
					for d, ch := range days {
						day, _ := time.ParseInLocation("2006-01-02", d, loc)
						var change any = ch
						if math.IsNaN(ch) {
							change = nil
						}
						out[id.(string)] = append(out[id.(string)], map[string]any{
							"start": day.UnixMilli(), "end": day.AddDate(0, 0, 1).UnixMilli(), "change": change,
						})
					}
				}
				if f.emptyAns {
					out = map[string][]map[string]any{}
				}
				res["result"] = out
			default:
				res["success"] = false
				res["error"] = map[string]any{"code": "unknown_command"}
			}
			send(res)
		}
	}))
}

func series(vals map[string]float64) map[string]float64 { return vals }

func defaultStats() map[string]map[string]float64 {
	return map[string]map[string]float64{
		"sensor.meter_base": series(map[string]float64{
			"2026-11-16": 100, // outside the 7 days before yesterday
			"2026-11-17": 5, "2026-11-18": 6, "2026-11-19": 7, "2026-11-20": 8, "2026-11-21": 9, "2026-11-22": 10, "2026-11-23": 11,
			"2026-11-24": 12,
		}),
		"sensor.meter_base_cost": series(map[string]float64{
			"2026-11-16": 19, "2026-11-17": 0.95, "2026-11-18": 1.14, "2026-11-19": 1.33, "2026-11-20": 1.52, "2026-11-21": 1.71,
			"2026-11-22": 1.9, "2026-11-23": 2.09, "2026-11-24": 2.28,
		}),
		"sensor.meter_offpeak": series(map[string]float64{
			"2026-11-17": 1, "2026-11-18": 1, "2026-11-19": 1, "2026-11-20": 1, "2026-11-21": 1, "2026-11-22": 1, "2026-11-23": 1, "2026-11-24": 2,
		}),
		"sensor.meter_offpeak_cost": series(map[string]float64{
			"2026-11-17": 0.15, "2026-11-18": 0.15, "2026-11-19": 0.15, "2026-11-20": 0.15, "2026-11-21": 0.15, "2026-11-22": 0.15, "2026-11-23": 0.15, "2026-11-24": 0.3,
		}),
		"sensor.elec_sub_zero_energy": series(map[string]float64{"2026-11-24": 0}),
		"sensor.elec_sub_cumul": series(map[string]float64{
			"2026-11-17": 0.5, "2026-11-18": 0.5, "2026-11-19": 0.5, "2026-11-20": 0.5, "2026-11-21": 0.5, "2026-11-22": 0.5, "2026-11-23": 0.5, "2026-11-24": 0.5,
		}),
		// Gazpar lags: no row for 2026-11-24.
		"sensor.gas_filtered": series(map[string]float64{
			"2026-11-16": 18, "2026-11-17": 20, "2026-11-18": 22, "2026-11-19": 24, "2026-11-20": 26, "2026-11-21": 28, "2026-11-22": 30,
			"2026-11-23": 32,
		}),
		// The id energy/info gives (_cost_2) holds the real values; the same name without the suffix is the orphan.
		"sensor.gas_filtered_cost_2": series(map[string]float64{
			"2026-11-16": 18 * 0.1789, "2026-11-17": 20 * 0.1789, "2026-11-18": 22 * 0.1789, "2026-11-19": 24 * 0.1789, "2026-11-20": 26 * 0.1789,
			"2026-11-21": 28 * 0.1789, "2026-11-22": 30 * 0.1789, "2026-11-23": 32 * 0.1789,
		}),
		"sensor.gas_filtered_cost":   series(map[string]float64{"2026-11-23": 999}),
		"sensor.gas_sub_zero_energy": series(map[string]float64{"2026-11-23": 0}),
		"sensor.gas_sub_cumul": series(map[string]float64{
			"2026-11-16": 0.42, "2026-11-17": 0.42, "2026-11-18": 0.42, "2026-11-19": 0.42, "2026-11-20": 0.42, "2026-11-21": 0.42,
			"2026-11-22": 0.42, "2026-11-23": 0.42, "2026-11-24": 0.42,
		}),
	}
}

func energyEnv(t *testing.T, srv *httptest.Server) *brief.Env {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ha_token"), []byte(energyToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := haEnv(nil, nil)
	env.Secrets = brief.NewSecrets(dir)
	env.Sources = brief.Sources{HAURL: srv.URL}
	return env
}

func runEnergy(t *testing.T, f *fakeHA) (brief.Result, *fakeHA) {
	t.Helper()
	loc, _ := time.LoadLocation("Europe/Paris")
	if f.stats == nil {
		f.stats = defaultStats()
	}
	srv := f.serve(t, loc)
	defer srv.Close()
	env := energyEnv(t, srv)
	if !f.now.IsZero() {
		env.Now = f.now
	}
	return collect(t, "energy", env, newEnergyConfig()), f
}

func TestEnergySumsAllSources(t *testing.T) {
	r, _ := runEnergy(t, &fakeHA{})
	if r.Status != brief.StatusReport {
		t.Fatalf("status %s err %q", r.Status, r.Err)
	}
	f, ok := fact(r, "electricity yesterday")
	if !ok || f.Value != "14.0 kWh, 3.08 €" {
		t.Fatalf("got %+v\n%s", f, dump(r))
	}
}

func TestEnergyGasCostFromInfo(t *testing.T) {
	r, fk := runEnergy(t, &fakeHA{})
	f, ok := fact(r, "gas latest complete day 2026-11-23")
	// 32 kWh at 0.1789 from _cost_2 (5.7248) + subscription 0.42; the orphan `_cost` holds 999.
	if !ok || f.Value != "32.0 kWh, 6.14 €" {
		t.Fatalf("got %+v\n%s", f, dump(r))
	}
	for _, req := range fk.requests {
		for _, id := range req["statistic_ids"].([]any) {
			if id == "sensor.gas_filtered_cost" {
				t.Fatalf("derived id requested: %v", id)
			}
		}
	}
}

func TestEnergyRequestShape(t *testing.T) {
	_, fk := runEnergy(t, &fakeHA{})
	if len(fk.requests) == 0 {
		t.Fatal("no statistics request")
	}
	req := fk.requests[0]
	if req["period"] != "day" || req["types"].([]any)[0] != "change" {
		t.Fatalf("request %v", req)
	}
	// Window starts at Paris midnight (23:00 UTC the day before).
	if st := req["start_time"].(string); !strings.HasSuffix(st, "T23:00:00Z") {
		t.Fatalf("start_time %s is not a Paris midnight", st)
	}
}

func TestEnergyAverage7Days(t *testing.T) {
	r, _ := runEnergy(t, &fakeHA{})
	// electricity: 17..23 (the 100 kWh row of the 16th and yesterday are excluded): base 8 + offpeak 1; EUR 1.52+0.15+0.5.
	if f, _ := fact(r, "electricity avg 7d"); f.Value != "9.0 kWh, 2.17 €" {
		t.Fatalf("elec avg %q\n%s", f.Value, dump(r))
	}
	// gas: the 7 days before the latest complete day (16..22): 24 kWh, 24*0.1789+0.42.
	if f, _ := fact(r, "gas avg 7d"); f.Value != "24.0 kWh, 4.71 €" {
		t.Fatalf("gas avg %q\n%s", f.Value, dump(r))
	}
}

func TestEnergyGazparLag(t *testing.T) {
	r, _ := runEnergy(t, &fakeHA{})
	if _, ok := fact(r, "gas latest complete day 2026-11-23"); !ok {
		t.Fatalf("no lagged gas fact\n%s", dump(r))
	}
	if _, ok := fact(r, "gas latest complete day 2026-11-24"); ok {
		t.Fatal("yesterday has no gas row yet")
	}
	// Once Gazpar catches up the date moves.
	st := defaultStats()
	st["sensor.gas_filtered"]["2026-11-24"] = 40
	r, _ = runEnergy(t, &fakeHA{stats: st})
	if _, ok := fact(r, "gas latest complete day 2026-11-24"); !ok {
		t.Fatalf("date did not move\n%s", dump(r))
	}
}

func TestEnergyNoGasDataIsStated(t *testing.T) {
	st := defaultStats()
	delete(st, "sensor.gas_filtered")
	r, _ := runEnergy(t, &fakeHA{stats: st})
	f, ok := fact(r, "gas latest complete day")
	if !ok || !strings.Contains(f.Value, "no data") {
		t.Fatalf("got %+v\n%s", f, dump(r))
	}
}

func TestEnergyWSAuthFailedIsError(t *testing.T) {
	r, _ := runEnergy(t, &fakeHA{authFail: true})
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "authentication failed") {
		t.Fatalf("status %s err %q", r.Status, r.Err)
	}
	if strings.Contains(r.Err, energyToken) || len(r.Facts) != 0 {
		t.Fatalf("token leaked or facts present: %q %v", r.Err, r.Facts)
	}
}

func TestEnergyUnreachableIsError(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Paris")
	f := &fakeHA{}
	srv := f.serve(t, loc)
	env := energyEnv(t, srv)
	srv.Close()
	r := collect(t, "energy", env, newEnergyConfig())
	if r.Status != brief.StatusError {
		t.Fatalf("status %s", r.Status)
	}
}

func TestEnergyDefaultEnabled(t *testing.T) {
	c := newEnergyConfig()
	if !c.IsEnabled() || c.Validate() != nil {
		t.Fatal("energy must default to enabled and valid")
	}
}

func TestEnergyMeterMissingYesterdayIsNoData(t *testing.T) {
	st := defaultStats()
	for _, id := range []string{"sensor.meter_base", "sensor.meter_offpeak", "sensor.meter_base_cost", "sensor.meter_offpeak_cost"} {
		delete(st[id], "2026-11-24")
		delete(st[id], "2026-11-17")
	}
	r, _ := runEnergy(t, &fakeHA{stats: st})
	// The subscription's zero-kWh sensor and its cost still have rows both days.
	if f, _ := fact(r, "electricity yesterday"); f.Value != "no data for 2026-11-24" {
		t.Fatalf("yesterday %q", f.Value)
	}
	// 17th has only subscription rows: skipped, so 6 days (6..11 -> 8.5) + offpeak 1.
	if f, _ := fact(r, "electricity avg 7d"); !strings.HasPrefix(f.Value, "9.5 kWh") {
		t.Fatalf("avg %q", f.Value)
	}
}

func TestEnergyMissingCostSeriesIsUnknownNotZero(t *testing.T) {
	st := defaultStats()
	delete(st["sensor.meter_offpeak_cost"], "2026-11-24")
	r, _ := runEnergy(t, &fakeHA{stats: st})
	if f, _ := fact(r, "electricity yesterday"); f.Value != "14.0 kWh, cost unknown" {
		t.Fatalf("yesterday %q", f.Value)
	}
	if f, _ := fact(r, "electricity avg 7d"); f.Value != "9.0 kWh, 2.17 €" {
		t.Fatalf("avg %q", f.Value)
	}
	delete(st, "sensor.gas_filtered_cost_2")
	r, _ = runEnergy(t, &fakeHA{stats: st})
	if f, _ := fact(r, "gas latest complete day 2026-11-23"); f.Value != "32.0 kWh, cost unknown" {
		t.Fatalf("gas %q", f.Value)
	}
}

func TestEnergyNullChangeIsNotADataDay(t *testing.T) {
	st := defaultStats()
	st["sensor.meter_base"]["2026-11-24"] = math.NaN()
	st["sensor.meter_offpeak"]["2026-11-24"] = math.NaN()
	r, _ := runEnergy(t, &fakeHA{stats: st})
	if f, _ := fact(r, "electricity yesterday"); f.Value != "no data for 2026-11-24" {
		t.Fatalf("yesterday %q", f.Value)
	}
}

func TestEnergyEmptyStatisticsAnswerIsError(t *testing.T) {
	r, _ := runEnergy(t, &fakeHA{emptyAns: true})
	if r.Status != brief.StatusError || len(r.Facts) != 0 {
		t.Fatalf("status %s facts %v", r.Status, r.Facts)
	}
}

func TestEnergyUsesEnvDial(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Paris")
	f := &fakeHA{stats: defaultStats()}
	srv := f.serve(t, loc)
	defer srv.Close()
	env := energyEnv(t, srv)
	dialed := 0
	env.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed++
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	if r := collect(t, "energy", env, newEnergyConfig()); r.Status != brief.StatusReport {
		t.Fatalf("status %s err %q", r.Status, r.Err)
	}
	if dialed == 0 {
		t.Fatal("Env.Dial was not used")
	}
}

func TestEnergyAcrossDSTChange(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Paris")
	// Summer time ended on 2026-10-25: yesterday is a 25-hour day.
	st := map[string]map[string]float64{
		"sensor.meter_base":         {"2026-10-24": 5, "2026-10-25": 12},
		"sensor.meter_base_cost":    {"2026-10-24": 1, "2026-10-25": 2.4},
		"sensor.meter_offpeak":      {"2026-10-25": 2},
		"sensor.meter_offpeak_cost": {"2026-10-25": 0.3},
		"sensor.elec_sub_cumul":     {"2026-10-25": 0.5},
	}
	r, fk := runEnergy(t, &fakeHA{stats: st, now: time.Date(2026, 10, 26, 7, 15, 0, 0, loc)})
	if f, _ := fact(r, "electricity yesterday"); f.Value != "14.0 kWh, 3.20 €" {
		t.Fatalf("yesterday %q\n%s", f.Value, dump(r))
	}
	req := fk.requests[0]
	if req["start_time"] != "2026-10-15T22:00:00Z" || req["end_time"] != "2026-10-25T23:00:00Z" {
		t.Fatalf("window %v .. %v", req["start_time"], req["end_time"])
	}
}

func TestEnergyFlowFromShape(t *testing.T) {
	r, _ := runEnergy(t, &fakeHA{prefs: "energy-prefs-flow-from.json"})
	if f, _ := fact(r, "electricity yesterday"); f.Value != "14.0 kWh, 3.08 €" {
		t.Fatalf("yesterday %q\n%s", f.Value, dump(r))
	}
	if _, ok := fact(r, "gas latest complete day 2026-11-23"); !ok {
		t.Fatalf("no gas\n%s", dump(r))
	}
}

func TestEnergyOddGridShapeIsVisible(t *testing.T) {
	raw := `{"energy_sources":[{"type":"grid","something_new":{"x":1}},{"type":"gas","stat_energy_from":"sensor.gas_filtered"}]}`
	r, _ := runEnergy(t, &fakeHA{rawPrefs: raw})
	if f, ok := fact(r, "electricity"); !ok || !strings.HasPrefix(f.Value, "no data") {
		t.Fatalf("electricity silently dropped\n%s", dump(r))
	}
	if _, ok := fact(r, "gas latest complete day 2026-11-23"); !ok {
		t.Fatalf("gas lost\n%s", dump(r))
	}
	raw = `{"energy_sources":[{"type":"gas"}]}`
	r, _ = runEnergy(t, &fakeHA{rawPrefs: raw})
	if f, ok := fact(r, "gas"); !ok || !strings.HasPrefix(f.Value, "no data") {
		t.Fatalf("gas silently dropped\n%s", dump(r))
	}
}
