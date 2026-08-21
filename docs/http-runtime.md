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
| session-stored CSRF secret | 7,042 req/s | 7.09 ms | 140,890 |
| cookie-stored CSRF secret | 29,978 req/s | 1.66 ms | 0 |
| cookie-stored, stateless prefix | 53,728 req/s | 0.93 ms | 0 |
| `net/http` alone, no framework | 93,886 req/s | 0.53 ms | 0 |

Concurrency sweep on the same endpoint and box, six seconds per level. The
load generator shares five cores with the server, so these are a floor:

| connections | stateless JSON | mean | p99 | stateful HTML | mean | p99 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 11,500 req/s | 0.08 ms | 0.14 ms | 8,775 req/s | 0.11 ms | 0.23 ms |
| 4 | 33,186 req/s | 0.12 ms | 0.71 ms | 24,200 req/s | 0.16 ms | 0.63 ms |
| 16 | 53,748 req/s | 0.30 ms | 2.54 ms | 29,196 req/s | 0.55 ms | 3.59 ms |
| 64 | 55,108 req/s | 1.16 ms | 7.30 ms | 29,999 req/s | 2.13 ms | 13.47 ms |
| 128 | 58,646 req/s | 2.18 ms | 14.60 ms | 32,562 req/s | 3.92 ms | 27.79 ms |
| 256 | 62,518 req/s | 4.08 ms | 30.00 ms | — | — | — |

Throughput saturates around sixteen connections, which is the point where five
cores are busy; past it the extra connections buy queueing, not work. Latency
stays proportional to concurrency rather than collapsing, so the chain has no
internal contention worth hunting — the remaining gap to bare `net/http` is
middleware work, not lock waiting.

The row count is the more serious half. Sustained anonymous traffic to a
stateful route no longer grows the session table, because the CSRF secret it
used to create one for now lives in a cookie. What a stateless prefix removes
on top of that is the session load, the CSRF work and the authentication
lookup, none of which a program calling a JSON endpoint asked for.

Declare the exempt prefixes in `internal/project/settings.go`, alongside the
middleware chain they modify:

```go
func StatelessPaths() web.StatelessPaths {
	return web.StatelessPaths{"/static", "/api"}
}
```

They are source rather than an environment variable on purpose. Which paths
skip authentication does not vary between development and production the way a
secret or a proxy in front does — it is a fact about the application's own
routes. It is also a security decision: adding a prefix removes authentication
from those routes, and that belongs in a line that is reviewed, diffed and
tested, not in a deployment setting that can be edited with no record.

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

A generated project starts with `/static`, because files served from disk
authorize nobody and loading a session for each one is a database read that
changes no byte of the response. Add API prefixes alongside it.

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

## Where the CSRF secret lives

The secret behind the masked token is kept in the CSRF cookie by default, and
in server-side session storage only when `CSRFConfig.UseSessions` is set. This
is Django's default too, and the reason is cost: a secret that lives in the
session has to exist before a form can be rendered, so the first anonymous
request to any page creates a session and writes a row. Anonymous traffic then
grows the session table without bound, for state no caller ever uses. Measured
here, twenty seconds of load against one JSON endpoint wrote 140,890 session
rows and 55 MB.

Both modes mask the secret per response and validate by unmasking it in
constant time, and both refuse an unsafe request whose `Origin` does not match.
The cookie mode leans on that `Origin` check for the one thing a plain
double-submit cookie cannot do on its own: a sibling host that can write
cookies for the parent domain can plant a secret, but it cannot make a browser
send a matching `Origin`. An application that shares a registrable domain with
hosts it does not control, and terminates its own TLS, should set
`UseSessions` and pay for the storage.

Sessions are still created the moment one is needed — at login — and still
rotate on login, logout and password change, which rotates the CSRF secret in
both modes.

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
