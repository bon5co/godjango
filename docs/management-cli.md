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

## Intentional differences from Django

- `makemigration` is singular and scaffolds paired explicit SQL. It does not
  infer schema changes from models.
- Static assets embed into Go binaries; there is no `collectstatic`.
- `dbshell` replaces a Python-style interactive application shell.
- Custom commands are compiled into `cmd/manage`.
- Automatic admin UI is omitted. Application-specific admin pages are cheap
  to generate with contemporary LLM tooling, while the reusable auth,
  permission, validation, routing, and persistence primitives remain framework
  responsibilities.
