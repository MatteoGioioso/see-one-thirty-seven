package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/MatteoGioioso/seeonethirtyseven/logger"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/avast/retry-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
)

const (
	master  = "master"
	replica = "replica"

	replicationUserName = "replicator"
)

var (
	extraFolder             = kingpin.Flag("pgextra", "folder for other stuff").Required().Envar("PGEXTRA").String()
	pgDataFolder            = kingpin.Flag("pgdata", "postgres main data folder").Required().Envar("PGDATA").String()
	pgPassword              = kingpin.Flag("pgpassword", "").Required().Envar("PGPASSWORD").String()
	pgUser                  = kingpin.Flag("pguser", "").Default("postgres").Envar("PGUSER").String()
	hostname                = kingpin.Flag("hostname", "").Required().Envar("HOSTNAME").String()
	replicationUserPassword = kingpin.Flag("pgreplication-user-password", "").Required().Envar("PGREPLICATION_PASSWORD").String()
	etcdCluster             = kingpin.Flag("etcd-cluster", "").Required().Envar("ETCD_CLUSTER").String()

	log       *logrus.Entry
	dcsClient *dcs.Etcd
)

func main() {
	kingpin.Parse()

	ctx := context.Background()
	instanceID := uuid.New()
	log = logger.NewDefaultLogger("info", "seeone")
	log = log.WithField("instanceID", instanceID)
	log.Println("Starting seeone")

	pgConfig := postgresql.Config{
		DataDir:             *pgDataFolder,
		ExtraDir:            *extraFolder,
		ReplicationUsername: "replicator",
		ReplicationPassword: *replicationUserPassword,
		AdminUsername:       *pgUser,
		AdminPassword:       *pgPassword,
	}

	postmaster := postgresql.Postmaster{Config: pgConfig, Log: log}

	if err := retry.Do(
		func() error {
			cli, err := dcs.NewEtcdImpl(strings.Split(*etcdCluster, " "), *hostname, instanceID.String(), log)
			if err != nil {
				return err
			}

			dcsClient = cli
			return nil
		}); err != nil {
		return
	}

	dcsClient.SetInstanceID(instanceID.String())
	if err := dcsClient.StartElection(ctx); err != nil {
		log.Fatal(err)
	}

	dcsClient.Observe(
		ctx,
		func() {
			pgConfig.SetRole(postgresql.Leader)
			log = log.WithField("role", postgresql.Leader)
			log.Println("I am the leader")
			if err := postmaster.Init(); err != nil {
				log.Fatal(err)
			}

			if err := pgConfig.CreateHBA(); err != nil {
				log.Fatal(err)
			}

			if err := pgConfig.CreateConfig(""); err != nil {
				log.Fatal(err)
			}

			if err := postmaster.Start(); err != nil {
				log.Fatal(err)
			}
		},
		func() {
			pgConfig.SetRole(postgresql.Replica)
			log = log.WithField("role", postgresql.Replica)
			log.Println("I am the replica")
			leaderInfo, err := dcsClient.GetLeaderInfo(ctx)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("%+v\n", leaderInfo)
			if err := postmaster.CheckIfLeaderIsReady(ctx, leaderInfo.Hostname); err != nil {
				log.Fatal(err)
			}

			log.Println("Leader is ready!")
		},
	)
}

func Master(ctx context.Context) error {
	log.Println("Writing pg_hba.conf")
	if err := writeHBA(); err != nil {
		log.Fatal(err)
	}

	log.Println("Writing postgresql.conf")
	//if err := writeConf(); err != nil {
	//	log.Fatal(err)
	//}

	log.Println("Starting postgres")
	if err := StartPostgres(); err != nil {
		log.Fatal(err)
	}

	log.Println("Connecting to database")
	conn, err := getConn(ctx, "localhost")
	if err != nil {
		log.Fatal(err)
	}

	if _, err := conn.Exec(
		ctx,
		fmt.Sprintf("CREATE USER %v WITH REPLICATION ENCRYPTED PASSWORD '%v'", replicationUserName, *replicationUserPassword),
	); err != nil {
		return err
	}

	//if _, err := conn.Exec(
	//	ctx,
	//	fmt.Sprintf("SELECT pg_create_physical_replication_slot('%v')", *seeoneMasterHost),
	//); err != nil {
	//	return err
	//}

	return nil
}

func Replica(ctx context.Context) error {
	log.Println("Running pg_basebackup")

	if err := retry.Do(
		func() error {
			backup, _ := makeBaseBackup()
			return backup.Run()
		}); err != nil {
		return err
	}

	log.Println("Done pg_basebackup!")

	if err := os.Chmod(*pgDataFolder, 0700); err != nil {
		return err
	}

	log.Println("Writing pg_hba.conf")
	if err := writeHBA(); err != nil {
		log.Fatal(err)
	}

	log.Println("Writing postgresql.conf")
	//if err := writeConf(); err != nil {
	//	log.Fatal(err)
	//}

	log.Println("Starting postgres")
	if err := StartPostgres(); err != nil {
		log.Fatal(err)
	}

	return nil
}

func makeBaseBackup() (*exec.Cmd, error) {
	cmd := exec.Command(
		"pg_basebackup",
		"-h",
		//*seeoneMasterHost,
		"-U",
		replicationUserName,
		"-p",
		"5432",
		"-D",
		*pgDataFolder,
		"-Fp",
		"-Xs",
		"-P",
		"-R",
	)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PGPASSWORD=%v", *replicationUserPassword))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, nil
}

func writeHBA() error {
	hba := bytes.NewBufferString("")
	hba.WriteString("local all all trust\n")
	hba.WriteString("host all all 0.0.0.0/0 scram-sha-256\n")
	hba.WriteString("host all all ::1/128 md5\n")
	if "" == master {
		hba.WriteString(fmt.Sprintf("host replication %v %v md5\n", replicationUserName, "0.0.0.0/0"))
	}

	if err := ioutil.WriteFile(
		path.Join(*extraFolder, "pg_hba.conf"),
		hba.Bytes(),
		0700,
	); err != nil {
		return err
	}

	return nil
}

func StartPostgres() error {
	app := "postgres"
	dataFolder := *pgDataFolder
	hbaLocation := fmt.Sprintf("--hba_file=%v", path.Join(*extraFolder, "pg_hba.conf"))
	cmd := exec.Command(app, "-D", dataFolder, "-h", `"*"`, hbaLocation)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		return err
	}

	return nil
}

func getConn(ctx context.Context, host string) (*pgx.Conn, error) {
	connString := fmt.Sprintf("postgres://%v:%v@%v:5432/postgres", *pgUser, *pgPassword, host)
	var conn *pgx.Conn

	if err := retry.Do(
		func() error {
			connTry, err := pgx.Connect(ctx, connString)
			if err != nil {
				log.Printf("Connection failed, retry: %v", err)
				return err
			}

			conn = connTry
			return nil
		},
	); err != nil {
		return nil, err
	}

	log.Println("Connected!")

	return conn, nil
}
