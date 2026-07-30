# HTTP runtime

GoDjangGo's HTTP layer stays on `net/http`. It uses two mature, narrow
libraries: Chi for explicit route and middleware composition, and SCS for
session lifecycle. Bun remains behind the auth/session persistence boundary.

Generated applications compile this stack into `cmd/server`. Startup loads the
complete typed runtime environment before opening a listener:

- `DATABASE_URL`: required secret
- `SESSION_SECRET`: required secret
- `DEBUG`: optional, default `false`
- `PORT`: optional, default `8000`

Database-only management commands load only `DATABASE_URL`; they do not require
HTTP settings. `godjango test` loads neither.

## Middleware

Generated servers declare middleware in source, in this order:

| Order | Middleware | Responsibility |
| --- | --- | --- |
| 1 | request ID | Preserve a valid edge ID or generate 128 random bits |
| 2 | recovery | Convert panics to a structured error without leaking values |
| 3 | secure headers | CSP, HSTS in HTTPS mode, frame/content/referrer policy |
| 4 | body limit | Reject declared and streaming bodies over 1 MiB |
| 5 | sessions | Load/save an opaque SCS token through PostgreSQL |
| 6 | CSRF | Masked synchronizer token, same-origin validation, secure cookie |
| 7 | authentication | Load and validate the session user and auth hash |

`TrustedProxy` is available but generated projects do not trust any network by
default. Configure exact `netip.Prefix` values for infrastructure-controlled
proxies. It walks `X-Forwarded-For` from the trusted edge toward the client and
stops at the first untrusted address, preventing a client-prepended value from
winning.

Applications implement `Routes(chi.Router)` explicitly. The project registry
invokes route providers in declared app order. Authorization uses
`RequireAuthentication` and `RequirePermission`; direct, group, inactive, and
superuser behavior comes from the auth domain backend.

## Sessions and CSRF

Session cookies are `HttpOnly`, default to `SameSite=Lax`, and can be marked
`Secure`. SCS tokens are hashed before storage, so PostgreSQL never contains
the bearer token held by the browser. Login, password change, and user changes
rotate the session token; logout destroys the row and expires the cookie.
Password-hash changes invalidate any other existing session on its next
request.

CSRF secrets live inside the server-side session. Each exposed token has a new
random mask, while validation compares the recovered secret in constant time.
Unsafe methods accept `X-CSRF-Token` or the `csrf_token` form field. A supplied
`Origin` must exactly match the request scheme and host. Login and password
change rotate both session and CSRF secrets.

## User flows

The built-in routes provide:

- login with generic credential errors and safe redirect validation;
- POST-only, CSRF-protected logout;
- authenticated password change with old-password verification;
- password-reset request with identical public behavior for present and absent
  email addresses;
- expiring reset confirmation whose token becomes invalid after password
  change.

HTML is deliberately minimal until the templ/HTMX/Alpine presentation layer.
Values redisplayed in forms are HTML-escaped. `Form` provides typed validation,
stable field/non-field errors, and defensive copies.

Generated projects must replace the default password-reset sender, which
returns a configuration error. Delivery failures are logged internally while
the public reset response remains enumeration-safe.

## Server lifecycle

`web.Server` owns read, write, idle, header, and graceful-shutdown timeouts.
Cancellation stops new work and allows in-flight requests to finish within the
declared shutdown window; expiration force-closes connections and returns the
deadline error. Generated `runserver` forwards interrupts through the compiled
project process.

Security-sensitive integration tests use isolated real PostgreSQL schemas and
verify login, session rotation, password change, logout, row counts, and usable
password hashes.
