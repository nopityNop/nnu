//go:build !windows

package traceroute

import (
	"context"
	"errors"
	"net/netip"
	"time"

	coretraceroute "github.com/nopityNop/nnu/internal/core/traceroute"
)

type Adapter struct{}

func NewAdapter() (*Adapter, error) {
	return nil, errors.New("platform not supported")
}

func (a *Adapter) Echo(ctx context.Context, addr netip.Addr, ttl int, payload []byte, timeout time.Duration) (coretraceroute.EchoResult, error) {
	return coretraceroute.EchoResult{}, errors.New("platform not supported")
}

func (a *Adapter) Close() error {
	return nil
}
