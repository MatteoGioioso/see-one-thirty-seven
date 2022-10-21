package postgresql

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
)

type Postmaster struct {
	Config
	Log *logrus.Entry
}

func (p Postmaster) InitDB() error {
	p.Log.Println("clean up PGDATA")
	if err := p.cleanupDataDir(); err != nil {
		return err
	}

	pwFile := path.Join(p.ExtraDir, "password", "pw")
	if err := p.createPasswordFile(pwFile); err != nil {
		return err
	}

	p.Log.Println("InitDB")
	cmd := exec.Command(
		"pg_ctl",
		"-D",
		p.DataDir,
		"initdb",
		"-o",
		fmt.Sprintf(`"--pwfile %v --username %v --auth-host scram-sha-256"`, pwFile, p.AdminUsername),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	return p.deletePasswordFile(pwFile)
}

func (p Postmaster) Start() error {
	hbaLocation := fmt.Sprintf("--hba_file=%v", path.Join(p.ExtraDir, "pg_hba.conf"))
	cmd := exec.Command(
		"postgres",
		"-D",
		p.DataDir,
		"-h",
		`"*"`,
		hbaLocation,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (p Postmaster) createPasswordFile(filename string) error {
	return ioutil.WriteFile(filename, []byte(p.AdminPassword), 0700)
}
func (p Postmaster) deletePasswordFile(filename string) error {
	return os.Remove(filename)
}

func (p Postmaster) cleanupDataDir() error {
	if err := os.RemoveAll(p.DataDir); err != nil {
		return err
	}
	return os.MkdirAll(p.DataDir, 0700)
}
