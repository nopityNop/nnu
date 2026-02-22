package traceroute

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Hop struct {
	TTL      int
	Address  string
	Name     string
	Sent     int
	Received int
	Loss     float64
	Min      time.Duration
	Avg      time.Duration
	Max      time.Duration
}

type hopSlot struct {
	mu   sync.RWMutex
	data hopStats
}

func (h *hopSlot) addSent() {
	h.mu.Lock()
	h.data.sent++
	h.mu.Unlock()
}

func (h *hopSlot) record(reply EchoResult, name string) {
	h.mu.Lock()
	h.data.record(reply, name)
	h.mu.Unlock()
}

func (h *hopSlot) hop(ttl int) Hop {
	h.mu.RLock()
	result := h.data.hop()
	h.mu.RUnlock()
	result.TTL = ttl
	return result
}

func (h *hopSlot) hasHostname() bool {
	h.mu.RLock()
	set := h.data.hostname != ""
	h.mu.RUnlock()
	return set
}

type Snapshot struct {
	Host        string
	Destination netip.Addr
	Pass        int
	Timestamp   time.Time
	Hops        []Hop
}

type Config struct {
	MaxHops    int
	Timeout    time.Duration
	Interval   time.Duration
	PacketSize int
	Probes     int
	Passes     int
	UseDNS     bool
}

func DefaultConfig() Config {
	return Config{
		MaxHops:    30,
		Timeout:    5 * time.Second,
		Interval:   time.Second,
		PacketSize: 64,
		Probes:     1,
		Passes:     0,
		UseDNS:     false,
	}
}

type Adapter interface {
	Echo(ctx context.Context, addr netip.Addr, ttl int, payload []byte, timeout time.Duration) (EchoResult, error)
	Close() error
}

type EchoResult struct {
	Address netip.Addr
	RTT     time.Duration
	Reached bool
}

type Resolver interface {
	Resolve(ctx context.Context, host string) (netip.Addr, error)
}

type Clock func() time.Time

type Engine struct {
	cfg      Config
	adapter  Adapter
	resolver Resolver
	clock    Clock
	useDNS   bool
	dnsMu    sync.RWMutex
	dnsCache map[string]string
	limit    atomic.Int32
}

var ErrTimeout = errors.New("icmp timeout")

func NewEngine(cfg Config, adapter Adapter, resolver Resolver, clock Clock) (*Engine, error) {
	if adapter == nil {
		return nil, errors.New("adapter is nil")
	}
	if resolver == nil {
		return nil, errors.New("resolver is nil")
	}
	if clock == nil {
		clock = time.Now
	}
	if cfg.MaxHops < 1 {
		cfg.MaxHops = DefaultConfig().MaxHops
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultConfig().Timeout
	}
	if cfg.Interval < 0 {
		cfg.Interval = 0
	}
	if cfg.PacketSize < 1 {
		cfg.PacketSize = DefaultConfig().PacketSize
	}
	if cfg.Probes < 1 {
		cfg.Probes = DefaultConfig().Probes
	}
	return &Engine{
		cfg:      cfg,
		adapter:  adapter,
		resolver: resolver,
		clock:    clock,
		useDNS:   cfg.UseDNS,
		dnsCache: make(map[string]string),
	}, nil
}

