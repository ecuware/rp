package storage

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/alptekinsunnetci/netplotter/internal/metrics"
)

// CSVExporter appends one row per hop per export cycle to a CSV file.
type CSVExporter struct {
	path string
	file *os.File
	w    *csv.Writer
}

var csvHeader = []string{
	"timestamp", "target", "ttl", "ip", "hostname",
	"sent", "recv", "loss_pct",
	"min_ms", "avg_ms", "max_ms", "last_ms", "jitter_ms",
}

// NewCSVExporter creates a CSVExporter that writes to path.
// It writes the CSV header immediately.
func NewCSVExporter(path string) (*CSVExporter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("csv exporter: open %q: %w", path, err)
	}
	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		f.Close()
		return nil, err
	}
	w.Flush()
	return &CSVExporter{path: path, file: f, w: w}, nil
}

// Export appends one row per hop to the CSV file.
func (e *CSVExporter) Export(snaps []metrics.HopSnapshot, summary metrics.SessionSummary) error {
	ts := time.Now().UTC().Format(time.RFC3339)
	target := summary.Target.String()

	for _, s := range snaps {
		if s.Sent == 0 {
			continue
		}
		row := []string{
			ts,
			target,
			strconv.Itoa(s.TTL),
			s.DisplayIP(),
			s.Hostname,
			strconv.Itoa(s.Sent),
			strconv.Itoa(s.Recv),
			fmt.Sprintf("%.2f", s.Loss*100),
			fmt.Sprintf("%.3f", msf(s.MinRTT)),
			fmt.Sprintf("%.3f", msf(s.AvgRTT)),
			fmt.Sprintf("%.3f", msf(s.MaxRTT)),
			fmt.Sprintf("%.3f", msf(s.LastRTT)),
			fmt.Sprintf("%.3f", msf(s.Jitter)),
		}
		if err := e.w.Write(row); err != nil {
			return err
		}
	}
	e.w.Flush()
	return e.w.Error()
}

// Close flushes and closes the underlying file.
func (e *CSVExporter) Close() error {
	e.w.Flush()
	return e.file.Close()
}
