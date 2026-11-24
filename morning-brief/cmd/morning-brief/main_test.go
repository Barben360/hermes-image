package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

func TestRunPrintsBriefAndExitsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := realMain([]string{"run", "--secrets-dir", t.TempDir(), "--now", "2026-11-25T07:15:00+01:00"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "# morning-brief 2026-11-25T07:15+01:00 Europe/Paris\n") ||
		!strings.Contains(out.String(), "## summary:") {
		t.Errorf("%s", out.String())
	}
}

func TestSectionsPrintsYAML(t *testing.T) {
	var out, errb bytes.Buffer
	if code := realMain([]string{"sections"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "sections:") {
		t.Errorf("%s", out.String())
	}
}

func TestBadFormatAndCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if realMain([]string{"run", "--format", "xml"}, &out, &errb) == 0 || realMain([]string{"nope"}, &out, &errb) == 0 {
		t.Error("want non-zero")
	}
}

type fakeCfg struct {
	Enabled bool `yaml:"enabled"`
	Hours   int  `yaml:"hours"`
}

func (c *fakeCfg) IsEnabled() bool { return c.Enabled }
func (c *fakeCfg) Validate() error { return nil }

// realSections are the sections internal/section registers, kept for the tests that run them all.
var realSections []brief.Section

// TestMain isolates the registry: real sections registered by internal/section never reach these
// tests, except those putting realSections back (the full replay).
func TestMain(m *testing.M) {
	realSections = brief.Sections()
	restore := brief.UseSectionsForTest(fakeSection())
	code := m.Run()
	restore()
	os.Exit(code)
}

func fakeSection() brief.Section {
	return brief.Section{
		Name: "fakeA", Description: "test section", Credentials: []string{"tok"},
		Default: func() brief.SectionConfig { return &fakeCfg{Enabled: true, Hours: 24} },
		Collect: func(ctx context.Context, env *brief.Env, c brief.SectionConfig) brief.Result {
			return brief.Result{Status: brief.StatusOK, Facts: []brief.Fact{
				{Key: "visible_things", Value: "3"}, {Key: "hidden", Value: "x"}, {Key: "ha", Value: env.Sources.HAURL}}}
		},
	}
}

func TestCheckReportsMissingCredential(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	var out, errb bytes.Buffer
	code := realMain([]string{"check", "--config", cfg, "--secrets-dir", dir}, &out, &errb)
	if code != 1 || !strings.Contains(out.String(), "fakeA: tok not readable") {
		t.Fatalf("exit %d: %q %q", code, out.String(), errb.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "tok"), []byte("s3cr3t-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := realMain([]string{"check", "--config", cfg, "--secrets-dir", dir}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %q", code, out.String())
	}
	if strings.Contains(out.String(), "s3cr3t-value") {
		t.Error("secret printed")
	}
}

func TestCheckSkipsDisabledAndReportsConfigErrors(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	var out, errb bytes.Buffer
	os.WriteFile(cfg, []byte("fakeA:\n  enabled: false\n"), 0o644)
	if code := realMain([]string{"check", "--config", cfg, "--secrets-dir", dir}, &out, &errb); code != 0 {
		t.Fatalf("disabled section checked: %q", out.String())
	}
	os.WriteFile(cfg, []byte("fakeA:\n  hourz: 1\n"), 0o644)
	out.Reset()
	code := realMain([]string{"check", "--config", cfg, "--secrets-dir", dir}, &out, &errb)
	if code != 1 || !strings.Contains(out.String(), "fakeA: config:") || !strings.Contains(out.String(), "hourz") {
		t.Errorf("exit %d: %q", code, out.String())
	}
}

func TestCheckLiveRunsEnabledSections(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tok"), []byte("tok-QQ99"), 0o600)
	var out, errb bytes.Buffer
	code := realMain([]string{"check", "--live", "--config", filepath.Join(dir, "c.yaml"), "--secrets-dir", dir}, &out, &errb)
	s := out.String()
	if code != 0 || !strings.Contains(s, "fakeA: ok") || !strings.Contains(s, "visible_things: 3") || strings.Contains(s, "hidden") {
		t.Errorf("exit %d: %q", code, s)
	}
}

func TestRunUsesConfigAndShowsTopErrors(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfg, []byte("bogus_key: 1\nsources:\n  ha_url: https://ha.example\n"), 0o644)
	var out, errb bytes.Buffer
	code := realMain([]string{"run", "--config", cfg, "--secrets-dir", dir, "--only", "fakeA", "--now", "2026-11-25T07:15:00+01:00"}, &out, &errb)
	s := out.String()
	if code != 0 || !strings.Contains(s, "bogus_key") || !strings.Contains(s, "https://ha.example") {
		t.Errorf("exit %d: %s", code, s)
	}
	os.WriteFile(cfg, []byte("fakeA:\n  hourz: 1\n"), 0o644)
	out.Reset()
	code = realMain([]string{"run", "--config", cfg, "--secrets-dir", dir, "--only", "fakeA"}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "config:") || !strings.Contains(out.String(), "hourz") {
		t.Errorf("exit %d: %s", code, out.String())
	}
}

