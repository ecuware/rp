package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alptekinsunnetci/netplotter/internal/config"
	"github.com/alptekinsunnetci/netplotter/internal/diff"
	"github.com/alptekinsunnetci/netplotter/internal/dns"
	"github.com/alptekinsunnetci/netplotter/internal/metrics"
	"github.com/alptekinsunnetci/netplotter/internal/probe"
	"github.com/alptekinsunnetci/netplotter/internal/renderer"
	"github.com/alptekinsunnetci/netplotter/internal/storage"
	"github.com/alptekinsunnetci/netplotter/internal/traceroute"
	"golang.org/x/term"
)

type targetState struct {
	name     string
	ip       net.IP
	session  *metrics.Session
	runner   *traceroute.Runner
	resolver *dns.Resolver
}

func main() {
	cfg, err := config.Parse()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n\n", err)
		os.Exit(1)
	}

	// Build prober (ICMP preferred, falls back to TCP)
	prober, err := probe.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer prober.Close()

	var baseline *diff.Baseline
	if cfg.DiffFile != "" {
		b, err := diff.LoadJSONBaseline(cfg.DiffFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: %v\n", err)
		} else {
			baseline = b
		}
	}

	states := make([]*targetState, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targetIP, rerr := traceroute.ResolveTarget(target)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "warn: resolve %q: %v\n", target, rerr)
			continue
		}

		trOpts := traceroute.Options{
			MaxHops: cfg.MaxHops,
			Timeout: cfg.Timeout,
			Retries: 2,
		}
		states = append(states, &targetState{
			name:    target,
			ip:      targetIP,
			session: metrics.NewSession(targetIP, cfg.BufferSize),
			runner:  traceroute.NewRunner(prober, targetIP, trOpts),
		})
	}

	if len(states) == 0 {
		fmt.Fprintln(os.Stderr, "error: no valid targets")
		os.Exit(1)
	}

	if len(states) == 1 {
		fmt.Printf("Starting netplotter — target: %s (%s) via %s\n",
			states[0].name, states[0].ip, prober.Name())
	} else {
		fmt.Printf("Starting netplotter — %d targets via %s\n", len(states), prober.Name())
	}

	// Use a shorter timeout for the initial parallel traceroute so the UI
	// appears quickly (≈ 1.5 s instead of Retries × Timeout × MaxHops).
	discoveryTimeout := 1500 * time.Millisecond
	if cfg.Timeout < discoveryTimeout {
		discoveryTimeout = cfg.Timeout
	}

	// DNS resolver per target
	if cfg.ResolveDNS {
		for _, st := range states {
			st.resolver = dns.NewResolver(cfg.DNSTimeout)
		}
	}

	// ── Initial traceroute (parallel — completes in ~discoveryTimeout) ───────
	fmt.Println("Discovering route (parallel scan)…")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, st := range states {
		st.runner = traceroute.NewRunner(prober, st.ip, traceroute.Options{
			MaxHops: cfg.MaxHops,
			Timeout: discoveryTimeout,
			Retries: 1,
		})
	}

	resCh := make(chan struct {
		state *targetState
		hops  []*traceroute.Hop
		err   error
	}, len(states))
	for _, st := range states {
		go func(s *targetState) {
			hops, err := s.runner.Run(ctx)
			resCh <- struct {
				state *targetState
				hops  []*traceroute.Hop
				err   error
			}{state: s, hops: hops, err: err}
		}(st)
	}

	for i := 0; i < len(states); i++ {
		res := <-resCh
		if res.err != nil {
			fmt.Fprintf(os.Stderr, "traceroute error (%s): %v\n", res.state.name, res.err)
		}
		if len(res.hops) == 0 {
			res.state.session.SetTTLIP(1, res.state.ip)
		}
		for _, h := range res.hops {
			res.state.session.SetTTLIP(h.TTL, h.IP)
			if h.State == traceroute.HopDestination {
				res.state.session.SetDestinationTTL(h.TTL)
			}
		}
	}

	// Switch runners to the configured timeout for subsequent refresh scans.
	for _, st := range states {
		st.runner = traceroute.NewRunner(prober, st.ip, traceroute.Options{
			MaxHops: cfg.MaxHops,
			Timeout: cfg.Timeout,
			Retries: 2,
		})
	}

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
	quitCh := make(chan struct{}, 1)

	if term.IsTerminal(int(os.Stdin.Fd())) {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err == nil {
			defer term.Restore(int(os.Stdin.Fd()), state)
			go watchQuitKey(quitCh)
		}
	}

	var wg sync.WaitGroup
	var seqCounter uint32

	// ── Probe loop ───────────────────────────────────────────────────────────
	for _, st := range states {
		state := st
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
					runProbeRound(ctx, prober, state.session, state.ip, cfg, &seqCounter)
				}
			}
		}()
	}

	// ── Route-refresh loop ───────────────────────────────────────────────────
	for _, st := range states {
		state := st
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
					newHops, rerr := state.runner.Run(ctx)
					if rerr != nil {
						continue
					}
					for _, h := range newHops {
						state.session.SetTTLIP(h.TTL, h.IP)
						if h.State == traceroute.HopDestination {
							state.session.SetDestinationTTL(h.TTL)
						}
					}
					if state.runner.ConsumeChanged() {
						state.session.RecordRouteChange()
					}
				}
			}
		}()
	}

	// ── DNS enrichment loop ──────────────────────────────────────────────────
	if cfg.ResolveDNS {
		for _, st := range states {
			state := st
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
						enrichDNS(ctx, state.session, state.resolver)
					}
				}
			}()
		}
	}

	// ── Render loop ──────────────────────────────────────────────────────────
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(cfg.RenderInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				views := make([]panelView, 0, len(states))
				for i, st := range states {
					routeChanged := st.runner.ConsumeChanged()
					if routeChanged {
						st.session.RecordRouteChange()
					}
					snaps := st.session.Snapshot()
					applyDiff(snaps, baseline, st.ip)
					sum := st.session.Summary()
					loss, avg, ok := panelMetrics(snaps)
					views = append(views, panelView{
						Panel: renderer.Panel{
							Title:        panelTitle(st.name, st.ip),
							Snaps:        snaps,
							Summary:      sum,
							RouteChanged: routeChanged,
						},
						Loss:  loss,
						Avg:   avg,
						Has:   ok,
						Order: i,
					})
				}
				panels := sortPanels(views, cfg.PanelSort)
				rend.Render(panels)
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
					for _, st := range states {
						runExport(st.session, exporters)
					}
				}
			}
		}()
	}

	// ── Adaptive probing ─────────────────────────────────────────────────────
	if cfg.Adaptive {
		for _, st := range states {
			state := st
			wg.Add(1)
			go func() {
				defer wg.Done()
				adaptiveLoop(ctx, state.session, cfg)
			}()
		}
	}

	// Wait for SIGINT / SIGTERM / Q
	select {
	case <-sigCh:
	case <-quitCh:
	}
	fmt.Fprintln(os.Stderr, "\nShutting down…")
	cancel()
	wg.Wait()

	if len(exporters) > 0 {
		for _, st := range states {
			runExport(st.session, exporters)
		}
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

	workerCount := cfg.ProbeWorkers
	if workerCount > len(snaps) {
		workerCount = len(snaps)
	}
	if workerCount < 1 {
		workerCount = 1
	}

	jobs := make(chan metrics.HopSnapshot)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for snap := range jobs {
				if snap.TTL == 0 {
					continue
				}
				ttl := snap.TTL
				seq := uint16(atomic.AddUint32(seqCounter, 1))
				result, err := prober.Probe(ctx, targetIP, ttl, seq, cfg.Timeout)
				if err != nil || result == nil {
					continue
				}
				if result.Success && result.Reached {
					session.SetDestinationTTL(ttl)
				}
				session.Record(ttl, result.RespondingIP, result.RTT, result.Success)
			}
		}()
	}
	for _, snap := range snaps {
		if snap.TTL == 0 {
			continue
		}
		jobs <- snap
	}
	close(jobs)
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

