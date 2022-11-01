package dcs_proxy

import (
	"context"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/sony/gobreaker"
)

var (
	ErrLeaderWithoutDCS      = fmt.Errorf("postgres is currently acting as a leader, but dcs is faulty")
	ErrCouldNotEstablishRole = fmt.Errorf("could not establish role, either dcs is not reachable or postgres is not running")
)

type ProxyImpl struct {
	DcsClient  *dcs.Etcd
	Postmaster postgresql.Postmaster
	CB         *gobreaker.CircuitBreaker
}

func (p *ProxyImpl) GetRole(ctx context.Context) (string, error) {
	role, err := p.CB.Execute(func() (interface{}, error) {
		return p.getRole(ctx)
	})
	if err != nil {
		return "", err
	}

	return role.(string), err
}

func (p *ProxyImpl) getRole(ctx context.Context) (string, error) {
	role, err := p.DcsClient.GetRoleWithRetries(ctx)
	if err != nil {
		// If we fail after the retries, we can assume that we lost network connection with the DCS, or it is crashed.
		if !p.Postmaster.IsRunning() {
			return "", ErrCouldNotEstablishRole
		}

		isInRecovery, err := p.Postmaster.IsInRecovery(ctx)
		if err != nil {
			return "", ErrCouldNotEstablishRole
		}

		if isInRecovery {
			return postgresql.Replica, nil
		} else {
			// We will return an error here, this because it would be too dangerous to proceed
			// as we cannot know other instances statuses, possibly causing split-brain:
			// https://www.percona.com/blog/2020/03/26/split-brain-101-what-you-should-know/
			return postgresql.Leader, ErrLeaderWithoutDCS
		}
	}

	return role, nil
}

func (p *ProxyImpl) SyncInstanceInfo(ctx context.Context, role string) error {
	_, err := p.CB.Execute(func() (interface{}, error) {
		return nil, p.DcsClient.SyncInstanceInfo(ctx, role)
	})
	if err != nil {
		return err
	}

	return nil
}
