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
- `STATELESS_PATHS`: optional, comma-separated, default empty

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

Middleware 6, 7 and 8 are wrapped in `StatelessPaths.Exempt` so a deployment
can declare routes that skip all three. See [Stateless paths](#stateless-paths).

## Stateless paths

The stateful part of the chain is not free, and it is not conditional on the
caller wanting it. CSRF asks the session for a secret on every request, which
creates a session for a caller that arrived without one, which writes a row.
An endpoint serving JSON to programs therefore pays a database round trip and
stores a session per request, for state nothing in the request or the response
refers to.

Measured on the development VM against a handler that encodes a 16-byte JSON
body, 50 connections for 20 seconds:

| chain | throughput | mean latency | session rows written |
| --- | ---: | ---: | ---: |
| full middleware | 7,042 req/s | 7.09 ms | 140,890 |
| stateless prefix | 37,175 req/s | 1.34 ms | 0 |
| `net/http` alone, no framework | 93,886 req/s | 0.53 ms | 0 |

The row count is the more serious half. Sustained anonymous traffic to a
stateful route grows the session table without bound, and nothing in the
request signals that it should.

Declare the exempt prefixes as a comma-separated `STATELESS_PATHS`, which the
generated server passes to `web.StatelessPaths`:

```
STATELESS_PATHS=/api,/healthz
```

A prefix matches a path exactly or at a segment boundary, so `/api` covers
`/api` and `/api/ping` and does not cover `/apiary`. Prefixes are matched
against the resolved path, so `/api/../admin` is matched as `/admin` and stays
stateful. The server refuses to start on a prefix that is empty, does not begin
at the root, or resolves to `/`, because each of those silently exempts either
nothing or everything.

**A stateless route is an anonymous route.** Skipping the session middleware
also skips authentication, so under a stateless prefix `CurrentUser` reports no
user, `CSRFToken` returns the empty string, and `RequireAuthentication` and
`RequirePermission` refuse the request. That is the intended failure direction —
they fail closed — but it means anything a stateless route authorizes has to
travel in the request itself, as a bearer credential the handler checks. A
handler mounted both inside and outside a stateless prefix can tell which chain
it is on with `web.IsStateless`.

Leave `STATELESS_PATHS` unset for an application whose routes are all
browser-facing. The default is empty, so an existing project keeps today's
behaviour until it opts in.

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
  proxy without promising a fixed proxy address, where the guarantee has to be
  topological — nothing but the proxy can open a connection to the port.
  Generated projects wire this to `TRUST_PROXY_HEADERS`. Confirm the guarantee
  on the platform rather than assuming it: a managed proxy in front is not
  enough on its own, because on several platforms the containers of one project
  share a network and can dial each other directly, and on some that network
  spans the whole account.

Trust is one decision, not two. Both the address and the scheme come from the
same peer, and there is no coherent position from which its word on one is worth
more than its word on the other — so turning trust on for the scheme accepts
`X-Forwarded-For` for `RemoteIP` as well.

`TrustedProxy` has to run ahead of every middleware that asks, which is why the
generated chain places it first. A request that reaches a caller without passing
through it is reported as plaintext.

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

Both forwarding headers are read across every line the request carries, nearest
hop last, and the nearest hop's entry is the one believed. A proxy that adds to
a header rather than replacing it leaves the client's own value in front of its
own, and reading that would let a prepended value decide the answer.

### Upgrading an existing project

A project scaffolded before this middleware existed keeps working over plain
HTTP and keeps failing behind a TLS-terminating proxy until it is wired up. Add
`TrustProxyHeaders` to `RuntimeSettings` and `env.Optional("TRUST_PROXY_HEADERS",
&settings.TrustProxyHeaders, false)` to its schema in
`internal/project/settings.go`, put `web.TrustedProxy(web.TrustedProxyConfig{
TrustAnyPeer: settings.TrustProxyHeaders})` first in the middleware list in
`cmd/server/main.go`, and set `TRUST_PROXY_HEADERS=true` in the deployment
environment. New projects get all three from the scaffold.

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
