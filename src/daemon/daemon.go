package daemon

import (
	"context"
	"errors"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/sirupsen/logrus"
	"go.etcd.io/etcd/client/v3/concurrency"
	"time"
)

type Config struct {
	TickDuration int
}

type Daemon struct {
	PgConfig   postgresql.Config
	Postmaster postgresql.Postmaster
	DcsClient  *dcs.Etcd
	Log        *logrus.Entry
	Config
}

func (d *Daemon) Start(ctx context.Context) error {
	tick := time.NewTicker(time.Duration(d.TickDuration) * time.Second)
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

func (d *Daemon) Leader(ctx context.Context) error {
	log := d.Log.WithField("role", postgresql.Leader)
	d.PgConfig.SetRole(postgresql.Leader)
	d.Postmaster.Log = log
	d.DcsClient.Log = log

	if err := d.DcsClient.SyncInstanceInfo(ctx, postgresql.Leader); err != nil {
		return err
	}

	if d.Postmaster.IsRunning() {
		d.Log.Debugf("postgres is running, check if its role is consistent")

		isInRecovery, err := d.Postmaster.IsInRecovery(ctx)
		if err != nil {
			return nil
		}

		if isInRecovery {
			d.Log.Warningf("current postgres is %v, possible failover, trying to promote this instance", postgresql.Replica)

			if err := d.Postmaster.Promote(); err != nil {
				return fmt.Errorf("could not promote postgres: %v", err)
			}

			d.Log.Infof("postgres was promoted to %v", postgresql.Leader)
			return nil
		}

		d.Log.Infof("postgres status is good")
		return nil
	}

	isBootstrapped, err := d.DcsClient.IsBootstrapped(ctx)
	if err != nil {
		return err
	}

	// isRunning here is most certainly false
	if isBootstrapped {
		if err := d.Postmaster.Start(); err != nil {
			return err
		}

		return nil
	}

	log.Infof("bootstrapping")
	if err := d.BootstrapLeader(ctx); err != nil {
		return err
	}

	if err := d.DcsClient.SetBootstrapped(ctx); err != nil {
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

func (d *Daemon) Replica(ctx context.Context) error {
	log := d.Log.WithField("role", postgresql.Replica)
	d.PgConfig.SetRole(postgresql.Replica)
	d.Postmaster.Log = log
	d.DcsClient.Log = log

	if err := d.DcsClient.SyncInstanceInfo(ctx, postgresql.Replica); err != nil {
		return err
	}

	//isRunning := d.Postmaster.IsRunning(ctx, 3)
	//if isRunning {
	//	return nil
	//}

	isBootstrapped, err := d.DcsClient.IsBootstrapped(ctx)
	if err != nil {
		return err
	}

	if isBootstrapped {
		return nil
	}

	leaderInfo, err := d.DcsClient.GetLeaderInfo(ctx)
	if err != nil {
		return err
	}

	if err := d.Postmaster.BlockAndWaitForLeader(leaderInfo.Hostname); err != nil {
		return err
	}

	if err := d.BootstrapReplica(ctx, leaderInfo.Hostname); err != nil {
		return err
	}

	if err := d.DcsClient.SetBootstrapped(ctx); err != nil {
		return err
	}

	if err := d.Postmaster.Start(); err != nil {
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

	return d.PgConfig.CreateConfig("")
}

func (d *Daemon) BootstrapReplica(ctx context.Context, leaderHostname string) error {
	if err := d.Postmaster.EmptyDataDir(); err != nil {
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

	return nil
}

func (d *Daemon) IsBootstrappedAndRunning(ctx context.Context) (bool, error) {
	isBootstrapped, err := d.DcsClient.IsBootstrapped(ctx)
	if err != nil {
		return false, err
	}

	if isBootstrapped && d.Postmaster.IsRunning() {
		return true, nil
	}

	return false, nil
}
