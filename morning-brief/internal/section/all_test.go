package section

import (
	"context"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/config"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

// failMode is one way a source can fail.
type failMode string

const (
	credMissing failMode = "credential file missing"
	http500     failMode = "HTTP 500"
	notJSON     failMode = "body `not json`"
	emptyObj    failMode = "body `{}`"
	htmlBody    failMode = "body `<html>`"
	hang        failMode = "hang past the section timeout"
)

// coreModes apply to every section, unless its row says why not.
var coreModes = []failMode{credMissing, http500, notJSON, emptyObj, htmlBody, hang}

// extra modes, run only for the sections listing them.
const (
	dialFail       failMode = "dial fails"
	dialHang       failMode = "dial hangs"
	vaultMissing   failMode = "vault directory missing"
	secondary500   failMode = "Home Assistant fine, the other source HTTP 500"
	secondaryEmpty failMode = "Home Assistant fine, the other source body `{}`"
	secondaryHang  failMode = "Home Assistant fine, the other source hangs"
)

func (m failMode) body() string {
	switch m {
	case notJSON:
		return "not json"
	case emptyObj, secondaryEmpty:
		return "{}"
	case htmlBody:
		return "<html><body>502 Bad Gateway</body></html>"
	}
	return ""
}

// failRow is one section's case: a valid minimal config, the modes that do not apply (with the
// reason) and the extra modes that do.
type failRow struct {
	cfg   func(r *failRig) brief.SectionConfig
	na    map[failMode]string
	extra []failMode
}

const failIMAPAddr = "imap.example.test:143"

var failRows = map[string]failRow{
	"weather": {
		cfg: func(r *failRig) brief.SectionConfig {
			c := newWeatherConfig()
			c.APIURL = r.other.URL + "/v1/forecast"
			c.Places = map[string]string{"home": "zone.home", "work": "zone.work"}
			return c
		},
		extra: []failMode{secondary500, secondaryEmpty, secondaryHang},
	},
	"calendar": {cfg: func(*failRig) brief.SectionConfig { return defaultCalendarConfig() }},
	"car": {cfg: func(*failRig) brief.SectionConfig {
		c := newCarConfig()
		c.Battery, c.Range, c.Plug, c.Charging, c.Target, c.Tracker = "sensor.car_battery", "sensor.car_range",
			"sensor.car_plug", "binary_sensor.car_charging", "number.car_target", "device_tracker.car"
		return c
	}},
	"robot": {cfg: func(*failRig) brief.SectionConfig {
		c := newRobotConfig()
		c.LastCleanEnd = "sensor.robot_end"
		return c
	}},
	"sensors": {cfg: func(*failRig) brief.SectionConfig {
		c := newSensorsConfig()
		c.Include = []string{"sensor.room_*"}
		return c
	}},
	"energy": {cfg: func(*failRig) brief.SectionConfig { return newEnergyConfig() }},
	"home": {
		cfg: func(*failRig) brief.SectionConfig {
			c := newHomeConfig()
			c.ShoppingList = "todo.shopping"
			return c
		},
		extra: []failMode{secondary500, secondaryEmpty, secondaryHang},
	},
	"alerts": {cfg: func(*failRig) brief.SectionConfig {
		return &AlertsConfig{Enabled: true, ExcludeAlertnames: []string{"Watchdog"}, IncludeInfo: true}
	}},
	"expiries": {
		cfg: func(*failRig) brief.SectionConfig {
			return &ExpiriesConfig{Enabled: true, Files: []string{"github_token_expires"}, WarnWithinDays: 30}
		},
		na: map[failMode]string{
			http500:  "no remote source: the dates are rendered secret files",
			emptyObj: "a file, not a JSON body: `not json` and `<html>` stand for a malformed file",
			hang:     "no remote source: reading a local file cannot outlast the timeout",
		},
	},
	"mail": {
		cfg: func(*failRig) brief.SectionConfig { return newMailConfig() },
		na: map[failMode]string{
			http500:  "IMAP, no HTTP: a refused connection is `dial fails`",
			emptyObj: "IMAP, no JSON: `not json` and `<html>` stand for a garbage greeting",
		},
		extra: []failMode{dialFail, dialHang},
	},
	"github": {cfg: func(r *failRig) brief.SectionConfig {
		c := githubDefault()
		c.APIURL = r.other.URL
		return c
	}},
	"deepseek": {cfg: func(r *failRig) brief.SectionConfig {
		c := deepseekDefault()
		c.BaseURL = r.other.URL
		return c
	}},
	"bugsink": {cfg: func(*failRig) brief.SectionConfig { return bugsinkDefault() }},
	"pet_feeder": {cfg: func(*failRig) brief.SectionConfig {
		c := newPetFeederConfig()
		c.FoodLevel, c.Fault, c.FeedSuccess = "sensor.feeder_food", "sensor.feeder_fault", "event.feeder_feedsuccess"
		return c
	}},
	"presence": {cfg: func(*failRig) brief.SectionConfig { return &PresenceConfig{Enabled: true, Person: "person.alex"} }},
	"tech_radar": {
		cfg: func(*failRig) brief.SectionConfig { return techRadarDefault() },
		na: map[failMode]string{
			credMissing: "no credential: it only looks for a file in the vault",
			http500:     "no HTTP: the vault is a local directory (its failure is `vault directory missing`)",
			notJSON:     "the note's content is never read, only its existence",
			emptyObj:    "the note's content is never read, only its existence",
			htmlBody:    "the note's content is never read, only its existence",
			hang:        "a local stat; a hung mount is not reproducible in a test",
		},
		extra: []failMode{vaultMissing},
	},
}

// healthyStates is a valid Home Assistant answer for the secondary modes: zones (fake coordinates), no update.
const healthyStates = `[
 {"entity_id":"zone.home","state":"0","attributes":{"latitude":45.0,"longitude":5.0,"friendly_name":"Home"},"last_changed":"2026-11-24T03:00:00+00:00","last_updated":"2026-11-24T03:00:00+00:00","last_reported":"2026-11-24T03:00:00+00:00"},
 {"entity_id":"zone.work","state":"0","attributes":{"latitude":45.0,"longitude":5.0,"friendly_name":"Work"},"last_changed":"2026-11-24T03:00:00+00:00","last_updated":"2026-11-24T03:00:00+00:00","last_reported":"2026-11-24T03:00:00+00:00"},
 {"entity_id":"sun.sun","state":"below_horizon","attributes":{},"last_changed":"2026-11-24T03:00:00+00:00","last_updated":"2026-11-24T03:00:00+00:00","last_reported":"2026-11-24T03:00:00+00:00"}
]`

// failRig is a set of failing doubles: Home Assistant (REST and WebSocket), pcb (TLS), one HTTP
// server for every other source, an IMAP endpoint, the secret files and the vault.
type failRig struct {
	mode       failMode
	ha, other  *httptest.Server
	pcb        *httptest.Server
	caFile     string
	secrets    string
	vault      string
	imapLn     net.Listener
	release    chan struct{}
	imapConfig string
}

func (r *failRig) fail(w http.ResponseWriter, req *http.Request, mode failMode) {
	switch mode {
	case hang, secondaryHang:
		select {
		case <-req.Context().Done():
		case <-r.release:
		}
	case notJSON, emptyObj, htmlBody, secondaryEmpty:
		if req.URL.Path == "/api/websocket" {
			c, err := websocket.Accept(w, req, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			_ = c.Write(req.Context(), websocket.MessageText, []byte(mode.body()))
			_, _, _ = c.Read(req.Context()) // keep the socket open until the client gives up
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mode.body()))
	default: // credMissing (every source down too, the error must still name the file), HTTP 500
		http.Error(w, "boom", http.StatusInternalServerError)
	}
}

func (r *failRig) secondary() bool {
	return r.mode == secondary500 || r.mode == secondaryEmpty || r.mode == secondaryHang
}

// otherMode is how the non-Home-Assistant sources fail in a secondary mode.
func (r *failRig) otherMode() failMode {
	switch r.mode {
	case secondary500:
		return http500
	case secondaryEmpty:
		return emptyObj
	case secondaryHang:
		return hang
	}
	return r.mode
}

func newFailRig(t *testing.T, mode failMode) *failRig {
	t.Helper()
	r := &failRig{mode: mode, release: make(chan struct{})}
	r.ha = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.secondary() && req.URL.Path == "/api/states" {
			w.Write([]byte(healthyStates))
			return
		}
		r.fail(w, req, r.otherMode())
	}))
	r.other = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.fail(w, req, r.otherMode()) }))
	r.pcb = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.fail(w, req, r.otherMode()) }))
	dir := t.TempDir()
	r.caFile = filepath.Join(dir, "pcb.pem")
	if err := os.WriteFile(r.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.pcb.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r.imapLn = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if b := mode.body(); b != "" {
					c.Write([]byte(b + "\r\n"))
				}
				select { // never a valid greeting: hang until the client goes or the test ends
				case <-r.release:
				case <-time.After(10 * time.Second):
				}
			}()
		}
	}()
	// release runs before the servers close (cleanups are LIFO), so no handler outlives the test
	t.Cleanup(func() { r.ha.Close(); r.other.Close(); r.pcb.Close(); ln.Close() })
	t.Cleanup(func() { close(r.release) })

	r.secrets = filepath.Join(dir, "secrets")
	os.Mkdir(r.secrets, 0o700)
	if mode != credMissing {
		for _, s := range brief.Sections() {
			for _, c := range s.Credentials {
				v := "dummy-" + c
				if strings.HasSuffix(c, "_expires") {
					v = "2027-06-30"
					if mode == notJSON || mode == htmlBody {
						v = mode.body()
					}
				}
				os.WriteFile(filepath.Join(r.secrets, c), []byte(v+"\n"), 0o600)
			}
		}
	}
	r.vault = filepath.Join(dir, "vault")
	if mode != vaultMissing {
		os.MkdirAll(filepath.Join(r.vault, "Tech", "Tech Radar", "Dailies"), 0o755)
	}
	r.imapConfig = filepath.Join(dir, "himalaya.toml")
	os.WriteFile(r.imapConfig, []byte(`[accounts.main]
default = true
email = "me@example.org"
imap.server = "imap://`+failIMAPAddr+`"
imap.starttls = false
imap.sasl.plain.username = "me@example.org"
`), 0o600)
	return r
}