func applyDiff(snaps []metrics.HopSnapshot, baseline *diff.Baseline, target net.IP) {
	if baseline == nil {
		return
	}
	if baseline.Target != "" && baseline.Target != target.String() {
		return
	}
	for i := range snaps {
		base, ok := baseline.Hops[snaps[i].TTL]
		if !ok {
			continue
		}
		snaps[i].HasDiff = true
		snaps[i].DiffLoss = snaps[i].Loss - base.Loss
		if snaps[i].Recv > 0 {
			snaps[i].DiffAvgRTT = snaps[i].AvgRTT - base.AvgRTT
		}
	}
}

func panelTitle(name string, ip net.IP) string {
	if ip == nil {
		return name
	}
	if name == ip.String() {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, ip.String())
}

func watchQuitKey(quitCh chan<- struct{}) {
	buf := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		if buf[0] == 'q' || buf[0] == 'Q' {
			select {
			case quitCh <- struct{}{}:
			default:
			}
			return
		}
	}
}

type panelView struct {
	renderer.Panel
	Loss  float64
	Avg   time.Duration
	Has   bool
	Order int
}

func panelMetrics(snaps []metrics.HopSnapshot) (float64, time.Duration, bool) {
	for i := len(snaps) - 1; i >= 0; i-- {
		s := snaps[i]
		if s.Recv > 0 || (s.Sent > 0 && s.IP != nil) {
			return s.Loss, s.AvgRTT, true
		}
	}
	return 0, 0, false
}

func sortPanels(views []panelView, mode string) []renderer.Panel {
	if mode != "loss" && mode != "avg" {
		panels := make([]renderer.Panel, 0, len(views))
		for _, v := range views {
			panels = append(panels, v.Panel)
		}
		return panels
	}

	sort.SliceStable(views, func(i, j int) bool {
		a, b := views[i], views[j]
		if a.Has != b.Has {
			return a.Has
		}
		if !a.Has && !b.Has {
			return a.Order < b.Order
		}
		if mode == "loss" {
			if a.Loss == b.Loss {
				return a.Order < b.Order
			}
			return a.Loss > b.Loss
		}
		if a.Avg == b.Avg {
			return a.Order < b.Order
		}
		return a.Avg > b.Avg
	})

	panels := make([]renderer.Panel, 0, len(views))
	for _, v := range views {
		panels = append(panels, v.Panel)
	}
	return panels
}
