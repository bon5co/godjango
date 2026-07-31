// Package database owns GoDjangGo's Bun and PostgreSQL connection lifecycle.
package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

var (
	ErrInvalidConfig = errors.New("godjango database: invalid configuration")
	ErrConnect       = errors.New("godjango database: connect")
)

type Config struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxIdleTime time.Duration
	ConnMaxLifetime time.Duration
	PingTimeout     time.Duration
}

func DefaultConfig(dsn string) Config {
	return Config{
		DSN:             dsn,
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxIdleTime: 30 * time.Second,
		ConnMaxLifetime: 30 * time.Minute,
		PingTimeout:     5 * time.Second,
	}
}

type DB struct {
	bun       *bun.DB
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

	sqlDB := pgdriver.NewConnector(
		pgdriver.WithDSN(config.DSN),
		pgdriver.WithResetSessionFunc(discardBrokenSession(config.PingTimeout)),
	)
	bunDB := bun.NewDB(
		sql.OpenDB(sqlDB),
		pgdialect.New(),
	)
	bunDB.SetMaxOpenConns(config.MaxOpenConns)
	bunDB.SetMaxIdleConns(config.MaxIdleConns)
	bunDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)
	bunDB.SetConnMaxLifetime(config.ConnMaxLifetime)

	pingCtx, cancel := context.WithTimeout(ctx, config.PingTimeout)
	defer cancel()
	if err := bunDB.PingContext(pingCtx); err != nil {
		_ = bunDB.Close()
		return nil, fmt.Errorf("%w: %w", ErrConnect, err)
	}
	return &DB{bun: bunDB}, nil
}

// discardBrokenSession validates an idle connection before database/sql gives
// it to a new operation. Returning driver.ErrBadConn here makes database/sql
// discard and replace the connection before any application query is sent.
func discardBrokenSession(timeout time.Duration) func(context.Context, *pgdriver.Conn) error {
	return func(ctx context.Context, conn *pgdriver.Conn) error {
		pingCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := conn.Ping(pingCtx); err != nil {
			return driver.ErrBadConn
		}
		return nil
	}
}

func (db *DB) Bun() *bun.DB {
	return db.bun
}

func (db *DB) Close() error {
	db.closeOnce.Do(func() {
		db.closeErr = db.bun.Close()
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
	case config.DSN == "":
		return fmt.Errorf("%w: DSN is required", ErrInvalidConfig)
	case config.MaxOpenConns <= 0:
		return fmt.Errorf("%w: MaxOpenConns must be positive", ErrInvalidConfig)
	case config.MaxIdleConns < 0 || config.MaxIdleConns > config.MaxOpenConns:
		return fmt.Errorf(
			"%w: MaxIdleConns must be between zero and MaxOpenConns",
			ErrInvalidConfig,
		)
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
