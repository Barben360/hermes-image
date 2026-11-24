package section

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/ha"
)

// haSnapshot returns the memoised Home Assistant snapshot of the run.
func haSnapshot(ctx context.Context, env *brief.Env) (*ha.Snapshot, error) {
	if env.HA == nil {
		return nil, fmt.Errorf("Home Assistant: not configured")
	}
	return env.HA(ctx)
}

// needState returns the entity, or an error naming its id.
func needState(snap *ha.Snapshot, id string) (ha.State, error) {
	st, ok := snap.ByID[id]
	if !ok {
		return ha.State{}, fmt.Errorf("entity %s not found in Home Assistant", id)
	}
	return st, nil
}

func unavailable(s string) bool { return s == "unavailable" || s == "unknown" || s == "" }

// number parses an entity's numeric state; ok is false for unavailable or non-numeric states.
func number(st ha.State) (float64, bool) {
	if unavailable(st.State) {
		return 0, false
	}
	f, err := strconv.ParseFloat(st.State, 64)
	return f, err == nil
}

// reported is when Home Assistant last heard from the entity.
func reported(st ha.State) time.Time {
	if !st.LastReported.IsZero() {
		return st.LastReported
	}
	return st.LastUpdated
}

func trim(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
