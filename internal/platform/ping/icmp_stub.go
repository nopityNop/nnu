//go:build !windows

package ping

import (
	"errors"
	"time"
)

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

type Adapter struct{}

func NewAdapter() (*Adapter, error) {
	return nil, errors.New("platform not supported")
}

func (a *Adapter) Echo(host string, ttl int, timeout time.Duration) (EchoResult, error) {
	return EchoResult{}, errors.New("platform not supported")
}

func (a *Adapter) Close() error {
	return nil
}
