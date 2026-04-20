package probe

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

const (
	protocolICMP = 1 // IP protocol number for ICMPv4
)

// waiter is a pending probe waiting for a reply.
type waiter struct {
	ch  chan *Result
	seq uint16
	ttl int
	at  time.Time
}

// ICMPProber implements Prober using raw ICMP sockets.
// It requires elevated privileges (root on Linux/macOS, Administrator on Windows).
type ICMPProber struct {
	conn *icmp.PacketConn
	pc   *ipv4.PacketConn // obtained via conn.IPv4PacketConn()
	id   uint16           // ICMP identifier (our PID & 0xffff)
	seq  uint32           // atomic sequence counter

	sendMu  sync.Mutex // serialises SetTTL + WriteTo to avoid TTL race
	mu      sync.Mutex
	waiters map[uint16]*waiter
	done    chan struct{}
}

// NewICMPProber creates a new ICMP prober and starts the receiver goroutine.
// Returns an error if privileges are insufficient.
func NewICMPProber() (*ICMPProber, error) {
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, fmt.Errorf("ICMP socket open failed (requires root/admin): %w", err)
	}

	// Use the PacketConn already set up inside icmp.PacketConn — do NOT call
	// ipv4.NewPacketConn(conn) because *icmp.PacketConn does not implement net.Conn.
	pc := conn.IPv4PacketConn()
	if pc == nil {
		conn.Close()
		return nil, fmt.Errorf("IPv4 packet control unavailable (non-IPv4 connection?)")
	}

	p := &ICMPProber{
		conn:    conn,
		pc:      pc,
		id:      uint16(os.Getpid() & 0xffff),
		waiters: make(map[uint16]*waiter),
		done:    make(chan struct{}),
	}

	go p.receive()
	return p, nil
}

func (p *ICMPProber) Name() string { return "ICMP" }

// Probe sends one ICMP Echo Request to target with the given TTL and waits for a reply.
func (p *ICMPProber) Probe(ctx context.Context, target net.IP, ttl int, seq uint16, timeout time.Duration) (*Result, error) {
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   int(p.id),
			Seq:  int(seq),
			Data: []byte("rp000000000000000000000000"),
		},
	}

	wb, err := msg.Marshal(nil)
	if err != nil {
		return nil, fmt.Errorf("marshal ICMP: %w", err)
	}

	// Register waiter BEFORE sending so the receiver can never miss the reply.
	ch := make(chan *Result, 1)
	w := &waiter{ch: ch, seq: seq, ttl: ttl}

	p.mu.Lock()
	p.waiters[seq] = w
	p.mu.Unlock()

	dst := &net.IPAddr{IP: target}

	// SetTTL and WriteTo must be atomic so concurrent Probe calls with
	// different TTLs don't clobber each other.
	p.sendMu.Lock()
	setErr := p.pc.SetTTL(ttl)
	sentAt := time.Now()
	w.at = sentAt
	_, sendErr := p.conn.WriteTo(wb, dst)
	p.sendMu.Unlock()

	if setErr != nil || sendErr != nil {
		p.mu.Lock()
		delete(p.waiters, seq)
		p.mu.Unlock()
		if sendErr != nil {
			return &Result{TTL: ttl, At: sentAt, Err: sendErr}, nil
		}
		// SetTTL failed — the packet was sent with the default TTL.
		// Traceroute accuracy is compromised but we continue.
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	select {
	case r := <-ch:
		r.At = sentAt
		return r, nil
	case <-deadline.C:
		p.mu.Lock()
		delete(p.waiters, seq)
		p.mu.Unlock()
		return &Result{TTL: ttl, At: sentAt, Success: false}, nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.waiters, seq)
		p.mu.Unlock()
		return nil, ctx.Err()
	}
}

