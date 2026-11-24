package ha

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/ha/states.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStatesParsesTimesAndAuth(t *testing.T) {
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		w.Write(fixture(t))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	snap, err := c.States(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" || path != "/api/states" {
		t.Errorf("auth %q path %q", auth, path)
	}
	s, ok := snap.ByID["sensor.car_battery"]
	if !ok || s.State != "25" || s.EntityID != "sensor.car_battery" {
		t.Fatalf("%+v", s)
	}
	want := time.Date(2026, 11, 24, 17, 49, 0, 0, time.UTC)
	if !s.LastReported.Equal(want) || !s.LastUpdated.Equal(want) || !s.LastChanged.Equal(want) {
		t.Errorf("times %v %v %v", s.LastReported, s.LastUpdated, s.LastChanged)
	}
	if s.Attributes["unit_of_measurement"] != "%" || snap.At.IsZero() {
		t.Errorf("attrs %v at %v", s.Attributes, snap.At)
	}
}

func TestHAUnauthorizedIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}).States(context.Background())
	if err == nil || err.Error() != "Home Assistant: 401 (token wrong or expired)" {
		t.Errorf("%v", err)
	}
}

func TestStatesOtherHTTPErrorAndBadJSON(t *testing.T) {
	code := 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		io.WriteString(w, "not json")
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	if _, err := c.States(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("%v", err)
	}
	code = 200
	if _, err := c.States(context.Background()); err == nil {
		t.Error("bad json accepted")
	}
}

func TestSchemelessBaseURLDefaultsToHTTPS(t *testing.T) {
	var got string
	c := &Client{BaseURL: "ha.example.test", Token: "t", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		got = r.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]")), Header: http.Header{}}, nil
	})}}
	if _, err := c.States(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "https://ha.example.test/api/states" {
		t.Errorf("%s", got)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServicePostsBodyWithReturnResponse(t *testing.T) {
	var method, path, query, ct string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, query, ct = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"service_response":{"x":1}}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	out, err := c.Service(context.Background(), "weather", "get_forecasts", map[string]any{"type": "daily"})
	if err != nil {
		t.Fatal(err)
	}
	if method != "POST" || path != "/api/services/weather/get_forecasts" || query != "return_response" || ct != "application/json" || body["type"] != "daily" {
		t.Errorf("%s %s ?%s %s %v", method, path, query, ct, body)
	}
	if string(out) != `{"service_response":{"x":1}}` {
		t.Errorf("%s", out)
	}
}

func TestServiceUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}).Service(context.Background(), "a", "b", nil)
	if err == nil || !strings.Contains(err.Error(), "401 (token wrong or expired)") {
		t.Errorf("%v", err)
	}
}

func TestGlob(t *testing.T) {
	s := &Snapshot{ByID: map[string]State{
		"sensor.b_x": {EntityID: "sensor.b_x"}, "sensor.a_x": {EntityID: "sensor.a_x"}, "light.a_x": {EntityID: "light.a_x"},
	}}
	got := s.Glob("sensor.*_x")
	if len(got) != 2 || got[0].EntityID != "sensor.a_x" || got[1].EntityID != "sensor.b_x" {
		t.Errorf("%v", got)
	}
	if len(s.Glob("nothing.*")) != 0 {
		t.Error("matched nothing expected")
	}
}
