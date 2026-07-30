// Package database owns GoDjangGo's Bun and PostgreSQL connection lifecycle.
package database

import (
	"context"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

var ErrNotImplemented = errors.New("godjango database: not implemented")

type Config struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxIdleTime time.Duration
	ConnMaxLifetime time.Duration
	PingTimeout     time.Duration
}

func DefaultConfig(dsn string) Config {
	return Config{DSN: dsn}
}

type DB struct{}

func Open(ctx context.Context, config Config) (*DB, error) {
	return nil, ErrNotImplemented
}

func (db *DB) Bun() *bun.DB {
	return nil
}

func (db *DB) Close() error {
	return ErrNotImplemented
}

func RunInTx(
	ctx context.Context,
	db *DB,
	fn func(context.Context, bun.Tx) error,
) error {
	return ErrNotImplemented
}
