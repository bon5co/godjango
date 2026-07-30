# Explicit migrations

GoDjangGo uses Bun's mature SQL migrator with a stricter project-level
contract. It does not infer schema changes from Go structs.

Apps opt in explicitly:

```go
//go:embed migrations/*.sql
var migrationFiles embed.FS

func (App) MigrationFS() fs.FS {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return files
}
```

Migration files are transactional pairs:

```text
20260731033456_add_books.tx.up.sql
20260731033456_add_books.tx.down.sql
```

The 14-digit UTC timestamp is the global identity. It must be unique across
every registered app. Both files are required. Collection fails before touching
the database when names, identities, or pairs are invalid.

## Scaffolding

The CLI `makemigration <name>` command will call the framework scaffolder:

```go
files, err := (migrations.Scaffolder{}).Create(directory, "add_books")
```

The scaffolder writes deterministic UTC names, accepts only safe lowercase
names, and refuses to overwrite either side of an existing pair. This is
explicit file creation, not Django-style model diffing. The plural
`makemigrations` command remains reserved.

## Running

Construct the catalog from the validated project and pair it with the framework
database:

```go
catalog, err := migrations.Collect(configured)
runner, err := migrations.NewRunner(
	db,
	catalog,
	migrations.DefaultRunnerConfig(),
)

applied, err := runner.Apply(ctx)
status, err := runner.Status(ctx)
```

Apply acquires Bun's database-backed migration lock. Applied bookkeeping is
written only after each transactional migration succeeds. A failed migration
rolls back its SQL, remains pending, and reports its stable identity.

Rollback is destructive and requires explicit intent:

```go
rolledBack, err := runner.Rollback(ctx, migrations.ConfirmRollback)
```

The runner releases its lock with a fresh bounded context even when the
migration context is canceled. After process death, operators should inspect
the migration and lock tables before removing a stale lock; GoDjangGo does not
silently steal one.

## Integration tests

Real PostgreSQL verification requires a dedicated database:

```bash
GODJANGO_TEST_DATABASE_URL=postgres://... \
	go test -tags=integration ./migrations
```

The suite verifies ordering, apply/no-op behavior, row persistence, rollback,
failed-transaction cleanup, pending status, and concurrent lock rejection.
