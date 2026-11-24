package section

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/imapsrc"
)

// MailConfig is the mail section's configuration.
type MailConfig struct {
	Enabled           bool     `yaml:"enabled"`
	Hours             int      `yaml:"hours"`
	Folders           []string `yaml:"folders"`
	DropSenders       []string `yaml:"drop_senders"`
	KeepSenders       []string `yaml:"keep_senders"`
	DropIfListHeaders bool     `yaml:"drop_if_list_headers"`
}

func newMailConfig() *MailConfig {
	return &MailConfig{Enabled: true, Hours: 24, Folders: []string{"INBOX"}, DropIfListHeaders: true}
}

// IsEnabled reports whether the section runs.
func (c *MailConfig) IsEnabled() bool { return c.Enabled }

// Validate checks the values.
func (c *MailConfig) Validate() error {
	if c.Hours <= 0 {
		return errors.New("mail.hours must be positive")
	}
	if len(c.Folders) == 0 {
		return errors.New("mail.folders must not be empty")
	}
	return nil
}

func init() {
	brief.Register(brief.Section{
		Name:        "mail",
		Description: "Unread mail of the last hours (sender and subject only, read-only IMAP, filtered)",
		ThirdParty:  true,
		Credentials: []string{"mail_password"},
		Default:     func() brief.SectionConfig { return newMailConfig() },
		Collect: func(ctx context.Context, env *brief.Env, cfg brief.SectionConfig) brief.Result {
			return collectMailSection(ctx, env, cfg.(*MailConfig))
		},
	})
}

func collectMailSection(ctx context.Context, env *brief.Env, c *MailConfig) brief.Result {
	fail := func(err error) brief.Result { return brief.Result{Status: brief.StatusError, Err: err.Error()} }
	acct, err := imapsrc.ParseHimalaya(env.Sources.IMAPConfig)
	if err != nil {
		return fail(err)
	}
	pw, err := env.Secrets.Read("mail_password")
	if err != nil {
		return fail(err)
	}
	now := env.Now
	if now.IsZero() {
		now = time.Now()
	}
	var msgs []imapsrc.Message
	if brief.Replaying(env.Rec) { // IMAP is not HTTP: the headers recorded by --record stand for the server
		if err := env.Rec.Load("mail", &msgs); err != nil {
			return fail(err)
		}
	} else {
		msgs, err = imapsrc.Unread(ctx, imapsrc.Dialer(env.Dial), acct, pw, c.Folders, now.Add(-time.Duration(c.Hours)*time.Hour))
		if err != nil {
			return fail(err)
		}
		if env.Rec != nil {
			_ = env.Rec.Save("mail", msgs)
		}
	}
	var items []brief.Item
	dropped := 0
	for _, m := range msgs {
		if !keepMail(c, m) {
			dropped++
			continue
		}
		from := m.From
		if m.FromName != "" {
			from = m.FromName + " <" + m.From + ">"
		}
		items = append(items, brief.Item{Facts: []brief.Fact{
			brief.Untrusted("from", from),
			brief.Untrusted("subject", m.Subject),
			{Key: "received", Value: m.Received.In(locOrUTC(env)).Format("2006-01-02 15:04")},
		}})
	}
	status := brief.StatusOK
	if len(items) > 0 {
		status = brief.StatusReport
	}
	return brief.Result{
		Status: status,
		Facts: []brief.Fact{{
			Key:   fmt.Sprintf("unread_%dh", c.Hours),
			Value: fmt.Sprintf("%d (after filters; %d dropped)", len(items), dropped),
		}},
		Items: items,
	}
}

func locOrUTC(env *brief.Env) *time.Location {
	if env.Loc != nil {
		return env.Loc
	}
	return time.UTC
}

// keepMail applies keep_senders (wins), drop_senders, then the list-header rule.
func keepMail(c *MailConfig, m imapsrc.Message) bool {
	if senderMatches(c.KeepSenders, m.From) {
		return true
	}
	if senderMatches(c.DropSenders, m.From) {
		return false
	}
	return !(c.DropIfListHeaders && m.HasListHdrs)
}

// senderMatches: an entry with a local part is an exact address; "@domain" or
// a bare "domain" matches that domain and its subdomains.
func senderMatches(list []string, addr string) bool {
	addr = strings.ToLower(addr)
	_, dom, _ := strings.Cut(addr, "@")
	for _, e := range list {
		e = strings.ToLower(strings.TrimSpace(e))
		switch {
		case e == "":
		case strings.HasPrefix(e, "@") || !strings.Contains(e, "@"):
			e = strings.TrimPrefix(e, "@")
			if dom == e || strings.HasSuffix(dom, "."+e) {
				return true
			}
		case e == addr:
			return true
		}
	}
	return false
}
