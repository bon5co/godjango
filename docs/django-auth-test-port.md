# Django auth test port

## Upstream pin

GoDjangGo's initial behavioral reference is Django development `main`:

| Field | Value |
|---|---|
| Commit | `274a1d494d11d87a1b767340d1f398f197810f93` |
| Commit date | 2026-07-29 |
| Auth/session tests discovered | 852 |
| Source | `django/django` |

Every adapted Go test names its upstream Python file and test method. The pin
makes future upstream refreshes reviewable instead of silently moving the target.

## Translation rules

Port observable contracts:

- accepted inputs and normalized outputs
- security invariants
- persistence-visible state
- session behavior
- permission decisions
- HTTP behavior and safe redirects
- user-facing validation failures

Do not port implementation accidents:

- Python object protocol behavior
- sync/async duplicate tests; Go receives one context-aware API
- Django ORM query-count assertions except security-relevant timing parity
- Django migration executor internals
- Django template tags and context processors
- automatic-admin behavior
- multi-database routing in the first release

## Upstream inventory

| Upstream area | Tests | GoDjangGo treatment |
|---|---:|---|
| sessions | 129 | Adapt public cookie/database session behavior; do not retest SCS internals |
| auth views | 117 | Adapt login/logout/password flow HTTP contracts |
| forms | 107 | Adapt validation, field errors, and password handling |
| auth backends | 81 | Adapt backend ordering, inactive users, timing, and permissions |
| management | 79 | Adapt `createsuperuser` and `changepassword`; drop Django-only commands |
| models | 57 | Adapt users, groups, permissions, normalization |
| hashers | 48 | Adapt supported formats, upgrades, unusable passwords, constant-time checks |
| validators | 31 | Adapt user/password validation |
| remote user | 31 | Defer until pluggable backends land |
| decorators/middleware/mixins | 73 | Adapt into Go middleware contracts |
| checks/migrations | 31 | Replace with Go config validation and GoDjangGo migration tests |
| tokens | 11 | Adapt fully |
| templates/context/tags | 25 | Replace with templ component tests |
| signals | 6 | Defer until an event contract is justified |
| handlers/basic/login | 24 | Adapt request/session behavior |
| admin multi-DB | 2 | Drop; automatic admin is out of scope |

## Slice 1

The first executable Go contracts cover:

- email-domain and username normalization
- user and superuser creation invariants
- usable and unusable passwords
- encoded password verification and upgrade signaling
- inactive-user authentication policy
- direct, group, superuser, and anonymous permissions
- login/logout session rotation and invalidation
- password reset token expiry and invalidation

The contracts were committed as a compiling, intentionally failing baseline
before implementation. The in-memory domain slice now passes. Bun-backed
persistence, HTTP flows, and the remaining upstream behaviors stay in separate
test-first slices so each contract is red before its implementation lands.

## Later verification

Database behaviors run against branch-scoped PostgreSQL through migrations.
HTTP flows run against a production-built example application using real browser
clicks. Unit tests alone will not close the user-system milestone.
