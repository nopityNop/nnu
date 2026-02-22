//go:build !windows

package traceroute

import (
	"errors"

	coretraceroute "github.com/nopityNop/nnu/internal/core/traceroute"
)

func NewAdapter() (coretraceroute.Adapter, error) {
	return nil, errors.New("windows adapter unavailable on this platform")
}
