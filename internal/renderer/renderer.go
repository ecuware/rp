// Package renderer defines the Renderer interface and shared display types.
package renderer

import (
	"github.com/alptekinsunnetci/netplotter/internal/metrics"
)

// Panel is a renderable target view.
type Panel struct {
	Title        string
	Snaps        []metrics.HopSnapshot
	Summary      metrics.SessionSummary
	RouteChanged bool
}

// Renderer is implemented by anything that can display session data.
type Renderer interface {
	// Render draws the current state to the output.
	Render(panels []Panel)

	// Close releases resources (e.g. restores terminal state).
	Close()
}
