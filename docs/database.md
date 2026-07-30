# Database lifecycle

The `database` package owns Bun/PostgreSQL construction, pool policy, startup
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
| Maximum idle connections | 10 |
| Maximum idle time | 30 seconds |
| Maximum lifetime | 30 minutes |
| Startup ping timeout | 5 seconds |

These are framework defaults, not hidden constants: copy `DefaultConfig` and
override fields for the deployment.

The 30-second idle limit deliberately addresses a failure observed on Railway.
Its internal network can reap idle TCP connections while pgdriver surfaces a
raw EOF rather than `driver.ErrBadConn`. In that case `database/sql` does not
retry on a fresh connection and the request fails. Recycling idle connections
before the network reaper reaches them prevents the stale socket from entering
a query.

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

The regression suite terminates an actual pooled PostgreSQL backend, waits for
a short configured idle policy to recycle it, and verifies that the next query
uses a healthy replacement connection.