func (e *Engine) Trace(ctx context.Context, host string) (<-chan Snapshot, error) {
	if e == nil {
		return nil, errors.New("tracer is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if host == "" {
		return nil, errors.New("empty host")
	}
	addr, err := e.resolver.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make(chan Snapshot)
	go e.loop(ctx, host, addr, out)
	return out, nil
}

func (e *Engine) loop(ctx context.Context, host string, addr netip.Addr, out chan<- Snapshot) {
	defer close(out)
	slots := make([]hopSlot, e.cfg.MaxHops)
	payload := make([]byte, e.cfg.PacketSize)
	e.fillPayload(payload)
	e.limit.Store(int32(len(slots)))
	pass := 0
	probes := e.cfg.Probes
	if probes < 1 {
		probes = 1
	}
	for {
		if ctx.Err() != nil {
			return
		}
		pass++
		limit := int(e.limit.Load())
		if limit < 1 {
			limit = 1
		}
		if limit > len(slots) {
			limit = len(slots)
		}
		if !e.runPass(ctx, addr, payload, slots, limit, probes) {
			return
		}
		if !e.sendSnapshot(ctx, host, addr, pass, slots, out) {
			return
		}
		if e.cfg.Passes > 0 && pass >= e.cfg.Passes {
			return
		}
		if !e.waitForNextPass(ctx) {
			return
		}
	}
}

func (e *Engine) runPass(ctx context.Context, addr netip.Addr, payload []byte, slots []hopSlot, limit int, probes int) bool {
	passCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errCh := make(chan error, limit)
	for i := 0; i < limit; i++ {
		ttl := i + 1
		wg.Add(1)
		go func(idx int, hopTTL int) {
			defer wg.Done()
			if err := e.runProbeBurst(passCtx, addr, hopTTL, payload, &slots[idx], probes); err != nil {
				select {
				case errCh <- err:
				default:
				}
				cancel()
			}
		}(i, ttl)
	}
	wg.Wait()
	close(errCh)
	return len(errCh) == 0
}

func (e *Engine) waitForNextPass(ctx context.Context) bool {
	interval := e.cfg.Interval
	if interval <= 0 {
		return true
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (e *Engine) runProbeBurst(ctx context.Context, addr netip.Addr, ttl int, payload []byte, slot *hopSlot, probes int) error {
	for i := 0; i < probes; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slot.addSent()
		reply, err := e.adapter.Echo(ctx, addr, ttl, payload, e.cfg.Timeout)
		switch {
		case err == nil:
			name := ""
			if e.useDNS && !slot.hasHostname() {
				name = e.lookupHostname(ctx, reply.Address)
			}
			slot.record(reply, name)
			if reply.Reached {
				e.updateLimit(ttl)
			}
		case errors.Is(err, ErrTimeout):
		default:
			return err
		}
	}
	return nil
}

func (e *Engine) sendSnapshot(ctx context.Context, host string, addr netip.Addr, pass int, slots []hopSlot, out chan<- Snapshot) bool {
	limit := int(e.limit.Load())
	if limit < 1 {
		limit = 1
	}
	if limit > len(slots) {
		limit = len(slots)
	}
	snapshot := e.snapshot(host, addr, pass, slots, limit)
	select {
	case <-ctx.Done():
		return false
	case out <- snapshot:
		return true
	}
}

func (e *Engine) snapshot(host string, addr netip.Addr, pass int, slots []hopSlot, limit int) Snapshot {
	hops := make([]Hop, limit)
	for i := 0; i < limit; i++ {
		hops[i] = slots[i].hop(i + 1)
	}
	return Snapshot{
		Host:        host,
		Destination: addr,
		Pass:        pass,
		Timestamp:   e.clock().UTC(),
		Hops:        hops,
	}
}

func (e *Engine) updateLimit(ttl int) {
	if ttl < 1 {
		ttl = 1
	}
	for {
		current := int(e.limit.Load())
		if ttl >= current {
			return
		}
		if e.limit.CompareAndSwap(int32(current), int32(ttl)) {
			return
		}
	}
}

func (e *Engine) fillPayload(buf []byte) {
	for i := range buf {
		buf[i] = 32
	}
}

type hopStats struct {
	address  string
	hostname string
	sent     int
	received int
	total    time.Duration
	min      time.Duration
	max      time.Duration
}

func (h *hopStats) record(reply EchoResult, name string) {
	h.received++
	h.total += reply.RTT
	if h.min == 0 || reply.RTT < h.min {
		h.min = reply.RTT
	}
	if reply.RTT > h.max {
		h.max = reply.RTT
	}
	if h.address == "" && reply.Address.IsValid() {
		h.address = reply.Address.String()
	}
	if h.hostname == "" && name != "" {
		h.hostname = name
	}
}

func (h hopStats) hop() Hop {
	loss := lossPercent(h.sent, h.received)
	result := Hop{
		Address:  h.address,
		Name:     h.hostname,
		Sent:     h.sent,
		Received: h.received,
		Loss:     loss,
	}
	if h.received > 0 {
		result.Min = h.min
		result.Max = h.max
		result.Avg = h.total / time.Duration(h.received)
	}
	if result.Address == "" {
		result.Address = "*"
	}
	return result
}

func lossPercent(sent, received int) float64 {
	if sent == 0 {
		return 0
	}
	return 100 - (100*float64(received))/float64(sent)
}

func (e *Engine) lookupHostname(ctx context.Context, addr netip.Addr) string {
	if !addr.IsValid() {
		return ""
	}
	key := addr.String()
	e.dnsMu.RLock()
	name, ok := e.dnsCache[key]
	e.dnsMu.RUnlock()
	if ok {
		return name
	}
	resolver := net.DefaultResolver
	ctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()
	names, err := resolver.LookupAddr(ctx, key)
	if err != nil || len(names) == 0 {
		name = ""
	} else {
		name = strings.TrimSuffix(names[0], ".")
	}
	e.dnsMu.Lock()
	e.dnsCache[key] = name
	e.dnsMu.Unlock()
	return name
}

type NetResolver struct{}

func (NetResolver) Resolve(ctx context.Context, host string) (netip.Addr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return toIPv4(ip)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, addr := range addrs {
		if v := addr.IP.To4(); v != nil {
			return toIPv4(addr.IP)
		}
	}
	return netip.Addr{}, errors.New("no ipv4 address")
}

func toIPv4(ip net.IP) (netip.Addr, error) {
	v4 := ip.To4()
	if v4 == nil {
		return netip.Addr{}, errors.New("non ipv4 address")
	}
	return netip.AddrFrom4([4]byte{v4[0], v4[1], v4[2], v4[3]}), nil
}

func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	return e.adapter.Close()
}
