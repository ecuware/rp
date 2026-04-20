//go:build !windows

package probe

import (
	"fmt"

	"github.com/TRNOG/rp/internal/config"
)

func New(cfg *config.Config) (Prober, error) {
	return NewWithIPv6(cfg, false)
}

func NewWithIPv6(cfg *config.Config, useIPv6 bool) (Prober, error) {
	switch cfg.Protocol {
	case config.ProtoICMP:
		if useIPv6 {
			p, err := NewICMPv6Prober()
			if err != nil {
				fmt.Printf("[warn] ICMPv6 unavailable (%v), falling back to TCP/%d\n", err, cfg.Port)
				return NewTCPProber(cfg.Port), nil
			}
			return p, nil
		}
		p, err := NewICMPProber()
		if err != nil {
			fmt.Printf("[warn] ICMP unavailable (%v), falling back to TCP/%d\n", err, cfg.Port)
			return NewTCPProber(cfg.Port), nil
		}
		return p, nil

	case config.ProtoTCP:
		return NewTCPProber(cfg.Port), nil

	case config.ProtoUDP:
		if useIPv6 {
			p, err := NewICMPv6Prober()
			if err != nil {
				return NewTCPProber(cfg.Port), nil
			}
			return p, nil
		}
		p, err := NewICMPProber()
		if err != nil {
			return NewTCPProber(cfg.Port), nil
		}
		return p, nil

	default:
		return nil, fmt.Errorf("unknown protocol: %s", cfg.Protocol)
	}
}
