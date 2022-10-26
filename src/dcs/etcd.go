package dcs

// https://akazuko.medium.com/leader-election-using-etcd-10301473843c
// https://medium.com/@felipedutratine/leader-election-in-go-with-etcd-2ca8f3876d79

import (
	"context"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"strings"
)

const (
	hostnameKey  = "hostname"
	roleKey      = "role"
	bootstrapKey = "bootstrap"
)

type Etcd struct {
	Log *logrus.Entry

	cli             *clientv3.Client
	electionSession *concurrency.Session
	instanceSession *concurrency.Session
	election        *concurrency.Election
	instanceID      string
	hostname        string
	lease           int
}

func NewEtcdImpl(endpoints []string, config Config, log *logrus.Entry) (*Etcd, error) {
	var cli *Etcd
	err := retry.Do(func() error {
		impl, err := newEtcdImpl(endpoints, config.Hostname, config.InstanceID, config.Lease, log)
		if err != nil {
			return err
		}

		cli = impl
		return nil
	})
	if err != nil {
		return nil, err
	}

	return cli, nil
}

func newEtcdImpl(endpoints []string, hostname string, instanceID string, lease int, log *logrus.Entry) (*Etcd, error) {
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	if err != nil {
		return nil, err
	}

	leaderSession, err := concurrency.NewSession(cli, concurrency.WithTTL(lease))
	if err != nil {
		return nil, err
	}

	instanceSession, err := concurrency.NewSession(cli, concurrency.WithTTL(3600))
	if err != nil {
		return nil, err
	}

	return &Etcd{
		cli:             cli,
		electionSession: leaderSession,
		instanceSession: instanceSession,
		election:        concurrency.NewElection(leaderSession, postgresql.LeaderElectionPrefix),
		hostname:        hostname,
		instanceID:      instanceID,
		lease:           lease,
		Log:             log,
	}, nil
}

func (e *Etcd) SetInstanceID(instanceID string) {
	e.instanceID = instanceID
}

func (e *Etcd) GetRole(ctx context.Context) (string, error) {
	leader, err := e.election.Leader(ctx)
	if err != nil {
		return "", err
	}
	if string(leader.Kvs[0].Value) == e.instanceID {
		return postgresql.Leader, nil
	} else {
		return postgresql.Replica, nil
	}
}

func (e *Etcd) AmITheLeader(ctx context.Context) (bool, error) {
	leader, err := e.election.Leader(ctx)
	if err != nil {
		return false, err
	}
	if string(leader.Kvs[0].Value) == e.instanceID {
		return true, nil
	} else {
		return false, nil
	}
}

func (e *Etcd) StartElection(ctx context.Context) error {
	go e.election.Campaign(ctx, e.instanceID)
	return nil
}

func (e *Etcd) SyncInstanceInfo(ctx context.Context, role string) error {
	if err := e.saveInstanceProp(ctx, hostnameKey, e.hostname); err != nil {
		return err
	}

	if err := e.saveInstanceProp(ctx, roleKey, role); err != nil {
		return err
	}

	return nil
}

func (e *Etcd) SetBootstrapped(ctx context.Context) error {
	if err := e.saveInstanceProp(ctx, bootstrapKey, "1"); err != nil {
		return err
	}

	return nil
}

func (e *Etcd) IsBootstrapped(ctx context.Context) (bool, error) {
	getResponse, err := e.instanceSession.Client().Get(ctx, e.getInstanceProKey(bootstrapKey))
	if err != nil {
		return false, err
	}

	if len(getResponse.Kvs) == 0 {
		return false, nil
	}

	if string(getResponse.Kvs[0].Value) == "1" {
		return true, nil
	} else {
		return false, nil
	}
}

func (e *Etcd) GetLeaderInfo(ctx context.Context) (InstanceInfo, error) {
	var i InstanceInfo
	err := retry.Do(
		func() error {
			iTry, err := e.getLeaderInfo(ctx)
			if err != nil {
				return err
			}
			i = iTry

			return nil
		},
		retry.OnRetry(func(n uint, err error) {
			e.Log.Warningf("leader not yet found in dcs, retry: %v", n)
		}),
	)
	if err != nil {
		return InstanceInfo{}, err
	}

	return i, nil
}

func (e *Etcd) getLeaderInfo(ctx context.Context) (InstanceInfo, error) {
	leader, err := e.election.Leader(ctx)
	if err != nil {
		return InstanceInfo{}, err
	}

	leaderID := string(leader.Kvs[0].Value)
	leaderInfo, err := e.getInstanceInfo(ctx, leaderID)
	if err != nil {
		return InstanceInfo{}, err
	}

	return leaderInfo, nil
}

func (e *Etcd) GetInstanceInfo(ctx context.Context) (InstanceInfo, error) {
	return e.getInstanceInfoWithRetry(ctx, e.instanceID)
}

func (e *Etcd) getInstanceInfoWithRetry(ctx context.Context, instanceID string) (InstanceInfo, error) {
	var i InstanceInfo
	err := retry.Do(
		func() error {
			iTry, err := e.getInstanceInfo(ctx, instanceID)
			if err != nil {
				return err
			}
			i = iTry

			return nil
		},
		retry.OnRetry(func(n uint, err error) {
			e.Log.Warningf("instance info not yet found in dcs, retry: %v", n)
		}),
	)
	if err != nil {
		return InstanceInfo{}, err
	}

	return i, nil
}

func (e *Etcd) getInstanceInfo(ctx context.Context, instanceID string) (InstanceInfo, error) {
	response, err := e.instanceSession.Client().Get(
		ctx,
		fmt.Sprintf("%v/%v", postgresql.InstanceInfoPrefix, instanceID),
		clientv3.WithPrefix(),
	)
	if err != nil {
		return InstanceInfo{}, err
	}

	if response.Count == 0 {
		return InstanceInfo{}, fmt.Errorf("instance info with id %v not found", instanceID)
	}

	i := InstanceInfo{
		ID: instanceID,
	}
	for _, kv := range response.Kvs {
		switch strings.Split(string(kv.Key), "/")[3] {
		case hostnameKey:
			i.Hostname = string(kv.Value)
		case roleKey:
			i.Role = string(kv.Value)
		}
	}

	return i, nil
}

func (e *Etcd) saveInstanceProp(ctx context.Context, prop, val string) error {
	if err := e.putKeyValWithLease(ctx, e.getInstanceProKey(prop), val); err != nil {
		return err
	}

	return nil
}

func (e *Etcd) putKeyValWithLease(ctx context.Context, key, val string) error {
	if _, err := e.instanceSession.Client().Put(
		ctx,
		key,
		val,
	); err != nil {
		return err
	}

	return nil
}

func (e *Etcd) setInstanceRole(ctx context.Context, role string) error {
	if err := e.saveInstanceProp(ctx, roleKey, role); err != nil {
		return err
	}

	return nil
}

func (e *Etcd) getInstanceKey() string {
	return fmt.Sprintf("%v/%v", postgresql.InstanceInfoPrefix, e.instanceID)
}

func (e *Etcd) getInstanceProKey(prop string) string {
	return fmt.Sprintf("%v/%v/%v", postgresql.InstanceInfoPrefix, e.instanceID, prop)
}
