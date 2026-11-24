// Command morning-brief collects the data of the daily brief and prints it for the agent.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/config"
	_ "github.com/Barben360/hermes-image/morning-brief/internal/section"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

const (
	defaultZone   = "Europe/Paris"
	defaultConfig = "/opt/data/morning-brief/config.yaml"
)

func main() { os.Exit(realMain(os.Args[1:], os.Stdout, os.Stderr)) }

func realMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: morning-brief run|check|sections|config|watch")
		return 2
	}
	switch args[0] {
	case "run":
		return runCmd(args[1:], stdout, stderr)
	case "check":
		return checkCmd(args[1:], stdout, stderr)
	case "config":
		return configCmd(args[1:], stdout, stderr)
	case "sections":
		return sectionsCmd(stdout, stderr)
	case "watch":
		return watchCmd(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q\n", args[0])
	return 2
}

// common holds the flags every command reading the config shares.
type common struct {
	config, secretsDir string
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.config, "config", defaultConfig, "owner's overrides")
	fs.StringVar(&c.secretsDir, "secrets-dir", "/run/secrets", "directory of credential files")
}

// parseInterleaved parses flags wherever they stand among the positional arguments.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func newEnv(now time.Time, loc *time.Location, secrets *brief.Secrets, src brief.Sources) *brief.Env {
	env := &brief.Env{
		Now:     now,
		Loc:     loc,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
		Dial:    (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		Secrets: secrets,
		Sources: src,
	}
	env.HA = memoHA(env)
	return env
}

// memoHA fetches Home Assistant's states once per run, however many sections ask (concurrently or not).
func memoHA(env *brief.Env) func(context.Context) (*ha.Snapshot, error) {
	var (
		once sync.Once
		snap *ha.Snapshot
		err  error
	)
	return func(ctx context.Context) (*ha.Snapshot, error) {
		once.Do(func() {
			var token string
			if token, err = env.Secrets.Read("ha_token"); err != nil {
				return
			}
			c := &ha.Client{BaseURL: env.Sources.HAURL, Token: token, HTTP: env.HTTP, Rec: env.Rec}
			snap, err = c.States(ctx)
		})
		return snap, err
	}
}

// useRecorder routes the HTTP client through rec; sources that save their own answers use env.Rec.
func useRecorder(env *brief.Env, rec brief.Recorder) {
	env.Rec = rec
	hc := *env.HTTP
	hc.Transport = brief.NewRecordingTransport(hc.Transport, rec)
	env.HTTP = &hc
	env.HA = memoHA(env)
}

func location() *time.Location {
	loc, err := time.LoadLocation(defaultZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func runCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cm common
	cm.register(fs)
	only := fs.String("only", "", "comma-separated sections")
	format := fs.String("format", "text", "text or json")
	nowFlag := fs.String("now", "", "RFC3339 time to use instead of the clock")
	recordDir := fs.String("record", "", "save every source answer, scrubbed, into this directory")
	replayDir := fs.String("replay", "", "answer from a --record directory, no network")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *recordDir != "" && *replayDir != "" {
		fmt.Fprintln(stderr, "--record and --replay are exclusive")
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	loc := location()
	now := time.Now().In(loc)
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			fmt.Fprintf(stderr, "bad --now: %v\n", err)
			return 2
		}
		now = t.In(loc)
	}
	loaded := config.Load(cm.config, brief.Sections())
	secrets := brief.NewSecrets(cm.secretsDir)
	env := newEnv(now, loc, secrets, loaded.Sources)
	switch {
	case *recordDir != "":
		useRecorder(env, brief.NewDirRecorder(*recordDir, secrets, false))
	case *replayDir != "":
		useRecorder(env, brief.NewDirRecorder(*replayDir, secrets, true))
	}
	plan := loaded.Plan
	if *only != "" {
		plan.Only = strings.Split(*only, ",")
	}
	results := brief.Run(context.Background(), env, plan)
	doc := brief.Doc{Now: now, Writer: loaded.Writer, TopErrors: loaded.TopErrors, Results: results}
	render := brief.RenderText
	if *format == "json" {
		render = brief.RenderJSON
	}
	if err := render(stdout, doc, secrets); err != nil {
		fmt.Fprintf(stderr, "render: %v\n", err)
		return 1
	}
	return 0
}

