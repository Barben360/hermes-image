// Package imapsrc reads message headers from an IMAP mailbox, read-only: it
// only ever EXAMINEs a folder and fetches with BODY.PEEK, so no flag changes.
package imapsrc

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Account is the login parsed from himalaya's config.
type Account struct {
	Addr     string // host:port
	Username string
}

// ParseHimalaya reads imap.server and imap.sasl.plain.username of the default
// account (else the only one) from himalaya's TOML. It never reads a password.
func ParseHimalaya(path string) (Account, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Account{}, fmt.Errorf("imap config %s: %w", path, err)
	}
	type acct struct {
		def          bool
		server, user string
	}
	accts := map[string]*acct{}
	var order []string
	get := func(name string) *acct {
		if accts[name] == nil {
			accts[name] = &acct{}
			order = append(order, name)
		}
		return accts[name]
	}
	cur := ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			cur = ""
			h := strings.Trim(line, "[] ")
			if name, ok := strings.CutPrefix(h, "accounts."); ok && !strings.Contains(name, ".") {
				cur = name
				get(cur)
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), tomlString(v)
		name := cur
		if rest, ok := strings.CutPrefix(k, "accounts."); ok { // dotted key at top level
			n, kk, found := strings.Cut(rest, ".")
			if !found {
				continue
			}
			name, k = n, kk
		}
		if name == "" {
			continue
		}
		a := get(name)
		switch k {
		case "default":
			a.def = v == "true"
		case "imap.server":
			a.server = v
		case "imap.sasl.plain.username":
			a.user = v
		}
	}
	var pick *acct
	for _, n := range order {
		if accts[n].def {
			pick = accts[n]
		}
	}
	if pick == nil && len(order) == 1 {
		pick = accts[order[0]]
	}
	if pick == nil {
		return Account{}, fmt.Errorf("imap config %s: no default account", path)
	}
	if pick.server == "" || pick.user == "" {
		return Account{}, fmt.Errorf("imap config %s: imap.server or imap.sasl.plain.username missing", path)
	}
	u, err := url.Parse(pick.server)
	if err != nil || u.Hostname() == "" {
		return Account{}, fmt.Errorf("imap config %s: bad imap.server", path)
	}
	port := u.Port()
	if port == "" {
		port = "143"
	}
	return Account{Addr: net.JoinHostPort(u.Hostname(), port), Username: pick.user}, nil
}

func tomlString(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		if j := strings.IndexByte(v[1:], v[0]); j >= 0 {
			return v[1 : 1+j]
		}
		return v
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// Message is one unread message's headers.
type Message struct {
	Folder      string
	From        string // address, lower case
	FromName    string
	Subject     string
	Date        string
	Received    time.Time // INTERNALDATE
	HasListHdrs bool      // List-Unsubscribe or List-Id present
}

// Dialer opens the TCP connection.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// Unread returns the unread messages of the folders received at or after since.
// Folders are opened with EXAMINE; headers come with BODY.PEEK. Nothing is
// ever written to the mailbox.
func Unread(ctx context.Context, dial Dialer, acct Account, password string, folders []string, since time.Time) ([]Message, error) {
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", acct.Addr)
	if err != nil {
		return nil, fmt.Errorf("imap connect %s: %w", acct.Addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c := imapclient.New(conn, nil)
	defer c.Close()
	// A cancelled context must unblock pending reads.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if err := c.WaitGreeting(); err != nil {
		return nil, fmt.Errorf("imap greeting: %w", ctxErr(ctx, err))
	}
	if err := c.Login(acct.Username, password).Wait(); err != nil {
		return nil, fmt.Errorf("imap login: %w", ctxErr(ctx, err))
	}
	var msgs []Message
	for _, folder := range folders {
		ms, err := unreadIn(c, folder, since)
		if err != nil {
			return nil, fmt.Errorf("imap folder %s: %w", folder, ctxErr(ctx, err))
		}
		msgs = append(msgs, ms...)
	}
	_ = c.Logout().Wait()
	return msgs, nil
}

func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func unreadIn(c *imapclient.Client, folder string, since time.Time) ([]Message, error) {
	if _, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, err
	}
	// SINCE has day granularity in the server's zone: ask a day wide, filter on INTERNALDATE below.
	crit := &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
		Since:   since.UTC().AddDate(0, 0, -1),
	}
	sd, err := c.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, err
	}
	uids := sd.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}
	hdr := &imap.FetchItemBodySection{
		Specifier:    imap.PartSpecifierHeader,
		HeaderFields: []string{"From", "Subject", "Date", "List-Unsubscribe", "List-Id"},
		Peek:         true,
	}
	bufs, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID:          true,
		InternalDate: true,
		BodySection:  []*imap.FetchItemBodySection{hdr},
	}).Collect()
	if err != nil {
		return nil, err
	}
	var out []Message
	for _, b := range bufs {
		if b.InternalDate.Before(since) {
			continue
		}
		m := Message{Folder: folder, Received: b.InternalDate}
		if raw := b.FindBodySection(hdr); raw != nil {
			parseHeaders(raw, &m)
		}
		out = append(out, m)
	}
	return out, nil
}

func parseHeaders(raw []byte, m *Message) {
	msg, err := mail.ReadMessage(bytes.NewReader(append(bytes.TrimRight(raw, "\r\n"), "\r\n\r\n"...)))
	if err != nil {
		return
	}
	dec := new(mime.WordDecoder)
	decode := func(s string) string {
		if d, err := dec.DecodeHeader(s); err == nil {
			return d
		}
		return s
	}
	h := msg.Header
	m.Subject = decode(h.Get("Subject"))
	m.Date = h.Get("Date")
	m.HasListHdrs = h.Get("List-Unsubscribe") != "" || h.Get("List-Id") != ""
	if a, err := mail.ParseAddress(decode(h.Get("From"))); err == nil {
		m.From, m.FromName = strings.ToLower(a.Address), a.Name
	} else {
		m.From = strings.ToLower(strings.TrimSpace(h.Get("From")))
	}
}
