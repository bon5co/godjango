// Package migrations provides explicit app-owned Bun SQL migrations.
package migrations

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing/fstest"
	"time"

	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/project"
	bunmigrate "github.com/uptrace/bun/migrate"
)

var (
	ErrInvalidCatalog       = errors.New("godjango migrations: invalid catalog")
	ErrRollbackConfirmation = errors.New("godjango migrations: rollback confirmation required")
)

var (
	fileNamePattern = regexp.MustCompile(
		`^(\d{14})_([0-9a-z_-]+)\.tx\.(up|down)\.sql$`,
	)
	scaffoldNamePattern = regexp.MustCompile(`^[0-9a-z][0-9a-z_-]*$`)
)

// Provider is implemented explicitly by apps that own SQL migrations.
type Provider interface {
	MigrationFS() fs.FS
}

type Catalog struct {
	migrations *bunmigrate.Migrations
	names      []string
}

func Collect(configured *project.Project) (*Catalog, error) {
	if configured == nil {
		return nil, fmt.Errorf("%w: project is nil", ErrInvalidCatalog)
	}

	type pair struct {
		name string
		up   bool
		down bool
	}
	pairs := make(map[string]*pair)
	combined := make(fstest.MapFS)

	for _, app := range configured.Apps() {
		provider, ok := app.(Provider)
		if !ok {
			continue
		}
		appFS := provider.MigrationFS()
		if appFS == nil {
			return nil, fmt.Errorf("%w: app %s returned a nil filesystem", ErrInvalidCatalog, app.Name())
		}
		err := fs.WalkDir(appFS, ".", func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			base := filepath.Base(path)
			if !strings.HasSuffix(base, ".sql") {
				return nil
			}
			matches := fileNamePattern.FindStringSubmatch(base)
			if matches == nil {
				return fmt.Errorf(
					"%w: app %s has invalid migration filename %q",
					ErrInvalidCatalog,
					app.Name(),
					base,
				)
			}
			identity := matches[1]
			name := identity + "_" + matches[2]
			current, exists := pairs[identity]
			if exists && current.name != name {
				return fmt.Errorf(
					"%w: migration identity %s is duplicated by %s and %s",
					ErrInvalidCatalog,
					identity,
					current.name,
					name,
				)
			}
			if !exists {
				current = &pair{name: name}
				pairs[identity] = current
			}
			switch matches[3] {
			case "up":
				if current.up {
					return fmt.Errorf("%w: duplicate up migration %s", ErrInvalidCatalog, name)
				}
				current.up = true
			case "down":
				if current.down {
					return fmt.Errorf("%w: duplicate down migration %s", ErrInvalidCatalog, name)
				}
				current.down = true
			}
			content, err := fs.ReadFile(appFS, path)
			if err != nil {
				return err
			}
			combined[app.Name()+"/"+base] = &fstest.MapFile{Data: content, Mode: 0o644}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	names := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if !pair.up || !pair.down {
			return nil, fmt.Errorf(
				"%w: migration %s requires both up and down files",
				ErrInvalidCatalog,
				pair.name,
			)
		}
		names = append(names, pair.name)
	}
	sort.Strings(names)

	discovered := bunmigrate.NewMigrations()
	if err := discovered.Discover(combined); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCatalog, err)
	}
	return &Catalog{migrations: discovered, names: names}, nil
}

func (catalog *Catalog) Names() []string {
	return append([]string(nil), catalog.names...)
}

type ScaffoldFiles struct {
	UpPath   string
	DownPath string
}

type Scaffolder struct {
	Now func() time.Time
}

