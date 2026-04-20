//go:build windows

package probe

import (
	"context"
	"fmt"
	"net"
	"time"
	"unsafe"
)

var (
	procIcmp6CreateFile  = iphlpapi.NewProc("Icmp6CreateFile")
	procIcmp6SendEcho2   = iphlpapi.NewProc("Icmp6SendEcho2")
	procIcmp6CloseHandle = iphlpapi.NewProc("Icmp6CloseHandle")
)

type icmp6EchoReply struct {
	SourceAddress      [16]byte
	DestinationAddress [16]byte
	Status             uint32
	RoundTripTime      uint32
	DataSize           uint16
	Reserved           uint16
	Data               uintptr
	Options            ipOptionInformation6
}

type ipOptionInformation6 struct {
	Ttl         uint8
	Tos         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData uintptr
}

type WindowsICMPv6Prober struct{}

func NewWindowsICMPv6Prober() (*WindowsICMPv6Prober, error) {
	h, _, err := procIcmp6CreateFile.Call()
	if h == 0 {
		return nil, fmt.Errorf("Icmp6CreateFile: %w", err)
	}
	procIcmp6CloseHandle.Call(h)
	return &WindowsICMPv6Prober{}, nil
}

func (p *WindowsICMPv6Prober) Name() string { return "ICMPv6(win)" }
func (p *WindowsICMPv6Prober) Close() error { return nil }

func (p *WindowsICMPv6Prober) Probe(
	ctx context.Context,
	target net.IP,
	ttl int,
	seq uint16,
	timeout time.Duration,
) (*Result, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	h, _, err := procIcmp6CreateFile.Call()
	if h == 0 {
		return nil, fmt.Errorf("Icmp6CreateFile: %w", err)
	}
	defer procIcmp6CloseHandle.Call(h)

	if len(target) != 16 {
		return nil, fmt.Errorf("invalid IPv6 address length")
	}

	srcAddr := [16]byte{}
	destAddr := [16]byte{}
	copy(destAddr[:], target)

	opts := ipOptionInformation6{Ttl: uint8(ttl)}

	reqData := []byte("rp-ipv6-probe-data-padding")

	replyBuf := make([]byte, int(unsafe.Sizeof(icmp6EchoReply{}))+len(reqData)+8+16)

	timeoutMs := uint32(timeout.Milliseconds())
	if timeoutMs == 0 {
		timeoutMs = 3000
	}

	sentAt := time.Now()
	ret, _, _ := procIcmp6SendEcho2.Call(
		h,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&srcAddr)),
		uintptr(unsafe.Pointer(&destAddr)),
		uintptr(unsafe.Pointer(&reqData[0])),
		uintptr(len(reqData)),
		uintptr(unsafe.Pointer(&opts)),
		uintptr(unsafe.Pointer(&replyBuf[0])),
		uintptr(len(replyBuf)),
		uintptr(timeoutMs),
	)
	rtt := time.Since(sentAt)

	if ret == 0 {
		return &Result{TTL: ttl, Success: false}, nil
	}

	reply := (*icmp6EchoReply)(unsafe.Pointer(&replyBuf[0]))
	respIP := net.IP(reply.SourceAddress[:])

	switch reply.Status {
	case ipSuccess:
		return &Result{
			TTL: ttl, RespondingIP: respIP, RTT: rtt,
			Success: true, Reached: true,
		}, nil

	case ipTTLExpiredTransit, ipTTLExpiredReassem:
		return &Result{
			TTL: ttl, RespondingIP: respIP, RTT: rtt,
			Success: true, Reached: false,
		}, nil

	case ipDestNetUnreachable, ipDestHostUnreachable, ipDestPortUnreachable:
		return &Result{
			TTL: ttl, RespondingIP: respIP, RTT: rtt,
			Success: true, Reached: true,
		}, nil

	default:
		return &Result{TTL: ttl, Success: false}, nil
	}
}
