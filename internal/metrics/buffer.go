// Package metrics provides per-hop time-series statistics.
package metrics

import (
	"sync"
	"time"
)

// Sample is one RTT measurement.
type Sample struct {
	RTT     time.Duration
	Success bool
	At      time.Time
}

// CircularBuffer is a fixed-capacity ring buffer of Samples.
// All methods are safe for concurrent use.
type CircularBuffer struct {
	mu       sync.RWMutex
	data     []Sample
	head     int // next write position
	count    int // number of valid entries (≤ cap)
	capacity int
}

// NewCircularBuffer creates a buffer with the given capacity.
func NewCircularBuffer(capacity int) *CircularBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &CircularBuffer{
		data:     make([]Sample, capacity),
		capacity: capacity,
	}
}

// Push adds a sample, overwriting the oldest entry when full.
func (b *CircularBuffer) Push(s Sample) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data[b.head] = s
	b.head = (b.head + 1) % b.capacity
	if b.count < b.capacity {
		b.count++
	}
}

// Samples returns a copy of all samples in chronological order (oldest first).
func (b *CircularBuffer) Samples() []Sample {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.count == 0 {
		return nil
	}

	out := make([]Sample, b.count)
	if b.count < b.capacity {
		// Buffer not yet full; data is at indices [0, count)
		copy(out, b.data[:b.count])
		return out
	}
	// Buffer is full; oldest entry is at b.head
	n := copy(out, b.data[b.head:])
	copy(out[n:], b.data[:b.head])
	return out
}

// Len returns the current number of stored samples.
func (b *CircularBuffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}

func (b *CircularBuffer) Cap() int {
	return b.capacity
}

func (b *CircularBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head = 0
	b.count = 0
}

// RecentRTTs returns up to n most-recent RTT values for successful probes only.
func (b *CircularBuffer) RecentRTTs(n int) []time.Duration {
	samples := b.Samples()
	out := make([]time.Duration, 0, n)
	for i := len(samples) - 1; i >= 0 && len(out) < n; i-- {
		if samples[i].Success {
			out = append([]time.Duration{samples[i].RTT}, out...)
		}
	}
	return out
}

// RecentLosses returns up to n most-recent loss samples (1=loss, 0=success).
func (b *CircularBuffer) RecentLosses(n int) []float64 {
	samples := b.Samples()
	out := make([]float64, 0, n)
	for i := len(samples) - 1; i >= 0 && len(out) < n; i-- {
		v := 0.0
		if !samples[i].Success {
			v = 1.0
		}
		out = append([]float64{v}, out...)
	}
	return out
}
