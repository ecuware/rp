//go:build windows

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
	case config.ProtoTCP:
		return NewTCPProber(cfg.Port), nil

	default:
		if useIPv6 {
			p, err := NewWindowsICMPv6Prober()
			if err != nil {
				fmt.Printf("[warn] Icmp6SendEcho2 unavailable (%v), falling back to TCP/%d\n", err, cfg.Port)
				return NewTCPProber(cfg.Port), nil
			}
			return p, nil
		}
		p, err := NewWindowsICMPProber()
		if err != nil {
			fmt.Printf("[warn] IcmpSendEcho2 unavailable (%v), falling back to TCP/%d\n", err, cfg.Port)
			return NewTCPProber(cfg.Port), nil
		}
		return p, nil
	}
}
