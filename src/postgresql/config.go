package postgresql

import (
	"bytes"
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"io/ioutil"
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
	if c.role == Leader {
		hba.WriteString(fmt.Sprintf("host replication %v %v md5\n", c.ReplicationUsername, "0.0.0.0/0"))
	}

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

	if c.role == Replica {
		pgConf.WriteString("\n")
		pgConf.WriteString(fmt.Sprintf(
			"primary_conninfo = 'user=%v password=%v host=%v port=5432 sslmode=prefer sslcompression=0'",
			c.ReplicationUsername,
			c.ReplicationPassword,
			leaderHostname,
		))
		pgConf.WriteString("\n")
		pgConf.WriteString(fmt.Sprintf("primary_slot_name = '%v'", c.InstanceID))
	}

	if err := ioutil.WriteFile(path.Join(c.DataDir, "postgresql.conf"), pgConf.Bytes(), 0700); err != nil {
		return err
	}

	return nil
}

func (c *Config) SetupReplication(ctx context.Context, conn *pgx.Conn) error {
	if _, err := conn.Exec(
		ctx,
		fmt.Sprintf("CREATE USER %v WITH REPLICATION ENCRYPTED PASSWORD '%v'", c.ReplicationUsername, c.ReplicationPassword),
	); err != nil {
		return err
	}

	if _, err := conn.Exec(
		ctx,
		fmt.Sprintf("SELECT pg_create_physical_replication_slot('%v')", c.InstanceID),
	); err != nil {
		return err
	}

	return nil
}
