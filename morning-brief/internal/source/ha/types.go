// Package ha holds the Home Assistant REST client and the types sections share.
package ha

import "time"

// State is one entity as returned by GET /api/states.
type State struct {
	EntityID     string
	State        string
	Attributes   map[string]any
	LastChanged  time.Time
	LastUpdated  time.Time
	LastReported time.Time
}

// Snapshot is every state at one instant.
type Snapshot struct {
	At   time.Time
	ByID map[string]State
}
