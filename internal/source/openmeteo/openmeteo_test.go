package openmeteo

import (
	"context"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func fixtureBody(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "forecast.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// server serves the fixture and records the query it saw.
func server(t *testing.T, status int, body []byte) (*httptest.Server, *string) {
	t.Helper()
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.RawQuery
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func newSource(t *testing.T, base, cache string, clock *time.Time) *Source {
	t.Helper()
	return New(Options{
		BaseURL:    base,
		Latitude:   31.9,
		Longitude:  -109.8,
		TiltDeg:    30,
		AzimuthDeg: 0,
		ArrayKWp:   4.0,
		PR:         0.5, // easy arithmetic: W = GTI * 4 * 0.5 = 2*GTI
		CachePath:  cache,
		Now:        func() time.Time { return *clock },
		Logger:     quiet(),
	})
}

func TestFetchParsesAndReduces(t *testing.T) {
	srv, seen := server(t, 200, fixtureBody(t))
	clock := now
	s := newSource(t, srv.URL, "", &clock)
	if err := s.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"latitude=31.9000", "longitude=-109.8000", "global_tilted_irradiance", "tilt=30.0", "azimuth=0.0", "timeformat=unixtime", "cloud_cover"} {
		if !strings.Contains(*seen, want) {
			t.Errorf("query missing %q: %s", want, *seen)
		}
	}
	f := s.Forecast()
	if !f.Available || f.FromCache || f.Error != "" {
		t.Fatalf("forecast flags: %+v", f)
	}
	// GTI now = 714 -> 714*4*0.5 = 1428 W
	if math.Abs(f.PVWNowExpected-1428) > 0.01 {
		t.Errorf("pv_w_now=%v want 1428", f.PVWNowExpected)
	}
	// 3826.4 Wh per (kWp*PR) * 2 = 7652.8
	if math.Abs(f.PVWhNext6h-7652.8) > 0.5 {
		t.Errorf("pv_wh_next_6h=%v want ~7652.8", f.PVWhNext6h)
	}
	if f.PrecipMMNext2h != 0 {
		t.Errorf("precip next 2h at 17:00 = %v want 0", f.PrecipMMNext2h)
	}
	if len(f.Steps) == 0 || f.Steps[0].Time.After(now) {
		t.Errorf("steps should start at/before now: %+v", f.Steps[:1])
	}
	if cc := s.CloudCoverNow(); cc == nil || *cc != 10 {
		t.Errorf("cloud cover now=%v want 10", cc)
	}
	if gti, ok := s.GTINow(); !ok || gti != 714 {
		t.Errorf("GTINow=%v,%v", gti, ok)
	}
	if s.Name() != "openmeteo" {
		t.Error("name")
	}
}

func TestStormWindowAndCloud(t *testing.T) {
	srv, _ := server(t, 200, fixtureBody(t))
	clock := now
	s := newSource(t, srv.URL, "", &clock)
	if err := s.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock = time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)
	f := s.Forecast()
	if math.Abs(f.PrecipMMNext2h-3.2) > 1e-9 {
		t.Errorf("precip next 2h at 20:00 = %v want 3.2", f.PrecipMMNext2h)
	}
	clock = time.Date(2026, 10, 31, 21, 30, 0, 0, time.UTC)
	if cc := s.CloudCoverNow(); cc == nil || *cc != 85 {
		t.Errorf("cloud cover during storm=%v want 85", cc)
	}
}

func TestNightHasNoPV(t *testing.T) {
	srv, _ := server(t, 200, fixtureBody(t))
	clock := time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC) // 23:00 local the night before
	s := newSource(t, srv.URL, "", &clock)
	if err := s.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := s.Forecast()
	if f.PVWNowExpected != 0 || f.PVWhNext6h != 0 {
		t.Fatalf("night forecast should be zero: now=%v 6h=%v", f.PVWNowExpected, f.PVWhNext6h)
	}
}

func TestCacheRoundTripAndOfflineFallback(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "sub", "forecast.json")
	srv, _ := server(t, 200, fixtureBody(t))
	clock := now
	s := newSource(t, srv.URL, cache, &clock)
	if err := s.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache not written: %v", err)
	}

	// New process, uplink down: a dead server. Cache must carry it.
	dead, _ := server(t, 500, []byte("boom"))
	clock2 := now.Add(30 * time.Minute)
	s2 := newSource(t, dead.URL, cache, &clock2)
	if err := s2.LoadCache(); err != nil {
		t.Fatal(err)
	}
	err := s2.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("expected HTTP 500 error, got %v", err)
	}
	f := s2.Forecast()
	if !f.Available || !f.FromCache {
		t.Fatalf("cached forecast should be served: %+v", f)
	}
	if f.Error == "" {
		t.Error("error from the failed refresh should be surfaced")
	}
	if !f.FetchedAt.Equal(now) {
		t.Errorf("fetched_at should be the cache time %v, got %v", now, f.FetchedAt)
	}
}

func TestStartUsesCacheWhenFetchFails(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "forecast.json")
	srv, _ := server(t, 200, fixtureBody(t))
	clock := now
	s := newSource(t, srv.URL, cache, &clock)
	if err := s.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s2 := newSource(t, "http://127.0.0.1:1/", cache, &clock)
	if err := s2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if f := s2.Forecast(); !f.Available || !f.FromCache {
		t.Fatalf("Start should fall back to cache: %+v", f)
	}
}

func TestParseRejectsErrorsAndRagged(t *testing.T) {
	if _, err := parse([]byte(`{"error":true,"reason":"Latitude must be in range"}`)); err == nil || !strings.Contains(err.Error(), "Latitude") {
		t.Fatalf("api error not surfaced: %v", err)
	}
	if _, err := parse([]byte(`{"minutely_15":{"time":[1,2],"global_tilted_irradiance":[1],"precipitation":[0,0]}}`)); err == nil {
		t.Fatal("ragged arrays should fail")
	}
	if _, err := parse([]byte(`{`)); err == nil {
		t.Fatal("bad json should fail")
	}
}

func TestNoForecastWhenNothingLoaded(t *testing.T) {
	clock := now
	s := newSource(t, "http://127.0.0.1:1/", "", &clock)
	f := s.Forecast()
	if f.Available {
		t.Fatal("nothing loaded -> not available")
	}
	if _, ok := s.GTINow(); ok {
		t.Fatal("GTINow should report !ok")
	}
	if s.CloudCoverNow() != nil {
		t.Fatal("cloud cover should be nil")
	}
	snap := s.Snapshot()
	if snap.Forecast == nil || snap.Power != nil {
		t.Fatal("snapshot shape")
	}
}

func TestSetPRChangesWatts(t *testing.T) {
	srv, _ := server(t, 200, fixtureBody(t))
	clock := now
	s := newSource(t, srv.URL, "", &clock)
	if err := s.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.SetPR(1.0, 12)
	f := s.Forecast()
	if math.Abs(f.PVWNowExpected-714*4) > 0.01 || f.PRLearned != 1.0 || f.PRSamples != 12 {
		t.Fatalf("SetPR not applied: %+v", f)
	}
	s.SetPR(0, 1)
	if s.Forecast().PRLearned != 1.0 {
		t.Fatal("non-positive PR must be ignored")
	}
}