// checkCmd validates the effective config and the credential files; --live also calls each enabled source.
func checkCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cm common
	cm.register(fs)
	live := fs.Bool("live", false, "also call each enabled source once")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sections := brief.Sections()
	loaded := config.Load(cm.config, sections)
	secrets := brief.NewSecrets(cm.secretsDir)
	problems := 0
	for _, e := range loaded.TopErrors {
		fmt.Fprintf(stdout, "config: %s\n", secrets.Scrub(e))
		problems++
	}
	for _, s := range sections {
		if msg, bad := loaded.Plan.ConfigErrors[s.Name]; bad {
			fmt.Fprintf(stdout, "%s: config: %s\n", s.Name, secrets.Scrub(msg))
			problems++
			continue
		}
		cfg, ok := loaded.Plan.Configs[s.Name]
		if !ok || !cfg.IsEnabled() {
			continue
		}
		for _, c := range s.Credentials {
			if _, err := secrets.Read(c); err != nil {
				fmt.Fprintf(stdout, "%s: %s not readable\n", s.Name, c)
				problems++
			}
		}
	}
	if *live {
		loc := location()
		env := newEnv(time.Now().In(loc), loc, secrets, loaded.Sources)
		for _, r := range brief.Run(context.Background(), env, loaded.Plan) {
			if r.Status == brief.StatusDisabled {
				continue
			}
			line := fmt.Sprintf("%s: %s", r.Name, r.Status)
			if r.Err != "" {
				line += ": " + r.Err
				problems++
			}
			fmt.Fprintln(stdout, secrets.Scrub(line))
			for _, f := range r.Facts {
				if strings.HasPrefix(f.Key, "visible_") {
					fmt.Fprintln(stdout, secrets.Scrub(fmt.Sprintf("  %s: %s", f.Key, f.Value)))
				}
			}
		}
	}
	if problems > 0 {
		return 1
	}
	fmt.Fprintln(stdout, "check: ok")
	return 0
}

// configCmd is how the owner (through Jerry) reads and changes the config.
func configCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cm common
	cm.register(fs)
	effective := fs.Bool("effective", false, "show the merged config instead of the owner's file")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return 2
	}
	usage := func() int {
		fmt.Fprintln(stderr, "usage: morning-brief config show [--effective] | set <section>.<key> <value> | unset <section>.<key>")
		return 2
	}
	if len(pos) == 0 {
		return usage()
	}
	sections := brief.Sections()
	fail := func(err error) int {
		fmt.Fprintf(stderr, "config %s: %v\n", pos[0], err)
		return 1
	}
	switch {
	case pos[0] == "show" && len(pos) == 1:
		if *effective {
			b, err := config.Effective(cm.config, sections)
			if err != nil {
				return fail(err)
			}
			stdout.Write(b)
			return 0
		}
		b, err := os.ReadFile(cm.config)
		if err != nil && !os.IsNotExist(err) {
			return fail(err)
		}
		if os.IsNotExist(err) {
			fmt.Fprintln(stdout, "# no overrides: every default applies")
			return 0
		}
		stdout.Write(b)
		return 0
	case pos[0] == "set" && len(pos) == 3:
		if err := config.Set(cm.config, sections, pos[1], pos[2]); err != nil {
			return fail(err)
		}
		return 0
	case pos[0] == "unset" && len(pos) == 2:
		if err := config.Unset(cm.config, sections, pos[1]); err != nil {
			return fail(err)
		}
		return 0
	}
	return usage()
}

func sectionsCmd(stdout, stderr io.Writer) int {
	type entry struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		ThirdParty  bool     `yaml:"third_party"`
		Credentials []string `yaml:"credentials"`
		Defaults    any      `yaml:"defaults"`
	}
	var out []entry
	for _, s := range brief.Sections() {
		e := entry{Name: s.Name, Description: s.Description, ThirdParty: s.ThirdParty, Credentials: s.Credentials}
		if s.Default != nil {
			e.Defaults = s.Default()
		}
		out = append(out, e)
	}
	b, err := yaml.Marshal(map[string]any{"sections": out})
	if err != nil {
		fmt.Fprintf(stderr, "sections: %v\n", err)
		return 1
	}
	_, err = stdout.Write(b)
	if err != nil {
		return 1
	}
	return 0
}
