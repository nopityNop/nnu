//go:build windows

package traceroute

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"
	"unsafe"

	coretraceroute "github.com/nopityNop/nnu/internal/core/traceroute"
	"golang.org/x/sys/windows"
)

var (
	modIphlpapi         = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = modIphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = modIphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho2Ex = modIphlpapi.NewProc("IcmpSendEcho2Ex")
)

const (
	ipSuccess           = 0
	ipTtlExpiredTransit = 11013
	ipTtlExpiredReassem = 11014
	ipReqTimedOut       = 11010
	ipFlagDontFragment  = 0x02
)

type ipOptionInformation struct {
	Ttl         uint8
	Tos         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData uintptr
}

type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       ipOptionInformation
}

type windowsAdapter struct {
	handle windows.Handle
}

func NewAdapter() (coretraceroute.Adapter, error) {
	handle, err := createIcmpHandle()
	if err != nil {
		return nil, err
	}
	return &windowsAdapter{handle: handle}, nil
}

func createIcmpHandle() (windows.Handle, error) {
	handle, _, err := procIcmpCreateFile.Call()
	if handle == 0 {
		if err != nil && err != windows.ERROR_SUCCESS {
			return 0, err
		}
		return 0, errors.New("icmp create failed")
	}
	return windows.Handle(handle), nil
}

func (w *windowsAdapter) Close() error {
	if w == nil || w.handle == 0 {
		return nil
	}
	_, _, err := procIcmpCloseHandle.Call(uintptr(w.handle))
	w.handle = 0
	if err != nil && err != windows.ERROR_SUCCESS {
		return err
	}
	return nil
}

func (w *windowsAdapter) Echo(ctx context.Context, addr netip.Addr, ttl int, payload []byte, timeout time.Duration) (coretraceroute.EchoResult, error) {
	if err := ctx.Err(); err != nil {
		return coretraceroute.EchoResult{}, err
	}
	res, err := w.send(addr, ttl, payload, timeout)
	if err != nil {
		return coretraceroute.EchoResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return coretraceroute.EchoResult{}, err
	}
	return res, nil
}

func (w *windowsAdapter) send(addr netip.Addr, ttl int, payload []byte, timeout time.Duration) (coretraceroute.EchoResult, error) {
	if w == nil || w.handle == 0 {
		return coretraceroute.EchoResult{}, errors.New("adapter closed")
	}
	if !addr.Is4() {
		return coretraceroute.EchoResult{}, errors.New("ipv4 required")
	}
	if ttl < 1 || ttl > 255 {
		return coretraceroute.EchoResult{}, errors.New("invalid ttl")
	}
	req := ensurePayload(payload)
	reply := make([]byte, replyBufferLen(len(req)))
	opt := ipOptionInformation{Ttl: uint8(ttl), Flags: ipFlagDontFragment}
	dst := addr.As4()
	dest := binary.LittleEndian.Uint32(dst[:])
	count, err := w.callEcho(dest, req, reply, &opt, timeout)
	if err != nil {
		return coretraceroute.EchoResult{}, err
	}
	if count == 0 {
		return coretraceroute.EchoResult{}, coretraceroute.ErrTimeout
	}
	return parseReply(reply)
}

func ensurePayload(src []byte) []byte {
	if len(src) > 0 {
		return src
	}
	return []byte{0}
}

func replyBufferLen(reqLen int) int {
	base := int(unsafe.Sizeof(icmpEchoReply{}))
	return base + reqLen
}

func (w *windowsAdapter) callEcho(dest uint32, payload, reply []byte, opt *ipOptionInformation, timeout time.Duration) (uint32, error) {
	reqPtr := uintptr(unsafe.Pointer(&payload[0]))
	replyPtr := uintptr(unsafe.Pointer(&reply[0]))
	optPtr := uintptr(unsafe.Pointer(opt))
	ms := timeout.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	ret, _, err := procIcmpSendEcho2Ex.Call(
		uintptr(w.handle),
		0,
		0,
		0,
		0,
		uintptr(dest),
		reqPtr,
		uintptr(len(payload)),
		optPtr,
		replyPtr,
		uintptr(len(reply)),
		uintptr(ms),
	)
	if ret == 0 {
		errno, ok := err.(windows.Errno)
		if ok && errno == windows.Errno(ipReqTimedOut) {
			return 0, coretraceroute.ErrTimeout
		}
		if err != nil {
			return 0, err
		}
		return 0, errors.New("icmp send failed")
	}
	return uint32(ret), nil
}

func parseReply(buf []byte) (coretraceroute.EchoResult, error) {
	if len(buf) < int(unsafe.Sizeof(icmpEchoReply{})) {
		return coretraceroute.EchoResult{}, errors.New("reply too small")
	}
	rep := (*icmpEchoReply)(unsafe.Pointer(&buf[0]))
	addr, err := addrFromUint32(rep.Address)
	if err != nil {
		return coretraceroute.EchoResult{}, err
	}
	latency := time.Duration(rep.RoundTripTime) * time.Millisecond
	switch rep.Status {
	case ipSuccess:
		return coretraceroute.EchoResult{Address: addr, RTT: latency, Reached: true}, nil
	case ipTtlExpiredTransit, ipTtlExpiredReassem:
		return coretraceroute.EchoResult{Address: addr, RTT: latency, Reached: false}, nil
	case ipReqTimedOut:
		return coretraceroute.EchoResult{}, coretraceroute.ErrTimeout
	default:
		return coretraceroute.EchoResult{}, fmt.Errorf("icmp error %d", rep.Status)
	}
}

func addrFromUint32(v uint32) (netip.Addr, error) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	addr := netip.AddrFrom4(b)
	if !addr.IsValid() {
		return netip.Addr{}, errors.New("invalid address")
	}
	return addr, nil
}
