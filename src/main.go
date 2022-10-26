package main

import (
	"context"
	"github.com/MatteoGioioso/seeonethirtyseven/daemon"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/MatteoGioioso/seeonethirtyseven/logger"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/avast/retry-go"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
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
	logLevel                = kingpin.Flag("log-level", "").Envar("LOG_LEVEL").Default("info").Enum("info", "debug", "warning")

	log *logrus.Entry
)

func main() {
	kingpin.Parse()

	ctx := context.Background()
	instanceID := uuid.New()
	log = logger.NewDefaultLogger(*logLevel, "seeone")
	log = log.WithField("instanceID", instanceID)
	log.Println("Starting seeone")

	retry.DefaultDelay = 2 * time.Second
	retry.DefaultAttempts = 15
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

	if err := dcsClient.StartElection(ctx); err != nil {
		log.Fatal(err)
	}

	d := daemon.Daemon{
		PgConfig:   pgConfig,
		Postmaster: postmaster,
		DcsClient:  dcsClient,
		Log:        log,
	}

	if err := d.Loop(ctx); err != nil {
		log.Fatal(err)
	}
}
