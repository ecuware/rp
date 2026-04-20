package metrics

import (
	"net"
	"sync"
	"time"
)

// Session tracks metrics for all hops in a monitoring session.
type Session struct {
	mu      sync.RWMutex
	hops    map[int]*HopMetrics // keyed by TTL
	bufCap  int
	target  net.IP
	startAt time.Time

	totalSent int
	totalRecv int

	routeChanges   int
	destinationTTL int // TTL at which target was first reached (0 = unknown)
}

// NewSession creates a new Session.
func NewSession(target net.IP, bufCap int) *Session {
	return &Session{
		hops:    make(map[int]*HopMetrics),
		bufCap:  bufCap,
		target:  target,
		startAt: time.Now(),
	}
}

// Record adds a probe result for the given TTL.
func (s *Session) Record(ttl int, ip net.IP, rtt time.Duration, success bool) {
	s.mu.Lock()
	h, ok := s.hops[ttl]
	if !ok {
		h = NewHopMetrics(ttl, s.bufCap)
		s.hops[ttl] = h
	}
	s.totalSent++
	if success {
		s.totalRecv++
	}
	s.mu.Unlock()

	h.Record(ip, rtt, success)
}

// SetHostname updates the hostname for the hop at the given TTL.
func (s *Session) SetHostname(ttl int, name string) {
	s.mu.RLock()
	h, ok := s.hops[ttl]
	s.mu.RUnlock()
	if ok {
		h.SetHostname(name)
	}
}

// SetTTLIP ensures a hop entry exists for the given TTL and IP.
func (s *Session) SetTTLIP(ttl int, ip net.IP) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hops[ttl]; !ok {
		h := NewHopMetrics(ttl, s.bufCap)
		h.IP = ip
		s.hops[ttl] = h
	}
}

// SetDestinationTTL records the TTL at which the target was reached.
// Only shrinks (the shortest path wins).
func (s *Session) SetDestinationTTL(ttl int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destinationTTL == 0 || ttl < s.destinationTTL {
		s.destinationTTL = ttl
	}
}

// DestinationTTL returns the known path length (0 if not yet discovered).
func (s *Session) DestinationTTL() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.destinationTTL
}

// Snapshot returns snapshots for all known hops in TTL order.
// When destinationTTL is known, only hops up to (and including) that TTL are returned.
func (s *Session) Snapshot() []HopSnapshot {
	s.mu.RLock()
	maxTTL := 0
	for ttl := range s.hops {
		if ttl > maxTTL {
			maxTTL = ttl
		}
	}
	// Clamp to discovered path length.
	if s.destinationTTL > 0 && s.destinationTTL < maxTTL {
		maxTTL = s.destinationTTL
	}
	hops := make(map[int]*HopMetrics, len(s.hops))
	for k, v := range s.hops {
		hops[k] = v
	}
	s.mu.RUnlock()

	snaps := make([]HopSnapshot, maxTTL)
	for i := range snaps {
		ttl := i + 1
		if h, ok := hops[ttl]; ok {
			snaps[i] = h.Snapshot()
		} else {
			snaps[i] = HopSnapshot{TTL: ttl}
		}
	}
	return snaps
}

// RecordRouteChange notes that the route changed.
func (s *Session) RecordRouteChange() {
	s.mu.Lock()
	s.routeChanges++
	s.mu.Unlock()
}

// SessionSummary holds overall session statistics.
type SessionSummary struct {
	Target         net.IP
	StartAt        time.Time
	Duration       time.Duration
	TotalSent      int
	TotalRecv      int
	RouteChanges   int
	DestinationTTL int
}

// Summary returns the current session summary.
func (s *Session) Summary() SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SessionSummary{
		Target:         s.target,
		StartAt:        s.startAt,
		Duration:       time.Since(s.startAt),
		TotalSent:      s.totalSent,
		TotalRecv:      s.totalRecv,
		RouteChanges:   s.routeChanges,
		DestinationTTL: s.destinationTTL,
	}
}

// Uptime returns elapsed time since the session started.
func (s *Session) Uptime() time.Duration {
	return time.Since(s.startAt)
}

func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.hops {
		h.Reset()
	}
	s.totalSent = 0
	s.totalRecv = 0
	s.routeChanges = 0
	s.startAt = time.Now()
}
