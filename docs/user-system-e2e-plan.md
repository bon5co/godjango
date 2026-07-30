# User-system E2E plan

Run after the auth HTTP surface and example application exist.

Environment:

- production Go binary, not a development proxy
- isolated PostgreSQL database created through migrations
- embedded templ, HTMX, and Alpine assets
- headed Chromium
- screenshots saved under `test-results/auth-e2e/`

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
