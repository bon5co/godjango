// Package database owns GoDjangGo's Bun, PostgreSQL, and SQLite connections.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

var (
	ErrInvalidConfig = errors.New("godjango database: invalid configuration")
	ErrConnect       = errors.New("godjango database: connect")
)

type Config struct {
	Driver          string
	DSN             string
	MaxOpenConns    int
	ConnMaxIdleTime time.Duration
	ConnMaxLifetime time.Duration
	PingTimeout     time.Duration
}

func DefaultConfig(dsn string) Config {
	return Config{
		Driver:          "postgres",
		DSN:             dsn,
		MaxOpenConns:    25,
		ConnMaxIdleTime: 30 * time.Second,
		ConnMaxLifetime: 30 * time.Minute,
		PingTimeout:     5 * time.Second,
	}
}

func DefaultSQLiteConfig(dsn string) Config {
	config := DefaultConfig(dsn)
	config.Driver = "sqlite"
	config.MaxOpenConns = 1
	return config
}

type DB struct {
	bun       *bun.DB
	pool      *pgxpool.Pool
	dialect   string
	closeOnce sync.Once
	closeErr  error
}

func Open(ctx context.Context, config Config) (*DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validate(config); err != nil {
		return nil, err
	}
	if config.Driver == "sqlite" {
		return openSQLite(ctx, config)
	}

	poolConfig, err := pgxpool.ParseConfig(config.DSN)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid DSN", ErrConnect)
	}
	poolConfig.MaxConns = int32(config.MaxOpenConns)
	poolConfig.MaxConnIdleTime = config.ConnMaxIdleTime
	poolConfig.MaxConnLifetime = config.ConnMaxLifetime
	poolConfig.HealthCheckPeriod = config.ConnMaxIdleTime
	poolConfig.PingTimeout = config.PingTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConnect, err)
	}
	bunDB := bun.NewDB(
		stdlib.OpenDBFromPool(pool),
		pgdialect.New(),
	)

	pingCtx, cancel := context.WithTimeout(ctx, config.PingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		_ = bunDB.Close()
		pool.Close()
		return nil, fmt.Errorf("%w: %w", ErrConnect, err)
	}
	return &DB{bun: bunDB, pool: pool, dialect: "postgres"}, nil
}

func openSQLite(ctx context.Context, config Config) (*DB, error) {
	dsn := strings.TrimPrefix(config.DSN, "sqlite:")
	if strings.HasPrefix(dsn, "///") {
		dsn = dsn[2:]
	}
	if dsn == "" {
		return nil, fmt.Errorf("%w: SQLite path is required", ErrInvalidConfig)
	}
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	dsn += separator + "_foreign_keys=on&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConnect, err)
	}
	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)
	sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
	bunDB := bun.NewDB(sqlDB, sqlitedialect.New())
	pingCtx, cancel := context.WithTimeout(ctx, config.PingTimeout)
	defer cancel()
	if err := bunDB.PingContext(pingCtx); err != nil {
		_ = bunDB.Close()
		return nil, fmt.Errorf("%w: %w", ErrConnect, err)
	}
	return &DB{bun: bunDB, dialect: "sqlite"}, nil
}

func (db *DB) Bun() *bun.DB {
	return db.bun
}

func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

func (db *DB) Dialect() string { return db.dialect }

func (db *DB) Close() error {
	db.closeOnce.Do(func() {
		db.closeErr = db.bun.Close()
		if db.pool != nil {
			db.pool.Close()
		}
	})
	return db.closeErr
}

func RunInTx(
	ctx context.Context,
	db *DB,
	fn func(context.Context, bun.Tx) error,
) error {
	return db.bun.RunInTx(ctx, nil, fn)
}

func validate(config Config) error {
	switch {
	case config.Driver != "" && config.Driver != "postgres" && config.Driver != "sqlite":
		return fmt.Errorf("%w: Driver must be postgres or sqlite", ErrInvalidConfig)
	case config.DSN == "":
		return fmt.Errorf("%w: DSN is required", ErrInvalidConfig)
	case config.MaxOpenConns <= 0 || config.MaxOpenConns > math.MaxInt32:
		return fmt.Errorf("%w: MaxOpenConns must be between 1 and %d", ErrInvalidConfig, math.MaxInt32)
	case config.ConnMaxIdleTime <= 0:
		return fmt.Errorf("%w: ConnMaxIdleTime must be positive", ErrInvalidConfig)
	case config.ConnMaxLifetime <= 0:
		return fmt.Errorf("%w: ConnMaxLifetime must be positive", ErrInvalidConfig)
	case config.PingTimeout <= 0:
		return fmt.Errorf("%w: PingTimeout must be positive", ErrInvalidConfig)
	default:
		return nil
	}
}
