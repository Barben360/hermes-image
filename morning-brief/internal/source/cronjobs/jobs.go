// Package cronjobs reads Hermes's scheduler file ($HERMES_HOME/cron/jobs.json), read-only.
package cronjobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Job is the part of a Hermes cron job the watcher needs; unknown fields are ignored.
type Job struct {
	ID                     string          `json:"id"`
	Name                   string          `json:"name"`
	Enabled                bool            `json:"enabled"`
	State                  string          `json:"state"`
	LastRunAt              *string         `json:"last_run_at"`
	LastStatus             *string         `json:"last_status"`
	LastError              *string         `json:"last_error"`
	LastDeliveryError      *string         `json:"last_delivery_error"`
	LastDeliveryUnverified json.RawMessage `json:"last_delivery_unverified"`
	FailureStreak          int             `json:"failure_streak"`
}

// Unverified reports whether Hermes flagged the last delivery as unconfirmed (anything but absent, null or false).
func (j Job) Unverified() bool {
	switch string(j.LastDeliveryUnverified) {
	case "", "null", "false", "0", `""`:
		return false
	}
	return true
}

// Load parses the file. A file that is not {"jobs":[...]} is an error, never an empty list.
func Load(path string) ([]Job, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("file not found")
		}
		return nil, fmt.Errorf("file not readable")
	}
	var f struct {
		Jobs *[]Job `json:"jobs"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("not the expected shape (%v)", errShort(err))
	}
	if f.Jobs == nil {
		return nil, fmt.Errorf("not the expected shape (no jobs list)")
	}
	return *f.Jobs, nil
}

func errShort(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return "field " + te.Field + " has the wrong type"
	}
	return "invalid JSON"
}
