package dcs

// https://akazuko.medium.com/leader-election-using-etcd-10301473843c
// https://medium.com/@felipedutratine/leader-election-in-go-with-etcd-2ca8f3876d79

import (
	"context"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
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
	endpoints       []string
}

func NewEtcdImpl(endpoints []string, config Config, log *logrus.Entry) (*Etcd, error) {
	return newEtcdImpl(endpoints, config.Hostname, config.InstanceID, config.Lease, log)
}

func newEtcdImpl(endpoints []string, hostname string, instanceID string, lease int, log *logrus.Entry) (*Etcd, error) {
	return &Etcd{
		endpoints:  endpoints,
		hostname:   hostname,
		instanceID: instanceID,
		lease:      lease,
		Log:        log,
	}, nil
}

func (e *Etcd) Connect(ctx context.Context) error {
	cli, err := clientv3.New(clientv3.Config{Endpoints: e.endpoints})
	if err != nil {
		return err
	}

	leaderSession, err := concurrency.NewSession(cli, concurrency.WithTTL(e.lease))
	if err != nil {
		return err
	}

	instanceSession, err := concurrency.NewSession(cli, concurrency.WithTTL(3600))
	if err != nil {
		return err
	}

	e.cli = cli
	e.instanceSession = instanceSession
	e.electionSession = leaderSession
	e.election = concurrency.NewElection(leaderSession, postgresql.LeaderElectionPrefix)

	return nil
}

func (e *Etcd) GetRole(ctx context.Context) (string, error) {
	return e.getRole(ctx)
}

func (e *Etcd) StartElection(ctx context.Context) error {
	return e.election.Campaign(ctx, e.instanceID)
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
	return e.getLeaderInfo(ctx)
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
	return e.getInstanceInfo(ctx, e.instanceID)
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

func (e *Etcd) getRole(ctx context.Context) (string, error) {
	leader, err := e.election.Leader(ctx)
	if err != nil {
		return "", err
	}

	if len(leader.Kvs) == 0 {
		return "", fmt.Errorf("leader is not yet present in dcs")
	}

	if string(leader.Kvs[0].Value) == e.instanceID {
		return postgresql.Leader, nil
	} else {
		return postgresql.Replica, nil
	}
}

func (e *Etcd) saveInstanceProp(ctx context.Context, prop, val string) error {
	if err := e.putKeyVal(ctx, e.getInstanceProKey(prop), val); err != nil {
		return err
	}

	return nil
}

func (e *Etcd) putKeyVal(ctx context.Context, key, val string) error {
	if _, err := e.instanceSession.Client().Put(
		ctx,
		key,
		val,
	); err != nil {
		return err
	}

	return nil
}

func (e *Etcd) getInstanceProKey(prop string) string {
	return fmt.Sprintf("%v/%v/%v", postgresql.InstanceInfoPrefix, e.instanceID, prop)
}
