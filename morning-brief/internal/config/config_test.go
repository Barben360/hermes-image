package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

type fakeCfg struct {
	Enabled bool   `yaml:"enabled"`
	Hours   int    `yaml:"hours"`
	Name    string `yaml:"name"`
}

func (c *fakeCfg) IsEnabled() bool { return c.Enabled }
func (c *fakeCfg) Validate() error {
	if c.Hours < 0 {
		return errors.New("hours must be >= 0")
	}
	return nil
}

func fakeSections() []brief.Section {
	mk := func(name string, third bool) brief.Section {
		return brief.Section{
			Name: name, ThirdParty: third,
			Default: func() brief.SectionConfig { return &fakeCfg{Enabled: true, Hours: 24, Name: "dflt"} },
		}
	}
	return []brief.Section{mk("fakeA", false), mk("fakeThirdParty", true)}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func cfgOf(t *testing.T, l Loaded, name string) *fakeCfg {
	t.Helper()
	c, ok := l.Plan.Configs[name].(*fakeCfg)
	if !ok {
		t.Fatalf("no config for %s: %#v", name, l.Plan.Configs)
	}
	return c
}

func TestMissingFileIsDefaults(t *testing.T) {
	l := Load(filepath.Join(t.TempDir(), "nope.yaml"), fakeSections())
	if len(l.TopErrors) != 0 || len(l.Plan.ConfigErrors) != 0 {
		t.Fatalf("%v %v", l.TopErrors, l.Plan.ConfigErrors)
	}
	if c := cfgOf(t, l, "fakeA"); !c.Enabled || c.Hours != 24 {
		t.Errorf("%+v", c)
	}
	if l.Writer.Language != "fr" {
		t.Errorf("writer %+v", l.Writer)
	}
	want := brief.Sources{HAURL: "ha.bbarrois.tech", GrafanaURL: "http://grafana:3000", PCBURL: "https://proton-calendar-bridge:1080/mcp",
		PCBCA: "/etc/hermes-ca/pcb.pem", IMAPConfig: "/etc/himalaya/config.toml", BugsinkURL: "http://bugsink:8000",
		BugsinkHost: "erreurs.infirlib.fr", VaultDir: "/opt/vault", CronJobs: "/opt/data/cron/jobs.json"}
	if l.Sources != want {
		t.Errorf("sources %+v", l.Sources)
	}
}

func TestOverrideOneKey(t *testing.T) {
	l := Load(writeFile(t, "fakeA:\n  hours: 6\nsources:\n  vault_dir: /x\n"), fakeSections())
	if len(l.TopErrors)+len(l.Plan.ConfigErrors) != 0 {
		t.Fatalf("%v %v", l.TopErrors, l.Plan.ConfigErrors)
	}
	c := cfgOf(t, l, "fakeA")
	if c.Hours != 6 || c.Name != "dflt" || !c.Enabled {
		t.Errorf("%+v", c)
	}
	if cfgOf(t, l, "fakeThirdParty").Hours != 24 {
		t.Error("other section changed")
	}
	if l.Sources.VaultDir != "/x" || l.Sources.CronJobs != "/opt/data/cron/jobs.json" {
		t.Errorf("%+v", l.Sources)
	}
}

func TestUnknownKeyBreaksOnlyThatSection(t *testing.T) {
	l := Load(writeFile(t, "fakeA:\n  hourz: 6\n"), fakeSections())
	if msg := l.Plan.ConfigErrors["fakeA"]; !strings.Contains(msg, "hourz") {
		t.Errorf("error %q", msg)
	}
	if _, bad := l.Plan.ConfigErrors["fakeThirdParty"]; bad || len(l.TopErrors) != 0 {
		t.Errorf("%v %v", l.Plan.ConfigErrors, l.TopErrors)
	}
	if !cfgOf(t, l, "fakeThirdParty").Enabled {
		t.Error("valid section disabled")
	}
}

func TestInvalidValueIsSectionError(t *testing.T) {
	l := Load(writeFile(t, "fakeA:\n  hours: -3\n"), fakeSections())
	if msg := l.Plan.ConfigErrors["fakeA"]; !strings.Contains(msg, "hours must be") {
		t.Errorf("error %q", msg)
	}
}

func TestDisabledStaysOffWhenInvalid(t *testing.T) {
	l := Load(writeFile(t, "fakeThirdParty:\n  enabled: false\n  hours: \"x\"\n"), fakeSections())
	if len(l.Plan.ConfigErrors) != 0 || len(l.TopErrors) != 0 {
		t.Fatalf("%v %v", l.Plan.ConfigErrors, l.TopErrors)
	}
	if cfgOf(t, l, "fakeThirdParty").Enabled {
		t.Error("still enabled")
	}
}

func TestBrokenFileTurnsThirdPartyOff(t *testing.T) {
	l := Load(writeFile(t, ":::"), fakeSections())
	if len(l.TopErrors) != 1 {
		t.Fatalf("top errors %v", l.TopErrors)
	}
	if cfgOf(t, l, "fakeThirdParty").Enabled {
		t.Error("third-party section on")
	}
	if !cfgOf(t, l, "fakeA").Enabled {
		t.Error("first-party section off")
	}
	if len(l.Plan.ConfigErrors) != 0 {
		t.Errorf("%v", l.Plan.ConfigErrors)
	}
}

func TestUnknownSectionIsError(t *testing.T) {
	l := Load(writeFile(t, "fakeThirdPary:\n  enabled: false\n"), fakeSections())
	if len(l.TopErrors) != 1 || !strings.Contains(l.TopErrors[0], "fakeThirdPary") {
		t.Fatalf("top errors %v", l.TopErrors)
	}
	if cfgOf(t, l, "fakeThirdParty").Enabled {
		t.Error("typo target stays on")
	}
	if !cfgOf(t, l, "fakeA").Enabled {
		t.Error("unrelated section off")
	}
}

func TestUnknownFarSectionLeavesThirdPartyOn(t *testing.T) {
	l := Load(writeFile(t, "zzzzzzzz:\n  enabled: false\n"), fakeSections())
	if len(l.TopErrors) != 1 || !strings.Contains(l.TopErrors[0], "zzzzzzzz") {
		t.Fatalf("top errors %v", l.TopErrors)
	}
	if !cfgOf(t, l, "fakeThirdParty").Enabled {
		t.Error("distant name disabled a section")
	}
}

func TestInvalidWriterKeepsDefaultWriter(t *testing.T) {
	l := Load(writeFile(t, "writer:\n  order: 3\n"), fakeSections())
	if len(l.TopErrors) != 1 || !strings.Contains(l.TopErrors[0], "writer") {
		t.Fatalf("top errors %v", l.TopErrors)
	}
	if l.Writer.Language != "fr" || strings.Join(l.Writer.Order, ",") != strings.Join(specOrder, ",") {
		t.Errorf("%+v", l.Writer)
	}
}

func TestWriterOverride(t *testing.T) {
	l := Load(writeFile(t, "writer:\n  order: [fakeA]\n  notes: car first\n"), fakeSections())
	if len(l.TopErrors) != 0 || l.Writer.Notes != "car first" || len(l.Writer.Order) != 1 || l.Writer.Language != "fr" {
		t.Errorf("%v %+v", l.TopErrors, l.Writer)
	}
}

func TestSetValidatesAndKeepsPrev(t *testing.T) {
	p := writeFile(t, "fakeA:\n  hours: 6\n")
	if err := Set(p, fakeSections(), "fakeA.hours", "12"); err != nil {
		t.Fatal(err)
	}
	if c := cfgOf(t, Load(p, fakeSections()), "fakeA"); c.Hours != 12 {
		t.Errorf("%+v", c)
	}
	prev, err := os.ReadFile(p + ".prev")
	if err != nil || !strings.Contains(string(prev), "hours: 6") {
		t.Errorf("prev %q %v", prev, err)
	}
	before, _ := os.ReadFile(p)
	for _, bad := range [][2]string{{"fakeA.hourz", "1"}, {"fakeA.hours", "abc"}, {"fakeA.hours", "-1"}, {"nosuch.x", "1"}, {"fakeA", "1"}} {
		if err := Set(p, fakeSections(), bad[0], bad[1]); err == nil {
			t.Errorf("Set(%v) accepted", bad)
		}
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Errorf("file changed by rejected sets:\n%s\n%s", before, after)
	}
}

func TestSetCreatesFileWithMode0644(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Set(p, fakeSections(), "fakeA.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("%v %v", st, err)
	}
	if cfgOf(t, Load(p, fakeSections()), "fakeA").Enabled {
		t.Error("not disabled")
	}
}

func TestSetWriterNotes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	for _, note := range []string{"j'ai toujours froid", "a: b", "3"} {
		if err := Set(p, fakeSections(), "writer.notes", note); err != nil {
			t.Fatalf("%q: %v", note, err)
		}
		if got := Load(p, fakeSections()).Writer.Notes; got != note {
			t.Errorf("notes %q, want %q", got, note)
		}
	}
	if err := Set(p, fakeSections(), "writer.order", "[fakeA, fakeThirdParty]"); err != nil {
		t.Fatal(err)
	}
	if o := Load(p, fakeSections()).Writer.Order; len(o) != 2 || o[0] != "fakeA" {
		t.Errorf("order %v", o)
	}
	if err := Set(p, fakeSections(), "writer.order", "[nosuch]"); err == nil {
		t.Error("unknown section in order accepted")
	}
}

