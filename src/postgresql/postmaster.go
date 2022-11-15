package postgresql

import (
	"context"
	"fmt"
	"github.com/avast/retry-go"
	"github.com/jackc/pgx/v5"
	"github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

type Postmaster struct {
	Config
	Log *logrus.Entry

	conn *pgx.Conn
	pid  int
}

func NewPostmaster(config Config, log *logrus.Entry) Postmaster {
	logWithField := log.WithField("subcomponent", "postgres")
	return Postmaster{Config: config, Log: logWithField}
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

	p.pid = cmd.Process.Pid
	p.Log.Infof("starting postgres process PID: %v", p.pid)

	return cmd.Process.Release()
}

func (p *Postmaster) Stop(mode string) error {
	// TODO add timeout and kill if necessary
	p.Log.Infof("Stopping postgres pid: %v", p.pid)
	cmd := exec.Command(
		"pg_ctl",
		"-D",
		fmt.Sprintf(`%v`, p.DataDir),
		"stop",
		"-m",
		mode,
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	p.Log.Infof("Postgres stopped pid: %v", p.pid)

	p.pid = 0

	return nil
}

func (p *Postmaster) IsRunning() bool {
	return p.isRunning()
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
		return fmt.Errorf("pg_ctl error: %v", err)
	}
	return cmd.Process.Release()
}

func (p *Postmaster) SyncData(leaderHostname string) error {
	cmd := exec.Command(
		"pg_rewind",
		fmt.Sprintf(`--source-server="host=%v port=%v dbname="`),
		fmt.Sprintf(`--target-pgdata=%v`, p.DataDir),
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
}

func (p *Postmaster) IsInRecovery(ctx context.Context) (bool, error) {
	conn, err := p.Connect(ctx)
	if err != nil {
		p.Log.Errorf("could not connect to establish if postgres is in recovery")
		return false, err
	}
	var isInRecovery bool
	if err := conn.QueryRow(ctx, "select pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
		p.Log.Errorf("could not execute query to establish if Postgres is in recovery, skipping: %v", err)
		return false, nil
	}

	return isInRecovery, nil
}

func (p *Postmaster) IsDataDirEmpty() (bool, error) {
	f, err := os.Open(p.DataDir)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}

	return false, err // Either not empty or error, suits both cases
}

func (p *Postmaster) Connect(ctx context.Context) (*pgx.Conn, error) {
	return p.ConnectWithRetry(ctx, retry.DefaultAttempts)
}

func (p *Postmaster) ConnectTo(ctx context.Context, hostname string) (*pgx.Conn, error) {
	return p.connectWithRetry(ctx, hostname, retry.DefaultAttempts)
}

func (p *Postmaster) ConnectWithRetry(ctx context.Context, retries uint) (*pgx.Conn, error) {
	if p.conn != nil {
		// Check if the connection is still active and if not reconnect again
		if err := p.conn.Ping(ctx); err != nil {
			conn, err := p.connectWithRetry(ctx, "localhost", retries)
			if err != nil {
				return nil, err
			}

			p.conn = conn
			return p.conn, nil
		}

		p.Log.Debugf("Reusing connection with PID: %v", p.conn.PgConn().PID())
		return p.conn, nil
	}

	conn, err := p.connectWithRetry(ctx, "localhost", retries)
	if err != nil {
		return nil, err
	}

	p.conn = conn
	return p.conn, nil
}

func (p *Postmaster) BlockAndWaitForLeader(leaderHostname string) error {
	err := retry.Do(func() error {
		cmd := exec.Command(
			"pg_isready",
			"-h",
			leaderHostname,
		)
		cmd.Env = os.Environ()
		cmd.Env = append(cmd.Env, fmt.Sprintf("PGPASSWORD=%v", p.AdminPassword))
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("postgres at host %v is not ready with error: %v", leaderHostname, err)
		}
		p.Log.Debugf("pg_isready: %s", out)
		p.Log.Infof("postgres leader at host %v, is ready", leaderHostname)

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// MakeBaseBackup TODO possibly move this to it's own class: in the future we could use something like wal-g
func (p *Postmaster) MakeBaseBackup(leaderHostname string) error {
	if err := retry.Do(
		func() error {
			return p.makeBaseBackup(leaderHostname)
		},
		retry.OnRetry(func(n uint, err error) {
			p.Log.Debugf("basebackup failed because %v, retry: %v/%v", err, n, retry.DefaultAttempts)
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
	p.Log.Debugf("emptying data directory")
	// https://superuser.com/questions/553045/fatal-lock-file-postmaster-pid-already-exists
	// Sanity check as wee cannot delete the data dir and having a postmaster process still running,
	// we may risk data corruption
	if p.IsRunning() {
		p.Log.Debugf("postgres is still running while trying to empty the data directory: proceeding with stopping the process")
		pid, err := p.getPIDFromFile()
		if err != nil {
			return err
		}

		if p.pid != pid {
			// TODO instead of crashing we could directly Kill both PIDs
			return fmt.Errorf(
				"the current registered pid (%v), is not the same as the one contained in postmaster.pid %v",
				p.pid,
				pid,
			)
		}

		p.Log.Debugf("stopping postgres PID %v, before empty data directory", pid)
		if err := p.Stop(StopModeFast); err != nil {
			return err
		}
	}

	dir, err := ioutil.ReadDir(p.DataDir)
	if err != nil {
		return err
	}
	for _, d := range dir {
		if err := os.RemoveAll(path.Join([]string{p.DataDir, d.Name()}...)); err != nil {
			return err
		}
	}
	p.Log.Debugf("postgres data directory was emptied successfully")

	return nil
}

func (p *Postmaster) getPIDFromFile() (int, error) {
	postmasterPidFile, err := ioutil.ReadFile(filepath.Join(p.DataDir, "postmaster.pid"))
	if err != nil {
		return 0, fmt.Errorf("could not read postmaster.pid file: %v", err)
	}

	// Check the layout of the postmaster.pid file
	lines := strings.Split(string(postmasterPidFile), "\n")
	pidStr := lines[0]

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("could not convert pid to integer: %v", err)
	}

	return pid, nil
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

func (p *Postmaster) isRunning() bool {
	cmd := exec.Command(
		"pg_isready",
	)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PGPASSWORD=%v", p.AdminPassword))
	out, err := cmd.Output()
	if err != nil {
		p.Log.Errorf("pg_isready: %v", err)
		return false
	}
	p.Log.Debugf("pg_isready: %s", out)

	return true
}

func (p *Postmaster) createPasswordFile(filename string) error {
	return ioutil.WriteFile(filename, []byte(p.AdminPassword), 0700)
}
func (p *Postmaster) deletePasswordFile(filename string) error {
	return os.Remove(filename)
}

func (p *Postmaster) connectWithRetry(ctx context.Context, hostname string, retries uint) (*pgx.Conn, error) {
	var conn *pgx.Conn
	err := retry.Do(
		func() error {
			connTry, err := p.connect(ctx, hostname)
			if err != nil {
				return err
			}

			conn = connTry
			return nil
		},
		retry.Attempts(retries),
		retry.OnRetry(func(n uint, err error) {
			p.Log.Debugf(
				"postgres process at hostname %v not ready with error %v, retry: %v/%v",
				hostname,
				err,
				n,
				retry.DefaultAttempts,
			)
		}),
	)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (p *Postmaster) connect(ctx context.Context, host string) (*pgx.Conn, error) {
	connString := fmt.Sprintf(
		"postgres://%v:%v@%v:5432/postgres",
		p.AdminUsername,
		p.AdminPassword,
		host,
	)
	return pgx.Connect(ctx, connString)
}
