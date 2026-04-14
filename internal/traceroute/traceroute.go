package traceroute

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alptekinsunnetci/netplotter/internal/probe"
)

// Options controls traceroute behaviour.
type Options struct {
	MaxHops int
	Timeout time.Duration
	Retries int // probes per TTL before giving up
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{
		MaxHops: 30,
		Timeout: 3 * time.Second,
		Retries: 2,
	}
}

// Runner performs traceroutes and tracks route changes.
type Runner struct {
	prober probe.Prober
	opts   Options
	target net.IP

	mu      sync.RWMutex
	hops    []*Hop
	changed bool
}

// NewRunner creates a Runner for the given target.
func NewRunner(p probe.Prober, target net.IP, opts Options) *Runner {
	return &Runner{
		prober: p,
		opts:   opts,
		target: target,
	}
}

// Run performs a fully-parallel traceroute: all TTLs are probed simultaneously.
// Total wall-clock time ≈ one timeout window (~1-1.5 s) regardless of hop count,
// instead of Retries × Timeout × MaxHops for the old sequential approach.
func (r *Runner) Run(ctx context.Context) ([]*Hop, error) {
	type indexed struct {
		ttl int
		hop *Hop
	}

	resultCh := make(chan indexed, r.opts.MaxHops)
	seqBase := uint16(0xA000)

	// pathLen tracks the minimum TTL that reached the target.
	// Initialise to MaxHops+1 so any real destination TTL beats it.
	var pathLen int32 = int32(r.opts.MaxHops) + 1

	var wg sync.WaitGroup
	for ttl := 1; ttl <= r.opts.MaxHops; ttl++ {
		wg.Add(1)
		go func(ttl int) {
			defer wg.Done()

			// Skip TTLs beyond the already-discovered path.
			if int32(ttl) > atomic.LoadInt32(&pathLen) {
				resultCh <- indexed{ttl: ttl, hop: &Hop{
					TTL:          ttl,
					State:        HopNoReply,
					DiscoveredAt: time.Now(),
				}}
				return
			}

			hop := r.probeHop(ctx, ttl, seqBase+uint16(ttl))
			if hop.State == HopDestination {
				// Atomically record the shortest path seen so far.
				for {
					old := atomic.LoadInt32(&pathLen)
					if int32(ttl) >= old {
						break
					}
					if atomic.CompareAndSwapInt32(&pathLen, old, int32(ttl)) {
						break
					}
				}
			}
			resultCh <- indexed{ttl: ttl, hop: hop}
		}(ttl)
	}

	// Close resultCh once all goroutines finish.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	raw := make([]*Hop, r.opts.MaxHops)
	for ih := range resultCh {
		raw[ih.ttl-1] = ih.hop
	}

	// Determine actual path length (minimum TTL that reached the target).
	actualLen := int(atomic.LoadInt32(&pathLen))
	if actualLen > r.opts.MaxHops {
		// Target never reached — show all probed hops.
		actualLen = r.opts.MaxHops
	}

	hops := make([]*Hop, actualLen)
	for i := 0; i < actualLen; i++ {
		if raw[i] != nil {
			hops[i] = raw[i]
		} else {
			hops[i] = &Hop{TTL: i + 1, State: HopNoReply, DiscoveredAt: time.Now()}
		}
	}

	r.mu.Lock()
	changed := r.routeChanged(hops)
	r.hops = hops
	r.changed = changed
	r.mu.Unlock()

	return hops, nil
}

// probeHop sends up to opts.Retries probes for a specific TTL.
func (r *Runner) probeHop(ctx context.Context, ttl int, seq uint16) *Hop {
	hop := &Hop{
		TTL:          ttl,
		State:        HopUnknown,
		DiscoveredAt: time.Now(),
	}

	for attempt := 0; attempt < r.opts.Retries; attempt++ {
		if ctx.Err() != nil {
			break
		}
		result, err := r.prober.Probe(ctx, r.target, ttl, seq+uint16(attempt), r.opts.Timeout)
		if err != nil || result == nil || !result.Success {
			continue
		}
		hop.IP = result.RespondingIP
		hop.LastSeen = time.Now()
		if result.Reached {
			hop.State = HopDestination
		} else {
			hop.State = HopIntermediate
		}
		return hop
	}

	hop.State = HopNoReply
	return hop
}

// Hops returns the current path snapshot (copy).
func (r *Runner) Hops() []*Hop {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make([]*Hop, len(r.hops))
	copy(cp, r.hops)
	return cp
}

// ConsumeChanged returns true (and resets the flag) if the route changed.
func (r *Runner) ConsumeChanged() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.changed
	r.changed = false
	return c
}

func (r *Runner) routeChanged(newHops []*Hop) bool {
	if len(newHops) != len(r.hops) {
		return true
	}
	for i := range newHops {
		n, o := newHops[i], r.hops[i]
		if n == nil && o == nil {
			continue
		}
		if n == nil || o == nil {
			return true
		}
		if !n.IP.Equal(o.IP) {
			return true
		}
	}
	return false
}

// ResolveTarget resolves a hostname/IP string to an IPv4 address.
func ResolveTarget(host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
		return ip, nil
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				return v4, nil
			}
		}
	}
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("no addresses found for %q", host)
}
