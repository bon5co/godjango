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
| PostgreSQL ping timeout | 5 seconds |

These are framework defaults, not hidden constants: copy `DefaultConfig` and
override fields for the deployment.

The 30-second idle limit bounds resource retention. It is not the recovery
mechanism for a database restart: `database/sql` enforces idle expiry on a
background cadence, so a dead connection can still be checked out first.

GoDjangGo installs pgdriver's reset-session hook to validate reused connections
before an application operation begins. It performs a `PingTimeout`-bounded
PostgreSQL ping on every reused checkout. If the ping fails, the hook returns
`driver.ErrBadConn`, so `database/sql` transparently opens a replacement before
sending the application query.

This portable check behaves the same for plain TCP, `sslmode=require`, and
`sslmode=verify-full`. It adds one SQL round trip per reused checkout, so include
that cost in deployment capacity plans.

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

The regression suite terminates an actual pooled PostgreSQL backend and verifies
that the first query immediately afterward uses a healthy replacement
connection.
