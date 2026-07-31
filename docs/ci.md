# Continuous integration

CI uses the same entry point locally and on GitHub:

```bash
scripts/ci.sh format
scripts/ci.sh dependencies
scripts/ci.sh licenses
scripts/ci.sh build
scripts/ci.sh vet
scripts/ci.sh race
scripts/ci.sh generated

GODJANGO_TEST_DATABASE_URL=postgres://... scripts/ci.sh integration
GODJANGO_TEST_DATABASE_URL=postgres://... DISPLAY=:99 scripts/ci.sh e2e
```

The PostgreSQL and browser gates fail immediately with actionable messages when
their required database or headed display is missing. CI uses PostgreSQL
18.4-alpine3.23 by immutable digest and Chrome for Testing 151.0.7922.71.
Chrome's sandbox remains enabled locally. The disposable GitHub-hosted Ubuntu
runner alone passes `--no-sandbox` because its AppArmor policy blocks Chrome's
user-namespace sandbox; the suite loads only its loopback generated app.

All actions are pinned to full commit SHAs. Go module and build caches key from
`go.sum`. Failure artifacts retain gate logs for 14 days; browser failures also
retain every screenshot already written under `test-results/auth-e2e/`.

The license gate scans normal and `e2e` dependency graphs with go-licenses
v1.6.0. First-party packages are excluded because this check governs
third-party compatibility. Accepted third-party licenses are MIT, Apache-2.0,
BSD-2-Clause, and BSD-3-Clause; embedded HTMX and Alpine notices remain in
`THIRD_PARTY_NOTICES.md`.

Repository administrators must require these checks on `main`:

- Static
- Race
- Generated project
- PostgreSQL integration
- Browser E2E

Draft pull requests may remain intentionally red during TDD, but must be marked
ready and pass every required check before merge.