// receive is the single goroutine that reads all incoming ICMP packets and
// dispatches them to the appropriate waiting Probe call.
func (p *ICMPProber) receive() {
	buf := make([]byte, 1500)
	for {
		select {
		case <-p.done:
			return
		default:
		}

		// Short read deadline so we can check for shutdown without blocking forever.
		_ = p.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, peer, err := p.conn.ReadFrom(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return // socket closed
		}

		recvAt := time.Now()

		// FIX 1 — Windows raw ICMP sockets sometimes deliver packets with the
		// outer IPv4 header prepended (unlike Linux which strips it).  Detect
		// the IPv4 version nibble and skip the header so icmp.ParseMessage
		// always receives a clean ICMP payload.
		raw := buf[:n]
		if len(raw) >= 20 && raw[0]>>4 == 4 {
			ihl := int(raw[0]&0x0f) * 4
			if ihl < len(raw) {
				raw = raw[ihl:]
			}
		}

		msg, err := icmp.ParseMessage(protocolICMP, raw)
		if err != nil {
			continue
		}

		switch msg.Type {
		case ipv4.ICMPTypeEchoReply:
			echo, ok := msg.Body.(*icmp.Echo)
			if !ok || uint16(echo.ID) != p.id {
				continue
			}
			p.dispatch(uint16(echo.Seq), &Result{
				RespondingIP: parseIP(peer),
				Success:      true,
				Reached:      true,
			}, recvAt)

		case ipv4.ICMPTypeTimeExceeded:
			te, ok := msg.Body.(*icmp.TimeExceeded)
			if !ok {
				continue
			}
			// FIX 2 — Do NOT filter by inner ICMP ID for Time Exceeded.
			// On Windows the kernel may rewrite the ICMP identifier, and the
			// seq number alone is sufficient to match our pending waiters.
			seq, _, ok := extractInnerICMP(te.Data)
			if !ok {
				continue
			}
			p.dispatch(seq, &Result{
				RespondingIP: parseIP(peer),
				Success:      true,
				Reached:      false,
			}, recvAt)

		case ipv4.ICMPTypeDestinationUnreachable:
			du, ok := msg.Body.(*icmp.DstUnreach)
			if !ok {
				continue
			}
			seq, _, ok := extractInnerICMP(du.Data)
			if !ok {
				continue
			}
			p.dispatch(seq, &Result{
				RespondingIP: parseIP(peer),
				Success:      true,
				Reached:      true,
			}, recvAt)
		}
	}
}

func (p *ICMPProber) dispatch(seq uint16, r *Result, recvAt time.Time) {
	p.mu.Lock()
	w, ok := p.waiters[seq]
	if ok {
		delete(p.waiters, seq)
	}
	p.mu.Unlock()

	if ok {
		r.TTL = w.ttl
		r.RTT = recvAt.Sub(w.at)
		select {
		case w.ch <- r:
		default:
		}
	}
}

func (p *ICMPProber) Close() error {
	close(p.done)
	return p.conn.Close()
}

// NextSeq returns a monotonically increasing 16-bit sequence number.
func (p *ICMPProber) NextSeq() uint16 {
	return uint16(atomic.AddUint32(&p.seq, 1))
}

// extractInnerICMP parses the "data" field of an ICMP Time Exceeded /
// Unreachable body and extracts the (seq, id) from the embedded original
// ICMP Echo Request header.
//
// FIX 3 — The golang.org/x/net/icmp library strips the 4 "unused" bytes from
// the Time Exceeded body before storing it in TimeExceeded.Data, so Data
// should start directly with the original IP header.  However some Windows
// builds of the library (or kernel quirks) leave those 4 bytes in place.
// We therefore try both offsets: [0] and [4].
func extractInnerICMP(data []byte) (seq uint16, id uint16, ok bool) {
	if seq, id, ok = parseInnerAt(data, 0); ok {
		return
	}
	if seq, id, ok = parseInnerAt(data, 4); ok {
		return
	}
	return 0, 0, false
}

// parseInnerAt attempts to parse an original-IP-header + ICMP-Echo header
// starting at byte offset `off` within data.
func parseInnerAt(data []byte, off int) (seq uint16, id uint16, ok bool) {
	if len(data) < off+28 {
		return
	}
	d := data[off:]
	// First byte must look like an IPv4 header (version nibble = 4).
	if d[0]>>4 != 4 {
		return
	}
	ihl := int(d[0]&0x0f) * 4
	if len(d) < ihl+8 {
		return
	}
	inner := d[ihl:]
	if inner[0] != 8 { // original packet must be an ICMP Echo Request
		return
	}
	id = binary.BigEndian.Uint16(inner[4:6])
	seq = binary.BigEndian.Uint16(inner[6:8])
	ok = true
	return
}

func parseIP(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPAddr:
		return v.IP
	case *net.UDPAddr:
		return v.IP
	case *net.TCPAddr:
		return v.IP
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return net.ParseIP(addr.String())
	}
	return net.ParseIP(host)
}
