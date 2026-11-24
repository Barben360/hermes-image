// Package ha holds Home Assistant types shared by sections; the client is in client.go (later task).
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
