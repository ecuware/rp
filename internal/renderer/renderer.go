// Package renderer defines the Renderer interface and shared display types.
package renderer

import (
	"github.com/TRNOG/rp/internal/metrics"
)

// Panel is a renderable target view.
type Panel struct {
	Title        string
	Snaps        []metrics.HopSnapshot
	Summary      metrics.SessionSummary
	RouteChanged bool
	Paused       bool
	SortMode     string
	ViewMode     string
}

// Renderer is implemented by anything that can display session data.
type Renderer interface {
	// Render draws the current state to the output.
	Render(panels []Panel)

	// Close releases resources (e.g. restores terminal state).
	Close()
}
