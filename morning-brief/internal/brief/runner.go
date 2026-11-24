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
	for i, s := range secs {
		if msg, bad := p.ConfigErrors[s.Name]; bad {
			results[i] = Result{Name: s.Name, Status: StatusError, Err: "config: " + msg}
			continue
		}
		cfg, ok := p.Configs[s.Name]
		if !ok && s.Default != nil {
			cfg = s.Default()
		}
		if cfg == nil || !cfg.IsEnabled() {
			results[i] = Result{Name: s.Name, Status: StatusDisabled}
			continue
		}
		ch := make(chan Result, 1) // buffered: a section ignoring ctx never blocks on send
		chans[i] = ch
		go func(s Section, cfg SectionConfig) {
			sctx, scancel := context.WithTimeout(ctx, secTO)
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
		case <-ctx.Done():
			// the run deadline passed: take a result already there, else timeout
			select {
			case r := <-ch:
				results[i] = r
			default:
				results[i] = Result{Name: secs[i].Name, Status: StatusError, Err: "timeout: run exceeded " + runTO.String()}
			}
		case <-time.After(secTO + 250*time.Millisecond):
			results[i] = Result{Name: secs[i].Name, Status: StatusError, Err: "timeout after " + secTO.String()}
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
