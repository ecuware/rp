package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alptekinsunnetci/netplotter/internal/config"
	"github.com/alptekinsunnetci/netplotter/internal/dns"
	"github.com/alptekinsunnetci/netplotter/internal/metrics"
	"github.com/alptekinsunnetci/netplotter/internal/probe"
	"github.com/alptekinsunnetci/netplotter/internal/renderer"
	"github.com/alptekinsunnetci/netplotter/internal/storage"
	"github.com/alptekinsunnetci/netplotter/internal/traceroute"
)

func main() {
	cfg, err := config.Parse()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n\n", err)
		os.Exit(1)
	}

	// Resolve target hostname → IPv4
	targetIP, err := traceroute.ResolveTarget(cfg.Target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Build prober (ICMP preferred, falls back to TCP)
	prober, err := probe.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer prober.Close()

	fmt.Printf("Starting netplotter — target: %s (%s) via %s\n",
		cfg.Target, targetIP, prober.Name())

	// Use a shorter timeout for the initial parallel traceroute so the UI
	// appears quickly (≈ 1.5 s instead of Retries × Timeout × MaxHops).
	discoveryTimeout := 1500 * time.Millisecond
	if cfg.Timeout < discoveryTimeout {
		discoveryTimeout = cfg.Timeout
	}

	trOpts := traceroute.Options{
		MaxHops: cfg.MaxHops,
		Timeout: discoveryTimeout, // fast initial scan
		Retries: 1,                // one attempt per hop during discovery
	}
	runner := traceroute.NewRunner(prober, targetIP, trOpts)

	// DNS resolver
	var resolver *dns.Resolver
	if cfg.ResolveDNS {
		resolver = dns.NewResolver(cfg.DNSTimeout)
	}

	// Metrics session
	session := metrics.NewSession(targetIP, cfg.BufferSize)

	// ── Initial traceroute (parallel — completes in ~discoveryTimeout) ───────
	fmt.Println("Discovering route (parallel scan)…")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hops, err := runner.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "traceroute error: %v\n", err)
	}

	if len(hops) == 0 {
		// Nothing responded — seed a single entry so the probe loop runs.
		session.SetTTLIP(1, targetIP)
	}
	for _, h := range hops {
		session.SetTTLIP(h.TTL, h.IP) // nil IP is fine; will be filled by probes
		if h.State == traceroute.HopDestination {
			session.SetDestinationTTL(h.TTL)
		}
	}

	// Switch the runner to the user's configured (longer) timeout for
	// subsequent route-refresh scans.
	trOpts.Timeout = cfg.Timeout
	trOpts.Retries = 2
	runner = traceroute.NewRunner(prober, targetIP, trOpts)

	// ── Terminal renderer ────────────────────────────────────────────────────
	rend := renderer.NewTerminalRenderer(cfg)
	defer rend.Close()

	// ── Storage exporters ────────────────────────────────────────────────────
	var exporters []storage.Exporter
	if cfg.ExportJSON != "" {
		je, err2 := storage.NewJSONExporter(cfg.ExportJSON)
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "warn: %v\n", err2)
		} else {
			exporters = append(exporters, je)
			defer je.Close()
		}
	}
	if cfg.ExportCSV != "" {
		ce, err2 := storage.NewCSVExporter(cfg.ExportCSV)
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "warn: %v\n", err2)
		} else {
			exporters = append(exporters, ce)
			defer ce.Close()
		}
	}

	// ── Signal handling ──────────────────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	var seqCounter uint32

	// ── Probe loop ───────────────────────────────────────────────────────────
	// Probes all known TTLs in parallel every cfg.Interval.
	// Records ICMP Time Exceeded (intermediate hops) and Echo Reply (target).
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runProbeRound(ctx, prober, session, targetIP, cfg, &seqCounter)
			}
		}
	}()

	// ── Route-refresh loop ───────────────────────────────────────────────────
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(cfg.RouteRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				newHops, rerr := runner.Run(ctx)
				if rerr != nil {
					continue
				}
				for _, h := range newHops {
					session.SetTTLIP(h.TTL, h.IP)
					if h.State == traceroute.HopDestination {
						session.SetDestinationTTL(h.TTL)
					}
				}
				if runner.ConsumeChanged() {
					session.RecordRouteChange()
				}
			}
		}
	}()

	// ── DNS enrichment loop ──────────────────────────────────────────────────
	if cfg.ResolveDNS {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					enrichDNS(ctx, session, resolver)
				}
			}
		}()
	}

	// ── Render loop ──────────────────────────────────────────────────────────
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(cfg.RenderInterval)
		defer ticker.Stop()
		var routeChanged bool
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if runner.ConsumeChanged() {
					routeChanged = true
					session.RecordRouteChange()
				}
				snaps := session.Snapshot()
				sum := session.Summary()
				rend.Render(snaps, sum, routeChanged)
				routeChanged = false
			}
		}
	}()

	// ── Export loop ──────────────────────────────────────────────────────────
	if len(exporters) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(cfg.ExportInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runExport(session, exporters)
				}
			}
		}()
	}

	// ── Adaptive probing ─────────────────────────────────────────────────────
	if cfg.Adaptive {
		wg.Add(1)
		go func() {
			defer wg.Done()
			adaptiveLoop(ctx, session, cfg)
		}()
	}

	// Wait for SIGINT / SIGTERM
	<-sigCh
	fmt.Fprintln(os.Stderr, "\nShutting down…")
	cancel()
	wg.Wait()

	if len(exporters) > 0 {
		runExport(session, exporters)
		fmt.Fprintln(os.Stderr, "Final export written.")
	}
}

