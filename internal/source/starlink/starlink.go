// Package starlink polls the dish's local gRPC endpoint for get_status (every
// few seconds), get_history (every minute) and get_diagnostics (every
// minute) and reduces them to the model.Starlink fields the engine gates on.
//
// The service is resolved through gRPC server reflection at run time (see
// client.go). If reflection fails, the snapshot carries Reachable=false and an
// Error, and the engine reports the uplink as unknown instead of red.
package starlink

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
	"github.com/rafe-walker/deadzone/internal/source"
)

// Options tune the poller.
type Options struct {
	StatusInterval      time.Duration
	HistoryInterval     time.Duration
	DiagnosticsInterval time.Duration
	// StaleAfter marks the snapshot unreachable when neither poll has
	// succeeded for this long. Default 30 s.
	StaleAfter time.Duration
	Now        func() time.Time
	Logger     *slog.Logger
}

func (o *Options) defaults() {
	if o.StatusInterval <= 0 {
		o.StatusInterval = 5 * time.Second
	}
	if o.HistoryInterval <= 0 {
		o.HistoryInterval = 60 * time.Second
	}
	if o.DiagnosticsInterval <= 0 {
		o.DiagnosticsInterval = 60 * time.Second
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = 30 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Source is the Starlink poller.
type Source struct {
	caller Caller
	opts   Options

	mu    sync.RWMutex
	state model.Starlink
	hist  model.History
	// diagUnsupported is set when the dish rejects get_diagnostics with a
	// non-transport error (old firmware without the RPC in its Request
	// oneof). The loop then stops asking; status and history carry on.
	diagUnsupported bool
}

// New builds a source around any Caller.
func New(c Caller, opts Options) *Source {
	opts.defaults()
	return &Source{caller: c, opts: opts, state: model.Starlink{Present: true, Alerts: []string{}, Outages15m: []model.Outage{}}}
}

// Name implements source.Source.
func (s *Source) Name() string { return "starlink" }

// Start launches the poll loops. The first status poll runs inline so
// `check` gets a value without waiting an interval.
func (s *Source) Start(ctx context.Context) error {
	s.PollStatus(ctx)
	go s.loop(ctx, s.opts.StatusInterval, s.PollStatus, true)
	go s.loop(ctx, s.opts.HistoryInterval, s.PollHistory, false)
	go s.loop(ctx, s.opts.DiagnosticsInterval, s.PollDiagnostics, false)
	return nil
}

func (s *Source) loop(ctx context.Context, every time.Duration, fn func(context.Context), skipFirst bool) {
	if !skipFirst {
		fn(ctx)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

// PollStatus performs one get_status call and merges the result.
func (s *Source) PollStatus(ctx context.Context) {
	raw, err := s.caller.Call(ctx, map[string]any{"get_status": map[string]any{}})
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.fail(err)
		return
	}
	if err := ApplyStatus(&s.state, raw, s.opts.Now()); err != nil {
		s.fail(err)
	}
}

// PollHistory performs one get_history call and merges the result.
func (s *Source) PollHistory(ctx context.Context) {
	raw, err := s.caller.Call(ctx, map[string]any{"get_history": map[string]any{}})
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.fail(err)
		return
	}
	hist, err := applyHistory(&s.state, raw, s.opts.Now())
	if err != nil {
		s.fail(err)
		return
	}
	s.hist = hist
}

// PollDiagnostics performs one get_diagnostics call and merges the result.
// A dish that does not know the RPC (reflection says the request does not
// fit) marks diagnostics unsupported and is not asked again; the dish is
// not considered failed, because status and history still work.
func (s *Source) PollDiagnostics(ctx context.Context) {
	s.mu.RLock()
	skip := s.diagUnsupported
	s.mu.RUnlock()
	if skip {
		return
	}
	raw, err := s.caller.Call(ctx, map[string]any{"get_diagnostics": map[string]any{}})
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.state.DiagnosticsError = err.Error()
		if errors.Is(err, ErrUnreachable) {
			// The status loop owns reachability; nothing more to record.
			return
		}
		s.diagUnsupported = true
		s.opts.Logger.Warn("starlink get_diagnostics not supported by this dish; disablement/stow checks off", "err", err)
		return
	}
	if err := ApplyDiagnostics(&s.state, raw, s.opts.Now()); err != nil {
		s.state.DiagnosticsError = err.Error()
		s.diagUnsupported = true
		s.opts.Logger.Warn("starlink get_diagnostics response not understood; disabling", "err", err)
	}
}

// DiagnosticsSupported reports whether the dish answered get_diagnostics
// (true until proven otherwise).
func (s *Source) DiagnosticsSupported() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.diagUnsupported
}

// History15m returns the downsampled 15-minute drop/latency history for
// the status page. Empty (but non-nil slices) before the first history poll.
func (s *Source) History15m() model.History {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.hist
	h.Drop = append([]*float64{}, h.Drop...)
	h.LatMs = append([]*float64{}, h.LatMs...)
	return h
}

// fail records an error without discarding the last good telemetry; the
// staleness check in Snapshot decides when that telemetry stops counting.
func (s *Source) fail(err error) {
	s.state.Error = err.Error()
	switch {
	case errors.Is(err, ErrReflection):
		s.opts.Logger.Warn("starlink reflection failed; uplink unknown", "err", err)
	case errors.Is(err, ErrUnreachable):
		s.state.Reachable = false
		s.opts.Logger.Debug("starlink unreachable", "err", err)
	default:
		s.opts.Logger.Warn("starlink poll failed", "err", err)
	}
}

// Snapshot implements source.Source.
func (s *Source) Snapshot() source.Snapshot {
	st := s.Starlink()
	return source.Snapshot{Starlink: &st, Updated: st.Updated}
}

// Starlink returns a copy of the current dish view with staleness applied.
func (s *Source) Starlink() model.Starlink {
	s.mu.RLock()
	st := s.state
	s.mu.RUnlock()
	st.Alerts = append([]string{}, st.Alerts...)
	st.Outages15m = append([]model.Outage{}, st.Outages15m...)
	now := s.opts.Now()
	if st.Updated.IsZero() || now.Sub(st.Updated) > s.opts.StaleAfter {
		st.Reachable = false
		if st.Error == "" {
			st.Error = "no dish telemetry yet"
		}
	}
	return st
}
