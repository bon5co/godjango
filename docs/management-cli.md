# Management CLI

GoDjangGo uses Django's familiar management-command vocabulary with compiled,
explicit Go registration.

The installed `godjango` binary owns global help, version reporting, project
discovery, and `startproject`. For every project command it walks upward to the
`.godjango` marker, builds that project's `cmd/manage` program through the Go
build cache, and executes it from the project root. The project's pinned
GoDjangGo module version therefore owns command behavior. Applications and
custom commands are registered in Go source; there is no package scanning,
runtime plugin loading, or hidden `init` registration.

## Unit tests

Run the project's ordinary Go unit suite from the project root or any nested
directory:

```bash
godjango test
godjango test ./apps/books/...
godjango test -- -race -count=1 ./...
```

The default is exactly `go test ./...`. It does not load project settings,
connect to PostgreSQL, run migrations, start a server, or enable build tags.
Arguments after `--` pass to `go test` without reinterpretation. Output,
errors, interrupts, and exit status pass through the manager.

Integration and browser suites stay explicit:

```bash
go test -tags=integration ./...
go test -tags=e2e ./...
```

GoDjangGo does not emulate Django's test-database lifecycle. Integration
fixtures own their PostgreSQL database and migration setup.

## Project commands

```bash
godjango startapp library
godjango check
godjango makemigration create_books --app library
godjango migrate
godjango migrationstatus
godjango createsuperuser --username root --email root@example.com --password-stdin
godjango changepassword root --password-stdin
godjango dbshell
godjango runserver
```

`makemigration` creates paired transactional `.up.sql` and `.down.sql` files
inside a registered app. It never guesses a schema diff. `migrate` and
`migrationstatus` collect migrations from the explicit compiled app registry.

Generated projects register the built-in auth app. User commands call the same
auth manager and Bun store as application code. Passwords come from one line on
stdin when `--password-stdin` is present. For non-interactive automation,
`createsuperuser` also accepts `GODJANGO_SUPERUSER_PASSWORD`, and
`changepassword` accepts `GODJANGO_PASSWORD`. Passwords are never command-line
arguments or output. With a terminal and no automation option, both commands
use a no-echo, confirmed password prompt. `createsuperuser` prompts for missing
identity fields; `--noinput` instead requires flags or the corresponding
`GODJANGO_SUPERUSER_*` variables.

Database, migration, auth, shell, and server services load lazily. `test` and
source-only commands do not open PostgreSQL. Database-owning commands always
close their pool before returning. Generated runtime settings declare
`DATABASE_URL` as required, with `DEBUG=false` and `PORT=8000` as explicit
optional defaults; a missing required value fails before the server listens or
a database command begins.

Each app's `commands.go` takes the project's `management.ProjectServices` and
returns `[]management.Command`. The generated `internal/project/commands.go`
registry passes `Services()` in and combines those functions deterministically,
so custom commands work through both `cmd/manage` and the global `godjango`
layer without runtime plugins.

```go
func Commands(services management.ProjectServices) []management.Command {
	return []management.Command{{
		Name:    "importcatalog",
		Summary: "Load the supplier catalog",
		Run: func(ctx context.Context, args []string, streams management.Streams) error {
			db, close, err := services.Database(ctx)
			if err != nil {
				return err
			}
			defer close()
			// ... query with Bun
			return nil
		},
	}}
}
```

`services.Database` opens the same validated, bounded pool the built-in
commands use. It is lazy like the rest — nothing connects until a command calls
it — and the returned closer must run before the command returns. Without it
every project would re-implement settings loading and pool construction just to
query its own tables.

## Intentional differences from Django

- `makemigration` is singular and scaffolds paired explicit SQL. It does not
  infer schema changes from models.
- Static assets embed into Go binaries; there is no `collectstatic`.
- `dbshell` replaces a Python-style interactive application shell.
- Custom commands are compiled into `cmd/manage`.
- Automatic admin UI is omitted. This is a deliberate 2026 tradeoff, not a
  claim that admin workflows are unnecessary: coding agents can now generate
  interactive apps and internal tools, and OpenAI reports building an internal
  product with agent-written application logic, tests, documentation, and
  tooling in roughly one-tenth the estimated manual time. GoDjangGo therefore
  spends its framework budget on reusable auth, permission, validation,
  routing, and persistence primitives, leaving each admin page
  application-specific. Sources: [Codex for every role, tool, and
  workflow](https://openai.com/index/codex-for-every-role-tool-workflow/) and
  [Harness engineering: leveraging Codex in an agent-first
  world](https://openai.com/index/harness-engineering/).
