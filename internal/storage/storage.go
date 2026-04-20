// Package storage provides export backends for session data.
package storage

import (
	"github.com/TRNOG/rp/internal/metrics"
)

// Exporter writes a snapshot of the current session to durable storage.
type Exporter interface {
	Export(snaps []metrics.HopSnapshot, summary metrics.SessionSummary) error
	Close() error
}
