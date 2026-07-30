# GoDjangGo

Django-inspired, batteries-included web framework for idiomatic Go.

Planned stack:

- Go `net/http`
- Bun over PostgreSQL
- templ
- HTMX
- Alpine.js

The first subsystem is the complete user system: users, password hashing,
authentication backends, permissions, groups, sessions, login/logout,
password-reset tokens, forms, middleware, and management commands.

Development starts from behavioral tests adapted from Django upstream. The
first in-memory auth domain slice now passes its ported contracts:

```bash
go test ./...
```

See [the auth test-port plan](docs/django-auth-test-port.md). Bun-backed
persistence, HTTP forms, middleware, and browser flows remain separate
test-first delivery slices.

Application configuration uses explicit, typed environment declarations. See
[environment declarations](docs/environment.md).

Projects use validated settings and an explicit ordered app registry. See
[project settings and apps](docs/projects.md).

Bun/PostgreSQL connections use a validated, bounded, Railway-safe lifecycle.
See [database lifecycle](docs/database.md).

Schema changes use explicit, paired, transactional Bun SQL migrations. See
[explicit migrations](docs/migrations.md).
