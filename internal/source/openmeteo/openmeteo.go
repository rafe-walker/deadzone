// Package openmeteo fetches global tilted irradiance (GTI), precipitation
// and cloud cover from Open-Meteo (no API key, non-commercial use) and turns
// it into the PV forecast the engine uses for the optimistic hours number
// and the storm-risk cap.
//
// The last good response is cached on disk so the daemon keeps a forecast
// when the uplink is down, which is exactly when it is most useful.
package openmeteo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
	"github.com/rafe-walker/deadzone/internal/source"
)

// DefaultBaseURL is the public Open-Meteo forecast endpoint.
const DefaultBaseURL = "https://api.open-meteo.com/v1/forecast"

// Options configure the source.
type Options struct {
	BaseURL   string
	Latitude  float64
	Longitude float64
	// TiltDeg is panel tilt from horizontal. AzimuthDeg uses Open-Meteo's
	// convention: 0 = south, -90 = east, 90 = west.
	TiltDeg    float64
	AzimuthDeg float64
	// ArrayKWp is the DC array size. 0 disables the PV watts (GTI and
	// precipitation still work).
	ArrayKWp float64
	// PR is the initial performance ratio; the daemon can replace it with
	// a learned one via SetPR.
	PR        float64
	Refresh   time.Duration
	CachePath string
	Client    *http.Client
	Now       func() time.Time
	Logger    *slog.Logger
}

func (o *Options) defaults() {
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.PR <= 0 {
		o.PR = 0.78
	}
	if o.Refresh <= 0 {
		o.Refresh = time.Hour
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 20 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// response is the subset of the Open-Meteo JSON we read (timeformat=unixtime).
type response struct {
	Minutely15 struct {
		Time          []int64   `json:"time"`
		GTI           []float64 `json:"global_tilted_irradiance"`
		Precipitation []float64 `json:"precipitation"`
	} `json:"minutely_15"`
	Hourly struct {
		Time       []int64   `json:"time"`
		CloudCover []float64 `json:"cloud_cover"`
	} `json:"hourly"`
	Reason string `json:"reason,omitempty"`
	Error  bool   `json:"error,omitempty"`
}

type cached struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Body      json.RawMessage `json:"body"`
}

// Source is the Open-Meteo forecast source.
type Source struct {
	opts Options

	mu        sync.RWMutex
	resp      *response
	fetchedAt time.Time
	fromCache bool
	lastErr   string
	pr        float64
	prSamples int
}

// New builds the source.
func New(opts Options) *Source {
	opts.defaults()
	return &Source{opts: opts, pr: opts.PR}
}

// Name implements source.Source.
func (s *Source) Name() string { return "openmeteo" }

// Start loads the cache, does one fetch inline, then refreshes on a timer.
func (s *Source) Start(ctx context.Context) error {
	if err := s.LoadCache(); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.opts.Logger.Warn("forecast cache unreadable", "path", s.opts.CachePath, "err", err)
	}
	if err := s.Fetch(ctx); err != nil {
		s.opts.Logger.Warn("forecast fetch failed", "err", err, "using_cache", s.resp != nil)
	}
	go func() {
		t := time.NewTicker(s.opts.Refresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.Fetch(ctx); err != nil {
					s.opts.Logger.Warn("forecast refresh failed", "err", err)
				}
			}
		}
	}()
	return nil
}

// URL builds the request URL (exported for tests and `--debug`).
func (s *Source) URL() string {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(s.opts.Latitude, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(s.opts.Longitude, 'f', 4, 64))
	q.Set("minutely_15", "global_tilted_irradiance,precipitation")
	q.Set("hourly", "cloud_cover")
	q.Set("tilt", strconv.FormatFloat(s.opts.TiltDeg, 'f', 1, 64))
	q.Set("azimuth", strconv.FormatFloat(s.opts.AzimuthDeg, 'f', 1, 64))
	q.Set("forecast_days", "2")
	q.Set("timezone", "UTC")
	q.Set("timeformat", "unixtime")
	return s.opts.BaseURL + "?" + q.Encode()
}

