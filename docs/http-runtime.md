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
- `TRUST_PROXY_HEADERS`: optional, default `false`

Database-only management commands load only `DATABASE_URL`; they do not require
HTTP settings. `godjango test` loads neither.

## Middleware

Generated servers declare middleware in source, in this order:

| Order | Middleware | Responsibility |
| --- | --- | --- |
| 1 | trusted proxy | Resolve the client's real address and scheme, or refuse to |
| 2 | request ID | Preserve a valid edge ID or generate 128 random bits |
| 3 | recovery | Convert panics to a structured error without leaking values |
| 4 | secure headers | CSP, HSTS in HTTPS mode, frame/content/referrer policy |
| 5 | body limit | Reject declared and streaming bodies over 1 MiB |
| 6 | sessions | Load/save an opaque SCS token through PostgreSQL |
| 7 | CSRF | Masked synchronizer token, same-origin validation, secure cookie |
| 8 | authentication | Load and validate the session user and auth hash |

## Reverse proxies

A reverse proxy rewrites the two request facts the security middleware depends
on. The address `net/http` accepts the connection from is the proxy's, and the
scheme it was reached by is plain HTTP whenever the proxy terminated TLS —
`request.TLS` is nil on exactly the deployments that are served over HTTPS. The
proxy restates the originals in `X-Forwarded-For` and `X-Forwarded-Proto`, and
`TrustedProxy` is the single place where an application says whether those
restatements can be believed. Downstream code reads the answer through
`RemoteIP` and `RequestScheme`/`RequestIsHTTPS`, never through the headers.

Trust is off by default, so an unconfigured application believes nothing and
reports the connection it actually accepted. Declare it one of two ways:

- `Networks`: exact `netip.Prefix` values for infrastructure-controlled proxies.
  `X-Forwarded-For` is walked from the trusted edge toward the client and stops
  at the first untrusted address, so a client-prepended value never wins.
- `TrustAnyPeer`: for platforms that place the application behind their own
  proxy on a private network without promising a fixed proxy address — Dokploy,
  Railway, Fly — where the guarantee is that nothing else can reach the port.
  Generated projects wire this to `TRUST_PROXY_HEADERS`.

Getting it wrong fails in one direction or the other. Left off behind a
TLS-terminating proxy, every POST is rejected with 403 `csrf_failed`, because
the browser sends an `https` `Origin` to a server that believes it is serving
`http`, and the login redirect check stops requiring HTTPS of a `?next=` target.
Turned on for a port that is reachable directly from the internet, any client
can name its own address and scheme: `RemoteIP` returns whatever the caller
typed, poisoning rate limits, audit logs and IP allowlists.

`X-Forwarded-Proto` is safe to rely on for origin validation in a way that a
general request header is not: it is not CORS-safelisted, so a cross-origin
script cannot make a browser send it and cannot forge a matching origin.

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
`Origin` must exactly match the host and the client's scheme, which behind a
trusted proxy is the forwarded one rather than the connection's. Login and password
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

Authentication HTML uses the shared templ/HTMX presentation layer documented in
[views](views.md).

Apps that only need a router implement `Routes(chi.Router)`. Apps that need the
server-owned database or auth services implement
`RoutesWithServices(chi.Router, web.RuntimeServices)` instead. The explicit
service value supplies the open database, user manager, auth store, and a
session-safe `Login` function. Apps must not close server-owned services.

The generated `/healthz` endpoint sits outside session, CSRF, and authentication
middleware. Readiness probes therefore do not create cookies or database rows.
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
