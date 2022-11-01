package daemon

import (
	"context"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs_proxy"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/sirupsen/logrus"
	"time"
)

type Config struct {
	TickDuration int
}

type Daemon struct {
	PgConfig   postgresql.Config
	Postmaster postgresql.Postmaster
	DcsClient  *dcs.Etcd
	DcsProxy   dcs_proxy.ProxyImpl
	Log        *logrus.Entry
	Config
}

func (d *Daemon) Start(ctx context.Context) error {
	tick := time.NewTicker(time.Duration(d.TickDuration) * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			role, err := d.DcsProxy.GetRole(ctx)
			if err != nil {
				return err
			}

			d.Log.Infof("I am the %v", role)
			if err := d.DcsProxy.SyncInstanceInfo(ctx, role); err != nil {
				d.Log.Errorf("Could not sync instance info: %v", err)
			}

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

	isDataDirEmpty, err := d.Postmaster.IsDataDirEmpty()
	if err != nil {
		return err
	}

	// isRunning here is most certainly false
	if !isDataDirEmpty {
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

	isDataDirEmpty, err := d.Postmaster.IsDataDirEmpty()
	if err != nil {
		return err
	}

	if !isDataDirEmpty {
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