func TestConfigSetShowUnset(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := realMain(append([]string{"config"}, append(args, "--config", cfg)...), &out, &errb)
		return code, out.String(), errb.String()
	}
	if code, _, e := run("set", "writer.notes", "j'ai toujours froid"); code != 0 {
		t.Fatalf("set: %s", e)
	}
	if code, o, _ := run("show"); code != 0 || !strings.Contains(o, "j'ai toujours froid") {
		t.Errorf("show: %d %q", code, o)
	}
	if code, o, _ := run("show", "--effective"); code != 0 || !strings.Contains(o, "language: fr") || !strings.Contains(o, "fakeA:") {
		t.Errorf("effective: %d %q", code, o)
	}
	if code, _, e := run("set", "fakeA.hourz", "1"); code == 0 || !strings.Contains(e, "hourz") {
		t.Errorf("bad set: %d %q", code, e)
	}
	if code, _, e := run("unset", "writer.notes"); code != 0 {
		t.Fatalf("unset: %s", e)
	}
	if code, o, _ := run("show"); code != 0 || strings.Contains(o, "froid") {
		t.Errorf("after unset: %q", o)
	}
}

// The syntax the skill documents works verbatim, on the real sections and on a fresh volume (no directory yet).
func TestSkillConfigExamplesWork(t *testing.T) {
	defer brief.UseSectionsForTest(realSections...)()
	skill, err := os.ReadFile("../../../skills/morning-brief/SKILL.md")
	if os.IsNotExist(err) {
		t.Skip("SKILL.md is outside the Docker build context of morning-brief/; the checkout's go test runs this")
	}
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(string(skill)), " ")
	cfg := filepath.Join(t.TempDir(), "fresh", "morning-brief", "config.yaml")
	run := func(args ...string) (int, string) {
		var out, errb bytes.Buffer
		code := realMain(append([]string{"config"}, append(args, "--config", cfg)...), &out, &errb)
		return code, errb.String()
	}
	// each example, as the shell would split it, and the text it must have in the skill
	examples := []struct {
		doc  string
		args []string
	}{
		{"`morning-brief config set mail.enabled false`", []string{"set", "mail.enabled", "false"}},
		{"`morning-brief config set expiries.warn_within_days 14`", []string{"set", "expiries.warn_within_days", "14"}},
		{`morning-brief config set writer.notes "car first, I'm always cold"`, []string{"set", "writer.notes", "car first, I'm always cold"}},
		{`morning-brief config set mail.drop_senders='[news@example.org, "@promo.example.com"]'`,
			[]string{"set", `mail.drop_senders=[news@example.org, "@promo.example.com"]`}},
		{"`morning-brief config set car.battery=sensor.a car.range=sensor.b car.plug=sensor.c car.charging=binary_sensor.d car.target=number.e car.tracker=device_tracker.f`",
			[]string{"set", "car.battery=sensor.a", "car.range=sensor.b", "car.plug=sensor.c", "car.charging=binary_sensor.d", "car.target=number.e", "car.tracker=device_tracker.f"}},
	}
	for _, ex := range examples {
		if !strings.Contains(flat, ex.doc) {
			t.Errorf("SKILL.md no longer documents %s", ex.doc)
		}
		if code, e := run(ex.args...); code != 0 {
			t.Errorf("%v: exit %d %s", ex.args, code, e)
		}
	}
	var out, errb bytes.Buffer
	realMain([]string{"config", "show", "--config", cfg}, &out, &errb)
	for _, want := range []string{"enabled: false", "warn_within_days: 14", "car first", "news@example.org", "battery: sensor.a"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("config show lacks %q:\n%s", want, out.String())
		}
	}
}

func TestMultiKeyCarSetWhereSingleKeyFails(t *testing.T) {
	defer brief.UseSectionsForTest(realSections...)()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	var out, errb bytes.Buffer
	if code := realMain([]string{"config", "set", "car.battery", "sensor.a", "--config", cfg}, &out, &errb); code == 0 || !strings.Contains(errb.String(), "car.range=<id>") {
		t.Errorf("single key: %d %q (want a refusal showing the multi-key form)", code, errb.String())
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("a refused set wrote the file")
	}
	out.Reset()
	errb.Reset()
	if code := realMain([]string{"config", "set", "car.battery=sensor.a", "car.range=sensor.b", "--config", cfg}, &out, &errb); code == 0 {
		t.Error("incomplete multi set accepted")
	}
	if code := realMain([]string{"config", "set", "car.battery=sensor.a", "car.range=sensor.b", "car.plug=sensor.c",
		"car.charging=binary_sensor.d", "car.target=number.e", "car.tracker=device_tracker.f", "--config", cfg}, &out, &errb); code != 0 {
		t.Errorf("full multi set: %d %s", code, errb.String())
	}
}
