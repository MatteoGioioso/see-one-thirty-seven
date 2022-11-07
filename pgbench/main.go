package main

import (
	"context"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs"
	"github.com/avast/retry-go"
	"github.com/jackc/pgx/v5"
	"gopkg.in/alecthomas/kingpin.v2"
	"log"
	"os"
	"os/exec"
	"strings"
)

var (
	etcdCluster = kingpin.Flag("etcd-cluster", "").Required().Envar("ETCD_CLUSTER").String()
	pgPassword  = kingpin.Flag("pgpassword", "").Required().Envar("PGPASSWORD").String()
	pgUser      = kingpin.Flag("pguser", "").Default("postgres").Envar("PGUSER").String()
)

func main() {
	kingpin.Parse()
	etcd, err := dcs.NewEtcdImpl(
		strings.Split(*etcdCluster, " "),
		dcs.Config{},
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	if err := retry.Do(func() error {
		if err := etcd.Connect(ctx); err != nil {
			return err
		}

		return nil
	}); err != nil {
		log.Fatal(err)
	}

	instancesInfo, err := etcd.GetClusterInstancesInfo(ctx)
	if err != nil {
		log.Fatal(err)
	}

	var leaderHostname string
	if err := retry.Do(func() error {
		fmt.Printf("%+v\n", instancesInfo)
		for _, info := range instancesInfo {
			connString := fmt.Sprintf(
				"postgres://%v:%v@%v:5432/postgres",
				*pgUser,
				*pgPassword,
				info.Hostname,
			)
			conn, err := pgx.Connect(ctx, connString)
			if err != nil {
				return err
			}

			var isInRecovery bool
			if err := conn.QueryRow(ctx, "select pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
				return err
			}
			log.Printf("instance id %v with host %v, recovery is %v", info.ID, info.Hostname, isInRecovery)
			if !isInRecovery {
				leaderHostname = info.Hostname
				return nil
			}

			continue
		}

		return fmt.Errorf("no leader yet")
	}); err != nil {
		log.Fatal(err)
	}

	setupPgbenchCmd := exec.Command(
		"pgbench",
		"-i",
		"-s",
		"1",
	)
	setupPgbenchCmd.Env = os.Environ()
	setupPgbenchCmd.Env = append(setupPgbenchCmd.Env, fmt.Sprintf("PGHOST=%v", leaderHostname))
	setupPgbenchCmd.Stdout = os.Stdout
	setupPgbenchCmd.Stderr = os.Stderr
	if err := setupPgbenchCmd.Run(); err != nil {
		log.Fatal(err)
	}

	startPgbenchCmd := exec.Command(
		"pgbench",
		"-c",
		"8",
		"-T",
		"3600",
		"-s",
		"5",
		"postgres",
	)
	startPgbenchCmd.Env = os.Environ()
	startPgbenchCmd.Env = append(startPgbenchCmd.Env, fmt.Sprintf("PGHOST=%v", leaderHostname))
	startPgbenchCmd.Stdout = os.Stdout
	startPgbenchCmd.Stderr = os.Stderr
	if err := startPgbenchCmd.Run(); err != nil {
		log.Fatal(err)
	}
}
