// Package config loads morning-brief's configuration: defaults embedded in the binary, overridden by the
// owner's file, validated section by section.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

//go:embed defaults.yaml
var defaultsYAML []byte

// Sources is where the data sources live.
type Sources = brief.Sources

// Loaded is a ready configuration. Load never fails: problems become TopErrors or per-section ConfigErrors.
type Loaded struct {
	Plan      brief.Plan
	Writer    brief.WriterConfig
	Sources   Sources
	TopErrors []string
}

type writerYAML struct {
	Language string   `yaml:"language"`
	Order    []string `yaml:"order"`
	Notes    string   `yaml:"notes"`
}

type sourcesYAML struct {
	HAURL       string `yaml:"ha_url"`
	GrafanaURL  string `yaml:"grafana_url"`
	PCBURL      string `yaml:"pcb_url"`
	PCBCA       string `yaml:"pcb_ca"`
	IMAPConfig  string `yaml:"imap_config"`
	BugsinkURL  string `yaml:"bugsink_url"`
	BugsinkHost string `yaml:"bugsink_host"`
	VaultDir    string `yaml:"vault_dir"`
	CronJobs    string `yaml:"cron_jobs"`
}

func (s sourcesYAML) toSources() Sources {
	return Sources{HAURL: s.HAURL, GrafanaURL: s.GrafanaURL, PCBURL: s.PCBURL, PCBCA: s.PCBCA, IMAPConfig: s.IMAPConfig,
		BugsinkURL: s.BugsinkURL, BugsinkHost: s.BugsinkHost, VaultDir: s.VaultDir, CronJobs: s.CronJobs}
}

type defaults struct {
	Writer  writerYAML  `yaml:"writer"`
	Sources sourcesYAML `yaml:"sources"`
}

func embeddedDefaults() defaults {
	var d defaults
	if err := yaml.Unmarshal(defaultsYAML, &d); err != nil {
		panic("config: embedded defaults.yaml: " + err.Error()) // a build-time bug, covered by tests
	}
	return d
}

// result is Load's full outcome, including what Set needs to judge a change.
type result struct {
	Loaded
	writer       writerYAML
	sources      sourcesYAML
	writerErr    string
	sourcesErr   string
	configs      map[string]brief.SectionConfig // every section, default when its own config is invalid
	explicitKeys map[string]bool
}

// Load reads path and merges it over the embedded defaults. A missing file is the defaults.
func Load(path string, sections []brief.Section) Loaded {
	return load(path, sections).Loaded
}

func load(path string, sections []brief.Section) result {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return loadMap(nil, sections, fmt.Sprintf("config file %s unreadable: %v", path, oneLine(err.Error())))
	}
	return loadBytes(b, path, sections)
}

func loadBytes(b []byte, path string, sections []brief.Section) result {
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(b, &top); err != nil {
		return loadMap(nil, sections, fmt.Sprintf("config file %s is not valid YAML, defaults used and third-party sections off: %s", path, oneLine(err.Error())))
	}
	for k := range top {
		if !identRe.MatchString(k) {
			return loadMap(nil, sections, fmt.Sprintf("config file %s is not a morning-brief config (key %q is not a name), defaults used and third-party sections off", path, k))
		}
	}
	return loadMap(top, sections, "")
}

// identRe is what a section, writer or sources key looks like; anything else means the file is not a config.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// loadMap builds the configuration. broken, when set, is the top error of an unreadable file.
func loadMap(top map[string]yaml.Node, sections []brief.Section, broken string) result {
	d := embeddedDefaults()
	r := result{
		writer:       d.Writer,
		sources:      d.Sources,
		configs:      map[string]brief.SectionConfig{},
		explicitKeys: map[string]bool{},
	}
	r.Plan.Configs = map[string]brief.SectionConfig{}
	r.Plan.ConfigErrors = map[string]string{}
	if broken != "" {
		r.TopErrors = append(r.TopErrors, broken)
	}

	known := map[string]brief.Section{}
	for _, s := range sections {
		known[s.Name] = s
	}
	forcedOff := map[string]bool{}
	if broken != "" {
		for _, s := range sections {
			if s.ThirdParty {
				forcedOff[s.Name] = true
			}
		}
	}

	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r.explicitKeys[k] = true
		if k == "writer" || k == "sources" {
			continue
		}
		if _, ok := known[k]; ok {
			continue
		}
		msg := fmt.Sprintf("unknown top-level key %q in config (not a section, writer or sources)", k)
		for _, s := range sections {
			if s.ThirdParty && editDistance(k, s.Name) <= 2 {
				forcedOff[s.Name] = true
				msg += fmt.Sprintf("; %s is off until fixed", s.Name)
			}
		}
		r.TopErrors = append(r.TopErrors, msg)
	}

	if n, ok := top["writer"]; ok {
		w := d.Writer
		if err := strictDecode(n, &w); err != nil {
			r.writerErr = oneLine(err.Error())
		} else if err := checkWriter(w, sections); err != nil {
			r.writerErr = err.Error()
		} else {
			r.writer = w
		}
		if r.writerErr != "" {
			r.TopErrors = append(r.TopErrors, "config writer ignored, default writer used: "+r.writerErr)
		}
	}
	if n, ok := top["sources"]; ok {
		s := d.Sources
		if err := strictDecode(n, &s); err != nil {
			r.sourcesErr = oneLine(err.Error())
			r.TopErrors = append(r.TopErrors, "config sources ignored, default sources used: "+r.sourcesErr)
		} else {
			r.sources = s
		}
	}

	for _, s := range sections {
		cfg, errMsg := sectionConfig(s, top, forcedOff[s.Name])
		r.configs[s.Name] = cfg
		if errMsg != "" {
			r.Plan.ConfigErrors[s.Name] = errMsg
			continue
		}
		r.Plan.Configs[s.Name] = cfg
	}
	r.Writer = brief.WriterConfig{Language: r.writer.Language, Order: r.writer.Order, Notes: r.writer.Notes}
	r.Sources = r.sources.toSources()
	return r
}

