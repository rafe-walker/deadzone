// Package app fuses the source snapshots into a model.State and runs the
// engine. It owns no I/O; cmd/deadzone wires the sources and outputs.
package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rafe-walker/deadzone/internal/actions"
	"github.com/rafe-walker/deadzone/internal/config"
	"github.com/rafe-walker/deadzone/internal/engine"
	"github.com/rafe-walker/deadzone/internal/model"
	"github.com/rafe-walker/deadzone/internal/source"
)

// PRSink receives the learned performance ratio (the Open-Meteo source).
type PRSink interface {
	SetPR(pr float64, samples int)
	GTINow() (float64, bool)
}

// App composes sources into State.
type App struct {
	Version string
	Params  engine.Params
	Power   source.Source // required
	Uplink  source.Source // optional
	Weather source.Source // optional
	PR      PRSink        // optional; usually the Weather source
	Actor   actions.Actor
	Logger  *slog.Logger
	Now     func() time.Time

	learner *engine.PRLearner
	mu      sync.RWMutex
	last    model.State
}

// New wires an App from config.
func New(cfg config.Config, version string, power, uplink, weather source.Source, pr PRSink, actor actions.Actor, log *slog.Logger) *App {
	if log == nil {
		log = slog.Default()
	}
	if actor == nil {
		actor = actions.NewAdvisory(false)
	}
	return &App{
		Version: version,
		Params:  engine.ParamsFromConfig(cfg),
		Power:   power,
		Uplink:  uplink,
		Weather: weather,
		PR:      pr,
		Actor:   actor,
		Logger:  log,
		Now:     time.Now,
		learner: engine.NewPRLearner(cfg.Forecast.ArrayKWp, cfg.Forecast.PRDefault),
	}
}

// Evaluate takes fresh snapshots, runs the engine and stores the result.
func (a *App) Evaluate(ctx context.Context) model.State {
	now := a.Now()
	st := model.State{TS: now.UTC(), Version: a.Version}
	st.Actions.Enabled = a.Actor.Mode() != actions.ModeDisabled
	st.Actions.Mode = string(a.Actor.Mode())

	if a.Power != nil {
		if s := a.Power.Snapshot(); s.Power != nil {
			st.Power = *s.Power
		}
	}
	st.Uplink.Primary = "none"
	st.Uplink.Starlink = model.Starlink{Alerts: []string{}, Outages15m: []model.Outage{}}
	if a.Uplink != nil {
		if s := a.Uplink.Snapshot(); s.Starlink != nil {
			st.Uplink.Starlink = *s.Starlink
			st.Uplink.Primary = "starlink"
		}
	}
	st.Uplink.Probe = model.Probe{Targets: []string{}}
	if a.Weather != nil {
		if s := a.Weather.Snapshot(); s.Forecast != nil {
			st.Forecast = *s.Forecast
		}
	}

	// PR learning: compare measured PV to forecast GTI while the sun is up.
	if a.PR != nil && st.Power.PVW != nil && !st.Power.Stale {
		if gti, ok := a.PR.GTINow(); ok && a.learner.Observe(gti, *st.Power.PVW) {
			pr, n := a.learner.PR()
			a.PR.SetPR(pr, n)
			st.Forecast.PRLearned, st.Forecast.PRSamples = pr, n
		}
	}

	st.Verdict = engine.Evaluate(engine.Inputs{
		Power:    st.Power,
		Starlink: st.Uplink.Starlink,
		Forecast: st.Forecast,
		Now:      now,
	}, a.Params)
	// The retained state should not carry the whole 2-day forecast.
	st.Forecast.Steps = trimSteps(st.Forecast.Steps, now, 6*time.Hour)

	if err := a.Actor.Shed(ctx, st.Verdict.Shed); err != nil {
		a.Logger.Warn("actor failed", "err", err)
	}
	a.mu.Lock()
	a.last = st
	a.mu.Unlock()
	return st
}

func trimSteps(steps []model.ForecastStep, now time.Time, horizon time.Duration) []model.ForecastStep {
	if len(steps) == 0 {
		return nil
	}
	end := now.Add(horizon)
	out := make([]model.ForecastStep, 0, 24)
	for _, s := range steps {
		if s.Time.Before(end) {
			out = append(out, s)
		}
	}
	return out
}

// State returns the last evaluation (zero value before the first).
func (a *App) State() model.State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.last
}
