package dcs_proxy

import (
	"context"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
	"github.com/sony/gobreaker"
)

var (
	ErrLeaderWithoutDCS      = fmt.Errorf("postgres is currently acting as a leader, but dcs is faulty")
	ErrCouldNotEstablishRole = fmt.Errorf("could not establish role, either dcs is not reachable or postgres is not running")
)

type ProxyImpl struct {
	dcsClient  *dcs.Etcd
	postmaster postgresql.Postmaster
	cb         *gobreaker.CircuitBreaker
	log        *logrus.Entry
}

func New(dcsClient *dcs.Etcd, postmaster postgresql.Postmaster, log *logrus.Entry) ProxyImpl {
	return ProxyImpl{
		dcsClient:  dcsClient,
		postmaster: postmaster,
		log:        log,
	}
}

func (p *ProxyImpl) Connect(ctx context.Context) error {
	_, err := p.cb.Execute(func() (interface{}, error) {
		return nil, retry.Do(
			func() error {
				return p.dcsClient.Connect(ctx)
			},
			retry.Attempts(5),
			retry.OnRetry(func(n uint, err error) {
				p.log.Debugf("unable to connect to dcs: %v, retrying: %v/%v", err, n, 5)
			}),
		)
	})

	p.cb = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name: "DCS",
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			// When the DCS network connection is re-established it is necessary to start a new election,
			// as the old one won't be running anymore
			if from == gobreaker.StateHalfOpen && to == gobreaker.StateClosed {
				go p.dcsClient.StartElection(ctx)
			}
		},
	})

	return err
}

func (p *ProxyImpl) StartElection(ctx context.Context) {
	go p.dcsClient.StartElection(ctx)
}

func (p *ProxyImpl) GetRole(ctx context.Context) (string, error) {
	role, err := p.cb.Execute(func() (interface{}, error) {
		var roleCb string
		return roleCb, retry.Do(
			func() error {
				roleTry, err := p.dcsClient.GetRole(ctx)
				roleCb = roleTry
				return err
			},
			retry.Attempts(5),
			retry.OnRetry(func(n uint, err error) {
				p.log.Debugf("unable to get role from dcs: %v, retrying: %v/%v", err, n, 5)
			}),
		)
	})
	if err != nil {
		p.log.Warningf("could not get role from dcs: %v", err)
		// If we fail after the retries, we can assume that we lost network connection with the DCS, or it is crashed.
		if !p.postmaster.IsRunning() {
			return "", ErrCouldNotEstablishRole
		}

		isInRecovery, err := p.postmaster.IsInRecovery(ctx)
		if err != nil {
			return "", ErrCouldNotEstablishRole
		}

		if isInRecovery {
			p.log = p.log.WithField("role", postgresql.Replica)
			return postgresql.Replica, nil
		} else {
			// We will return an error here, this because it would be too dangerous to proceed
			// as we cannot know other instances statuses, possibly causing split-brain:
			// https://www.percona.com/blog/2020/03/26/split-brain-101-what-you-should-know/
			return postgresql.Leader, ErrLeaderWithoutDCS
		}
	}

	p.log = p.log.WithField("role", role.(string))
	return role.(string), err
}

func (p *ProxyImpl) GetLeaderInfo(ctx context.Context) (dcs.InstanceInfo, error) {
	var instanceInfo dcs.InstanceInfo

	_, err := p.cb.Execute(func() (interface{}, error) {
		return nil, retry.Do(
			func() error {
				leaderInfoTry, err := p.dcsClient.GetLeaderInfo(ctx)
				instanceInfo = leaderInfoTry
				return err
			},
			retry.Attempts(5),
			retry.OnRetry(func(n uint, err error) {
				p.log.Debugf("unable to get leader info from dcs: %v, retrying: %v/%v", err, n, 5)
			}),
		)
	})

	return instanceInfo, err
}

func (p *ProxyImpl) SyncInstanceInfo(ctx context.Context, role string) error {
	_, err := p.cb.Execute(func() (interface{}, error) {
		return nil, p.dcsClient.SyncInstanceInfo(ctx, role)
	})

	return err
}

func (p *ProxyImpl) IsRoleConsistent(ctx context.Context) (bool, error) {
	return false, nil
}
