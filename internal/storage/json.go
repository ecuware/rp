package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/alptekinsunnetci/netplotter/internal/metrics"
)

// jsonHop is the JSON representation of one hop.
type jsonHop struct {
	TTL      int     `json:"ttl"`
	IP       string  `json:"ip"`
	Hostname string  `json:"hostname,omitempty"`
	Sent     int     `json:"sent"`
	Recv     int     `json:"recv"`
	Loss     float64 `json:"loss_pct"`
	MinMS    float64 `json:"min_ms"`
	AvgMS    float64 `json:"avg_ms"`
	MaxMS    float64 `json:"max_ms"`
	LastMS   float64 `json:"last_ms"`
	JitterMS float64 `json:"jitter_ms"`
}

// jsonExport is the top-level JSON document.
type jsonExport struct {
	ExportedAt   time.Time  `json:"exported_at"`
	Target       string     `json:"target"`
	StartAt      time.Time  `json:"start_at"`
	UptimeSec    float64    `json:"uptime_sec"`
	TotalSent    int        `json:"total_sent"`
	TotalRecv    int        `json:"total_recv"`
	RouteChanges int        `json:"route_changes"`
	Hops         []jsonHop  `json:"hops"`
}

// JSONExporter writes session data to a JSON file on every Export call.
type JSONExporter struct {
	path string
}

// NewJSONExporter creates a JSONExporter that writes to path.
func NewJSONExporter(path string) (*JSONExporter, error) {
	// Test that we can create/truncate the file
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("json exporter: create %q: %w", path, err)
	}
	f.Close()
	return &JSONExporter{path: path}, nil
}

// Export writes the current snapshot to the JSON file (truncate + rewrite).
func (e *JSONExporter) Export(snaps []metrics.HopSnapshot, summary metrics.SessionSummary) error {
	doc := jsonExport{
		ExportedAt:   time.Now().UTC(),
		Target:       summary.Target.String(),
		StartAt:      summary.StartAt.UTC(),
		UptimeSec:    summary.Duration.Seconds(),
		TotalSent:    summary.TotalSent,
		TotalRecv:    summary.TotalRecv,
		RouteChanges: summary.RouteChanges,
		Hops:         make([]jsonHop, 0, len(snaps)),
	}

	for _, s := range snaps {
		if s.Sent == 0 {
			continue
		}
		h := jsonHop{
			TTL:      s.TTL,
			IP:       s.DisplayIP(),
			Hostname: s.Hostname,
			Sent:     s.Sent,
			Recv:     s.Recv,
			Loss:     s.Loss * 100,
			MinMS:    msf(s.MinRTT),
			AvgMS:    msf(s.AvgRTT),
			MaxMS:    msf(s.MaxRTT),
			LastMS:   msf(s.LastRTT),
			JitterMS: msf(s.Jitter),
		}
		doc.Hops = append(doc.Hops, h)
	}

	f, err := os.Create(e.path)
	if err != nil {
		return fmt.Errorf("json export: open %q: %w", e.path, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func (e *JSONExporter) Close() error { return nil }

func msf(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}
