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
