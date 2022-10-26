package main

import (
	"context"
	"errors"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/MatteoGioioso/seeonethirtyseven/logger"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/avast/retry-go"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"go.etcd.io/etcd/client/v3/concurrency"
	"gopkg.in/alecthomas/kingpin.v2"
	"strings"
	"time"
)

var (
	extraFolder             = kingpin.Flag("pgextra", "folder for other stuff").Required().Envar("PGEXTRA").String()
	pgDataFolder            = kingpin.Flag("pgdata", "postgres main data folder").Required().Envar("PGDATA").String()
	pgPassword              = kingpin.Flag("pgpassword", "").Required().Envar("PGPASSWORD").String()
	pgUser                  = kingpin.Flag("pguser", "").Default("postgres").Envar("PGUSER").String()
	hostname                = kingpin.Flag("hostname", "").Required().Envar("HOSTNAME").String()
	replicationUserPassword = kingpin.Flag("pgreplication-user-password", "").Required().Envar("PGREPLICATION_PASSWORD").String()
	etcdCluster             = kingpin.Flag("etcd-cluster", "").Required().Envar("ETCD_CLUSTER").String()
	leaderLease             = kingpin.Flag("leader-lease", "").Envar("LEADER_LEASE").Default("10").Int()

	log *logrus.Entry
)

func main() {
	kingpin.Parse()

	ctx := context.Background()
	instanceID := uuid.New()
	log = logger.NewDefaultLogger("info", "seeone")
	log = log.WithField("instanceID", instanceID)
	log.Println("Starting seeone")

	retry.DefaultDelay = 2 * time.Second
	retry.DefaultAttempts = 15
	retry.DefaultOnRetry = func(n uint, err error) {
		log.Warningf("%v retrying: %v/%v", err, n, retry.DefaultAttempts)
	}

	pgConfig := postgresql.Config{
		DataDir:             *pgDataFolder,
		ExtraDir:            *extraFolder,
		ReplicationUsername: "replicator",
		ReplicationPassword: *replicationUserPassword,
		AdminUsername:       *pgUser,
		AdminPassword:       *pgPassword,
	}

	postmaster := postgresql.Postmaster{Config: pgConfig, Log: log}

	dcsClient, err := dcs.NewEtcdImpl(
		strings.Split(*etcdCluster, " "),
		dcs.Config{
			Hostname:   *hostname,
			InstanceID: instanceID.String(),
			Lease:      *leaderLease,
		},
		log,
	)
	if err != nil {
		log.Fatal(err)
	}

	dcsClient.SetInstanceID(instanceID.String())
	if err := dcsClient.StartElection(ctx); err != nil {
		log.Fatal(err)
	}

	Loop(ctx, pgConfig, postmaster, dcsClient)
}

func BootstrapLeader(ctx context.Context, pgConfig postgresql.Config, postmaster postgresql.Postmaster) error {
	if err := postmaster.Init(); err != nil {
		return err
	}

	if err := pgConfig.CreateHBA(); err != nil {
		return err
	}

	if err := pgConfig.CreateConfig(""); err != nil {
		return err
	}

	if err := postmaster.Start(); err != nil {
		return err
	}

	connect, err := postmaster.Connect(ctx)
	if err != nil {
		return err
	}

	return pgConfig.SetupReplication(ctx, connect)
}

func BootstrapReplica(ctx context.Context, pgConfig postgresql.Config, postmaster postgresql.Postmaster, leaderHostname string) error {
	if err := postmaster.CheckIfLeaderPostgresIsReady(ctx, leaderHostname); err != nil {
		return err
	}

	if err := postmaster.MakeBaseBackup(leaderHostname); err != nil {
		return err
	}

	if err := pgConfig.CreateConfig(leaderHostname); err != nil {
		return err
	}

	if err := pgConfig.CreateHBA(); err != nil {
		return err
	}

	if err := postmaster.Start(); err != nil {
		return err
	}

	return nil
}

func Loop(ctx context.Context, pgConfig postgresql.Config, postmaster postgresql.Postmaster, dcsClient *dcs.Etcd) {
	tick := time.NewTicker(time.Duration(*leaderLease) * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			role, err := dcsClient.GetRole(ctx)
			if err != nil {
				if errors.Is(err, concurrency.ErrElectionNoLeader) {
					log.Errorf("no leader yet available: %v", err)
					return
				} else {
					log.Fatal(err)
				}
			}

			log.Infof("I am the %v", role)

			if role == postgresql.Leader {
				pgConfig.SetRole(postgresql.Leader)
				log = log.WithField("role", postgresql.Leader)
				postmaster.Log = log
				dcsClient.Log = log

				if err := dcsClient.SyncInstanceInfo(ctx, postgresql.Leader); err != nil {
					log.Fatal(err)
				}

				isBootstrapped, err := dcsClient.IsBootstrapped(ctx)
				if err != nil {
					log.Fatal(err)
				}

				if isBootstrapped {
					continue
				}

				log.Infof("bootstrapping")
				if err := BootstrapLeader(ctx, pgConfig, postmaster); err != nil {
					log.Fatal(err)
				}

				if err := dcsClient.SetBootstrapped(ctx); err != nil {
					log.Fatal(err)
				}
			} else {
				pgConfig.SetRole(postgresql.Replica)
				log = log.WithField("role", postgresql.Replica)
				postmaster.Log = log
				dcsClient.Log = log

				leaderInfo, err := dcsClient.GetLeaderInfo(ctx)
				if err != nil {
					log.Fatal(err)
				}

				if err := dcsClient.SyncInstanceInfo(ctx, postgresql.Leader); err != nil {
					log.Fatal(err)
				}

				isBootstrapped, err := dcsClient.IsBootstrapped(ctx)
				if err != nil {
					log.Fatal(err)
				}

				if isBootstrapped {
					continue
				}

				log.Infof("bootstrapping")
				if err := BootstrapReplica(ctx, pgConfig, postmaster, leaderInfo.Hostname); err != nil {
					log.Fatal(err)
				}

				if err := dcsClient.SetBootstrapped(ctx); err != nil {
					log.Fatal(err)
				}
			}
		}
	}
}
