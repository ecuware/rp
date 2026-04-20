//go:build windows

package probe

// WindowsICMPProber uses the Windows IcmpSendEcho2 kernel API (iphlpapi.dll)
// instead of raw sockets.  This is the same mechanism used by Windows tracert.
//
// Why: Windows Vista+ prevents user-space raw sockets from receiving ICMP
// Time Exceeded (type 11) messages — so the generic ICMPProber can reach the
// target (Echo Reply works) but never hears back from intermediate routers.
// IcmpSendEcho2 operates inside the kernel's ICMP module and is not subject
// to that filtering.  It also works without Administrator privileges.

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	// IcmpSendEcho (not IcmpSendEcho2) — the simpler synchronous API.
	// IcmpSendEcho2's Event/APC parameters caused spurious failures on
	// some Windows configurations; IcmpSendEcho is always synchronous and
	// works reliably for per-call handles.
	procIcmpSendEcho = iphlpapi.NewProc("IcmpSendEcho")
)

// Windows ICMP status codes (from ipexport.h / Iphlpapi.h).
const (
	ipSuccess             uint32 = 0
	ipDestNetUnreachable  uint32 = 11002
	ipDestHostUnreachable uint32 = 11003
	ipDestPortUnreachable uint32 = 11005
	ipReqTimedOut         uint32 = 11010 // IP_REQ_TIMED_OUT
	ipTTLExpiredTransit   uint32 = 11013 // IP_TTL_EXPIRED_TRANSIT ← key for traceroute
	ipTTLExpiredReassem   uint32 = 11014 // IP_TTL_EXPIRED_REASSEM
)

// ipOptionInformation mirrors IP_OPTION_INFORMATION from iphlpapi.h.
type ipOptionInformation struct {
	Ttl         uint8
	Tos         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData uintptr
}

// icmpEchoReply mirrors ICMP_ECHO_REPLY from iphlpapi.h (32-bit address fields).
type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       ipOptionInformation
}

// WindowsICMPProber implements Prober using IcmpSendEcho2.
// The struct holds no shared state — every Probe() call opens its own
// IcmpCreateFile handle so concurrent goroutines never share an ICMP
// identifier and responses are always dispatched to the correct caller.
type WindowsICMPProber struct{}

// NewWindowsICMPProber verifies the API is available by opening and closing
// a test handle, then returns the prober.
func NewWindowsICMPProber() (*WindowsICMPProber, error) {
	h, _, err := procIcmpCreateFile.Call()
	if h == 0 {
		return nil, fmt.Errorf("IcmpCreateFile: %w", err)
	}
	procIcmpCloseHandle.Call(h)
	return &WindowsICMPProber{}, nil
}

func (p *WindowsICMPProber) Name() string { return "ICMP(win)" }
func (p *WindowsICMPProber) Close() error { return nil }

// Probe sends one ICMP Echo to target with the given TTL and returns the
// result.  A fresh IcmpCreateFile handle is opened per call so concurrent
// goroutines each get a unique ICMP identifier — they never interfere.
func (p *WindowsICMPProber) Probe(
	ctx context.Context,
	target net.IP,
	ttl int,
	seq uint16,
	timeout time.Duration,
) (*Result, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Open a per-call handle so each concurrent probe gets its own ICMP id.
	h, _, err := procIcmpCreateFile.Call()
	if h == 0 {
		return nil, fmt.Errorf("IcmpCreateFile: %w", err)
	}
	defer procIcmpCloseHandle.Call(h)

	t4 := target.To4()
	if t4 == nil {
		return nil, fmt.Errorf("only IPv4 targets supported")
	}
	destAddr := binary.LittleEndian.Uint32(t4)

	opts := ipOptionInformation{Ttl: uint8(ttl)}

	// Payload — 32 bytes of recognisable data.
	reqData := []byte("rp-probe-data-padding000")

	// Reply buffer: sizeof(ICMP_ECHO_REPLY) + RequestSize + 8 (ICMP error) + 16 (IO_STATUS_BLOCK).
	replyBuf := make([]byte, int(unsafe.Sizeof(icmpEchoReply{}))+len(reqData)+8+16)

	timeoutMs := uint32(timeout.Milliseconds())
	if timeoutMs == 0 {
		timeoutMs = 3000
	}

	sentAt := time.Now()
	ret, _, _ := procIcmpSendEcho.Call(
		h,
		uintptr(destAddr),
		uintptr(unsafe.Pointer(&reqData[0])),
		uintptr(len(reqData)),
		uintptr(unsafe.Pointer(&opts)),
		uintptr(unsafe.Pointer(&replyBuf[0])),
		uintptr(len(replyBuf)),
		uintptr(timeoutMs),
	)
	rtt := time.Since(sentAt)

	if ret == 0 {
		// Timeout or error — no response.
		return &Result{TTL: ttl, Success: false}, nil
	}

	reply := (*icmpEchoReply)(unsafe.Pointer(&replyBuf[0]))

	// Decode responding IP (little-endian uint32 → net.IP).
	a := reply.Address
	respIP := net.IP{byte(a), byte(a >> 8), byte(a >> 16), byte(a >> 24)}

	switch reply.Status {
	case ipSuccess:
		return &Result{
			TTL: ttl, RespondingIP: respIP, RTT: rtt,
			Success: true, Reached: true,
		}, nil

	case ipTTLExpiredTransit, ipTTLExpiredReassem:
		// Intermediate router replied with TTL Exceeded.
		return &Result{
			TTL: ttl, RespondingIP: respIP, RTT: rtt,
			Success: true, Reached: false,
		}, nil

	case ipDestNetUnreachable, ipDestHostUnreachable, ipDestPortUnreachable:
		// Destination unreachable — treat as "reached" (end of path).
		return &Result{
			TTL: ttl, RespondingIP: respIP, RTT: rtt,
			Success: true, Reached: true,
		}, nil

	default:
		return &Result{TTL: ttl, Success: false}, nil
	}
}