func (scaffolder Scaffolder) Create(directory, name string) (ScaffoldFiles, error) {
	if !scaffoldNamePattern.MatchString(name) {
		return ScaffoldFiles{}, fmt.Errorf("godjango migrations: invalid migration name %q", name)
	}
	now := time.Now
	if scaffolder.Now != nil {
		now = scaffolder.Now
	}
	prefix := now().UTC().Format("20060102150405") + "_" + name + ".tx."
	files := ScaffoldFiles{
		UpPath:   filepath.Join(directory, prefix+"up.sql"),
		DownPath: filepath.Join(directory, prefix+"down.sql"),
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return ScaffoldFiles{}, err
	}
	for _, path := range []string{files.UpPath, files.DownPath} {
		if _, err := os.Stat(path); err == nil {
			return ScaffoldFiles{}, fmt.Errorf("godjango migrations: refusing to overwrite %s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return ScaffoldFiles{}, err
		}
	}
	if err := writeExclusive(files.UpPath); err != nil {
		return ScaffoldFiles{}, err
	}
	if err := writeExclusive(files.DownPath); err != nil {
		_ = os.Remove(files.UpPath)
		return ScaffoldFiles{}, err
	}
	return files, nil
}

type RunnerConfig struct {
	Table      string
	LocksTable string
}

func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		Table:      "godjango_migrations",
		LocksTable: "godjango_migration_locks",
	}
}

type Status struct {
	Name    string
	Comment string
	Applied bool
}

type Runner struct {
	migrator *bunmigrate.Migrator
}

func NewRunner(db *database.DB, catalog *Catalog, config RunnerConfig) (*Runner, error) {
	if db == nil || catalog == nil {
		return nil, errors.New("godjango migrations: database and catalog are required")
	}
	if config.Table == "" || config.LocksTable == "" {
		return nil, errors.New("godjango migrations: bookkeeping table names are required")
	}
	return &Runner{migrator: bunmigrate.NewMigrator(
		db.Bun(),
		catalog.migrations,
		bunmigrate.WithTableName(config.Table),
		bunmigrate.WithLocksTableName(config.LocksTable),
		bunmigrate.WithMarkAppliedOnSuccess(true),
	)}, nil
}

func (runner *Runner) Status(ctx context.Context) ([]Status, error) {
	if err := runner.migrator.Init(ctx); err != nil {
		return nil, err
	}
	current, err := runner.migrator.MigrationsWithStatus(ctx)
	if err != nil {
		return nil, err
	}
	status := make([]Status, len(current))
	for index, migration := range current {
		status[index] = Status{
			Name:    migration.Name + "_" + migration.Comment,
			Comment: migration.Comment,
			Applied: migration.IsApplied(),
		}
	}
	return status, nil
}

func (runner *Runner) Apply(ctx context.Context) (names []string, resultErr error) {
	if err := runner.migrator.Init(ctx); err != nil {
		return nil, err
	}
	if err := runner.migrator.Lock(ctx); err != nil {
		return nil, err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, runner.migrator.Unlock(unlockCtx))
	}()

	group, err := runner.migrator.Migrate(ctx)
	if err != nil {
		return migrationGroupNames(group), err
	}
	return migrationGroupNames(group), nil
}

type RollbackConfirmation string

const ConfirmRollback RollbackConfirmation = "rollback-last-group"

func (runner *Runner) Rollback(
	ctx context.Context,
	confirmation RollbackConfirmation,
) (names []string, resultErr error) {
	if confirmation != ConfirmRollback {
		return nil, ErrRollbackConfirmation
	}
	if err := runner.migrator.Init(ctx); err != nil {
		return nil, err
	}
	if err := runner.migrator.Lock(ctx); err != nil {
		return nil, err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, runner.migrator.Unlock(unlockCtx))
	}()

	group, err := runner.migrator.Rollback(ctx)
	if err != nil {
		return migrationGroupNames(group), err
	}
	return migrationGroupNames(group), nil
}

func writeExclusive(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.WriteString("-- Write SQL here.\n"); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func migrationGroupNames(group *bunmigrate.MigrationGroup) []string {
	if group == nil {
		return nil
	}
	names := make([]string, len(group.Migrations))
	for index, migration := range group.Migrations {
		names[index] = migration.Name + "_" + migration.Comment
	}
	return names
}
