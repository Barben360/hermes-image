package section

import (
	"context"
	"fmt"
	"path"
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

// entityName is the entity's friendly name, or its id.
func entityName(st ha.State) string {
	if n, ok := st.Attributes["friendly_name"].(string); ok && n != "" {
		return n
	}
	return st.EntityID
}

// stamp is a time as printed in the brief: local date and minutes.
func stamp(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02 15:04") }

// matchAny reports whether id matches one of the path.Match patterns.
func matchAny(patterns []string, id string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, id); ok {
			return true
		}
	}
	return false
}

func inList(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// matchGlob wraps path.Match so a config can be checked for malformed patterns.
func matchGlob(pattern, s string) (bool, error) { return path.Match(pattern, s) }

const maxItems = 20

// capItems keeps the first maxItems items and says how many were left out.
func capItems(res *brief.Result) {
	if n := len(res.Items) - maxItems; n > 0 {
		res.Items = res.Items[:maxItems]
		res.Facts = append(res.Facts, brief.Fact{Key: "more", Value: fmt.Sprintf("+%d more", n)})
	}
}
