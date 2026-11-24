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
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	_ "github.com/Barben360/hermes-image/morning-brief/internal/section"
)

const defaultZone = "Europe/Paris"

func main() { os.Exit(realMain(os.Args[1:], os.Stdout, os.Stderr)) }

func realMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: morning-brief run|sections")
		return 2
	}
	switch args[0] {
	case "run":
		return runCmd(args[1:], stdout, stderr)
	case "sections":
		return sectionsCmd(stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q\n", args[0])
	return 2
}

func runCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	config := fs.String("config", "/opt/data/morning-brief/config.yaml", "owner's overrides (read by the config package, later)")
	secretsDir := fs.String("secrets-dir", "/run/secrets", "directory of credential files")
	only := fs.String("only", "", "comma-separated sections")
	format := fs.String("format", "text", "text or json")
	nowFlag := fs.String("now", "", "RFC3339 time to use instead of the clock")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	_ = config
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "unknown format %q\n", *format)
		return 2
	}
	loc, err := time.LoadLocation(defaultZone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			fmt.Fprintf(stderr, "bad --now: %v\n", err)
			return 2
		}
		now = t.In(loc)
	}
	secrets := brief.NewSecrets(*secretsDir)
	env := &brief.Env{
		Now:     now,
		Loc:     loc,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
		Dial:    (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		Secrets: secrets,
	}
	plan := brief.Plan{}
	if *only != "" {
		plan.Only = strings.Split(*only, ",")
	}
	results := brief.Run(context.Background(), env, plan)
	doc := brief.Doc{Now: now, Writer: brief.WriterConfig{Language: "fr"}, Results: results}
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
