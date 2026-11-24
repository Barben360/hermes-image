package section

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/pcb"
)

func init() {
	brief.Register(brief.Section{
		Name:        "calendar",
		Description: "today's and tomorrow's events from pcb (title, time, location), public holidays as day context; an error when pcb is not synced",
		ThirdParty:  true,
		Credentials: []string{"pcb_token"},
		Default:     func() brief.SectionConfig { return defaultCalendarConfig() },
		Collect:     collectCalendar,
	})
}

// duration is a time.Duration written as "6h" in YAML.
type duration struct{ D time.Duration }

func (d *duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: %q is not a duration like 6h", n.Line, s)
	}
	d.D = v
	return nil
}

func (d duration) MarshalYAML() (any, error) { return d.D.String(), nil }

type calendarConfig struct {
	Enabled bool `yaml:"enabled"`
	// Calendars are the pcb calendar types shown (normal, holidays, subscribed): selected by type, never by name.
	Calendars []string `yaml:"calendars"`
	// CalendarsExclude are calendar names (any case) never shown, whatever their type.
	CalendarsExclude []string `yaml:"calendars_exclude"`
	Days             int      `yaml:"days"`
	MaxSyncAge       duration `yaml:"max_sync_age"`
}

func defaultCalendarConfig() *calendarConfig {
	return &calendarConfig{Enabled: true, Calendars: []string{"normal", "holidays"}, CalendarsExclude: []string{},
		Days: 2, MaxSyncAge: duration{6 * time.Hour}}
}

func (c *calendarConfig) IsEnabled() bool { return c.Enabled }

func (c *calendarConfig) Validate() error {
	if c.Days < 1 || c.Days > 14 {
		return errors.New("calendar.days must be between 1 and 14")
	}
	if c.MaxSyncAge.D <= 0 {
		return errors.New("calendar.max_sync_age must be positive (like 6h)")
	}
	if len(c.Calendars) == 0 {
		return errors.New("calendar.calendars is empty: list calendar types (normal, holidays, subscribed)")
	}
	for _, t := range c.Calendars {
		if !slices.Contains([]string{"normal", "holidays", "subscribed"}, t) {
			return fmt.Errorf("calendar.calendars: unknown type %q (normal, holidays, subscribed)", t)
		}
	}
	return nil
}

type pcbStatus struct {
	Accounts []struct {
		Account        string   `json:"account"`
		ID             string   `json:"id"`
		Phase          string   `json:"phase"`
		LastSyncAgeS   *int     `json:"last_sync_age_s"`
		StaleCalendars []string `json:"stale_calendars"`
	} `json:"accounts"`
}

type pcbCalendars struct {
	Calendars []struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Untrusted struct {
			Name string `json:"name"`
		} `json:"untrusted"`
	} `json:"calendars"`
	Unavailable []json.RawMessage `json:"unavailable"`
}

type pcbTime struct {
	At       string `json:"at"`
	Date     string `json:"date"`
	Floating bool   `json:"floating"`
}

type pcbEvents struct {
	Events []struct {
		CalendarID   string  `json:"calendar_id"`
		CalendarType string  `json:"calendar_type"`
		AllDay       bool    `json:"all_day"`
		Start        pcbTime `json:"start"`
		End          pcbTime `json:"end"`
		Untrusted    struct {
			Summary  string `json:"summary"`
			Location string `json:"location"`
		} `json:"untrusted"`
	} `json:"events"`
	Truncated   bool              `json:"truncated"`
	NextCursor  string            `json:"next_cursor"`
	Skipped     int               `json:"skipped"`
	Unavailable []json.RawMessage `json:"unavailable"`
}

func (t pcbTime) in(loc *time.Location) (time.Time, error) {
	if t.Date != "" {
		return time.ParseInLocation("2006-01-02", t.Date, loc)
	}
	if v, err := time.Parse(time.RFC3339, t.At); err == nil {
		return v.In(loc), nil
	}
	return time.ParseInLocation("2006-01-02T15:04:05", t.At, loc)
}

