package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Creds are a database user and password, as the Secret's files hold them.
type Creds struct{ User, Password string }

// readCreds reads dir/username and dir/password. The kubelet updates a
// Secret volume by swapping a symlink, so a read sees the old or the new
// pair, never half of each; reading both files is still checked for both.
func readCreds(dir string) (Creds, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		v := strings.TrimSpace(string(b))
		if v == "" {
			return "", fmt.Errorf("%s is empty", filepath.Join(dir, name))
		}
		return v, nil
	}
	u, err := read("username")
	if err != nil {
		return Creds{}, err
	}
	p, err := read("password")
	if err != nil {
		return Creds{}, err
	}
	return Creds{User: u, Password: p}, nil
}

// Pool is the part of a connection pool the app uses.
type Pool interface {
	CurrentUser(ctx context.Context) (string, error)
	Ping(ctx context.Context) error
	Close()
}

// Connector opens a pool with the credentials and checks it answers.
type Connector func(ctx context.Context, c Creds) (Pool, error)

// DB holds the pool in use and moves to a new one when the credentials
// change. Requests take the current pool; a switch never leaves them
// without one.
type DB struct {
	cfg     Config
	connect Connector
	m       *Metrics
	log     *slog.Logger

	pool  atomic.Pointer[poolRef]
	mu    sync.Mutex // one switch at a time
	creds Creds      // the credentials of the pool in use
	wg    sync.WaitGroup
}

type poolRef struct {
	Pool
	user string
}

func newDB(cfg Config, connect Connector, m *Metrics, log *slog.Logger) *DB {
	return &DB{cfg: cfg, connect: connect, m: m, log: log}
}

// ErrNoPool means the app has not connected yet.
var ErrNoPool = errors.New("not connected to the database yet")

// Current returns the pool in use.
func (d *DB) Current() (Pool, error) {
	p := d.pool.Load()
	if p == nil {
		return nil, ErrNoPool
	}
	return p.Pool, nil
}

// Check reloads the credential files and, when they changed, opens a pool
// with them. Only a pool that answers replaces the old one; until then the
// old credentials stay in use (they still work while both roles exist).
func (d *DB) Check(ctx context.Context) error {
	c, err := readCreds(d.cfg.CredDir)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if c == d.creds && d.pool.Load() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p, err := d.connect(ctx, c)
	if err != nil {
		d.m.reconnects.WithLabelValues("failed").Inc()
		return fmt.Errorf("connect as %s: %w", c.User, err)
	}
	old := d.pool.Swap(&poolRef{Pool: p, user: c.User})
	d.creds = c
	d.m.reconnects.WithLabelValues("ok").Inc()
	d.m.setUser(c.User)
	if old != nil {
		d.m.reloads.Inc()
		d.log.Info("moved to new credentials", "user", c.User, "previous", old.user)
		// Close waits for in-flight queries, so it runs apart from requests.
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			old.Close()
		}()
	} else {
		d.log.Info("connected", "user", c.User)
	}
	return nil
}

// Watch checks the credential files every cfg.Every until ctx ends.
func (d *DB) Watch(ctx context.Context) {
	tick := time.NewTicker(d.cfg.Every)
	defer tick.Stop()
	last := ""
	for {
		if err := d.Check(ctx); err != nil && err.Error() != last {
			d.log.Warn("credentials", "err", err)
			last = err.Error()
		} else if err == nil {
			last = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Close closes the pool in use and waits for drained ones.
func (d *DB) Close() {
	if p := d.pool.Swap(nil); p != nil {
		p.Close()
	}
	d.wg.Wait()
}
