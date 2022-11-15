package daemon

import (
	"context"
	"fmt"
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
	DcsProxy   dcs_proxy.ProxyImpl
	Log        *logrus.Entry
	Config
}

func (d *Daemon) Start(ctx context.Context) error {
	tick := time.NewTicker(time.Duration(d.TickDuration) * time.Second)
	defer tick.Stop()

loop:
	for {
		select {
		case <-tick.C:
			role, err := d.DcsProxy.GetRole(ctx)
			if err != nil {
				// TODO add possibility to keep running as replica
				return err
			}

			if role == postgresql.Leader {
				if err := d.LeaderFunc(ctx); err != nil {
					return err
				}
			}

			if role == postgresql.Replica {
				if err := d.ReplicaFunc(ctx); err != nil {
					return err
				}
			}

			d.Log.Infof("I am the %v", role)
			if err := d.DcsProxy.SaveInstanceInfo(ctx, role); err != nil {
				d.Log.Errorf("Could not sync instance info: %v", err)
			}
		case <-ctx.Done():
			d.Log.Infof("Stopping daemon loop")
			tick.Stop()
			break loop
		}
	}

	return nil
}

func (d *Daemon) LeaderFunc(ctx context.Context) error {
	log := d.Log.WithField("role", postgresql.Leader)
	d.PgConfig.SetRole(postgresql.Leader)
	d.Postmaster.Log = log

	isDataDirEmpty, err := d.Postmaster.IsDataDirEmpty()
	if err != nil {
		return err
	}

	if isDataDirEmpty {
		if err := d.BootstrapLeader(ctx); err != nil {
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
	} else if d.Postmaster.IsRunning() {
		d.Log.Debugf("postgres is running, check if its role is consistent")

		isInRecovery, err := d.Postmaster.IsInRecovery(ctx)
		if err != nil {
			return err
		}

		if isInRecovery {
			d.Log.Warningf(
				"current postgres is in recovery, but is supposed to be the %v, possible failover, trying to promote this instance",
				postgresql.Leader,
			)

			// Before promoting we MUST make sure that there is no other postgres process running in non-recovery mode present in the cluster
			isThereOrphanLeader, err := d.IsThereOrphanLeader(ctx)
			if err != nil {
				return fmt.Errorf("could not establish if there are orphan leader in the cluster: %v", err)
			}

			if isThereOrphanLeader {
				d.Log.Errorf("an orphan leader was found, we cannot promote the current instance")
				return nil
			}

			if err := d.Postmaster.Promote(); err != nil {
				return fmt.Errorf("could not promote postgres: %v", err)
			}

			d.Log.Infof("postgres was promoted to %v", postgresql.Leader)
			return nil
		} else {
			d.Log.Debugf("postgres status is good: running as leader")
			return nil
		}
	} else {
		// Postgres is not running but the data directory is not empty
		d.Log.Debugf("postgres is not running: trying to start")
		if err := d.Postmaster.Start(); err != nil {
			return err
		}

		return nil
	}
}

func (d *Daemon) ReplicaFunc(ctx context.Context) error {
	log := d.Log.WithField("role", postgresql.Replica)
	d.PgConfig.SetRole(postgresql.Replica)
	d.Postmaster.Log = log

	isDataDirEmpty, err := d.Postmaster.IsDataDirEmpty()
	if err != nil {
		return err
	}

	if isDataDirEmpty {
		d.Log.Debugf("postgres data directory is empty")
		return d.bootstrapAndStartReplica(ctx)
	} else if d.Postmaster.IsRunning() {
		d.Log.Debugf("postgres is running, check if its role is consistent")

		isInRecovery, err := d.Postmaster.IsInRecovery(ctx)
		if err != nil {
			return err
		}

		if isInRecovery {
			d.Log.Debugf("postgres status is good: running and in recovery mode")
			return nil
		} else {
			// TODO if we find ourself in this situation we could possibly have a temporary split brain (crash, kill or smart shutdown?)
			// If we arrive at this point it means that postgres should be running as a replica, but instead is accepting
			// update/insert transaction. This is highly unlikely though: the method IsThereOrphanLeader will shield us
			// from such a case.
			// I will still keep this just in case
			d.Log.Errorf("postgres is running not in recovery mode, but it is supposed to be a replica, POSSIBLE CORRUPTION: stopping")
			if err := d.Postmaster.Stop(postgresql.StopModeFast); err != nil {
				return err
			}

			return d.bootstrapAndStartReplica(ctx)
		}
	} else {
		// If postgres is not running but the data directory is not empty, we cannot risk to start the process
		// because it might NOT be in recovery mode, therefore we proceed to empty the data folder and make a base backup
		return d.bootstrapAndStartReplica(ctx)
	}
}

func (d *Daemon) bootstrapAndStartReplica(ctx context.Context) error {
	leaderInfo, err := d.DcsProxy.GetLeaderInfo(ctx)
	if err != nil {
		return err
	}

	if err := d.Postmaster.BlockAndWaitForLeader(leaderInfo.Hostname); err != nil {
		return err
	}

	if err := d.BootstrapReplica(ctx, leaderInfo.Hostname); err != nil {
		return err
	}

	if err := d.Postmaster.Start(); err != nil {
		return err
	}

	// TODO wait for it to be ready, or it will cause race condition

	return nil
}

func (d *Daemon) BootstrapLeader(ctx context.Context) error {
	d.Log.Debugf("bootstrapping")
	if err := d.Postmaster.Init(); err != nil {
		return err
	}

	d.Log.Debugf("creating pg_hba.conf")
	if err := d.PgConfig.CreateHBA(); err != nil {
		return err
	}

	d.Log.Debugf("creating postgresql.conf")
	return d.PgConfig.CreateConfig("")
}

func (d *Daemon) BootstrapReplica(ctx context.Context, leaderHostname string) error {
	d.Log.Debugf("bootstrapping")
	if err := d.Postmaster.EmptyDataDir(); err != nil {
		return err
	}

	if err := d.Postmaster.MakeBaseBackup(leaderHostname); err != nil {
		return err
	}

	d.Log.Debugf("creating postgresql.conf")
	if err := d.PgConfig.CreateConfig(leaderHostname); err != nil {
		return err
	}

	d.Log.Debugf("creating pg_hba.conf")
	return d.PgConfig.CreateHBA()
}

// IsThereOrphanLeader In here we check if there are other instance in the cluster running NOT in recovery mode
// if that is the case we MUST NOT proceed, for example, to promote a new postgres process, as we will likely be causing
// split-brain
func (d *Daemon) IsThereOrphanLeader(ctx context.Context) (bool, error) {
	d.Log.Infof("checking for possible orphan leader")
	instances, err := d.DcsProxy.GetClusterInstances(ctx)
	if err != nil {
		return true, err
	}

	for _, instance := range instances {
		d.Log.Debugf("checking hostname %v with instance id %v", instance.Hostname, instance.ID)
		conn, err := d.Postmaster.ConnectTo(ctx, instance.Hostname)
		if err != nil {
			return true, err
		}

		var isInRecovery bool
		if err := conn.QueryRow(ctx, "select pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
			return true, err
		}

		if !isInRecovery {
			d.Log.Errorf("hostname %v with instance id %v, is NOT in recovery mode", instance.Hostname, instance.ID)
			return true, nil
		}

		d.Log.Debugf("hostname %v with instance id %v, is in recovery mode", instance.Hostname, instance.ID)
	}

	d.Log.Debugf("no orphan leaders found in the cluster")
	return false, nil
}
