# Database lifecycle

The `database` package owns Bun/PostgreSQL construction, pgxpool policy, startup
health checking, transactions, and shutdown:

```go
config := database.DefaultConfig(settings.DatabaseURL.String())
db, err := database.Open(ctx, config)
if err != nil {
	return err
}
defer db.Close()
```

`Open` validates configuration before constructing a connector and performs a
bounded `PingContext` before returning. The caller owns the handle. `Close` is
idempotent so converging shutdown paths cannot close the pool twice.

## Pool defaults

| Setting | Default |
|---|---:|
| Maximum open connections | 25 |
| Maximum idle time | 30 seconds |
| Maximum lifetime | 30 minutes |
| Startup ping timeout | 5 seconds |

These are framework defaults, not hidden constants: copy `DefaultConfig` and
override fields for the deployment.

GoDjangGo uses native `pgxpool` and adapts it to Bun through pgx's official
`stdlib.OpenDBFromPool` bridge. pgxpool pings a connection before acquisition
when it has been idle for more than one second. A database sleep/wake period
longer than that threshold therefore replaces a dead connection before the
next application query, without custom socket inspection or blanket retries.

The one-second boundary matters: a database killed and queried again in under
one second can still expose the server error once. The supported deployment
assumption is that database sleep/wake periods exceed one second.

Do not add blanket query retries. Retrying writes or transactions after an
ambiguous network failure can duplicate side effects.

## Transactions

Use the shared transaction boundary:

```go
err := database.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
	// All writes in this operation use tx.
	return nil
})
```

Returning an error rolls back. Returning nil commits. Lower layers should
accept `bun.IDB` when they need to compose under either a database or
transaction.

## Integration verification

Real PostgreSQL contracts are behind the `integration` build tag and require a
dedicated test database:

```bash
GODJANGO_TEST_DATABASE_URL=postgres://... \
	go test -tags=integration ./database
```

The regression suite terminates an actual pooled PostgreSQL backend, waits past
pgxpool's built-in one-second liveness threshold, and verifies that the next
query uses a healthy replacement connection.

## SQLite

For a local file, use `database.DefaultSQLiteConfig("sqlite:./db.sqlite")` or an
absolute path such as `sqlite:///var/lib/example/app.sqlite`. The framework
uses the pure Go `modernc.org/sqlite` driver with Bun's SQLite dialect. SQLite
defaults to one connection for predictable local write serialization. The
driver sets foreign key enforcement on each connection, including replacements
after a connection expires. `DB.Dialect()` returns `sqlite` or `postgres`.

`godjango startproject --db sqlite <name>` creates a project whose
`DATABASE_URL` defaults to `sqlite:./db.sqlite`; PostgreSQL projects still
require `DATABASE_URL`. Override the SQLite value with an environment variable
when the file belongs elsewhere. `dbshell` opens `sqlite3` for a SQLite URL and
`psql` for PostgreSQL.