func collectCalendar(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	cfg := sc.(*calendarConfig)
	res := brief.Result{Name: "calendar"}
	fail := func(format string, a ...any) brief.Result {
		res.Status, res.Err = brief.StatusError, fmt.Sprintf(format, a...)
		return res
	}
	token, err := env.Secrets.Read("pcb_token")
	if err != nil {
		return fail("%v", err)
	}
	cl, err := pcb.New(env.HTTP, env.Sources.PCBURL, token, env.Sources.PCBCA)
	if err != nil {
		return fail("%v", err)
	}

	var st pcbStatus
	if err := callJSON(ctx, cl, "status", nil, &st); err != nil {
		return fail("%v", err)
	}
	if len(st.Accounts) == 0 {
		return fail("pcb sees no account: no calendar can be read")
	}
	var oldest *int
	for _, a := range st.Accounts {
		if a.Phase != "ready" {
			return fail("pcb is not ready: account phase %s", brief.Sanitize(a.Phase))
		}
		if a.LastSyncAgeS == nil {
			return fail("pcb has not synced yet")
		}
		if oldest == nil || *a.LastSyncAgeS > *oldest {
			oldest = a.LastSyncAgeS
		}
	}
	age := time.Duration(*oldest) * time.Second
	if age > cfg.MaxSyncAge.D {
		return fail("pcb's last sync is %s old (max %s)", age.Round(time.Minute), cfg.MaxSyncAge.D)
	}

	var cals pcbCalendars
	if err := callJSON(ctx, cl, "calendars_list", nil, &cals); err != nil {
		return fail("%v", err)
	}
	if len(cals.Unavailable) > 0 {
		return fail("pcb could not list the calendars of %d account(s)", len(cals.Unavailable))
	}
	names := map[string]string{}
	include := map[string]bool{}
	var ids []string
	for _, c := range cals.Calendars {
		names[c.ID] = c.Untrusted.Name
		if !slices.Contains(cfg.Calendars, c.Type) || excluded(cfg.CalendarsExclude, c.Untrusted.Name) {
			continue
		}
		include[c.ID] = true
		ids = append(ids, c.ID)
	}
	if len(ids) == 0 {
		return fail("no calendar selected (types %s)", strings.Join(cfg.Calendars, ", "))
	}
	var stale []string
	for _, a := range st.Accounts {
		for _, id := range a.StaleCalendars {
			if include[id] {
				stale = append(stale, fmt.Sprintf("%q", brief.Sanitize(names[id])))
			}
		}
	}
	if len(stale) > 0 {
		return fail("calendar not synced: %s", strings.Join(stale, ", "))
	}

	loc := env.Loc
	now := env.Now.In(loc)
	y, m, d := now.Date()
	day := func(i int) time.Time { return time.Date(y, m, d+i, 0, 0, 0, 0, loc) }
	args := map[string]any{"from": day(0).Format(time.RFC3339), "to": day(cfg.Days).Format(time.RFC3339),
		"tz": loc.String(), "calendars": ids, "limit": 500}
	var evs []pcbEvents
	for page := 0; ; page++ {
		if page >= 20 {
			return fail("pcb returned more than 20 pages of events")
		}
		var p pcbEvents
		if err := callJSON(ctx, cl, "events_list", args, &p); err != nil {
			return fail("%v", err)
		}
		if p.Skipped > 0 {
			return fail("pcb could not read %d event(s): the day may be incomplete", p.Skipped)
		}
		if len(p.Unavailable) > 0 {
			return fail("pcb could not read the events of %d account(s)", len(p.Unavailable))
		}
		evs = append(evs, p)
		if !p.Truncated {
			break
		}
		if p.NextCursor == "" {
			return fail("pcb truncated the events without a cursor")
		}
		args["cursor"] = p.NextCursor
	}

	label := func(i int) string {
		switch i {
		case 0:
			return "today"
		case 1:
			return "tomorrow"
		}
		return day(i).Format("2006-01-02")
	}
	type line struct {
		allDay     bool
		start      time.Time
		title, loc string
		when       string
	}
	perDay := make([][]line, cfg.Days)
	holidays := make([][]string, cfg.Days)
	for _, p := range evs {
		for _, e := range p.Events {
			if !include[e.CalendarID] || !slices.Contains(cfg.Calendars, e.CalendarType) {
				continue
			}
			s, err1 := e.Start.in(loc)
			en, err2 := e.End.in(loc)
			if err1 != nil || err2 != nil {
				return fail("pcb returned an event time that cannot be read")
			}
			for i := 0; i < cfg.Days; i++ {
				ds, de := day(i), day(i+1)
				overlap := s.Before(de) && en.After(ds)
				if !en.After(s) {
					overlap = !s.Before(ds) && s.Before(de)
				}
				if !overlap {
					continue
				}
				if e.CalendarType == "holidays" {
					holidays[i] = append(holidays[i], e.Untrusted.Summary)
					continue
				}
				l := line{allDay: e.AllDay, start: s, title: e.Untrusted.Summary, loc: e.Untrusted.Location}
				l.when = "all-day"
				if !e.AllDay {
					l.when = s.Format("15:04") + "-" + en.Format("15:04")
				}
				perDay[i] = append(perDay[i], l)
			}
		}
	}

	syncedAt := now.Add(-age)
	var dates []string
	for i := 0; i < cfg.Days; i++ {
		dates = append(dates, label(i)+" "+day(i).Format("2006-01-02 Mon"))
	}
	res.Facts = append(res.Facts, brief.Fact{Key: "days", Value: strings.Join(dates, " · "), AsOf: syncedAt})
	report := false
	for i := 0; i < cfg.Days; i++ {
		if len(holidays[i]) > 0 {
			report = true
			res.Facts = append(res.Facts, brief.Untrusted("holiday "+label(i), strings.Join(holidays[i], ", ")))
		}
		ls := perDay[i]
		if len(ls) == 0 {
			res.Facts = append(res.Facts, brief.Fact{Key: label(i), Value: "no events"})
			continue
		}
		report = true
		sort.SliceStable(ls, func(a, b int) bool {
			if ls[a].allDay != ls[b].allDay {
				return ls[a].allDay
			}
			if !ls[a].start.Equal(ls[b].start) {
				return ls[a].start.Before(ls[b].start)
			}
			return ls[a].title < ls[b].title
		})
		for _, l := range ls {
			facts := []brief.Fact{{Key: label(i), Value: l.when}, brief.Untrusted("title", l.title)}
			if strings.TrimSpace(l.loc) != "" {
				facts = append(facts, brief.Untrusted("location", l.loc))
			}
			res.Items = append(res.Items, brief.Item{Facts: facts})
		}
	}
	res.Status = brief.StatusOK
	if report {
		res.Status = brief.StatusReport
	}
	return res
}

func excluded(list []string, name string) bool {
	for _, n := range list {
		if strings.EqualFold(strings.TrimSpace(n), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func callJSON(ctx context.Context, cl *pcb.Client, tool string, args any, into any) error {
	raw, err := cl.Call(ctx, tool, args)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("pcb %s: unexpected result shape", tool)
	}
	return nil
}
