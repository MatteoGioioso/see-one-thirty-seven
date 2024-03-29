package main

import (
	"context"
	"github.com/MatteoGioioso/seeonethirtyseven/api"
	"github.com/MatteoGioioso/seeonethirtyseven/daemon"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs_proxy"
	"github.com/MatteoGioioso/seeonethirtyseven/logger"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/avast/retry-go"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
	"os"
	"strings"
)

var (
	extraFolder             = kingpin.Flag("pgextra", "folder additional config").Required().Envar("PGEXTRA").String()
	pgDataFolder            = kingpin.Flag("pgdata", "postgres main data folder").Required().Envar("PGDATA").String()
	pgPassword              = kingpin.Flag("pgpassword", "").Required().Envar("PGPASSWORD").String()
	pgUser                  = kingpin.Flag("pguser", "").Default("postgres").Envar("PGUSER").String()
	hostname                = kingpin.Flag("hostname", "").Required().Envar("HOSTNAME").String()
	replicationUserPassword = kingpin.Flag("pgreplication-user-password", "").Required().Envar("PGREPLICATION_PASSWORD").String()
	etcdCluster             = kingpin.Flag("etcd-cluster", "").Required().Envar("ETCD_CLUSTER").String()
	leaderLease             = kingpin.Flag("leader-lease", "").Envar("LEADER_LEASE").Default("10").Int()
	logLevel                = kingpin.Flag("log-level", "").Envar("LOG_LEVEL").Default("info").Enum("info", "debug", "warning")

	log *logrus.Entry
)

func main() {
	kingpin.Parse()

	quit := make(chan int)
	ctx, cancel := context.WithCancel(context.Background())
	instanceID := uuid.New()
	log = logger.NewDefaultLogger(*logLevel, "seeone")
	log = log.WithField("instanceID", instanceID)
	log.Println("Starting seeone")

	retry.DefaultOnRetry = func(n uint, err error) {
		log.Debugf("%v, retrying: %v/%v", err, n, retry.DefaultAttempts)
	}

	pgConfig := postgresql.Config{
		DataDir:             *pgDataFolder,
		ExtraDir:            *extraFolder,
		ReplicationUsername: "replicator",
		ReplicationPassword: *replicationUserPassword,
		AdminUsername:       *pgUser,
		AdminPassword:       *pgPassword,
		InstanceID:          instanceID.String(),
	}

	postmaster := postgresql.NewPostmaster(pgConfig, log)

	factory := dcs.NewFactory(
		strings.Split(*etcdCluster, " "),
		dcs.Config{
			Hostname:   *hostname,
			InstanceID: instanceID.String(),
			Lease:      *leaderLease,
		},
		log,
	)
	dcsClient := factory.Get("etcd")
	dcsProxy := dcs_proxy.New(dcsClient, postmaster, log)
	if err := dcsProxy.Connect(ctx); err != nil {
		log.Fatal(err)
	}

	dcsProxy.StartElection(ctx)

	a := api.Api{
		Postmaster: postmaster,
		DcsProxy:   dcsProxy,
		Log:        log,
		Config:     api.Config{Port: "8080", InstanceID: instanceID.String()},
		QuitChan:   quit,
	}

	d := daemon.Daemon{
		PgConfig:   pgConfig,
		Postmaster: postmaster,
		DcsProxy:   dcsProxy,
		Log:        log,
		Config:     daemon.Config{TickDuration: 10},
	}

	go a.Start(ctx)
	go func() {
		if err := d.Start(ctx); err != nil {
			log.Fatal(err)
		}
	}()

	<-quit
	log.Infof("Initiating shutting down sequence...")

	if err := dcsProxy.Demote(ctx); err != nil {
		log.Errorf("could not demote instance: %v", err)
	}

	cancel()

	select {
	case <-ctx.Done():
		if err := postmaster.Stop(postgresql.StopModeFast); err != nil {
			log.Errorf("could not stop postgres: %v", err)
		}

		if err := dcsProxy.Disconnect(); err != nil {
			log.Errorf("could disconnect from dcs: %v", err)
		}

		log.Infof("Seeone exiting...See you!")
		os.Exit(0)
	}
}
