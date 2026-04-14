package metrics

import (
	"math"
	"net"
	"testing"
	"time"
)

func TestHopMetrics_BasicStats(t *testing.T) {
	h := NewHopMetrics(1, 100)
	ip := net.ParseIP("192.168.1.1")

	rtts := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
	}

	for _, rtt := range rtts {
		h.Record(ip, rtt, true)
	}

	snap := h.Snapshot()

	if snap.Sent != 3 {
		t.Errorf("Sent = %d, want 3", snap.Sent)
	}
	if snap.Recv != 3 {
		t.Errorf("Recv = %d, want 3", snap.Recv)
	}
	if snap.Loss != 0 {
		t.Errorf("Loss = %v, want 0", snap.Loss)
	}
	if snap.MinRTT != 10*time.Millisecond {
		t.Errorf("MinRTT = %v, want 10ms", snap.MinRTT)
	}
	if snap.MaxRTT != 30*time.Millisecond {
		t.Errorf("MaxRTT = %v, want 30ms", snap.MaxRTT)
	}
	wantAvg := 20 * time.Millisecond
	if snap.AvgRTT != wantAvg {
		t.Errorf("AvgRTT = %v, want %v", snap.AvgRTT, wantAvg)
	}
	if snap.LastRTT != 30*time.Millisecond {
		t.Errorf("LastRTT = %v, want 30ms", snap.LastRTT)
	}
}

func TestHopMetrics_PacketLoss(t *testing.T) {
	h := NewHopMetrics(2, 50)
	ip := net.ParseIP("10.0.0.1")

	// 2 success, 1 failure = 33.3% loss
	h.Record(ip, 5*time.Millisecond, true)
	h.Record(ip, 0, false)
	h.Record(ip, 7*time.Millisecond, true)

	snap := h.Snapshot()

	if snap.Sent != 3 {
		t.Errorf("Sent = %d, want 3", snap.Sent)
	}
	if snap.Recv != 2 {
		t.Errorf("Recv = %d, want 2", snap.Recv)
	}

	wantLoss := 1.0 / 3.0
	if math.Abs(snap.Loss-wantLoss) > 0.001 {
		t.Errorf("Loss = %v, want ~%v", snap.Loss, wantLoss)
	}
}

func TestHopMetrics_Jitter(t *testing.T) {
	h := NewHopMetrics(1, 100)
	ip := net.ParseIP("1.1.1.1")

	// All identical RTTs → jitter should be near 0
	for i := 0; i < 10; i++ {
		h.Record(ip, 10*time.Millisecond, true)
	}

	snap := h.Snapshot()
	if snap.Jitter > time.Microsecond {
		t.Errorf("Jitter for constant RTTs = %v, want ~0", snap.Jitter)
	}

	// Now add high variance
	h2 := NewHopMetrics(2, 100)
	h2.Record(ip, 1*time.Millisecond, true)
	h2.Record(ip, 100*time.Millisecond, true)

	snap2 := h2.Snapshot()
	if snap2.Jitter == 0 {
		t.Error("Jitter for variable RTTs should be > 0")
	}
}

func TestHopMetrics_Hostname(t *testing.T) {
	h := NewHopMetrics(3, 10)
	h.SetHostname("router.example.com")

	snap := h.Snapshot()
	if snap.Hostname != "router.example.com" {
		t.Errorf("Hostname = %q, want %q", snap.Hostname, "router.example.com")
	}
	if snap.DisplayName() != "router.example.com" {
		t.Errorf("DisplayName() = %q, want hostname", snap.DisplayName())
	}
}

func TestHopMetrics_NoData(t *testing.T) {
	h := NewHopMetrics(5, 10)
	snap := h.Snapshot()

	if snap.Sent != 0 || snap.Recv != 0 {
		t.Error("fresh HopMetrics should have zero counts")
	}
	if snap.DisplayIP() != "???" {
		t.Errorf("DisplayIP() with no IP = %q, want '???'", snap.DisplayIP())
	}
}
