// Package renderer defines the Renderer interface and shared display types.
package renderer

import (
	"github.com/alptekinsunnetci/netplotter/internal/metrics"
)

// Renderer is implemented by anything that can display session data.
type Renderer interface {
	// Render draws the current state to the output.
	Render(snaps []metrics.HopSnapshot, summary metrics.SessionSummary, routeChanged bool)

	// Close releases resources (e.g. restores terminal state).
	Close()
}
