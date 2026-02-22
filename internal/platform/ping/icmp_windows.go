//go:build windows

package ping

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"time"
	"unsafe"
)

var (
	iphlpapi        = syscall.NewLazyDLL("iphlpapi.dll")
	icmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	icmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
	icmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
)

const replyBufferSize = 1024

type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       uint32
}

type EchoResult struct {
	Address string
	RTT     time.Duration
	Reached bool
}

func (r EchoResult) GetRTT() time.Duration {
	return r.RTT
}

func (r EchoResult) IsReached() bool {
	return r.Reached
}

type Adapter struct {
	handle syscall.Handle
}

func NewAdapter() (*Adapter, error) {
	handle, _, err := icmpCreateFile.Call()
	if handle == uintptr(syscall.InvalidHandle) {
		return nil, fmt.Errorf("failed to create ICMP file: %v", err)
	}
	return &Adapter{handle: syscall.Handle(handle)}, nil
}

func (a *Adapter) Echo(host string, ttl int, timeout time.Duration) (EchoResult, error) {
	if a.handle == syscall.InvalidHandle {
		return EchoResult{}, fmt.Errorf("invalid ICMP handle")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.LookupIP(host)
		if err != nil || len(addrs) == 0 {
			return EchoResult{}, fmt.Errorf("failed to resolve host: %s", host)
		}
		ip = addrs[0]
	}
	ip = ip.To4()
	if ip == nil {
		return EchoResult{}, fmt.Errorf("IPv6 not supported")
	}
	destAddr := binary.LittleEndian.Uint32(ip)
	requestData := []byte("NNU-Ping-Request")
	replyBuffer := make([]byte, replyBufferSize)
	timeoutMs := uint32(timeout.Milliseconds())
	if timeoutMs == 0 {
		timeoutMs = 1000
	}
	start := time.Now()
	ret, _, _ := icmpSendEcho.Call(
		uintptr(a.handle),
		uintptr(destAddr),
		uintptr(unsafe.Pointer(&requestData[0])),
		uintptr(len(requestData)),
		uintptr(0),
		uintptr(unsafe.Pointer(&replyBuffer[0])),
		uintptr(len(replyBuffer)),
		uintptr(timeoutMs),
	)
	elapsed := time.Since(start)
	if ret == 0 {
		return EchoResult{Address: host, RTT: timeout, Reached: false}, nil
	}
	reply := (*icmpEchoReply)(unsafe.Pointer(&replyBuffer[0]))
	if reply.Status != 0 {
		return EchoResult{Address: host, RTT: timeout, Reached: false}, nil
	}
	return EchoResult{Address: host, RTT: elapsed, Reached: true}, nil
}

func (a *Adapter) Close() error {
	if a.handle != syscall.InvalidHandle {
		icmpCloseHandle.Call(uintptr(a.handle))
		a.handle = syscall.InvalidHandle
	}
	return nil
}
