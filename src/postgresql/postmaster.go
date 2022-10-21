package postgresql

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
)

type Postmaster struct {
	PGDataFolder    string
	PGAdminUser     string
	PGAdminPassword string
	ExtraFolder     string
}

func (p Postmaster) InitDB() error {
	if err := p.cleanupDataDir(); err != nil {
		return err
	}

	pwFile := path.Join(p.ExtraFolder, "password", "pw")
	if err := p.createPasswordFile(pwFile); err != nil {
		return err
	}
	cmd := exec.Command(
		"pg_ctl",
		"-D",
		p.PGDataFolder,
		"initdb",
		"-o",
		fmt.Sprintf(`"--pwfile %v --username %v --auth-host scram-sha-256"`, pwFile, p.PGAdminUser),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	return p.deletePasswordFile(pwFile)
}

func (p Postmaster) Start() error {
	hbaLocation := fmt.Sprintf("--hba_file=%v", path.Join(p.ExtraFolder, "pg_hba.conf"))
	cmd := exec.Command(
		"postgres",
		"-D",
		p.PGDataFolder,
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
	return ioutil.WriteFile(filename, []byte(p.PGAdminPassword), 0700)
}
func (p Postmaster) deletePasswordFile(filename string) error {
	return os.Remove(filename)
}

func (p Postmaster) cleanupDataDir() error {
	if err := os.RemoveAll(p.PGDataFolder); err != nil {
		return err
	}
	return os.MkdirAll(p.PGDataFolder, 0700)
}
