package daemon

import (
	"context"
	"errors"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/sirupsen/logrus"
	"go.etcd.io/etcd/client/v3/concurrency"
	"time"
)

type Daemon struct {
	PgConfig   postgresql.Config
	Postmaster postgresql.Postmaster
	DcsClient  *dcs.Etcd
	Log        *logrus.Entry
}

func (d *Daemon) Loop(ctx context.Context) error {
	tick := time.NewTicker(time.Duration(10) * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			role, err := d.DcsClient.GetRole(ctx)
			if err != nil {
				if errors.Is(err, concurrency.ErrElectionNoLeader) {
					d.Log.Errorf("no leader yet available: %v", err)
					continue
				} else {
					return err
				}
			}

			d.Log.Infof("I am the %v", role)

			if role == postgresql.Leader {
				if err := d.Leader(ctx); err != nil {
					return err
				}

				continue
			}

			if role == postgresql.Replica {
				if err := d.Replica(ctx); err != nil {
					return err
				}
			}
		}
	}
}

func (d *Daemon) AmIReplica(ctx context.Context) (bool, error) {
	conn, err := d.Postmaster.ConnectWithRetry(ctx, 3)
	if err == nil {
		var isInRecovery bool
		if err := conn.QueryRow(ctx, "select pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
			return false, err
		}

		return isInRecovery, nil
	} else {
		return false, nil
	}
}

func (d *Daemon) Leader(ctx context.Context) error {
	replica, err := d.AmIReplica(ctx)
	if err != nil {
		return err
	}

	if replica {
		if err := d.Postmaster.Promote(); err != nil {
			return err
		}
	}

	d.PgConfig.SetRole(postgresql.Leader)
	log := d.Log.WithField("role", postgresql.Leader)
	d.Postmaster.Log = log
	d.DcsClient.Log = log

	if err := d.DcsClient.SyncInstanceInfo(ctx, postgresql.Leader); err != nil {
		return err
	}

	isBootstrapped, err := d.DcsClient.IsBootstrapped(ctx)
	if err != nil {
		return err
	}

	if isBootstrapped {
		return nil
	}

	log.Infof("bootstrapping")
	if err := d.BootstrapLeader(ctx); err != nil {
		return err
	}

	if err := d.DcsClient.SetBootstrapped(ctx); err != nil {
		return err
	}
	return nil
}

func (d *Daemon) Replica(ctx context.Context) error {
	d.PgConfig.SetRole(postgresql.Replica)
	log := d.Log.WithField("role", postgresql.Replica)
	d.Postmaster.Log = log
	d.DcsClient.Log = log

	leaderInfo, err := d.DcsClient.GetLeaderInfo(ctx)
	if err != nil {
		return err
	}

	if err := d.DcsClient.SyncInstanceInfo(ctx, postgresql.Leader); err != nil {
		return err
	}

	isBootstrapped, err := d.DcsClient.IsBootstrapped(ctx)
	if err != nil {
		return err
	}

	if isBootstrapped {
		return nil
	}

	d.Log.Infof("bootstrapping")
	if err := d.BootstrapReplica(ctx, leaderInfo.Hostname); err != nil {
		return err
	}

	if err := d.DcsClient.SetBootstrapped(ctx); err != nil {
		return err
	}

	return nil
}

func (d *Daemon) BootstrapLeader(ctx context.Context) error {
	if err := d.Postmaster.Init(); err != nil {
		return err
	}

	if err := d.PgConfig.CreateHBA(); err != nil {
		return err
	}

	if err := d.PgConfig.CreateConfig(""); err != nil {
		return err
	}

	if err := d.Postmaster.Start(); err != nil {
		return err
	}

	connect, err := d.Postmaster.Connect(ctx)
	if err != nil {
		return err
	}

	return d.PgConfig.SetupReplication(ctx, connect)
}

func (d *Daemon) BootstrapReplica(ctx context.Context, leaderHostname string) error {
	if err := d.Postmaster.EmptyDataDir(); err != nil {
		return err
	}

	if err := d.Postmaster.IsPostgresReady(ctx, leaderHostname); err != nil {
		return err
	}

	if err := d.Postmaster.MakeBaseBackup(leaderHostname); err != nil {
		return err
	}

	if err := d.PgConfig.CreateConfig(leaderHostname); err != nil {
		return err
	}

	if err := d.PgConfig.CreateHBA(); err != nil {
		return err
	}

	if err := d.Postmaster.Start(); err != nil {
		return err
	}

	return nil
}
