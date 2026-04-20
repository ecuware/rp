package storage

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/TRNOG/rp/internal/metrics"
)

type TXTExporter struct {
	path string
}

func NewTXTExporter(path string) (*TXTExporter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("txt exporter: create %q: %w", path, err)
	}
	f.Close()
	return &TXTExporter{path: path}, nil
}

func (e *TXTExporter) Export(snaps []metrics.HopSnapshot, summary metrics.SessionSummary) error {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("rp export — %s\n", time.Now().UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("Target: %s\n", summary.Target))
	b.WriteString(fmt.Sprintf("Uptime: %s\n", fmtDurTXT(summary.Duration)))
	b.WriteString(fmt.Sprintf("Sent: %d  Recv: %d  Loss: %.1f%%  Route Changes: %d\n",
		summary.TotalSent, summary.TotalRecv,
		lossPctTXT(summary.TotalSent, summary.TotalRecv),
		summary.RouteChanges))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("%-4s %-18s %-28s %7s %8s %8s %8s %8s %8s\n",
		"Hop", "IP Address", "Hostname", "Loss%", "Last", "Avg", "Min", "Max", "Jitter"))
	b.WriteString(strings.Repeat("-", 102) + "\n")

	for _, s := range snaps {
		if s.Sent == 0 {
			continue
		}
		ip := s.DisplayIP()
		host := s.Hostname
		if host == "" {
			host = "-"
		}
		if s.Recv == 0 {
			b.WriteString(fmt.Sprintf("%-4d %-18s %-28s %7s %8s %8s %8s %8s %8s\n",
				s.TTL, ip, host,
				fmt.Sprintf("%.1f%%", s.Loss*100),
				"???", "???", "???", "???", "???"))
			continue
		}
		b.WriteString(fmt.Sprintf("%-4d %-18s %-28s %7s %8s %8s %8s %8s %8s\n",
			s.TTL, ip, host,
			fmt.Sprintf("%.1f%%", s.Loss*100),
			fmtDurTXT(s.LastRTT),
			fmtDurTXT(s.AvgRTT),
			fmtDurTXT(s.MinRTT),
			fmtDurTXT(s.MaxRTT),
			fmtDurTXT(s.Jitter)))
	}

	b.WriteString("\n")

	f, err := os.Create(e.path)
	if err != nil {
		return fmt.Errorf("txt export: open %q: %w", e.path, err)
	}
	defer f.Close()

	_, err = f.WriteString(b.String())
	return err
}

func (e *TXTExporter) Close() error { return nil }

func fmtDurTXT(d time.Duration) string {
	if d == 0 {
		return "0ms"
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func lossPctTXT(sent, recv int) float64 {
	if sent == 0 {
		return 0
	}
	return float64(sent-recv) / float64(sent) * 100
}
