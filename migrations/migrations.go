// Package migrations provides explicit app-owned Bun SQL migrations.
package migrations

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/project"
)

var ErrNotImplemented = errors.New("godjango migrations: not implemented")

// Provider is implemented explicitly by apps that own SQL migrations.
type Provider interface {
	MigrationFS() fs.FS
}

type Catalog struct{}

func Collect(configured *project.Project) (*Catalog, error) {
	return nil, ErrNotImplemented
}

func (catalog *Catalog) Names() []string {
	return nil
}

type ScaffoldFiles struct {
	UpPath   string
	DownPath string
}

type Scaffolder struct {
	Now func() time.Time
}

func (scaffolder Scaffolder) Create(directory, name string) (ScaffoldFiles, error) {
	return ScaffoldFiles{}, ErrNotImplemented
}

type RunnerConfig struct {
	Table      string
	LocksTable string
}

func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{}
}

type Status struct {
	Name    string
	Comment string
	Applied bool
}

type Runner struct{}

func NewRunner(db *database.DB, catalog *Catalog, config RunnerConfig) (*Runner, error) {
	return nil, ErrNotImplemented
}

func (runner *Runner) Status(ctx context.Context) ([]Status, error) {
	return nil, ErrNotImplemented
}

func (runner *Runner) Apply(ctx context.Context) ([]string, error) {
	return nil, ErrNotImplemented
}

type RollbackConfirmation string

const ConfirmRollback RollbackConfirmation = "rollback-last-group"

func (runner *Runner) Rollback(
	ctx context.Context,
	confirmation RollbackConfirmation,
) ([]string, error) {
	return nil, ErrNotImplemented
}
