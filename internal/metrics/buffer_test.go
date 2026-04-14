package metrics

import (
	"testing"
	"time"
)

func TestCircularBuffer_BasicPushAndSamples(t *testing.T) {
	buf := NewCircularBuffer(5)

	if buf.Len() != 0 {
		t.Fatalf("expected empty buffer, got %d", buf.Len())
	}

	for i := 0; i < 3; i++ {
		buf.Push(Sample{RTT: time.Duration(i+1) * time.Millisecond, Success: true})
	}

	if buf.Len() != 3 {
		t.Fatalf("expected 3 items, got %d", buf.Len())
	}

	samples := buf.Samples()
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(samples))
	}

	for i, s := range samples {
		want := time.Duration(i+1) * time.Millisecond
		if s.RTT != want {
			t.Errorf("sample[%d] RTT = %v, want %v", i, s.RTT, want)
		}
	}
}

func TestCircularBuffer_Wrap(t *testing.T) {
	cap := 4
	buf := NewCircularBuffer(cap)

	// Fill beyond capacity
	for i := 1; i <= 7; i++ {
		buf.Push(Sample{RTT: time.Duration(i) * time.Millisecond, Success: true})
	}

	if buf.Len() != cap {
		t.Fatalf("expected len=%d, got %d", cap, buf.Len())
	}

	// Oldest should be 4 (7-4+1), newest should be 7
	samples := buf.Samples()
	if len(samples) != cap {
		t.Fatalf("expected %d samples, got %d", cap, len(samples))
	}

	// After 7 pushes into cap-4 buffer, items 4,5,6,7 remain
	expectedFirst := 4 * time.Millisecond
	if samples[0].RTT != expectedFirst {
		t.Errorf("oldest sample RTT = %v, want %v", samples[0].RTT, expectedFirst)
	}
	expectedLast := 7 * time.Millisecond
	if samples[cap-1].RTT != expectedLast {
		t.Errorf("newest sample RTT = %v, want %v", samples[cap-1].RTT, expectedLast)
	}
}

func TestCircularBuffer_RecentRTTs(t *testing.T) {
	buf := NewCircularBuffer(10)

	// Push mixed success/failure
	for i := 1; i <= 5; i++ {
		buf.Push(Sample{RTT: time.Duration(i) * time.Millisecond, Success: true})
	}
	buf.Push(Sample{RTT: 100 * time.Millisecond, Success: false}) // should be excluded

	rtts := buf.RecentRTTs(10)
	if len(rtts) != 5 {
		t.Fatalf("expected 5 RTTs (failures excluded), got %d", len(rtts))
	}
}

func TestCircularBuffer_Clear(t *testing.T) {
	buf := NewCircularBuffer(5)
	for i := 0; i < 3; i++ {
		buf.Push(Sample{RTT: time.Millisecond, Success: true})
	}
	buf.Clear()
	if buf.Len() != 0 {
		t.Fatalf("expected empty after Clear, got %d", buf.Len())
	}
	if buf.Samples() != nil {
		t.Fatal("expected nil samples after Clear")
	}
}

func TestCircularBuffer_Concurrent(t *testing.T) {
	buf := NewCircularBuffer(100)
	done := make(chan struct{})

	// Writer goroutine
	go func() {
		for i := 0; i < 500; i++ {
			buf.Push(Sample{RTT: time.Duration(i) * time.Microsecond, Success: true})
		}
		close(done)
	}()

	// Reader goroutine — must not panic
	for i := 0; i < 100; i++ {
		_ = buf.Samples()
		_ = buf.Len()
	}

	<-done
}
