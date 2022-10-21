package dcs

// https://akazuko.medium.com/leader-election-using-etcd-10301473843c
// https://medium.com/@felipedutratine/leader-election-in-go-with-etcd-2ca8f3876d79

import (
	"context"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

const leaderElectionPrefix = "/postgresql-leader"

type EtcdImpl struct {
	cli      *clientv3.Client
	session  *concurrency.Session
	election *concurrency.Election
}

func NewEtcdImpl(endpoints []string) (*EtcdImpl, error) {
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	if err != nil {
		return nil, err
	}

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(5))
	if err != nil {
		return nil, err
	}

	return &EtcdImpl{
		cli:      cli,
		session:  session,
		election: concurrency.NewElection(session, leaderElectionPrefix),
	}, nil
}

func (e *EtcdImpl) StartElection(ctx context.Context, instanceId string) error {
	go e.election.Campaign(ctx, instanceId)
	return nil
}

func (e *EtcdImpl) AmITheLeader(ctx context.Context, instanceID string) (bool, error) {
	
	return false, nil
}

func (e *EtcdImpl) ObserveLoop(ctx context.Context, instanceID string, callback func() error) {
	for response := range e.election.Observe(ctx) {
		if string(response.Kvs[0].Value) == instanceID {

		}
	}
}