// Fetch performs one HTTP request and updates the forecast and the cache.
func (s *Source) Fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "deadzone (+https://github.com/rafe-walker/deadzone)")
	res, err := s.opts.Client.Do(req)
	if err != nil {
		s.setErr(err)
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		s.setErr(err)
		return err
	}
	if res.StatusCode != http.StatusOK {
		err = fmt.Errorf("open-meteo: HTTP %d: %s", res.StatusCode, truncate(body, 200))
		s.setErr(err)
		return err
	}
	r, err := parse(body)
	if err != nil {
		s.setErr(err)
		return err
	}
	now := s.opts.Now()
	s.mu.Lock()
	s.resp = r
	s.fetchedAt = now
	s.fromCache = false
	s.lastErr = ""
	s.mu.Unlock()
	if s.opts.CachePath != "" {
		if err := writeCache(s.opts.CachePath, cached{FetchedAt: now, Body: body}); err != nil {
			s.opts.Logger.Warn("forecast cache write failed", "path", s.opts.CachePath, "err", err)
		}
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

func (s *Source) setErr(err error) {
	s.mu.Lock()
	s.lastErr = err.Error()
	s.mu.Unlock()
}

func parse(body []byte) (*response, error) {
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("open-meteo: decode: %w", err)
	}
	if r.Error {
		return nil, fmt.Errorf("open-meteo: %s", r.Reason)
	}
	m := r.Minutely15
	if len(m.Time) == 0 || len(m.GTI) != len(m.Time) || len(m.Precipitation) != len(m.Time) {
		return nil, errors.New("open-meteo: minutely_15 arrays missing or ragged")
	}
	return &r, nil
}

func writeCache(path string, c cached) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadCache reads the on-disk cache if present. It never overrides a
// fresher in-memory forecast.
func (s *Source) LoadCache() error {
	if s.opts.CachePath == "" {
		return os.ErrNotExist
	}
	b, err := os.ReadFile(s.opts.CachePath)
	if err != nil {
		return err
	}
	var c cached
	if err := json.Unmarshal(b, &c); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	r, err := parse(c.Body)
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resp == nil || c.FetchedAt.After(s.fetchedAt) {
		s.resp = r
		s.fetchedAt = c.FetchedAt
		s.fromCache = true
	}
	return nil
}

// SetPR replaces the performance ratio with a learned value.
func (s *Source) SetPR(pr float64, samples int) {
	if pr <= 0 {
		return
	}
	s.mu.Lock()
	s.pr = pr
	s.prSamples = samples
	s.mu.Unlock()
}

// GTINow returns the irradiance of the current 15-minute slot, for the PR
// learner. ok is false when no forecast covers now.
func (s *Source) GTINow() (gti float64, ok bool) {
	f := s.Forecast()
	if !f.Available {
		return 0, false
	}
	now := s.opts.Now()
	for _, st := range f.Steps {
		if !now.Before(st.Time) && now.Before(st.Time.Add(15*time.Minute)) {
			return st.GTI, true
		}
	}
	return 0, false
}

// Snapshot implements source.Source.
func (s *Source) Snapshot() source.Snapshot {
	f := s.Forecast()
	return source.Snapshot{Forecast: &f, Updated: f.FetchedAt}
}

// CloudCoverNow returns the hourly cloud cover (%) for the current hour.
func (s *Source) CloudCoverNow() *float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cloudCoverAt(s.opts.Now())
}

// cloudCoverAt requires s.mu held.
func (s *Source) cloudCoverAt(at time.Time) *float64 {
	if s.resp == nil {
		return nil
	}
	now := at.Unix()
	h := s.resp.Hourly
	for i, t := range h.Time {
		if i < len(h.CloudCover) && now >= t && now < t+3600 {
			return model.F(h.CloudCover[i])
		}
	}
	return nil
}

// Forecast reduces the raw response to model.Forecast relative to now.
func (s *Source) Forecast() model.Forecast {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := model.Forecast{PRLearned: s.pr, PRSamples: s.prSamples, Error: s.lastErr, FromCache: s.fromCache, FetchedAt: s.fetchedAt}
	if s.resp == nil {
		return f
	}
	f.Available = true
	now := s.opts.Now()
	m := s.resp.Minutely15
	kW := s.opts.ArrayKWp
	steps := make([]model.ForecastStep, 0, len(m.Time))
	for i, ts := range m.Time {
		t := time.Unix(ts, 0).UTC()
		// Keep from the slot containing now onward; drop slots that have
		// already ended.
		if !t.Add(15 * time.Minute).After(now) {
			continue
		}
		gti := m.GTI[i]
		if gti < 0 {
			gti = 0
		}
		steps = append(steps, model.ForecastStep{
			Time:   t,
			GTI:    gti,
			PVW:    gti * kW * s.pr, // W/m² × kWp × PR = W
			Precip: m.Precipitation[i],
		})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Time.Before(steps[j].Time) })
	f.Steps = steps
	f.CloudCoverPct = s.cloudCoverAt(now)
	h6 := now.Add(6 * time.Hour)
	h2 := now.Add(2 * time.Hour)
	for _, st := range steps {
		if st.Time.Before(h6) {
			f.PVWhNext6h += st.PVW * 0.25
		}
		if st.Time.Before(h2) {
			f.PrecipMMNext2h += st.Precip
		}
		if !now.Before(st.Time) && now.Before(st.Time.Add(15*time.Minute)) {
			f.PVWNowExpected = st.PVW
		}
	}
	return f
}
