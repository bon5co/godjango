package database_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bon5co/godjango/database"
)

func TestDefaultConfigPreventsStaleIdleConnections(t *testing.T) {
	config := database.DefaultConfig("postgres://example/database")

	if config.MaxOpenConns != 25 {
		t.Errorf("MaxOpenConns = %d, want 25", config.MaxOpenConns)
	}
	if config.MaxIdleConns != 10 {
		t.Errorf("MaxIdleConns = %d, want 10", config.MaxIdleConns)
	}
	if config.ConnMaxIdleTime != 30*time.Second {
		t.Errorf("ConnMaxIdleTime = %s, want 30s", config.ConnMaxIdleTime)
	}
	if config.ConnMaxLifetime != 30*time.Minute {
		t.Errorf("ConnMaxLifetime = %s, want 30m", config.ConnMaxLifetime)
	}
	if config.PingTimeout != 5*time.Second {
		t.Errorf("PingTimeout = %s, want 5s", config.PingTimeout)
	}
}

func TestInvalidConfigFailsBeforeConnecting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*database.Config)
		want   string
	}{
		{
			name: "missing DSN",
			mutate: func(config *database.Config) {
				config.DSN = ""
			},
			want: "DSN",
		},
		{
			name: "zero open connections",
			mutate: func(config *database.Config) {
				config.MaxOpenConns = 0
			},
			want: "MaxOpenConns",
		},
		{
			name: "idle exceeds open",
			mutate: func(config *database.Config) {
				config.MaxOpenConns = 2
				config.MaxIdleConns = 3
			},
			want: "MaxIdleConns",
		},
		{
			name: "zero idle lifetime",
			mutate: func(config *database.Config) {
				config.ConnMaxIdleTime = 0
			},
			want: "ConnMaxIdleTime",
		},
		{
			name: "zero lifetime",
			mutate: func(config *database.Config) {
				config.ConnMaxLifetime = 0
			},
			want: "ConnMaxLifetime",
		},
		{
			name: "zero ping timeout",
			mutate: func(config *database.Config) {
				config.PingTimeout = 0
			},
			want: "PingTimeout",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := database.DefaultConfig("postgres://example/database")
			test.mutate(&config)
			_, err := database.Open(context.Background(), config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Open() error = %v, want mention of %s", err, test.want)
			}
		})
	}
}

func TestStartupPingUsesBoundedContext(t *testing.T) {
	config := database.DefaultConfig("postgres://127.0.0.1:1/missing?sslmode=disable")
	config.PingTimeout = 50 * time.Millisecond

	started := time.Now()
	_, err := database.Open(context.Background(), config)
	if err == nil {
		t.Fatal("Open() error = nil")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Open() took %s with 50ms ping timeout", elapsed)
	}
}

func TestConnectionErrorsDoNotLeakDSNPassword(t *testing.T) {
	const secret = "never-print-this-password"
	config := database.DefaultConfig(
		"postgres://user:" + secret + "@127.0.0.1:1/missing?sslmode=disable",
	)
	config.PingTimeout = 50 * time.Millisecond

	_, err := database.Open(context.Background(), config)
	if !errors.Is(err, database.ErrConnect) {
		t.Fatalf("Open() error = %v, want ErrConnect", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Open() leaked DSN password in %q", err)
	}
}

func TestOpenHonorsAlreadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := database.Open(ctx, database.DefaultConfig("postgres://example/database"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Open() error = %v, want context.Canceled", err)
	}
}
