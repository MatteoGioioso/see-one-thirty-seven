package postgresql

import (
	"bytes"
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"io/ioutil"
	"os"
	"path"
)

type Config struct {
	DataDir             string
	ExtraDir            string
	ReplicationUsername string
	ReplicationPassword string
	AdminUsername       string
	AdminPassword       string
	Port                string
	InstanceID          string

	role string
}

func (c *Config) SetRole(role string) {
	c.role = role
}

func (c *Config) CreateHBA() error {
	hba := bytes.NewBufferString("")
	hba.WriteString("local all all trust\n")
	hba.WriteString("host all all 0.0.0.0/0 scram-sha-256\n")
	hba.WriteString("host all all ::1/128 md5\n")
	hba.WriteString(fmt.Sprintf("host replication %v %v md5\n", c.ReplicationUsername, "0.0.0.0/0"))

	return ioutil.WriteFile(
		path.Join(c.ExtraDir, "pg_hba.conf"),
		hba.Bytes(),
		0700,
	)
}

func (c *Config) CreateConfig(leaderHostname string) error {
	file, err := ioutil.ReadFile(path.Join(c.ExtraDir, "postgresql.template.conf"))
	if err != nil {
		return err
	}

	pgConf := bytes.NewBuffer(file)
	// If we are using replication slots and the replica goes down for long time, the leader might accumulate an infinite
	// amount of WAL files. To prevent this we set max_slot_wal_keep_size
	pgConf.WriteString("max_slot_wal_keep_size = 40GB")
	pgConf.WriteString("\n")
	pgConf.WriteString(fmt.Sprintf(
		"primary_conninfo = 'user=%v password=%v host=%v port=5432 sslmode=prefer sslcompression=0'",
		c.ReplicationUsername,
		c.ReplicationPassword,
		leaderHostname,
	))
	pgConf.WriteString("\n")
	pgConf.WriteString(fmt.Sprintf("primary_slot_name = '%v'", os.Getenv("HOSTNAME")))

	if err := ioutil.WriteFile(path.Join(c.DataDir, "postgresql.conf"), pgConf.Bytes(), 0700); err != nil {
		return err
	}

	return nil
}

func (c *Config) CreateReplicationUser(ctx context.Context, conn *pgx.Conn) error {
	if _, err := conn.Exec(
		ctx,
		fmt.Sprintf(
			"CREATE USER %v WITH REPLICATION ENCRYPTED PASSWORD '%v'",
			c.ReplicationUsername,
			c.ReplicationPassword,
		),
	); err != nil {
		return err
	}

	return nil
}

func (c *Config) CreateReplicationSlot(ctx context.Context, conn *pgx.Conn, slotName string) error {
	var exists bool
	if err := conn.QueryRow(ctx, "select exists(select 1 from pg_replication_slots where slot_name= $1)", slotName).Scan(&exists); err != nil {
		return err
	}

	if exists {
		return nil
	}

	if _, err := conn.Exec(
		ctx,
		fmt.Sprintf("SELECT pg_create_physical_replication_slot('%v')", slotName),
	); err != nil {
		return fmt.Errorf("could not create replication slot: %v", err)
	}

	return nil
}