func (r *failRig) env() *brief.Env {
	loc, _ := time.LoadLocation("Europe/Paris")
	env := &brief.Env{
		Now:     time.Date(2026, 11, 25, 7, 15, 0, 0, loc),
		Loc:     loc,
		HTTP:    &http.Client{},
		Secrets: brief.NewSecrets(r.secrets),
		Sources: brief.Sources{
			HAURL: r.ha.URL, GrafanaURL: r.other.URL, PCBURL: r.pcb.URL + "/mcp", PCBCA: r.caFile,
			IMAPConfig: r.imapConfig, BugsinkURL: r.other.URL, BugsinkHost: "bugsink.example.test", VaultDir: r.vault,
		},
	}
	env.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != failIMAPAddr {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		switch r.mode {
		case dialFail:
			return nil, errors.New("connection refused")
		case dialHang:
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return (&net.Dialer{}).DialContext(ctx, network, r.imapLn.Addr().String())
	}
	// as cmd/morning-brief's memoHA: the token file, then GET /api/states
	env.HA = func(ctx context.Context) (*ha.Snapshot, error) {
		tok, err := env.Secrets.Read("ha_token")
		if err != nil {
			return nil, err
		}
		return (&ha.Client{BaseURL: env.Sources.HAURL, Token: tok, HTTP: env.HTTP}).States(ctx)
	}
	return env
}

// TestEverySectionFailsLoudly: whatever the way a source fails, a section says error, never ok or
// report (an empty "nothing to say" would hide the failure from the owner).
func TestEverySectionFailsLoudly(t *testing.T) {
	secs := brief.Sections()
	if len(secs) != 16 {
		t.Errorf("%d sections registered, want the 16 of the plan", len(secs))
	}
	names := map[string]bool{}
	for _, s := range secs {
		names[s.Name] = true
		if _, ok := failRows[s.Name]; !ok {
			t.Errorf("section %s has no failure case in failRows: add one", s.Name)
		}
	}
	for n := range failRows {
		if !names[n] {
			t.Errorf("failRows names %s, which is not a registered section", n)
		}
	}
	const timeout = 400 * time.Millisecond
	for _, s := range secs {
		row, ok := failRows[s.Name]
		if !ok {
			continue
		}
		for m := range row.na {
			if !slices.Contains(coreModes, m) {
				t.Errorf("%s: na lists %q, not a core mode", s.Name, m)
			}
		}
		modes := append(append([]failMode(nil), coreModes...), row.extra...)
		for _, m := range modes {
			if reason, skip := row.na[m]; skip {
				t.Logf("%s / %s: not applicable: %s", s.Name, m, reason)
				continue
			}
			t.Run(s.Name+"/"+string(m), func(t *testing.T) {
				t.Parallel()
				rig := newFailRig(t, m)
				cfg := row.cfg(rig)
				if err := cfg.Validate(); err != nil {
					t.Fatalf("the test's config is invalid: %v", err)
				}
				start := time.Now()
				res := brief.Run(context.Background(), rig.env(), brief.Plan{
					Configs: map[string]brief.SectionConfig{s.Name: cfg}, Only: []string{s.Name}, SectionTimeout: timeout,
				})
				if len(res) != 1 {
					t.Fatalf("%d results", len(res))
				}
				r := res[0]
				if r.Status != brief.StatusError {
					t.Fatalf("status %s, want error (err %q, facts %+v, items %+v)", r.Status, r.Err, r.Facts, r.Items)
				}
				t.Logf("err: %s", r.Err)
				if r.Err == "" {
					t.Error("error without a message")
				}
				if m == credMissing && len(s.Credentials) > 0 && !strings.Contains(r.Err, s.Credentials[0]) {
					t.Errorf("error %q does not name the missing file %s", r.Err, s.Credentials[0])
				}
				if el := time.Since(start); el > timeout+2*time.Second {
					t.Errorf("took %s, the section timeout is %s", el, timeout)
				}
			})
		}
	}
}

// TestDefaultOrderNamesEverySection: writer.order's default lists each registered section once, so
// none falls to the alphabetical tail by accident.
func TestDefaultOrderNamesEverySection(t *testing.T) {
	l := config.Load(filepath.Join(t.TempDir(), "none.yaml"), brief.Sections())
	var names []string
	for _, s := range brief.Sections() {
		names = append(names, s.Name)
	}
	got := append([]string(nil), l.Writer.Order...)
	slices.Sort(got)
	slices.Sort(names)
	if !slices.Equal(got, names) {
		t.Errorf("default order %v, registered %v", l.Writer.Order, names)
	}
}
