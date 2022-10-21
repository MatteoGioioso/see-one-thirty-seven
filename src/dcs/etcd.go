package dcs

// https://akazuko.medium.com/leader-election-using-etcd-10301473843c
// https://medium.com/@felipedutratine/leader-election-in-go-with-etcd-2ca8f3876d79

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

type EtcdImpl struct {
	cli        *clientv3.Client
	session    *concurrency.Session
	election   *concurrency.Election
	instanceID string
	hostname   string
}

func NewEtcdImpl(endpoints []string, hostname string, instanceID string) (*EtcdImpl, error) {
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	if err != nil {
		return nil, err
	}

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(5))
	if err != nil {
		return nil, err
	}

	return &EtcdImpl{
		cli:        cli,
		session:    session,
		election:   concurrency.NewElection(session, postgresql.LeaderElectionPrefix),
		hostname:   hostname,
		instanceID: instanceID,
	}, nil
}

func (e *EtcdImpl) SetInstanceID(instanceID string) {
	e.instanceID = instanceID
}

func (e *EtcdImpl) StartElection(ctx context.Context) error {
	go e.election.Campaign(ctx, e.instanceID)
	return nil
}

func (e *EtcdImpl) Observe(ctx context.Context, leaderFunc, replicaFunc func()) {
	for response := range e.election.Observe(ctx) {
		if string(response.Kvs[0].Value) == e.instanceID {
			if err := e.saveInstanceInfo(ctx, postgresql.Leader); err != nil {
				return
			}
			leaderFunc()
		} else {
			if err := e.saveInstanceInfo(ctx, postgresql.Replica); err != nil {
				return
			}
			replicaFunc()
		}
	}
}

func (e *EtcdImpl) GetLeaderInfo(ctx context.Context) (InstanceInfo, error) {
	response, err := e.cli.Get(ctx, fmt.Sprintf("%v/%v", postgresql.InstanceInfoPrefix, postgresql.Leader))
	if err != nil {
		return InstanceInfo{}, err
	}
	i := InstanceInfo{}

	if err := json.Unmarshal(response.Kvs[0].Value, &i); err != nil {
		return InstanceInfo{}, err
	}

	return i, err
}

func (e *EtcdImpl) saveInstanceInfo(ctx context.Context, role string) error {
	info := InstanceInfo{
		ID:       e.instanceID,
		Role:     role,
		Hostname: e.hostname,
	}
	infoBytes, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if _, err := e.cli.Put(
		ctx,
		fmt.Sprintf("%v/%v", postgresql.InstanceInfoPrefix, role),
		string(infoBytes),
	); err != nil {
		return err
	}

	return nil
}
