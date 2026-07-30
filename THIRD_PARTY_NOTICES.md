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
