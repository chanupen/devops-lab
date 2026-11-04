// sample-db is the lab's database-backed sample app. It reads its Postgres
// credentials from files in a mounted Secret (username and password, not
// environment variables), notices when the kubelet replaces them, and moves
// to a new connection pool without dropping a request: the new pool must
// answer before it replaces the old one, which then drains and closes.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("sample-db", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var cfg Config
	flag.StringVar(&cfg.CredDir, "cred-dir", envOr("CRED_DIR", "/etc/db"), "directory with the username and password files")
	flag.StringVar(&cfg.Host, "db-host", envOr("DB_HOST", "postgres"), "Postgres host")
	flag.StringVar(&cfg.Port, "db-port", envOr("DB_PORT", "5432"), "Postgres port")
	flag.StringVar(&cfg.Database, "db-name", envOr("DB_NAME", "app"), "database")
	flag.StringVar(&cfg.Listen, "listen", envOr("LISTEN", ":8080"), "HTTP address")
	flag.DurationVar(&cfg.Every, "check-every", 2*time.Second, "how often to look at the credential files")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	m := newMetrics()
	db := newDB(cfg, pgxConnector(cfg), m, log)
	go db.Watch(ctx)

	srv := &http.Server{Addr: cfg.Listen, Handler: newHandler(db, m), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("listening", "addr", cfg.Listen, "credentials", cfg.CredDir)
	defer db.Close()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Config is where the app finds its database and credentials.
type Config struct {
	CredDir  string
	Host     string
	Port     string
	Database string
	Listen   string
	Every    time.Duration
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// pgxConnector opens a real pgx pool and checks it answers.
func pgxConnector(cfg Config) Connector {
	return func(ctx context.Context, c Creds) (Pool, error) {
		pc, err := pgxpool.ParseConfig("postgres://" + cfg.Host + ":" + cfg.Port + "/" + cfg.Database + "?sslmode=disable")
		if err != nil {
			return nil, err
		}
		pc.ConnConfig.User, pc.ConnConfig.Password = c.User, c.Password
		pc.MaxConns = 4
		p, err := pgxpool.NewWithConfig(ctx, pc)
		if err != nil {
			return nil, err
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			return nil, err
		}
		return pgxPool{p}, nil
	}
}

type pgxPool struct{ p *pgxpool.Pool }

func (p pgxPool) CurrentUser(ctx context.Context) (string, error) {
	var u string
	err := p.p.QueryRow(ctx, "SELECT current_user").Scan(&u)
	return u, err
}

func (p pgxPool) Ping(ctx context.Context) error { return p.p.Ping(ctx) }

// Close waits for the connections in use to come back, then closes all.
func (p pgxPool) Close() { p.p.Close() }
