package postgresql

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path"
)

type Config struct {
	LeaderHostname      string
	DataDir             string
	ExtraDir            string
	ReplicationUsername string
	ReplicationPassword string
	AdminUsername       string
	AdminPassword       string
}

func (c *Config) CreateHBA() {

}

func (c *Config) SetLeaderHostname(hostname string) {
	c.LeaderHostname = hostname
}

func (c *Config) CreateConfig(role string) error {
	file, err := ioutil.ReadFile(path.Join(c.ExtraDir, "postgresql.template.conf"))
	if err != nil {
		return err
	}

	pgConf := bytes.NewBuffer(file)
	if role == Replica {
		pgConf.WriteString("\n")
		pgConf.WriteString(fmt.Sprintf(
			"primary_conninfo = 'user=%v password=%v host=%v port=5432 sslmode=prefer sslcompression=0'",
			c.ReplicationUsername,
			c.ReplicationPassword,
			c.LeaderHostname,
		))
		pgConf.WriteString("\n")
		pgConf.WriteString(fmt.Sprintf("primary_slot_name = '%v'", c.LeaderHostname))
	}

	if err := ioutil.WriteFile(path.Join(c.DataDir, "postgresql.conf"), pgConf.Bytes(), 0700); err != nil {
		return err
	}

	return nil
}
