# User-system E2E plan

# Generated-project user-system E2E plan

This suite is an explicit `e2e` build-tag target. It starts from
`godjango startproject` and `startapp`, then uses only public framework APIs in
the representative application. Package-private test helpers are forbidden.

Environment:

- production Go binary, not a development proxy
- a unique PostgreSQL schema created and destroyed by the suite
- embedded templ, HTMX, and Alpine assets
- headed Chromium with a temporary profile
- screenshots saved under `test-results/auth-e2e/`
- direct SQL assertions use a separate administrative connection

Every browser transition waits for a semantic DOM condition before capturing
its numbered screenshot. Independent temporary browser profiles represent
separate devices where stale-session behavior matters.

Flows:

1. Register a user; verify persisted normalized email and unusable staff flags.
2. Log in with correct credentials; verify session rotation and authenticated UI.
3. Reject wrong credentials without revealing whether the username exists.
4. Log out; verify session data is flushed and protected routes redirect safely.
5. Change password; verify the current session survives only through explicit
   session-hash refresh and every other session becomes invalid.
6. Request password reset for existing and missing emails; responses must be
   indistinguishable.
7. Use a reset token once; verify expiry, replay rejection, and old-password
   rejection.
8. Assign a direct permission and a group permission; verify both grant access.
9. Deactivate the user; verify authentication and permissions are denied.
10. Create a superuser through the CLI and verify all registered permissions.

Coverage matrix:

| Concern | Browser evidence | Persistence evidence |
| --- | --- | --- |
| registration | submitted form and authenticated home | normalized user row; non-staff flags |
| validation | 422 HTMX swap, generic login error, focused invalid field | no unexpected user |
| authentication | login, logout, protected navigation | rotated/flushed session rows |
| passwords | change and one-use reset links | new hash; old hash rejected |
| authorization | denied then granted protected navigation | direct and group grants |
| stale sessions | second browser rejected after password change | stale session remains unusable |
| superuser CLI | superuser login and protected navigation | staff and superuser flags |

The suite first runs the generated project's build, vet, unit, and PostgreSQL
integration targets. Browser automation runs only after those gates pass.
Browser control uses pinned
[chromedp v0.16.0](https://github.com/chromedp/chromedp/releases/tag/v0.16.0)
against the locally installed Chromium; headless mode is explicitly disabled.
The environment must expose a usable `WAYLAND_DISPLAY` or `DISPLAY`.

Run locally:

```bash
GODJANGO_TEST_DATABASE_URL=postgres://... \
  go test -tags=e2e -count=1 ./e2e
```
