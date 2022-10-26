package postgresql

import (
	"context"
	"fmt"
	"github.com/avast/retry-go"
	"github.com/jackc/pgx/v5"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
)

type Postmaster struct {
	Config
	Log *logrus.Entry

	conn *pgx.Conn
}

func (p *Postmaster) Init() error {
	pwFile := path.Join(p.ExtraDir, "password", "pw")
	if err := p.createPasswordFile(pwFile); err != nil {
		return err
	}

	cmd := exec.Command(
		"pg_ctl",
		"-D",
		fmt.Sprintf(`"%v"`, p.DataDir),
		"initdb",
		fmt.Sprintf(`-o --pwfile %v --username %v --auth-host scram-sha-256`, pwFile, p.AdminUsername),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	return p.deletePasswordFile(pwFile)
}

func (p *Postmaster) Start() error {
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

func (p *Postmaster) Promote() error {
	cmd := exec.Command(
		"pg_ctl",
		"promote",
		"-D",
		fmt.Sprintf(`%v`, p.DataDir),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (p *Postmaster) Connect(ctx context.Context) (*pgx.Conn, error) {
	return p.ConnectWithRetry(ctx, retry.DefaultAttempts)
}

func (p *Postmaster) ConnectWithRetry(ctx context.Context, retries uint) (*pgx.Conn, error) {
	var conn *pgx.Conn
	if p.conn != nil {
		return p.conn, nil
	}

	err := retry.Do(
		func() error {
			connTry, err := p.getConn(ctx, "localhost")
			if err != nil {
				return err
			}

			conn = connTry
			p.conn = connTry
			return nil
		},
		retry.Attempts(retries),
	)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (p *Postmaster) IsReplica(ctx context.Context) (bool, error) {
	conn, err := p.getConn(ctx, "localhost")
	if err != nil {
		// If we cannot connect to postgres process means that
		return false, nil
	}

	var isInRecovery bool
	if err := conn.QueryRow(ctx, "select pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
		return false, err
	}

	return isInRecovery, nil
}

func (p *Postmaster) IsPostgresReady(ctx context.Context, hostname string) error {
	err := retry.Do(
		func() error {
			if _, err := p.getConn(ctx, hostname); err != nil {
				return err
			}

			return nil
		},
		retry.OnRetry(func(n uint, err error) {
			p.Log.Warningf("Postgres at hostname %v not ready, retry: %v", hostname, n)
		}),
	)
	if err != nil {
		return err
	}

	return nil
}

func (p *Postmaster) MakeBaseBackup(leaderHostname string) error {
	if err := retry.Do(
		func() error {
			return p.makeBaseBackup(leaderHostname)
		},
		retry.OnRetry(func(n uint, err error) {
			p.Log.Warningf("basebackup failed retry: %v", n)
		}),
	); err != nil {
		return err
	}

	if err := os.Chmod(p.DataDir, 0700); err != nil {
		return err
	}

	return nil
}

func (p *Postmaster) EmptyDataDir() error {
	dir, err := ioutil.ReadDir(p.DataDir)
	if err != nil {
		return err
	}
	for _, d := range dir {
		if err := os.RemoveAll(path.Join([]string{p.DataDir, d.Name()}...)); err != nil {
			return err
		}
	}

	return nil
}

func (p *Postmaster) makeBaseBackup(leaderHostname string) error {
	cmd := exec.Command(
		"pg_basebackup",
		"-h",
		leaderHostname,
		"-U",
		p.ReplicationUsername,
		"-p",
		"5432",
		"-D",
		p.DataDir,
		"-Fp",
		"-Xs",
		"-P",
		"-R",
	)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PGPASSWORD=%v", p.ReplicationPassword))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
}

func (p *Postmaster) createPasswordFile(filename string) error {
	return ioutil.WriteFile(filename, []byte(p.AdminPassword), 0700)
}
func (p *Postmaster) deletePasswordFile(filename string) error {
	return os.Remove(filename)
}

func (p *Postmaster) cleanupDataDir() error {
	dir, err := ioutil.ReadDir(p.DataDir)
	if err != nil {
		return err
	}
	for _, d := range dir {
		if err := os.RemoveAll(path.Join([]string{p.DataDir, d.Name()}...)); err != nil {
			return err
		}
	}

	return nil
}

func (p *Postmaster) getConn(ctx context.Context, host string) (*pgx.Conn, error) {
	connString := fmt.Sprintf(
		"postgres://%v:%v@%v:5432/postgres",
		p.AdminUsername,
		p.AdminPassword,
		host,
	)
	return pgx.Connect(ctx, connString)
}
