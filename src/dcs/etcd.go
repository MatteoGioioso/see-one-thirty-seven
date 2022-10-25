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
	"time"
)

const (
	hostnameKey = "hostname"
	roleKey     = "role"
)

type Etcd struct {
	Log *logrus.Entry

	cli        *clientv3.Client
	session    *concurrency.Session
	election   *concurrency.Election
	instanceID string
	hostname   string
	lease      int
}

func NewEtcdImpl(endpoints []string, hostname string, instanceID string, log *logrus.Entry) (*Etcd, error) {
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	if err != nil {
		return nil, err
	}
	lease := 10

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(lease))
	if err != nil {
		return nil, err
	}

	return &Etcd{
		cli:        cli,
		session:    session,
		election:   concurrency.NewElection(session, postgresql.LeaderElectionPrefix),
		hostname:   hostname,
		instanceID: instanceID,
		lease:      lease,
		Log:        log,
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

func (e *Etcd) StartElection(ctx context.Context) error {
	go e.election.Campaign(ctx, e.instanceID)
	return nil
}

func (e *Etcd) Observe(ctx context.Context, leaderFunc, replicaFunc func()) {
	go e.syncInstanceInfo(ctx)

	// The channel response will return the latest leader key/value
	for response := range e.election.Observe(ctx) {
		// Check if the current instance is the leader
		if string(response.Kvs[0].Value) == e.instanceID {
			if err := e.setInstanceRole(ctx, postgresql.Leader); err != nil {
				return
			}
			leaderFunc()
		} else {
			if err := e.setInstanceRole(ctx, postgresql.Replica); err != nil {
				return
			}
			replicaFunc()
		}
	}
}

func (e *Etcd) getLeaderInfo(ctx context.Context) (InstanceInfo, error) {
	response, err := e.cli.Get(
		ctx,
		postgresql.InstanceInfoPrefix,
		clientv3.WithPrefix(),
	)
	if err != nil {
		return InstanceInfo{}, err
	}

	leaderID := ""
	for _, kv := range response.Kvs {
		if string(kv.Value) == postgresql.Leader {
			leaderID = strings.Split(string(kv.Key), "/")[2]
			break
		}
	}

	if leaderID == "" {
		return InstanceInfo{}, fmt.Errorf("no leader was found in the DCS")
	}

	i := InstanceInfo{
		ID: leaderID,
	}
	for _, kv := range response.Kvs {
		if strings.Contains(string(kv.Key), leaderID) {
			switch strings.Split(string(kv.Key), "/")[3] {
			case hostnameKey:
				i.Hostname = string(kv.Value)
			case roleKey:
				i.Role = string(kv.Value)
			}
		}
	}

	return i, nil
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
		retry.Attempts(15),
		retry.Delay(1*time.Second),
		retry.OnRetry(func(n uint, err error) {
			e.Log.Warningf("leader not yet found in dcs, retry: %v", n)
		}),
	)
	if err != nil {
		return InstanceInfo{}, err
	}

	return i, nil
}

func (e *Etcd) syncInstanceInfo(ctx context.Context) error {
	if err := e.saveInstanceProp(ctx, hostnameKey, e.hostname); err != nil {
		return err
	}
	// https://jedri.medium.com/watching-for-expired-etcd-leases-in-go-4cf7006eba3
	watch := e.cli.Watch(
		ctx,
		e.getInstanceKey(),
		clientv3.WithFilterPut(),
		clientv3.WithPrefix(),
		clientv3.WithPrevKV(),
	)
	for resp := range watch {
		for _, ev := range resp.Events {
			fmt.Printf("%+v\n", ev.PrevKv)
			if err := e.putKeyValWithLease(ctx, string(ev.PrevKv.Key), string(ev.PrevKv.Value)); err != nil {
				return err
			}
		}
		e.Log.Printf("instance %v info synched", e.instanceID)
	}

	return nil
}

func (e *Etcd) saveInstanceProp(ctx context.Context, prop, val string) error {
	if err := e.putKeyValWithLease(ctx, e.getInstanceProKey(prop), val); err != nil {
		return err
	}

	return nil
}

func (e *Etcd) putKeyValWithLease(ctx context.Context, key, val string) error {
	grant, err := e.cli.Grant(ctx, int64(e.lease))
	if err != nil {
		return err
	}

	if _, err := e.cli.Put(
		ctx,
		key,
		val,
		clientv3.WithLease(grant.ID),
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
