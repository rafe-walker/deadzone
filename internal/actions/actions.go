// Package actions is the write side of deadzone: anything that would flip
// an inverter setting, a smart plug or a Home Assistant switch goes
// through Actor.
//
// v0.1 ships only Advisory, which records what it would have done. Real
// actuators (Solar Assistant /set writes, HA service calls, webhooks) are
// v0.2 scope; see ROADMAP.md. The interface exists now so the state JSON
// already carries actions.mode and automations can rely on the shape.
package actions

import (
	"context"
	"sync"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
)

// Mode is published in State.Actions.Mode.
type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeDryRun   Mode = "dry-run"
	ModeLive     Mode = "live"
)

// Actor applies a shed recommendation.
type Actor interface {
	// Mode reports how the actor behaves.
	Mode() Mode
	// Shed is called with the current recommendation after every
	// evaluation. Implementations must be idempotent.
	Shed(ctx context.Context, items []model.ShedItem) error
}

// Record is one advisory entry.
type Record struct {
	At    time.Time        `json:"at"`
	Items []model.ShedItem `json:"items"`
}

// Advisory never touches anything; it keeps the last recommendation so the
// operator can see what live mode would have done.
type Advisory struct {
	mode Mode
	mu   sync.Mutex
	last *Record
	now  func() time.Time
}

// NewAdvisory returns an actor in dry-run (enabled=true) or disabled mode.
func NewAdvisory(enabled bool) *Advisory {
	m := ModeDisabled
	if enabled {
		m = ModeDryRun
	}
	return &Advisory{mode: m, now: time.Now}
}

// Mode implements Actor.
func (a *Advisory) Mode() Mode { return a.mode }

// Shed implements Actor.
func (a *Advisory) Shed(_ context.Context, items []model.ShedItem) error {
	if a.mode == ModeDisabled {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = &Record{At: a.now(), Items: append([]model.ShedItem{}, items...)}
	return nil
}

// Last returns the most recent recommendation seen (nil if none/disabled).
func (a *Advisory) Last() *Record {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last
}

var _ Actor = (*Advisory)(nil)
