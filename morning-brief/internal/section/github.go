package section

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/github"
)

// GitHubConfig configures the github section.
type GitHubConfig struct {
	Enabled          bool     `yaml:"enabled"`
	Owner            string   `yaml:"owner"`
	Hours            int      `yaml:"hours"`
	IncludeOwnLabels []string `yaml:"include_own_labels"`
	ExcludeRepos     []string `yaml:"exclude_repos"`
	Renovate         bool     `yaml:"renovate"`
	APIURL           string   `yaml:"api_url"`
}

func githubDefault() *GitHubConfig {
	return &GitHubConfig{Enabled: true, Owner: "Barben360", Hours: 24, IncludeOwnLabels: []string{"hermes"},
		ExcludeRepos: []string{}, Renovate: true, APIURL: "https://api.github.com"}
}

func (c *GitHubConfig) IsEnabled() bool { return c.Enabled }

func (c *GitHubConfig) Validate() error {
	if strings.TrimSpace(c.Owner) == "" {
		return fmt.Errorf("set github.owner (morning-brief config set github.owner <login>)")
	}
	if c.Hours <= 0 {
		return fmt.Errorf("github.hours must be positive")
	}
	if strings.TrimSpace(c.APIURL) == "" {
		return fmt.Errorf("set github.api_url (morning-brief config set github.api_url https://api.github.com)")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "github",
		Description: "New issues on the owner's repositories, open PRs from Hermes and Renovate",
		ThirdParty:  true,
		Credentials: []string{"github_issues_token"},
		Default:     func() brief.SectionConfig { return githubDefault() },
		Collect:     collectGitHub,
	})
}

func inList(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func collectGitHub(ctx context.Context, env *brief.Env, sc brief.SectionConfig) brief.Result {
	cfg := sc.(*GitHubConfig)
	res := brief.Result{Name: "github"}
	fail := func(err error) brief.Result {
		res.Status, res.Err = brief.StatusError, err.Error()
		return res
	}
	tok, err := env.Secrets.Read("github_issues_token")
	if err != nil {
		return fail(err)
	}
	c := &github.Client{BaseURL: cfg.APIURL, Token: tok, HTTP: env.HTTP}
	cutoff := env.Now.Add(-time.Duration(cfg.Hours) * time.Hour).UTC()

	excluded := func(repo string) bool {
		if inList(cfg.ExcludeRepos, repo) {
			return true
		}
		if i := strings.IndexByte(repo, '/'); i >= 0 {
			return inList(cfg.ExcludeRepos, repo[i+1:])
		}
		return false
	}
	item := func(kind string, i github.Issue) brief.Item {
		return brief.Item{Facts: []brief.Fact{
			{Key: "kind", Value: kind},
			{Key: "repo", Value: i.Repo()},
			{Key: "number", Value: strconv.Itoa(i.Number)},
			brief.Untrusted("title", i.Title),
			brief.Untrusted("author", i.User.Login),
			{Key: "url", Value: i.HTMLURL},
		}}
	}

	incomplete := false
	issues, inc, err := c.Search(ctx, fmt.Sprintf("is:issue user:%s created:>=%s", cfg.Owner, cutoff.Format(time.RFC3339)))
	if err != nil {
		return fail(err)
	}
	incomplete = incomplete || inc
	for _, i := range issues {
		created, perr := time.Parse(time.RFC3339, i.CreatedAt)
		if perr != nil {
			return fail(fmt.Errorf("github: unreadable created_at on an issue"))
		}
		if created.Before(cutoff) || excluded(i.Repo()) {
			continue
		}
		if strings.EqualFold(i.User.Login, cfg.Owner) {
			keep := false
			for _, l := range i.Labels {
				if inList(cfg.IncludeOwnLabels, l.Name) {
					keep = true
				}
			}
			if !keep {
				continue
			}
		}
		res.Items = append(res.Items, item("issue", i))
	}

	prQueries := []string{fmt.Sprintf("is:pr is:open user:%s label:hermes", cfg.Owner)}
	if cfg.Renovate {
		prQueries = append(prQueries, fmt.Sprintf("is:pr is:open user:%s author:app/renovate", cfg.Owner))
	}
	seen := map[string]bool{}
	for _, q := range prQueries {
		prs, inc, err := c.Search(ctx, q)
		if err != nil {
			return fail(err)
		}
		incomplete = incomplete || inc
		for _, p := range prs {
			if excluded(p.Repo()) || seen[p.HTMLURL] {
				continue
			}
			seen[p.HTMLURL] = true
			res.Items = append(res.Items, item("pr", p))
		}
	}

	// the number of repositories the token sees: how `check --live` shows a token that lost its scope
	if n, err := c.VisibleRepos(ctx); err != nil {
		res.Facts = append(res.Facts, brief.Fact{Key: "visible_repos", Value: "unavailable"})
	} else {
		res.Facts = append(res.Facts, brief.Fact{Key: "visible_repos", Value: strconv.Itoa(n)})
	}
	res.Status = brief.StatusOK
	if incomplete {
		res.Facts = append(res.Facts, brief.Fact{Key: "warning", Value: "GitHub search results incomplete (timed out); some issues may be missing"})
		res.Status = brief.StatusReport
	}
	if len(res.Items) > 0 {
		res.Status = brief.StatusReport
	}
	return res
}
