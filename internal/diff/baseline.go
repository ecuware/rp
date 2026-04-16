package diff

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Baseline holds previous-run metrics used for diffing.
type Baseline struct {
	Target string
	Hops   map[int]HopBaseline
}

// HopBaseline stores baseline values for a hop.
type HopBaseline struct {
	Loss   float64
	AvgRTT time.Duration
}

type baselineFile struct {
	Target string `json:"target"`
	Hops   []struct {
		TTL   int     `json:"ttl"`
		Loss  float64 `json:"loss_pct"`
		AvgMS float64 `json:"avg_ms"`
	} `json:"hops"`
}

// LoadJSONBaseline loads a JSON export as a diff baseline.
func LoadJSONBaseline(path string) (*Baseline, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("diff file: read %q: %w", path, err)
	}

	var doc baselineFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("diff file: parse %q: %w", path, err)
	}

	b := &Baseline{
		Target: doc.Target,
		Hops:   make(map[int]HopBaseline, len(doc.Hops)),
	}
	for _, h := range doc.Hops {
		if h.TTL <= 0 {
			continue
		}
		b.Hops[h.TTL] = HopBaseline{
			Loss:   h.Loss / 100.0,
			AvgRTT: time.Duration(h.AvgMS * float64(time.Millisecond)),
		}
	}
	return b, nil
}
