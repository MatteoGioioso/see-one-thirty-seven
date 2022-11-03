package dcs_proxy

import "context"

// Seeone should be able to work, with some compromise, also when the dcs is down,
// the Proxy will control the behaviour when the DCS is suspected to be down.
// The implementation should also have circuit breaker

type Proxy interface {
	GetRole(ctx context.Context) (string, error)
	SaveInstanceInfo(ctx context.Context, role string) error
}
