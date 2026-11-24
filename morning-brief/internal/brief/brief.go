// Package brief holds the core of morning-brief: section types, registry, runner, renderer, secret scrubber.
package brief

import (
	"context"
	"sync"
	"time"
)

// Status of one section's result.
type Status string

const (
	StatusOK       Status = "ok"
	StatusReport   Status = "report"
	StatusError    Status = "error"
	StatusDisabled Status = "disabled"
)

// Fact is one printed value. Untrusted values are third-party text, printed JSON-quoted.
type Fact struct {
	Key, Value string
	AsOf       time.Time
	Untrusted  bool
}

// Item is one entry of a list (an event, a mail, an issue).
type Item struct{ Facts []Fact }

// Result is what a section returns.
type Result struct {
	Name   string
	Status Status
	Facts  []Fact
	Items  []Item
	Err    string
}

// SectionConfig is a section's validated configuration.
type SectionConfig interface {
	IsEnabled() bool
	Validate() error
}

// Section describes one section and how to collect it.
type Section struct {
	Name, Description string
	ThirdParty        bool     // mail, github, calendar, bugsink
	Credentials       []string // secret file names it needs
	Default           func() SectionConfig
	Collect           func(ctx context.Context, env *Env, cfg SectionConfig) Result
}

var (
	regMu    sync.Mutex
	registry []Section
)

// Register adds a section; call it from an init(). Registration order is output order.
func Register(s Section) {
	regMu.Lock()
	defer regMu.Unlock()
	registry = append(registry, s)
}

// Sections returns the registered sections in registration order.
func Sections() []Section {
	regMu.Lock()
	defer regMu.Unlock()
	return append([]Section(nil), registry...)
}

// UseSectionsForTest replaces the registry with ss (copied) and returns a function that puts the
// previous contents back. For tests only: it keeps sections registered by init() of imported
// packages out of a test's view.
func UseSectionsForTest(ss ...Section) (restore func()) {
	regMu.Lock()
	defer regMu.Unlock()
	prev := registry
	registry = append([]Section(nil), ss...)
	return func() {
		regMu.Lock()
		defer regMu.Unlock()
		registry = prev
	}
}
