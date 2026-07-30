# Architecture

## Product line

GoDjangGo adopts Django's cohesive, batteries-included developer experience
without attempting Python source compatibility.

The framework owns stable public APIs and composes mature libraries underneath:

- `net/http` for HTTP and middleware
- Bun for models, queries, transactions, and migration execution
- templ for type-safe full pages and fragments
- HTMX for server-owned interactions
- Alpine.js for transient browser-only state

Automatic admin is outside scope. The complete user system is inside scope.

## User-system boundary

The `auth` package will own:

- default Bun user, group, and permission models
- username and email normalization
- configurable password hashers and transparent upgrades
- authentication backend composition
- inactive-user policy
- direct and group permissions
- anonymous-user behavior
- session login, logout, rotation, and invalidation
- password reset tokens
- login, password-change, and password-reset forms
- request authentication and authorization middleware
- `createsuperuser` and `changepassword` commands

The package must expose behavior, not Bun internals. Applications may replace
the authentication backend, but the default Bun-backed system must work without
configuration beyond a database connection and secret key.

## Frontend ownership

- templ renders every full page and HTMX fragment.
- HTMX owns requests, validation swaps, pagination, and server state.
- Alpine owns dropdowns, disclosure, tabs, and modal visibility.
- A value must not have competing HTMX and Alpine sources of truth.
- Framework assets are version-pinned and embedded in the Go binary.
