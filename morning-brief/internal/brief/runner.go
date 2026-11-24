package brief

import (
	"context"
	"fmt"
	"time"
)

const (
	DefaultSectionTimeout = 15 * time.Second
	DefaultRunTimeout     = 90 * time.Second
)

// Plan says what to run. A section without an entry in Configs uses its Default.
type Plan struct {
	Configs        map[string]SectionConfig
	ConfigErrors   map[string]string
	Only           []string      // empty = every section
	SectionTimeout time.Duration // 0 = DefaultSectionTimeout
	RunTimeout     time.Duration // 0 = DefaultRunTimeout
}

// Run collects every selected section in parallel and returns results in registration order.
// A section in ConfigErrors is not run; a disabled one opens no connection.
func Run(ctx context.Context, env *Env, p Plan) []Result {
	secTO, runTO := p.SectionTimeout, p.RunTimeout
	if secTO <= 0 {
		secTO = DefaultSectionTimeout
	}
	if runTO <= 0 {
		runTO = DefaultRunTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, runTO)
	defer cancel()

	only := map[string]bool{}
	for _, n := range p.Only {
		only[n] = true
	}
	var secs []Section
	for _, s := range Sections() {
		if len(only) == 0 || only[s.Name] {
			secs = append(secs, s)
		}
	}

	results := make([]Result, len(secs))
	chans := make([]chan Result, len(secs))
	// one hard deadline for every section, taken at launch: waits never stack
	hard, hardCancel := context.WithTimeout(ctx, secTO+250*time.Millisecond)
	defer hardCancel()
	for i, s := range secs {
		if msg, bad := p.ConfigErrors[s.Name]; bad {
			results[i] = Result{Name: s.Name, Status: StatusError, Err: "config: " + msg}
			continue
		}
		cfg, enabled, err := resolve(s, p)
		if err != nil {
			results[i] = Result{Name: s.Name, Status: StatusError, Err: err.Error()}
			continue
		}
		if !enabled {
			results[i] = Result{Name: s.Name, Status: StatusDisabled}
			continue
		}
		ch := make(chan Result, 1) // buffered: a section ignoring ctx never blocks on send
		chans[i] = ch
		go func(s Section, cfg SectionConfig) {
			sctx, scancel := context.WithTimeout(WithSection(ctx, s.Name), secTO)
			defer scancel()
			ch <- collectSafe(sctx, env, s, cfg)
		}(s, cfg)
	}
	for i, ch := range chans {
		if ch == nil {
			continue
		}
		select {
		case r := <-ch:
			results[i] = r
		case <-hard.Done():
			// deadline passed: take a result already there, else timeout
			select {
			case r := <-ch:
				results[i] = r
			default:
				msg := "timeout after " + secTO.String()
				if ctx.Err() != nil {
					msg = "timeout: run exceeded " + runTO.String()
				}
				results[i] = Result{Name: secs[i].Name, Status: StatusError, Err: msg}
			}
		}
	}
	for i := range results {
		results[i].Name = secs[i].Name
		if results[i].Status == "" {
			results[i].Status = StatusError
			results[i].Err = "section returned no status"
		}
		if results[i].Err != "" {
			results[i].Err = env.Secrets.Scrub(results[i].Err)
		}
	}
	return results
}

// resolve picks a section's config and enabled state, recovering a panic in user code.
func resolve(s Section, p Plan) (cfg SectionConfig, enabled bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			cfg, enabled, err = nil, false, fmt.Errorf("panic: %v", r)
		}
	}()
	cfg, ok := p.Configs[s.Name]
	if !ok && s.Default != nil {
		cfg = s.Default()
	}
	if cfg == nil {
		return nil, false, nil
	}
	return cfg, cfg.IsEnabled(), nil
}

func collectSafe(ctx context.Context, env *Env, s Section, cfg SectionConfig) (r Result) {
	defer func() {
		if p := recover(); p != nil {
			r = Result{Name: s.Name, Status: StatusError, Err: fmt.Sprintf("panic: %v", p)}
		}
	}()
	r = s.Collect(ctx, env, cfg)
	if ctx.Err() != nil && r.Status != StatusError {
		r = Result{Name: s.Name, Status: StatusError, Err: "timeout: " + ctx.Err().Error()}
	}
	return r
}
