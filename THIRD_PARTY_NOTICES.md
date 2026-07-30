# Third-party notices

## Django

The behavioral contract tests under `auth/` are adapted from the Django test
suite:

- Repository: https://github.com/django/django
- Upstream commit: `274a1d494d11d87a1b767340d1f398f197810f93`
- Upstream date: 2026-07-29
- Source paths: `tests/auth_tests/` and `tests/sessions_tests/`
- License: BSD 3-Clause

The tests translate observable behavior into idiomatic Go APIs. They do not copy
Django's Python implementation.

## HTMX

The embedded `web/view/assets/htmx-2.0.9.min.js` artifact is HTMX v2.0.9:

- Source: https://github.com/bigskysoftware/htmx/tree/v2.0.9
- License: Zero-Clause BSD
- License text: `web/view/assets/LICENSE-HTMX`

## Alpine.js

The embedded `web/view/assets/alpine-3.15.12.min.js` artifact is the Alpine CSP
build v3.15.12:

- Source: https://github.com/alpinejs/alpine/tree/v3.15.12/packages/csp
- License: MIT
- License text: `web/view/assets/LICENSE-ALPINE`