func TestSetRefusesBrokenFile(t *testing.T) {
	p := writeFile(t, ":::")
	if err := Set(p, fakeSections(), "fakeA.hours", "1"); err == nil {
		t.Error("Set over a broken file")
	}
	if b, _ := os.ReadFile(p); string(b) != ":::" {
		t.Errorf("file touched: %q", b)
	}
}

func TestUnsetRestoresDefault(t *testing.T) {
	p := writeFile(t, "fakeA:\n  hours: 6\n  name: x\n")
	if err := Unset(p, fakeSections(), "fakeA.hours"); err != nil {
		t.Fatal(err)
	}
	c := cfgOf(t, Load(p, fakeSections()), "fakeA")
	if c.Hours != 24 || c.Name != "x" {
		t.Errorf("%+v", c)
	}
	if err := Unset(p, fakeSections(), "fakeA.name"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); strings.Contains(string(b), "fakeA") {
		t.Errorf("empty section left: %q", b)
	}
	if err := Unset(p, fakeSections(), "fakeA.name"); err == nil {
		t.Error("unset of a key not set")
	}
}

func TestEffectiveShowsMergedConfig(t *testing.T) {
	p := writeFile(t, "fakeA:\n  hours: 6\n")
	b, err := Effective(p, fakeSections())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"hours: 6", "hours: 24", "fakeThirdParty:", "writer:", "language: fr", "ha_url: ha.bbarrois.tech"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
}

// specOrder is the spec's section order, the default writer.order.
var specOrder = []string{"weather", "calendar", "car", "robot", "sensors", "energy", "home", "alerts", "expiries",
	"mail", "github", "deepseek", "bugsink", "pet_feeder", "presence", "tech_radar"}

func TestDefaultWriterOrderIsSpecOrder(t *testing.T) {
	l := Load(filepath.Join(t.TempDir(), "nope.yaml"), fakeSections())
	if strings.Join(l.Writer.Order, ",") != strings.Join(specOrder, ",") {
		t.Errorf("default order %v", l.Writer.Order)
	}
	// a writer block without order keeps the default order, even when the known sections differ
	l = Load(writeFile(t, "writer:\n  notes: car first\n"), fakeSections())
	if len(l.TopErrors) != 0 || l.Writer.Notes != "car first" || strings.Join(l.Writer.Order, ",") != strings.Join(specOrder, ",") {
		t.Errorf("%v %+v", l.TopErrors, l.Writer)
	}
}
