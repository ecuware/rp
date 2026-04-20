package metrics

import (
	"math"
	"net"
	"sync"
	"time"
)

// HopMetrics tracks all statistics for one network hop.
type HopMetrics struct {
	mu sync.RWMutex

	TTL      int
	IP       net.IP
	Hostname string

	// counters
	sent int
	recv int

	// running stats
	minRTT  time.Duration
	maxRTT  time.Duration
	sumRTT  time.Duration // for average
	sumSqRT float64       // for jitter/stddev (nanoseconds²)
	lastRTT time.Duration

	// time-series
	buf *CircularBuffer

	// for anomaly detection
	lastLossSpike time.Time
	lastLatSpike  time.Time
	anomalyWindow int // consecutive anomalies
}

// NewHopMetrics creates a HopMetrics with the given circular-buffer capacity.
func NewHopMetrics(ttl int, bufCap int) *HopMetrics {
	return &HopMetrics{
		TTL:    ttl,
		minRTT: math.MaxInt64,
		buf:    NewCircularBuffer(bufCap),
	}
}

// Record adds one probe result.
func (h *HopMetrics) Record(ip net.IP, rtt time.Duration, success bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.sent++
	if ip != nil && (h.IP == nil || !h.IP.Equal(ip)) {
		h.IP = ip
	}

	h.buf.Push(Sample{RTT: rtt, Success: success, At: time.Now()})

	if !success {
		return
	}

	h.recv++
	h.lastRTT = rtt

	if rtt < h.minRTT {
		h.minRTT = rtt
	}
	if rtt > h.maxRTT {
		h.maxRTT = rtt
	}
	h.sumRTT += rtt
	ns := float64(rtt.Nanoseconds())
	h.sumSqRT += ns * ns
}

// Snapshot returns a read-only snapshot of current statistics.
func (h *HopMetrics) Snapshot() HopSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()

	snap := HopSnapshot{
		TTL:      h.TTL,
		IP:       h.IP,
		Hostname: h.Hostname,
		Sent:     h.sent,
		Recv:     h.recv,
		LastRTT:  h.lastRTT,
	}

	if h.sent > 0 {
		snap.Loss = float64(h.sent-h.recv) / float64(h.sent)
	}
	if h.recv > 0 {
		snap.MinRTT = h.minRTT
		snap.MaxRTT = h.maxRTT
		snap.AvgRTT = h.sumRTT / time.Duration(h.recv)

		// Jitter = standard deviation of RTT
		mean := float64(snap.AvgRTT.Nanoseconds())
		variance := h.sumSqRT/float64(h.recv) - mean*mean
		if variance > 0 {
			snap.Jitter = time.Duration(math.Sqrt(variance))
		}
	}

	snap.RecentRTTs = h.buf.RecentRTTs(60)
	snap.RecentLosses = h.buf.RecentLosses(60)
	return snap
}

// SetHostname updates the reverse-DNS name.
func (h *HopMetrics) SetHostname(name string) {
	h.mu.Lock()
	h.Hostname = name
	h.mu.Unlock()
}

// HopSnapshot is an immutable view of HopMetrics at a point in time.
type HopSnapshot struct {
	TTL      int
	IP       net.IP
	Hostname string

	Sent int
	Recv int
	Loss float64 // 0.0-1.0

	MinRTT  time.Duration
	MaxRTT  time.Duration
	AvgRTT  time.Duration
	LastRTT time.Duration
	Jitter  time.Duration

	HasDiff    bool
	DiffLoss   float64
	DiffAvgRTT time.Duration

	// RecentRTTs holds the last ≤60 successful RTTs for sparkline rendering.
	RecentRTTs []time.Duration
	// RecentLosses holds the last ≤60 loss samples (1=loss, 0=success).
	RecentLosses []float64
}

// DisplayIP returns the IP or "???" if unknown.
func (s HopSnapshot) DisplayIP() string {
	if s.IP != nil {
		return s.IP.String()
	}
	return "???"
}

// DisplayName returns hostname if resolved, otherwise IP.
func (s HopSnapshot) DisplayName() string {
	if s.Hostname != "" {
		return s.Hostname
	}
	return s.DisplayIP()
}

func (h *HopMetrics) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sent = 0
	h.recv = 0
	h.minRTT = math.MaxInt64
	h.maxRTT = 0
	h.sumRTT = 0
	h.sumSqRT = 0
	h.lastRTT = 0
	h.buf = NewCircularBuffer(h.buf.Cap())
}
