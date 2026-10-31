// Package source defines the contract every telemetry input implements.
//
// v0.1 ships three sources: solarassistant (MQTT), starlink (local gRPC) and
// openmeteo (HTTP). Each runs its own goroutines under Start and exposes a
// point-in-time Snapshot that the engine fuses. Sources never block the
// engine and never panic on missing data: an unreachable dish or an empty
// broker yields a Snapshot with the corresponding field marked unavailable.
package source

import (
	"context"
	"time"

	"github.com/rafe-walker/deadzone/internal/model"
)

// Snapshot is the union of what a source can contribute. A source fills only
// the fields it owns; the daemon merges snapshots from every source.
type Snapshot struct {
	// Power is set by power-side sources (solarassistant in v0.1).
	Power *model.Power
	// Starlink is set by the dish poller.
	Starlink *model.Starlink
	// Forecast is set by the PV forecast source.
	Forecast *model.Forecast
	// Updated is when the source last changed its snapshot.
	Updated time.Time
}

// Source is implemented by every input.
type Source interface {
	// Name is a short stable identifier used in logs and the state JSON.
	Name() string
	// Start launches the source's goroutines. It returns once startup work
	// is done (not when the source stops) or with an error if the source
	// cannot start at all. Goroutines exit when ctx is cancelled.
	Start(ctx context.Context) error
	// Snapshot returns the latest data. Safe for concurrent use.
	Snapshot() Snapshot
}
