#!/usr/bin/env bash
set -euo pipefail

gate="${1:-}"

require_database() {
  if [[ -z "${GODJANGO_TEST_DATABASE_URL:-}" ]]; then
    echo "GODJANGO_TEST_DATABASE_URL is required for the $gate gate" >&2
    exit 2
  fi
}

case "$gate" in
  format)
    mapfile -t go_files < <(git ls-files '*.go')
    unformatted="$(gofmt -l "${go_files[@]}")"
    if [[ -n "$unformatted" ]]; then
      echo "gofmt required:" >&2
      echo "$unformatted" >&2
      exit 1
    fi
    go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate
    git diff --exit-code -- web/view
    ;;
  dependencies)
    go mod tidy -diff
    go mod verify
    ;;
  licenses)
    GOFLAGS="-tags=e2e" go run github.com/google/go-licenses@v1.6.0 \
      check ./... \
      --ignore github.com/bon5co/godjango \
      --allowed_licenses=MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause
    ;;
  build)
    go build ./...
    go test -tags=e2e ./e2e -run '^$'
    ;;
  vet)
    go vet ./...
    go vet -tags=e2e ./e2e
    ;;
  race)
    go test -race ./...
    ;;
  generated)
    go test -count=1 ./management \
      -run 'TestStartProject|TestStartApp|TestGeneratedProject'
    ;;
  integration)
    require_database
    go test -tags=integration -count=1 ./...
    ;;
  e2e)
    require_database
    if [[ -z "${WAYLAND_DISPLAY:-}" && -z "${DISPLAY:-}" ]]; then
      echo "a headed WAYLAND_DISPLAY or DISPLAY is required for the e2e gate" >&2
      exit 2
    fi
    go test -tags=e2e -count=1 -v ./e2e
    ;;
  *)
    echo "usage: scripts/ci.sh {format|dependencies|licenses|build|vet|race|generated|integration|e2e}" >&2
    exit 2
    ;;
esac
