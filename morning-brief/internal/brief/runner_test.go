package brief

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type cfg struct{ on bool }

func (c cfg) IsEnabled() bool { return c.on }
func (c cfg) Validate() error { return nil }

func sec(name string, collect func(ctx context.Context, env *Env, c SectionConfig) Result) Section {
	return Section{
		Name:    name,
		Default: func() SectionConfig { return cfg{on: true} },
		Collect: collect,
	}
}

func reportFn(ctx context.Context, env *Env, c SectionConfig) Result {
	return Result{Status: StatusReport, Facts: []Fact{{Key: "k", Value: "v"}}}
}

func byName(rs []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

func withRegistry(t *testing.T, ss ...Section) {
	t.Helper()
	saved := registry
	registry = nil
	for _, s := range ss {
		Register(s)
	}
	t.Cleanup(func() { registry = saved })
}

func TestRunPanicIsSectionError(t *testing.T) {
	withRegistry(t,
		sec("a", func(context.Context, *Env, SectionConfig) Result { panic("boom") }),
		sec("b", reportFn),
	)
	rs := Run(context.Background(), &Env{}, Plan{})
	if len(rs) != 2 || rs[0].Name != "a" || rs[1].Name != "b" {
		t.Fatalf("want results a,b in registration order, got %+v", rs)
	}
	if rs[0].Status != StatusError || !strings.Contains(rs[0].Err, "panic") {
		t.Errorf("a: %+v", rs[0])
	}
	if rs[1].Status != StatusReport {
		t.Errorf("b: %+v", rs[1])
	}
}

func TestRunTimeoutIsSectionError(t *testing.T) {
	withRegistry(t,
		sec("slow", func(ctx context.Context, env *Env, c SectionConfig) Result {
			<-ctx.Done()
			return Result{Status: StatusReport}
		}),
		sec("stuck", func(ctx context.Context, env *Env, c SectionConfig) Result {
			time.Sleep(3 * time.Second) // ignores ctx entirely
			return Result{Status: StatusReport}
		}),
	)
	start := time.Now()
	rs := Run(context.Background(), &Env{}, Plan{SectionTimeout: 50 * time.Millisecond})
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Run took %v", d)
	}
	for _, r := range rs {
		if r.Status != StatusError || !strings.Contains(r.Err, "timeout") {
			t.Errorf("%s: %+v", r.Name, r)
		}
	}
}

func TestRunDisabledOpensNoConnection(t *testing.T) {
	off := func(name string) Section {
		s := sec(name, func(context.Context, *Env, SectionConfig) Result {
			t.Fatalf("%s collected", name)
			return Result{}
		})
		s.Default = func() SectionConfig { return cfg{on: false} }
		return s
	}
	withRegistry(t, off("a"), off("b"))
	env := &Env{
		HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("http call")
			return nil, nil
		})},
		Dial: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("dial")
			return nil, nil
		},
	}
	rs := Run(context.Background(), env, Plan{})
	if len(rs) != 2 {
		t.Fatalf("got %+v", rs)
	}
	for _, r := range rs {
		if r.Status != StatusDisabled {
			t.Errorf("%s: %+v", r.Name, r)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunExplicitDisabledConfigWins(t *testing.T) {
	withRegistry(t, sec("a", func(context.Context, *Env, SectionConfig) Result {
		t.Fatal("collected")
		return Result{}
	}))
	rs := Run(context.Background(), &Env{}, Plan{Configs: map[string]SectionConfig{"a": cfg{on: false}}})
	if rs[0].Status != StatusDisabled {
		t.Errorf("%+v", rs[0])
	}
}

func TestRunConfigErrorNotCollected(t *testing.T) {
	withRegistry(t, sec("a", func(context.Context, *Env, SectionConfig) Result {
		t.Fatal("collected")
		return Result{}
	}), sec("b", reportFn))
	rs := Run(context.Background(), &Env{}, Plan{ConfigErrors: map[string]string{"a": "bad key"}})
	m := byName(rs)
	if m["a"].Status != StatusError || m["a"].Err != "config: bad key" {
		t.Errorf("a: %+v", m["a"])
	}
	if m["b"].Status != StatusReport {
		t.Errorf("b: %+v", m["b"])
	}
}

func TestRunOnly(t *testing.T) {
	withRegistry(t, sec("a", reportFn), sec("b", reportFn), sec("c", reportFn))
	rs := Run(context.Background(), &Env{}, Plan{Only: []string{"c", "a"}})
	if len(rs) != 2 || rs[0].Name != "a" || rs[1].Name != "c" {
		t.Errorf("%+v", rs)
	}
}

func TestRunSetsNameAndSurvivesCollectError(t *testing.T) {
	withRegistry(t, sec("a", func(context.Context, *Env, SectionConfig) Result {
		return Result{Name: "wrong", Status: StatusError, Err: errors.New("x").Error()}
	}))
	rs := Run(context.Background(), &Env{}, Plan{})
	if rs[0].Name != "a" || rs[0].Err != "x" {
		t.Errorf("%+v", rs[0])
	}
}
