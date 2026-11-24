package brief

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

// Sources are the locations of the data sources, filled from the config (B2).
type Sources struct {
	HAURL, GrafanaURL, PCBURL, PCBCA, IMAPConfig, BugsinkURL, BugsinkHost, VaultDir, CronJobs string
}

// Env is everything a section may use; sections build their own clients from it.
type Env struct {
	Now     time.Time
	Loc     *time.Location
	HTTP    *http.Client
	Dial    func(ctx context.Context, network, addr string) (net.Conn, error) // IMAP, WebSocket
	Secrets *Secrets
	Rec     Recorder
	Sources Sources
	HA      func(ctx context.Context) (*ha.Snapshot, error) // memoised; wired in B4
}
