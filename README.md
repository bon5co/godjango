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

See [the auth test-port plan](docs/django-auth-test-port.md). Browser-level
presentation remains a separate test-first delivery slice.

Application configuration uses explicit, typed environment declarations. See
[environment declarations](docs/environment.md).

Projects use validated settings and an explicit ordered app registry. See
[project settings and apps](docs/projects.md).

Bun/PostgreSQL connections use a validated, bounded, Railway-safe lifecycle.
See [database lifecycle](docs/database.md).

Schema changes use explicit, paired, transactional Bun SQL migrations. See
[explicit migrations](docs/migrations.md).

The default user, group, permission, and session store is PostgreSQL-backed
without exposing Bun through auth domain APIs. See
[auth persistence](docs/auth-persistence.md).

The Django-familiar management CLI uses a global discovery/scaffolding layer
and a compiled project-local manager. `godjango test` runs only the ordinary Go
unit suite by default. See [management CLI](docs/management-cli.md).

The `net/http` runtime now provides explicit Chi routes, secure middleware,
PostgreSQL-backed rotating SCS sessions, masked CSRF, forms, authorization, and
complete login/logout/password flows. See [HTTP runtime](docs/http-runtime.md).

The view layer renders shared templ components as full pages or HTMX fragments,
uses Alpine's CSP build only for transient browser state, and embeds pinned
assets in the binary. See [views](docs/views.md).

The generated-project E2E suite builds a fresh app, migrates an isolated
PostgreSQL schema, and drives the production binary through headed Chromium.
See [the E2E plan](docs/user-system-e2e-plan.md).