// runProbeRound sends one TTL-limited ICMP probe per known hop in parallel.
// ICMP Time Exceeded replies reveal intermediate hop IPs; Echo Reply means
// we reached the target. Results update the session in real time.
func runProbeRound(
	ctx context.Context,
	prober probe.Prober,
	session *metrics.Session,
	targetIP net.IP,
	cfg *config.Config,
	seqCounter *uint32,
) {
	snaps := session.Snapshot()
	if len(snaps) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, snap := range snaps {
		if snap.TTL == 0 {
			continue
		}
		ttl := snap.TTL
		seq := uint16(atomic.AddUint32(seqCounter, 1))

		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := prober.Probe(ctx, targetIP, ttl, seq, cfg.Timeout)
			if err != nil || result == nil {
				return
			}
			// Track path length: when a probe reaches the destination record TTL.
			if result.Success && result.Reached {
				session.SetDestinationTTL(ttl)
			}
			session.Record(ttl, result.RespondingIP, result.RTT, result.Success)
		}()
	}
	wg.Wait()
}

// enrichDNS performs reverse-DNS lookups for all hops without a hostname.
func enrichDNS(ctx context.Context, session *metrics.Session, resolver *dns.Resolver) {
	snaps := session.Snapshot()
	for _, snap := range snaps {
		if snap.IP == nil || snap.Hostname != "" {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if name := resolver.LookupSync(ctx, snap.IP); name != "" {
			session.SetHostname(snap.TTL, name)
		}
	}
}

// runExport writes the current snapshot to all configured exporters.
func runExport(session *metrics.Session, exporters []storage.Exporter) {
	snaps := session.Snapshot()
	sum := session.Summary()
	for _, exp := range exporters {
		if err := exp.Export(snaps, sum); err != nil {
			fmt.Fprintf(os.Stderr, "export error: %v\n", err)
		}
	}
}

// adaptiveLoop monitors overall loss and could adjust probe frequency.
func adaptiveLoop(ctx context.Context, session *metrics.Session, cfg *config.Config) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sum := session.Summary()
			if sum.TotalSent == 0 {
				continue
			}
			_ = float64(sum.TotalSent-sum.TotalRecv) / float64(sum.TotalSent)
		}
	}
}