func checkWriter(w writerYAML, sections []brief.Section) error {
	if strings.TrimSpace(w.Language) == "" {
		return errors.New("language must not be empty")
	}
	have := map[string]bool{}
	for _, s := range sections {
		have[s.Name] = true
	}
	for _, n := range w.Order {
		if !have[n] {
			return fmt.Errorf("order names unknown section %q", n)
		}
	}
	return nil
}

// sectionConfig returns the section's config and, when it is invalid, the error (config is then its default).
func sectionConfig(s brief.Section, top map[string]yaml.Node, forceOff bool) (brief.SectionConfig, string) {
	cfg, target := newConfig(s)
	if cfg == nil {
		return nil, ""
	}
	n, has := top[s.Name]
	if has && disabledInFile(n) {
		forceOff = true
	}
	if forceOff {
		setDisabled(target)
		return cfg, ""
	}
	if has {
		if err := strictDecode(n, target); err != nil {
			cfg, _ = newConfig(s)
			return cfg, oneLine(err.Error())
		}
	}
	if cfg.IsEnabled() {
		if err := cfg.Validate(); err != nil {
			cfg, _ = newConfig(s)
			return cfg, oneLine(err.Error())
		}
	}
	return cfg, ""
}

// newConfig returns a fresh default and a pointer to decode into.
func newConfig(s brief.Section) (brief.SectionConfig, any) {
	if s.Default == nil {
		return nil, nil
	}
	c := s.Default()
	if c == nil {
		return nil, nil
	}
	v := reflect.ValueOf(c)
	if v.Kind() == reflect.Ptr {
		return c, c
	}
	p := reflect.New(v.Type())
	p.Elem().Set(v)
	return p.Interface().(brief.SectionConfig), p.Interface()
}

// disabledInFile reports whether the section's node says `enabled: false`, whatever its other keys hold.
func disabledInFile(n yaml.Node) bool {
	var m map[string]any
	if err := n.Decode(&m); err != nil {
		return false
	}
	v, ok := m["enabled"].(bool)
	return ok && !v
}

func setDisabled(target any) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("enabled: false"), &n); err == nil {
		_ = strictDecode(*n.Content[0], target)
	}
}

func strictDecode(n yaml.Node, into any) error {
	b, err := yaml.Marshal(&n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func oneLine(s string) string {
	s = strings.TrimPrefix(s, "yaml: unmarshal errors:")
	parts := strings.Split(s, "\n")
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// Effective renders the merged configuration (defaults and overrides) with the problems found as comments.
func Effective(path string, sections []brief.Section) ([]byte, error) {
	r := load(path, sections)
	var buf bytes.Buffer
	for _, e := range r.TopErrors {
		fmt.Fprintf(&buf, "# error: %s\n", e)
	}
	names := make([]string, 0, len(r.Plan.ConfigErrors))
	for n := range r.Plan.ConfigErrors {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&buf, "# error: %s: %s (not run until fixed; its defaults are shown)\n", n, r.Plan.ConfigErrors[n])
	}
	doc := map[string]any{"writer": r.writer, "sources": r.sources}
	for _, s := range sections {
		if c := r.configs[s.Name]; c != nil {
			doc[s.Name] = c
		}
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	buf.Write(b)
	return buf.Bytes(), nil
}
