package dcs

// https://akazuko.medium.com/leader-election-using-etcd-10301473843c
// https://medium.com/@felipedutratine/leader-election-in-go-with-etcd-2ca8f3876d79

import (
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

type EtcdImpl struct {
	cli     *clientv3.Client
	session *concurrency.Session
}

func NewEtcdImpl(endpoints []string) (*EtcdImpl, error) {
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	if err != nil {
		return nil, err
	}

	session, err := concurrency.NewSession(cli)
	if err != nil {
		return nil, err
	}

	return &EtcdImpl{
		cli:     cli,
		session: session,
	}, nil
}

func (e EtcdImpl) StartElection() {
	concurrency.NewElection(e.session, "")
}
