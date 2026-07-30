package env_test

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bon5co/godjango/env"
)

type settings struct {
	DatabaseURL url.URL
	Address     string
	Debug       bool
	Workers     int
	Timeout     time.Duration
	PublicURL   url.URL
	SecretKey   env.Secret
}

func settingsSchema(config *settings) *env.Schema {
	defaultURL := url.URL{Scheme: "https", Host: "example.com"}
	return env.New(
		env.Required("DATABASE_URL", &config.DatabaseURL),
		env.Optional("ADDRESS", &config.Address, ":8000"),
		env.Optional("DEBUG", &config.Debug, false),
		env.Optional("WORKERS", &config.Workers, 4),
		env.Optional("TIMEOUT", &config.Timeout, 5*time.Second),
		env.Optional("PUBLIC_URL", &config.PublicURL, defaultURL),
		env.Required("SECRET_KEY", &config.SecretKey),
	)
}

func TestOptionalVariablesUseTypedDefaults(t *testing.T) {
	var config settings
	schema := settingsSchema(&config)

	err := schema.Load(env.WithEnvironment(map[string]string{
		"DATABASE_URL": "postgres://localhost/godjango",
		"SECRET_KEY":   "development-secret",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.Address != ":8000" {
		t.Errorf("Address = %q, want :8000", config.Address)
	}
	if config.Debug {
		t.Error("Debug = true, want false")
	}
	if config.Workers != 4 {
		t.Errorf("Workers = %d, want 4", config.Workers)
	}
	if config.Timeout != 5*time.Second {
		t.Errorf("Timeout = %s, want 5s", config.Timeout)
	}
	if config.PublicURL.String() != "https://example.com" {
		t.Errorf("PublicURL = %q", config.PublicURL.String())
	}
	if config.DatabaseURL.String() != "postgres://localhost/godjango" {
		t.Errorf("DatabaseURL = %q", config.DatabaseURL.String())
	}
	if config.SecretKey.Reveal() != "development-secret" {
		t.Error("SecretKey did not load")
	}
	if config.SecretKey.String() != "[REDACTED]" {
		t.Errorf("SecretKey.String() = %q", config.SecretKey.String())
	}
}

func TestEnvironmentOverridesDefaultsWithTypedValues(t *testing.T) {
	var config settings
	schema := settingsSchema(&config)

	err := schema.Load(env.WithEnvironment(map[string]string{
		"DATABASE_URL": "postgres://localhost/godjango",
		"ADDRESS":      "127.0.0.1:9000",
		"DEBUG":        "true",
		"WORKERS":      "12",
		"TIMEOUT":      "750ms",
		"SECRET_KEY":   "development-secret",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.Address != "127.0.0.1:9000" || !config.Debug || config.Workers != 12 {
		t.Errorf("typed overrides = %+v", config)
	}
	if config.Timeout != 750*time.Millisecond {
		t.Errorf("Timeout = %s, want 750ms", config.Timeout)
	}
}

func TestMissingAndMalformedVariablesFailTogetherBeforeStart(t *testing.T) {
	var config settings
	schema := settingsSchema(&config)
	started := false

	err := schema.Load(env.WithEnvironment(map[string]string{
		"DEBUG":      "sometimes",
		"WORKERS":    "many",
		"TIMEOUT":    "eventually",
		"PUBLIC_URL": "relative",
	}))
	if err == nil {
		started = true
	}
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if !errors.Is(err, env.ErrInvalidEnvironment) {
		t.Errorf("Load() error = %v, want ErrInvalidEnvironment", err)
	}
	if started {
		t.Error("application work started after invalid environment")
	}
	for _, name := range []string{
		"DATABASE_URL",
		"SECRET_KEY",
		"DEBUG",
		"WORKERS",
		"TIMEOUT",
		"PUBLIC_URL",
	} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("Load() error %q missing %s", err, name)
		}
	}
}

func TestLoadIsAtomicAndRedactsSecretValues(t *testing.T) {
	config := settings{
		Address: ":original",
		Workers: 99,
	}
	schema := settingsSchema(&config)
	secret := "never-print-this-secret"

	err := schema.Load(env.WithEnvironment(map[string]string{
		"DATABASE_URL": "postgres://localhost/godjango",
		"DEBUG":        "invalid",
		"SECRET_KEY":   secret,
	}))
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Load() leaked secret in %q", err)
	}
	if config.Address != ":original" || config.Workers != 99 || config.SecretKey != "" {
		t.Errorf("failed Load() partially mutated config: %+v", config)
	}
}

func TestDotEnvLoadsByDefaultAndProcessEnvironmentWins(t *testing.T) {
	directory := t.TempDir()
	dotEnv := strings.Join([]string{
		"DATABASE_URL=postgres://dotenv/godjango",
		"ADDRESS=dotenv:8000",
		"SECRET_KEY=dotenv-secret",
	}, "\n")
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte(dotEnv), 0o600); err != nil {
		t.Fatal(err)
	}

	var config settings
	err := settingsSchema(&config).Load(
		env.WithWorkingDirectory(directory),
		env.WithEnvironment(map[string]string{
			"ADDRESS": "process:9000",
		}),
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.DatabaseURL.String() != "postgres://dotenv/godjango" {
		t.Errorf("DatabaseURL = %q", config.DatabaseURL.String())
	}
	if config.Address != "process:9000" {
		t.Errorf("Address = %q, want process:9000", config.Address)
	}
	if config.SecretKey.Reveal() != "dotenv-secret" {
		t.Error("SecretKey did not load from .env")
	}
}

func TestDotEnvCanBeDisabled(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(directory, ".env"),
		[]byte("DATABASE_URL=postgres://dotenv/godjango\nSECRET_KEY=dotenv-secret\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var config settings
	err := settingsSchema(&config).Load(
		env.WithWorkingDirectory(directory),
		env.WithEnvironment(map[string]string{}),
		env.WithoutDotEnv(),
	)
	if err == nil {
		t.Fatal("Load() error = nil with .env disabled")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "SECRET_KEY") {
		t.Fatalf("Load() error = %v", err)
	}
}
