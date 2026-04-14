//go:build !windows

package probe

import (
	"fmt"

	"github.com/alptekinsunnetci/netplotter/internal/config"
)

// New creates a Prober based on the configured protocol.
// It tries the preferred prober first and falls back if creation fails.
func New(cfg *config.Config) (Prober, error) {
	switch cfg.Protocol {
	case config.ProtoICMP:
		p, err := NewICMPProber()
		if err != nil {
			// Fall back to TCP automatically
			fmt.Printf("[warn] ICMP unavailable (%v), falling back to TCP/%d\n", err, cfg.Port)
			return NewTCPProber(cfg.Port), nil
		}
		return p, nil

	case config.ProtoTCP:
		return NewTCPProber(cfg.Port), nil

	case config.ProtoUDP:
		// UDP probing uses the same technique as ICMP traceroute but sends UDP datagrams.
		// For simplicity we alias to ICMP for now, with UDP traceroute as a future extension.
		p, err := NewICMPProber()
		if err != nil {
			return NewTCPProber(cfg.Port), nil
		}
		return p, nil

	default:
		return nil, fmt.Errorf("unknown protocol: %s", cfg.Protocol)
	}
}
