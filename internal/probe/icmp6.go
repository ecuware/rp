//go:build !windows

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
	"golang.org/x/net/ipv6"
)

const (
	protocolICMPv6 = 58
)

type waiter6 struct {
	ch  chan *Result
	seq uint16
	ttl int
	at  time.Time
}

type ICMPv6Prober struct {
	conn *icmp.PacketConn
	pc   *ipv6.PacketConn
	id   uint16
	seq  uint32

	sendMu  sync.Mutex
	mu      sync.Mutex
	waiters map[uint16]*waiter6
	done    chan struct{}
}

func NewICMPv6Prober() (*ICMPv6Prober, error) {
	conn, err := icmp.ListenPacket("ip6:ipv6-icmp", "::")
	if err != nil {
		return nil, fmt.Errorf("ICMPv6 socket open failed (requires root/admin): %w", err)
	}

	pc := conn.IPv6PacketConn()
	if pc == nil {
		conn.Close()
		return nil, fmt.Errorf("IPv6 packet control unavailable")
	}

	p := &ICMPv6Prober{
		conn:    conn,
		pc:      pc,
		id:      uint16(os.Getpid() & 0xffff),
		waiters: make(map[uint16]*waiter6),
		done:    make(chan struct{}),
	}

	go p.receive()
	return p, nil
}

func (p *ICMPv6Prober) Name() string { return "ICMPv6" }

func (p *ICMPv6Prober) Probe(ctx context.Context, target net.IP, ttl int, seq uint16, timeout time.Duration) (*Result, error) {
	msg := icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Code: 0,
		Body: &icmp.Echo{
			ID:   int(p.id),
			Seq:  int(seq),
			Data: []byte("rp-ipv6-000000000000000000"),
		},
	}

	wb, err := msg.Marshal(nil)
	if err != nil {
		return nil, fmt.Errorf("marshal ICMPv6: %w", err)
	}

	ch := make(chan *Result, 1)
	w := &waiter6{ch: ch, seq: seq, ttl: ttl}

	p.mu.Lock()
	p.waiters[seq] = w
	p.mu.Unlock()

	dst := &net.IPAddr{IP: target}

	p.sendMu.Lock()
	setErr := p.pc.SetHopLimit(ttl)
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

func (p *ICMPv6Prober) receive() {
	buf := make([]byte, 1500)
	for {
		select {
		case <-p.done:
			return
		default:
		}

		_ = p.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, peer, err := p.conn.ReadFrom(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}

		recvAt := time.Now()
		raw := buf[:n]

		msg, err := icmp.ParseMessage(protocolICMPv6, raw)
		if err != nil {
			continue
		}

		switch msg.Type {
		case ipv6.ICMPTypeEchoReply:
			echo, ok := msg.Body.(*icmp.Echo)
			if !ok || uint16(echo.ID) != p.id {
				continue
			}
			p.dispatch6(uint16(echo.Seq), &Result{
				RespondingIP: parseIP(peer),
				Success:      true,
				Reached:      true,
			}, recvAt)

		case ipv6.ICMPTypeTimeExceeded:
			te, ok := msg.Body.(*icmp.TimeExceeded)
			if !ok {
				continue
			}
			seq, ok := extractInnerICMPv6(te.Data)
			if !ok {
				continue
			}
			p.dispatch6(seq, &Result{
				RespondingIP: parseIP(peer),
				Success:      true,
				Reached:      false,
			}, recvAt)

		case ipv6.ICMPTypeDestinationUnreachable:
			du, ok := msg.Body.(*icmp.DstUnreach)
			if !ok {
				continue
			}
			seq, ok := extractInnerICMPv6(du.Data)
			if !ok {
				continue
			}
			p.dispatch6(seq, &Result{
				RespondingIP: parseIP(peer),
				Success:      true,
				Reached:      true,
			}, recvAt)
		}
	}
}

func (p *ICMPv6Prober) dispatch6(seq uint16, r *Result, recvAt time.Time) {
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

func (p *ICMPv6Prober) Close() error {
	close(p.done)
	return p.conn.Close()
}

func (p *ICMPv6Prober) NextSeq() uint16 {
	return uint16(atomic.AddUint32(&p.seq, 1))
}

func extractInnerICMPv6(data []byte) (seq uint16, ok bool) {
	if len(data) < 48 {
		return 0, false
	}

	off := 0
	if len(data) >= 4 && data[0]>>4 != 6 {
		off = 4
	}

	if len(data) < off+48 {
		return 0, false
	}

	d := data[off:]
	if d[0]>>4 != 6 {
		return 0, false
	}

	ihl := 40
	if len(d) < ihl+8 {
		return 0, false
	}

	inner := d[ihl:]
	if inner[0] != 128 {
		return 0, false
	}

	seq = binary.BigEndian.Uint16(inner[6:8])
	ok = true
	return
}
